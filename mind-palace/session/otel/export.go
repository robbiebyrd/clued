package otel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// Request is the OTLP/HTTP JSON body (ExportTraceServiceRequest) of spans.
func Request(tr Trace, resource map[string]any, spans []Span) []byte {
	encoded := make([]map[string]any, 0, len(spans))
	for _, s := range spans {
		span := map[string]any{
			"traceId":           tr.ID,
			"spanId":            s.ID,
			"name":              s.Name,
			"kind":              1, // SPAN_KIND_INTERNAL
			"startTimeUnixNano": strconv.FormatInt(s.Start.UnixNano(), 10),
			"endTimeUnixNano":   strconv.FormatInt(max(s.End.UnixNano(), s.Start.UnixNano()), 10),
			"attributes":        attributes(s.Attrs),
		}
		if s.ParentID != "" {
			span["parentSpanId"] = s.ParentID
		}
		if s.Error != "" {
			span["status"] = map[string]any{"code": 2, "message": s.Error} // STATUS_CODE_ERROR
		}
		encoded = append(encoded, span)
	}
	body, _ := json.Marshal(map[string]any{"resourceSpans": []any{map[string]any{
		"resource":   map[string]any{"attributes": attributes(resource)},
		"scopeSpans": []any{map[string]any{"scope": map[string]any{"name": "clued", "version": Version}, "spans": encoded}},
	}}})
	return body
}

// Version is the instrumentation scope version.
var Version = "1"

// attributes encodes a map as OTLP KeyValues, sorted by key.
func attributes(m map[string]any) []any {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]any, 0, len(keys))
	for _, k := range keys {
		var v map[string]any
		switch x := m[k].(type) {
		case string:
			v = map[string]any{"stringValue": x}
		case bool:
			v = map[string]any{"boolValue": x}
		case int:
			v = map[string]any{"intValue": strconv.Itoa(x)}
		case int64:
			v = map[string]any{"intValue": strconv.FormatInt(x, 10)}
		case float64:
			v = map[string]any{"doubleValue": x}
		default:
			v = map[string]any{"stringValue": fmt.Sprint(x)}
		}
		out = append(out, map[string]any{"key": k, "value": v})
	}
	return out
}

// Exporter sends session traces to an OTLP/HTTP endpoint. StateDir keeps,
// per session, the ids of the spans sent; the root span is sent once, so a
// session exported again (resumed and ended anew) adds only its new spans.
type Exporter struct {
	Endpoint string            // the collector, e.g. http://localhost:4318; /v1/traces is appended unless present
	Headers  map[string]string // e.g. an Authorization header; never logged
	StateDir string            // empty keeps no state: every export sends every span
	Client   *http.Client      // nil uses a client with a 10 s timeout
	Resource map[string]any    // extra resource attributes
}

// New returns the exporter cfg asks for, or nil when it names no
// endpoint. Its state lives in <config dir>/otel.
func New(cfg session.Config) *Exporter {
	if cfg.OTelEndpoint == "" {
		return nil
	}
	base := cfg.WalPath
	if base == "" {
		base = session.DefaultConfigPath()
	}
	return &Exporter{Endpoint: cfg.OTelEndpoint, Headers: cfg.OTelHeaders, StateDir: filepath.Join(filepath.Dir(base), "otel")}
}

// Sent counts what one export sent.
type Sent struct {
	Session string `json:"session"`
	Trace   string `json:"trace_id"`
	Spans   int    `json:"spans"`
}

// ExportSession sends the session's spans not sent before.
func (e *Exporter) ExportSession(ctx context.Context, store session.Store, accountID, sessionID string) (Sent, error) {
	out := Sent{Session: sessionID}
	if !filepath.IsLocal(sessionID) || filepath.Base(sessionID) != sessionID {
		return out, fmt.Errorf("session id %q is not a single path element", sessionID)
	}
	full, err := store.FullSession(ctx, accountID, sessionID)
	if err != nil {
		return out, fmt.Errorf("session %s: %w", sessionID, err)
	}
	tr := Build(accountID, full)
	out.Trace = tr.ID
	sent := e.loadState(sessionID)
	var spans []Span
	for _, s := range tr.Spans {
		if !sent[s.ID] {
			spans = append(spans, s)
		}
	}
	if len(spans) == 0 {
		return out, nil
	}
	resource := map[string]any{"service.name": "claude-code", "telemetry.sdk.name": "clued"}
	for k, v := range e.Resource {
		resource[k] = v
	}
	if err := e.post(ctx, Request(tr, resource, spans)); err != nil {
		return out, err
	}
	for _, s := range spans {
		sent[s.ID] = true
	}
	out.Spans = len(spans)
	return out, e.saveState(sessionID, sent)
}

func (e *Exporter) url() string {
	u := strings.TrimRight(e.Endpoint, "/")
	if !strings.HasSuffix(u, "/v1/traces") {
		u += "/v1/traces"
	}
	return u
}

func (e *Exporter) post(ctx context.Context, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range e.Headers {
		req.Header.Set(k, v)
	}
	client := e.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("otlp: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return fmt.Errorf("otlp: %s answered %d: %s", e.url(), res.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

func (e *Exporter) statePath(sessionID string) string {
	return filepath.Join(e.StateDir, sessionID+".json")
}

func (e *Exporter) loadState(sessionID string) map[string]bool {
	sent := map[string]bool{}
	if e.StateDir == "" {
		return sent
	}
	raw, err := os.ReadFile(e.statePath(sessionID))
	if err != nil {
		return sent
	}
	var ids []string
	if json.Unmarshal(raw, &ids) == nil {
		for _, id := range ids {
			sent[id] = true
		}
	}
	return sent
}

func (e *Exporter) saveState(sessionID string, sent map[string]bool) error {
	if e.StateDir == "" {
		return nil
	}
	if err := os.MkdirAll(e.StateDir, 0o700); err != nil {
		return err
	}
	ids := make([]string, 0, len(sent))
	for id := range sent {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	raw, _ := json.Marshal(ids)
	tmp := e.statePath(sessionID) + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, e.statePath(sessionID))
}
