// Package otlp ingests the OpenTelemetry logs Claude Code exports
// (CLAUDE_CODE_ENABLE_TELEMETRY=1 with OTEL_LOGS_EXPORTER=otlp) over
// OTLP/HTTP, as JSON or protobuf: it keeps the api_request, api_error and
// tool_result events of the sessions the account already holds, with an
// allow-list of their attributes, as session.OtelEvent. Prompt text, tool
// input and parameters, response bodies and the user's e-mail address are
// never kept, whatever OTEL_LOG_USER_PROMPTS and friends say.
//
// Metrics (/v1/metrics) are accepted and decoded but not stored: the events
// carry the same usage per request, and accepting them lets one
// OTEL_EXPORTER_OTLP_ENDPOINT serve both signals.
package otlp

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// The event names kept (event.name, without the claude_code. prefix).
const (
	EventAPIRequest = "api_request"
	EventAPIError   = "api_error"
	EventToolResult = "tool_result"
)

// eventPrefix is the prefix of Claude Code's event names in a record's body
// or event_name field.
const eventPrefix = "claude_code."

// kept lists, per event, the attributes stored. Every other attribute is
// dropped, the standard ones (user.email, user.account_uuid,
// organization.id, workspace.host_paths, …) included.
var kept = map[string][]string{
	EventAPIRequest: {"model", "cost_usd", "cost_usd_micros", "duration_ms", "input_tokens", "output_tokens",
		"cache_read_tokens", "cache_creation_tokens", "request_id", "speed", "query_source", "effort",
		"agent.name", "skill.name", "plugin.name", "marketplace.name", "mcp_server.name", "mcp_tool.name"},
	EventAPIError: {"model", "error", "status_code", "duration_ms", "attempt", "request_id", "speed", "query_source",
		"effort", "agent.name", "skill.name", "plugin.name", "marketplace.name", "mcp_server.name", "mcp_tool.name"},
	EventToolResult: {"tool_name", "tool_use_id", "success", "duration_ms", "error_type", "decision_type",
		"decision_source", "tool_input_size_bytes", "tool_result_size_bytes", "mcp_server_scope"},
}

// common attributes every kept event also keeps.
var commonKept = []string{"app.version", "terminal.type"}

// integers and decimals are the attributes stored as numbers (Claude Code
// sends many of them as strings); flags as booleans.
var (
	integers = map[string]bool{"cost_usd_micros": true, "duration_ms": true, "input_tokens": true, "output_tokens": true,
		"cache_read_tokens": true, "cache_creation_tokens": true, "status_code": true, "attempt": true,
		"tool_input_size_bytes": true, "tool_result_size_bytes": true}
	decimals = map[string]bool{"cost_usd": true}
	flags    = map[string]bool{"success": true}
)

// maxString bounds a kept string attribute (an api_error's error text).
const maxString = 2048

// Content types of OTLP/HTTP.
const (
	ContentJSON     = "application/json"
	ContentProtobuf = "application/x-protobuf"
)

// ErrUnsupportedType is the error for a body that is neither JSON nor
// protobuf.
var ErrUnsupportedType = errors.New("otlp: unsupported content type")

// Format is how a request body is encoded; the response uses the same.
type Format int

const (
	FormatJSON Format = iota
	FormatProtobuf
)

// FormatOf maps a Content-Type to its Format.
func FormatOf(contentType string) (Format, error) {
	mediaType, _, _ := strings.Cut(contentType, ";")
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case ContentJSON:
		return FormatJSON, nil
	case ContentProtobuf, "application/protobuf":
		return FormatProtobuf, nil
	}
	return 0, fmt.Errorf("%w %q (send %s or %s)", ErrUnsupportedType, contentType, ContentJSON, ContentProtobuf)
}

// ErrTooLarge is the error for a body above the limit once decompressed.
var ErrTooLarge = errors.New("otlp: body too large")

// Body reads r, gunzipping it when encoding is gzip, within limit bytes
// (decompressed).
func Body(r io.Reader, encoding string, limit int64) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "identity":
	case "gzip":
		zr, err := gzip.NewReader(r)
		if err != nil {
			return nil, fmt.Errorf("otlp: gzip: %w", err)
		}
		defer zr.Close()
		r = zr
	default:
		return nil, fmt.Errorf("otlp: unsupported content encoding %q", encoding)
	}
	raw, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, ErrTooLarge
	}
	return raw, nil
}

var unmarshalJSON = protojson.UnmarshalOptions{DiscardUnknown: true}

func unmarshal(f Format, raw []byte, m proto.Message) error {
	var err error
	if f == FormatProtobuf {
		err = proto.Unmarshal(raw, m)
	} else {
		err = unmarshalJSON.Unmarshal(bytes.TrimSpace(raw), m)
	}
	if err != nil {
		return fmt.Errorf("otlp: decode: %w", err)
	}
	return nil
}

// DecodeLogs decodes an ExportLogsServiceRequest.
func DecodeLogs(f Format, raw []byte) (*collogs.ExportLogsServiceRequest, error) {
	req := &collogs.ExportLogsServiceRequest{}
	return req, unmarshal(f, raw, req)
}

// DecodeMetrics decodes an ExportMetricsServiceRequest and counts its data
// points.
func DecodeMetrics(f Format, raw []byte) (int, error) {
	req := &colmetrics.ExportMetricsServiceRequest{}
	if err := unmarshal(f, raw, req); err != nil {
		return 0, err
	}
	n := 0
	for _, rm := range req.GetResourceMetrics() {
		for _, sm := range rm.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				n += len(m.GetGauge().GetDataPoints()) + len(m.GetSum().GetDataPoints()) +
					len(m.GetHistogram().GetDataPoints()) + len(m.GetExponentialHistogram().GetDataPoints()) +
					len(m.GetSummary().GetDataPoints())
			}
		}
	}
	return n, nil
}

// Records counts the log records of a request.
func Records(req *collogs.ExportLogsServiceRequest) int {
	n := 0
	for _, rl := range req.GetResourceLogs() {
		for _, sl := range rl.GetScopeLogs() {
			n += len(sl.GetLogRecords())
		}
	}
	return n
}

// Events returns the kept events of the request, stamped with accountID,
// and how many records it left out (other events, no session id, an
// invalid session id). now stands in for a record without any time.
func Events(req *collogs.ExportLogsServiceRequest, accountID string, now time.Time) (events []session.OtelEvent, skipped int) {
	for _, rl := range req.GetResourceLogs() {
		resource := attrs(rl.GetResource().GetAttributes())
		for _, sl := range rl.GetScopeLogs() {
			for _, rec := range sl.GetLogRecords() {
				a := attrs(rec.GetAttributes())
				name := eventName(rec.GetEventName(), a, rec.GetBody().GetStringValue())
				allowed, ok := kept[name]
				sid := str(a, resource, "session.id")
				if !ok || !ValidSessionID(sid) {
					skipped++
					continue
				}
				e := session.OtelEvent{
					AccountID: accountID,
					SessionID: sid,
					PromptID:  str(a, nil, "prompt.id"),
					Name:      name,
					Sequence:  integer(a["event.sequence"]),
					Timestamp: timestamp(a, rec.GetTimeUnixNano(), rec.GetObservedTimeUnixNano(), now),
					// Attributes: the allow-list of the event and the common
					// ones, keyed with dots as underscores (a stored field
					// name never holds a dot).
					Attributes: session.Doc{},
				}
				for _, list := range [][]string{allowed, commonKept} {
					for _, k := range list {
						v, has := a[k]
						if !has {
							if v, has = resource[k]; !has {
								continue
							}
						}
						e.Attributes[strings.ReplaceAll(k, ".", "_")] = normalize(k, v)
					}
				}
				events = append(events, e)
			}
		}
	}
	return events, skipped
}

// ValidSessionID reports whether id can be a session id: a single safe
// path element, as the session transport requires, of at most 128 bytes.
func ValidSessionID(id string) bool {
	return id != "" && id != "." && len(id) <= 128 && filepath.IsLocal(id) && filepath.Base(id) == id && !strings.ContainsAny(id, `/\`)
}

// eventName is the record's event name without the claude_code. prefix:
// its event_name field, else its event.name attribute, else its body.
func eventName(field string, a map[string]any, body string) string {
	for _, n := range []string{field, stringOf(a["event.name"]), body} {
		if n = strings.TrimPrefix(strings.TrimSpace(n), eventPrefix); n != "" {
			return n
		}
	}
	return ""
}

func str(a, fallback map[string]any, k string) string {
	if s := stringOf(a[k]); s != "" {
		return s
	}
	return stringOf(fallback[k])
}

func stringOf(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	}
	return ""
}

// timestamp is the event's event.timestamp, else the record's time, else
// its observed time, else now; always UTC and at millisecond precision, as
// a store keeps it.
func timestamp(a map[string]any, unixNano, observed uint64, now time.Time) time.Time {
	at := now
	if t, err := time.Parse(time.RFC3339Nano, stringOf(a["event.timestamp"])); err == nil {
		at = t
	} else if unixNano > 0 {
		at = time.Unix(0, int64(unixNano))
	} else if observed > 0 {
		at = time.Unix(0, int64(observed))
	}
	return at.UTC().Truncate(time.Millisecond)
}

func integer(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case float64:
		return int64(x)
	case string:
		if n, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64); err == nil {
			return n
		}
		if f, err := strconv.ParseFloat(strings.TrimSpace(x), 64); err == nil {
			return int64(f)
		}
	}
	return 0
}

func decimal(v any) float64 {
	switch x := v.(type) {
	case int64:
		return float64(x)
	case float64:
		return x
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(x), 64); err == nil {
			return f
		}
	}
	return 0
}

// normalize stores an attribute as its type: numbers and flags Claude Code
// sends as strings become numbers and booleans, long text is cut.
func normalize(k string, v any) any {
	switch {
	case integers[k]:
		return integer(v)
	case decimals[k]:
		return decimal(v)
	case flags[k]:
		if b, ok := v.(bool); ok {
			return b
		}
		b, _ := strconv.ParseBool(strings.TrimSpace(stringOf(v)))
		return b
	}
	if s, ok := v.(string); ok && len(s) > maxString {
		return s[:maxString] + "…"
	}
	return v
}

// attrs flattens key-values to Go values: strings, booleans, int64,
// float64, and lists and maps of those; bytes are left out.
func attrs(kvs []*common.KeyValue) map[string]any {
	out := make(map[string]any, len(kvs))
	for _, kv := range kvs {
		if v, ok := value(kv.GetValue()); ok {
			out[kv.GetKey()] = v
		}
	}
	return out
}

func value(v *common.AnyValue) (any, bool) {
	switch x := v.GetValue().(type) {
	case *common.AnyValue_StringValue:
		return x.StringValue, true
	case *common.AnyValue_BoolValue:
		return x.BoolValue, true
	case *common.AnyValue_IntValue:
		return x.IntValue, true
	case *common.AnyValue_DoubleValue:
		return x.DoubleValue, true
	case *common.AnyValue_ArrayValue:
		list := []any{}
		for _, e := range x.ArrayValue.GetValues() {
			if ev, ok := value(e); ok {
				list = append(list, ev)
			}
		}
		return list, true
	case *common.AnyValue_KvlistValue:
		return attrs(x.KvlistValue.GetValues()), true
	}
	return nil, false
}
