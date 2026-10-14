package otlp_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
	"github.com/robbiebyrd/clued/mind-palace/session/otlp"
)

// export is a logs export the way Claude Code sends it over http/json:
// event names in the body and event.name, numbers as strings, int values
// as JSON strings, standard attributes (user.email, …) beside the event's.
func export(records ...string) string {
	return `{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}},{"key":"terminal.type","value":{"stringValue":"vscode"}}]},` +
		`"scopeLogs":[{"scope":{"name":"com.anthropic.claude_code.events"},"logRecords":[` + strings.Join(records, ",") + `]}]}]}`
}

func record(name, sid string, seq int, extra ...string) string {
	attrs := []string{
		`{"key":"event.name","value":{"stringValue":"` + name + `"}}`,
		`{"key":"event.timestamp","value":{"stringValue":"2026-10-14T10:00:0` + string(rune('0'+seq)) + `.123Z"}}`,
		`{"key":"event.sequence","value":{"intValue":"` + string(rune('0'+seq)) + `"}}`,
		`{"key":"user.email","value":{"stringValue":"someone@example.com"}}`,
		`{"key":"user.account_uuid","value":{"stringValue":"acct-uuid"}}`,
		`{"key":"organization.id","value":{"stringValue":"org"}}`,
		`{"key":"workspace.host_paths","value":{"arrayValue":{"values":[{"stringValue":"/home/me/secret-project"}]}}}`,
		`{"key":"prompt.id","value":{"stringValue":"p-1"}}`,
	}
	if sid != "" {
		attrs = append(attrs, `{"key":"session.id","value":{"stringValue":"`+sid+`"}}`)
	}
	attrs = append(attrs, extra...)
	return `{"timeUnixNano":"1760436000000000000","body":{"stringValue":"claude_code.` + name + `"},"attributes":[` + strings.Join(attrs, ",") + `]}`
}

func kv(k, v string) string {
	return `{"key":"` + k + `","value":{"stringValue":` + jsonString(v) + `}}`
}

func jsonString(s string) string { b, _ := json.Marshal(s); return string(b) }

var apiRequest = record("api_request", "s-1", 1,
	kv("model", "claude-opus-5-5"), kv("cost_usd", "0.0125"), kv("duration_ms", "1500"),
	kv("input_tokens", "1200"), kv("output_tokens", "300"), kv("cache_read_tokens", "5000"),
	kv("cache_creation_tokens", "0"), kv("request_id", "req_1"), kv("agent.name", "reviewer"))

var toolResult = record("tool_result", "s-1", 2,
	kv("tool_name", "Bash"), kv("success", "false"), kv("duration_ms", "40"), kv("error_type", "exit_code"),
	kv("tool_parameters", `{"command":"cat ~/.aws/credentials"}`), kv("tool_input", "cat ~/.aws/credentials"),
	kv("error", "secret output"))

var userPrompt = record("user_prompt", "s-1", 3, kv("prompt", "my password is hunter2"), kv("prompt_length", "22"))

func decode(t *testing.T, body string) *collogs.ExportLogsServiceRequest {
	t.Helper()
	req, err := otlp.DecodeLogs(otlp.FormatJSON, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestEventsKeepTheAllowListAndNothingPersonal(t *testing.T) {
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	req := decode(t, export(apiRequest, toolResult, userPrompt, record("api_request", "", 4), record("api_request", "../etc", 5)))
	events, skipped := otlp.Events(req, "acct", now)
	if len(events) != 2 || skipped != 3 {
		t.Fatalf("got %d events, %d skipped; want 2 kept (api_request, tool_result) and 3 skipped (user_prompt, no session, bad session)", len(events), skipped)
	}
	api, tool := events[0], events[1]
	if api.AccountID != "acct" || api.SessionID != "s-1" || api.PromptID != "p-1" || api.Name != "api_request" || api.Sequence != 1 {
		t.Errorf("api_request = %+v", api)
	}
	if want := time.Date(2026, 10, 14, 10, 0, 1, 123e6, time.UTC); !api.Timestamp.Equal(want) {
		t.Errorf("timestamp = %v, want event.timestamp %v", api.Timestamp, want)
	}
	want := session.Doc{
		"model": "claude-opus-5-5", "cost_usd": 0.0125, "duration_ms": int64(1500), "input_tokens": int64(1200),
		"output_tokens": int64(300), "cache_read_tokens": int64(5000), "cache_creation_tokens": int64(0),
		"request_id": "req_1", "agent_name": "reviewer", "terminal_type": "vscode",
	}
	if got, _ := json.Marshal(api.Attributes); string(got) != mustJSON(want) {
		t.Errorf("api_request attributes = %s, want %s", got, mustJSON(want))
	}
	want = session.Doc{"tool_name": "Bash", "success": false, "duration_ms": int64(40), "error_type": "exit_code", "terminal_type": "vscode"}
	if got, _ := json.Marshal(tool.Attributes); string(got) != mustJSON(want) {
		t.Errorf("tool_result attributes = %s, want %s", got, mustJSON(want))
	}
	for _, e := range events {
		raw, _ := json.Marshal(e)
		for _, leak := range []string{"someone@example.com", "acct-uuid", "secret-project", "credentials", "hunter2", "secret output", "org"} {
			if bytes.Contains(raw, []byte(leak)) {
				t.Errorf("%s kept %q: %s", e.Name, leak, raw)
			}
		}
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestEventsFallBackToTheRecordsTimeAndEventNameField(t *testing.T) {
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	body := `{"resourceLogs":[{"resource":{"attributes":[{"key":"session.id","value":{"stringValue":"s-9"}}]},"scopeLogs":[{"logRecords":[` +
		`{"timeUnixNano":"1760436000000000000","eventName":"claude_code.api_error","attributes":[{"key":"status_code","value":{"intValue":"529"}},{"key":"error","value":{"stringValue":"` + strings.Repeat("x", 3000) + `"}}]},` +
		`{"eventName":"api_request"}` +
		`]}]}]}`
	events, _ := otlp.Events(decode(t, body), "", now)
	if len(events) != 2 {
		t.Fatalf("got %d events", len(events))
	}
	if e := events[0]; e.Name != "api_error" || e.SessionID != "s-9" || !e.Timestamp.Equal(time.Unix(0, 1760436000000000000)) {
		t.Errorf("api_error = %+v (session from the resource, time from the record)", e)
	}
	if code := events[0].Attributes["status_code"]; code != int64(529) {
		t.Errorf("status_code = %#v", code)
	}
	if msg, _ := events[0].Attributes["error"].(string); len(msg) > 2100 {
		t.Errorf("error kept %d bytes, want it cut", len(msg))
	}
	if !events[1].Timestamp.Equal(now) {
		t.Errorf("a record without time takes now: %v", events[1].Timestamp)
	}
}

func TestBodyAndFormat(t *testing.T) {
	for ct, want := range map[string]otlp.Format{"application/json": otlp.FormatJSON, "application/json; charset=utf-8": otlp.FormatJSON, "application/x-protobuf": otlp.FormatProtobuf} {
		if f, err := otlp.FormatOf(ct); err != nil || f != want {
			t.Errorf("FormatOf(%q) = %v, %v", ct, f, err)
		}
	}
	if _, err := otlp.FormatOf("text/plain"); !errors.Is(err, otlp.ErrUnsupportedType) {
		t.Errorf("text/plain: %v", err)
	}
	var zipped bytes.Buffer
	zw := gzip.NewWriter(&zipped)
	zw.Write([]byte(strings.Repeat("a", 100)))
	zw.Close()
	if raw, err := otlp.Body(bytes.NewReader(zipped.Bytes()), "gzip", 100); err != nil || len(raw) != 100 {
		t.Errorf("gzip within the limit: %d bytes, %v", len(raw), err)
	}
	if _, err := otlp.Body(bytes.NewReader(zipped.Bytes()), "gzip", 99); !errors.Is(err, otlp.ErrTooLarge) {
		t.Errorf("the limit applies after gunzip: %v", err)
	}
	if _, err := otlp.Body(strings.NewReader("x"), "br", 10); err == nil {
		t.Error("brotli accepted")
	}
}

// failing is a store whose OpenTelemetry writes fail.
type failing struct{ *memsession.Store }

func (failing) InsertOtelEvents(context.Context, []session.OtelEvent) (int, error) {
	return 0, errors.New("disk full")
}

func post(t *testing.T, h http.Handler, path, contentType string, body []byte, header ...string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	r.Header.Set("Content-Type", contentType)
	for i := 0; i+1 < len(header); i += 2 {
		r.Header.Set(header[i], header[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestHandlerStoresKnownSessionsOnceInEitherEncoding(t *testing.T) {
	ctx := context.Background()
	store := memsession.New()
	if err := store.UpsertSession(ctx, session.Session{SessionID: "s-1", AccountID: "acct"}); err != nil {
		t.Fatal(err)
	}
	h := otlp.Handler(otlp.Options{Store: store, Account: func(r *http.Request) (string, bool) {
		a := r.Header.Get("X-Account")
		return a, a != ""
	}})
	body := []byte(export(apiRequest, toolResult, record("api_request", "s-unknown", 6)))

	w := post(t, h, otlp.PathLogs, otlp.ContentJSON, body, "X-Account", "acct")
	if w.Code != 200 || w.Header().Get("Content-Type") != otlp.ContentJSON || strings.TrimSpace(w.Body.String()) != "{}" {
		t.Fatalf("json export: %d %s %q", w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}
	stored, _ := store.OtelEvents(ctx, session.OtelQuery{AccountID: "acct"})
	if len(stored) != 2 {
		t.Fatalf("stored %d events, want the 2 of the known session", len(stored))
	}
	// The same records again as protobuf, gzip-compressed: nothing new.
	pb, err := proto.Marshal(decode(t, string(body)))
	if err != nil {
		t.Fatal(err)
	}
	var zipped bytes.Buffer
	zw := gzip.NewWriter(&zipped)
	zw.Write(pb)
	zw.Close()
	w = post(t, h, otlp.PathLogs, otlp.ContentProtobuf, zipped.Bytes(), "X-Account", "acct", "Content-Encoding", "gzip")
	if w.Code != 200 || w.Header().Get("Content-Type") != otlp.ContentProtobuf {
		t.Fatalf("protobuf export: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	var res collogs.ExportLogsServiceResponse
	if err := proto.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Errorf("protobuf response: %v", err)
	}
	if stored, _ := store.OtelEvents(ctx, session.OtelQuery{AccountID: "acct"}); len(stored) != 2 {
		t.Errorf("a resent export stored again: %d events", len(stored))
	}
	// Another account holds no session s-1: nothing is stored for it.
	post(t, h, otlp.PathLogs, otlp.ContentJSON, body, "X-Account", "other")
	if stored, _ := store.OtelEvents(ctx, session.OtelQuery{AccountID: "other"}); len(stored) != 0 {
		t.Errorf("stored %d events for an account without the session", len(stored))
	}
}

func TestHandlerErrors(t *testing.T) {
	ctx := context.Background()
	store := memsession.New()
	store.UpsertSession(ctx, session.Session{SessionID: "s-1"})
	h := otlp.Handler(otlp.Options{Store: store, MaxBody: 1 << 16})
	body := []byte(export(apiRequest))
	status := func(w *httptest.ResponseRecorder) string {
		var s struct {
			Message string `json:"message"`
		}
		json.Unmarshal(w.Body.Bytes(), &s)
		return s.Message
	}
	for _, tc := range []struct {
		name, path, ct string
		body           []byte
		code           int
	}{
		{"unknown content type", otlp.PathLogs, "text/plain", body, 415},
		{"not OTLP", otlp.PathLogs, otlp.ContentJSON, []byte(`{"resourceLogs": 5}`), 400},
		{"too large", otlp.PathLogs, otlp.ContentJSON, bytes.Repeat([]byte(" "), 1<<17), 413},
		{"metrics are accepted", otlp.PathMetrics, otlp.ContentJSON, []byte(`{"resourceMetrics":[{"scopeMetrics":[{"metrics":[{"name":"claude_code.cost.usage","sum":{"dataPoints":[{"asDouble":0.5}]}}]}]}]}`), 200},
		{"no such path", "/v1/traces", otlp.ContentJSON, body, 404},
	} {
		w := post(t, h, tc.path, tc.ct, tc.body)
		if w.Code != tc.code {
			t.Errorf("%s: %d %s, want %d", tc.name, w.Code, w.Body.String(), tc.code)
		}
		if tc.code >= 400 && tc.code != 404 && status(w) == "" {
			t.Errorf("%s: no google.rpc.Status message in %s", tc.name, w.Body.String())
		}
	}
	if stored, _ := store.OtelEvents(ctx, session.OtelQuery{}); len(stored) != 0 {
		t.Errorf("metrics or a failed export stored %d events", len(stored))
	}
	r := httptest.NewRequest(http.MethodGet, otlp.PathLogs, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 405 {
		t.Errorf("GET: %d", w.Code)
	}
	// A failing store is 503, which an exporter retries.
	h = otlp.Handler(otlp.Options{Store: failing{store}})
	if w := post(t, h, otlp.PathLogs, otlp.ContentJSON, body); w.Code != 503 || !strings.Contains(status(w), "disk full") {
		t.Errorf("store failure: %d %s", w.Code, w.Body.String())
	}
	// Without an account the handler answers 401.
	h = otlp.Handler(otlp.Options{Store: store, Account: func(*http.Request) (string, bool) { return "", false }})
	if w := post(t, h, otlp.PathLogs, otlp.ContentJSON, body); w.Code != 401 {
		t.Errorf("no account: %d", w.Code)
	}
}

func TestProtojsonAcceptsOTLPsHexIDs(t *testing.T) {
	// OTLP/JSON encodes trace and span ids as hex, which protojson reads as
	// base64: it must still decode (the ids are not used).
	body := `{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"traceId":"5b8efff798038103d269b633813fc60c","spanId":"eee19b7ec3c1b174","eventName":"api_request"}]}]}]}`
	if _, err := otlp.DecodeLogs(otlp.FormatJSON, []byte(body)); err != nil {
		t.Fatal(err)
	}
	if _, err := protojson.Marshal(&collogs.ExportLogsServiceResponse{}); err != nil {
		t.Fatal(err)
	}
}
