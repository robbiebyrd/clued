package otel_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
	"github.com/robbiebyrd/clued/mind-palace/session/otel"
	"github.com/robbiebyrd/clued/mind-palace/session/sessiontest"
)

const account = "acc"

var (
	ctx = context.Background()
	t0  = time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
)

// collector is an in-process OTLP/HTTP collector stub.
type collector struct {
	mu       sync.Mutex
	requests []map[string]any
	headers  []http.Header
	status   int
}

func newCollector(t *testing.T) (*collector, *httptest.Server) {
	c := &collector{status: http.StatusOK}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/traces" || r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.status != http.StatusOK {
			w.WriteHeader(c.status)
			return
		}
		c.requests = append(c.requests, body)
		c.headers = append(c.headers, r.Header.Clone())
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	return c, srv
}

// spans flattens the spans of every request so far.
func (c *collector) spans() []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []map[string]any
	for _, req := range c.requests {
		for _, rs := range req["resourceSpans"].([]any) {
			for _, ss := range rs.(map[string]any)["scopeSpans"].([]any) {
				for _, s := range ss.(map[string]any)["spans"].([]any) {
					out = append(out, s.(map[string]any))
				}
			}
		}
	}
	return out
}

func attr(span map[string]any, key string) any {
	for _, a := range span["attributes"].([]any) {
		kv := a.(map[string]any)
		if kv["key"] == key {
			for _, v := range kv["value"].(map[string]any) {
				return v
			}
		}
	}
	return nil
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func ev(sid, name string, at time.Duration, extra session.Doc) session.Doc {
	d := session.Doc{"session_id": sid, "account_id": account, "hook_event_name": name, "created_at": t0.Add(at)}
	for k, v := range extra {
		d[k] = v
	}
	return d
}

// seed stores a session of two turns: a Bash call and a failed Read with a
// subagent in the first, an Edit in the second, and usage on two messages.
func seed(t *testing.T, st session.Store, sid string) {
	t.Helper()
	must(t, st.UpsertSession(ctx, session.Session{SessionID: sid, AccountID: account, ProjectPath: "/repo", Cwd: "/repo", LastSeen: t0.Add(20 * time.Second)}))
	for _, e := range []session.Doc{
		ev(sid, "SessionStart", 0, nil),
		ev(sid, "UserPromptSubmit", time.Second, session.Doc{"prompt": "fix the tests"}),
		ev(sid, "PreToolUse", 2*time.Second, session.Doc{"tool_name": "Bash", "tool_use_id": "tu1"}),
		ev(sid, "PostToolUse", 3*time.Second, session.Doc{"tool_name": "Bash", "tool_use_id": "tu1", "tool_response": map[string]any{"stdout": "ok"}}),
		ev(sid, "PreToolUse", 4*time.Second, session.Doc{"tool_name": "Read", "tool_use_id": "tu2"}),
		ev(sid, "PostToolUseFailure", 5*time.Second, session.Doc{"tool_name": "Read", "tool_use_id": "tu2", "error": "no such file"}),
		ev(sid, "SubagentStart", 6*time.Second, session.Doc{"agent_id": "a1", "agent_type": "Explore"}),
		ev(sid, "SubagentStop", 8*time.Second, session.Doc{"agent_id": "a1", "agent_type": "Explore"}),
		ev(sid, "UserPromptSubmit", 10*time.Second, session.Doc{"prompt": "now commit"}),
		ev(sid, "PreToolUse", 11*time.Second, session.Doc{"tool_name": "Edit", "tool_use_id": "tu3"}),
		ev(sid, "PostToolUse", 12*time.Second, session.Doc{"tool_name": "Edit", "tool_use_id": "tu3"}),
		ev(sid, "SessionEnd", 20*time.Second, nil),
	} {
		must(t, st.InsertHookEvent(ctx, e))
	}
	usage := func(seq int, msg string, at time.Duration, in, out int64) {
		must(t, st.UpsertTranscriptLines(ctx, []session.TranscriptLine{{SessionID: sid, Seq: seq, AccountID: account,
			Line: map[string]any{"type": "assistant", "timestamp": t0.Add(at).Format(time.RFC3339Nano)}}}))
		for _, d := range must2(st.Unenriched(ctx, "transcript_lines", "usage", 0)) {
			if d["session_id"] == sid && d.Map("line")["timestamp"] == t0.Add(at).Format(time.RFC3339Nano) {
				must(t, st.SetEnrichment(ctx, "transcript_lines", d["_id"], "usage", session.Doc{
					"message_id": msg, "model": "claude-opus-5-5", "input_tokens": in, "output_tokens": out,
				}))
			}
		}
	}
	usage(0, "m1", 2500*time.Millisecond, 100, 200)
	usage(1, "m1", 2600*time.Millisecond, 100, 250) // the same message, its last block
	usage(2, "m2", 11500*time.Millisecond, 40, 60)
}

func must2[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func TestACollectorReceivesOneTracePerSessionWithASpanPerToolUse(t *testing.T) {
	sessiontest.EachStore(t, func(t *testing.T, st session.Store) {
		c, srv := newCollector(t)
		seed(t, st, "s1")
		seed(t, st, "s2")
		exp := &otel.Exporter{Endpoint: srv.URL, StateDir: t.TempDir(), Headers: map[string]string{"Authorization": "Bearer otlp-secret"}}
		for _, sid := range []string{"s1", "s2"} {
			sent, err := exp.ExportSession(ctx, st, account, sid)
			must(t, err)
			if sent.Spans != 7 { // session, 2 turns, 3 tools, 1 subagent
				t.Errorf("%s: sent %d spans, want 7", sid, sent.Spans)
			}
		}
		spans := c.spans()
		traces := map[string][]map[string]any{}
		for _, s := range spans {
			traces[s["traceId"].(string)] = append(traces[s["traceId"].(string)], s)
		}
		if len(traces) != 2 {
			t.Fatalf("got %d traces for 2 sessions", len(traces))
		}
		for id, trace := range traces {
			if len(id) != 32 {
				t.Errorf("trace id %q is not 16 bytes of hex", id)
			}
			byName := map[string][]map[string]any{}
			byID := map[string]map[string]any{}
			for _, s := range trace {
				byName[s["name"].(string)] = append(byName[s["name"].(string)], s)
				byID[s["spanId"].(string)] = s
			}
			root := byName["invoke_agent claude-code"]
			if len(root) != 1 || root[0]["parentSpanId"] != nil {
				t.Fatalf("root spans = %v", root)
			}
			tools := append(append(byName["execute_tool Bash"], byName["execute_tool Read"]...), byName["execute_tool Edit"]...)
			if len(tools) != 3 {
				t.Fatalf("tool spans = %d, want one per tool use (3)", len(tools))
			}
			turns := byName["turn"]
			if len(turns) != 2 {
				t.Fatalf("turn spans = %d, want 2", len(turns))
			}
			for _, tool := range tools {
				parent := byID[tool["parentSpanId"].(string)]
				if parent == nil || parent["name"] != "turn" {
					t.Errorf("tool %s is not under a turn", tool["name"])
				}
			}
			edit := byName["execute_tool Edit"][0]
			if byID[edit["parentSpanId"].(string)]["spanId"] == byID[byName["execute_tool Bash"][0]["parentSpanId"].(string)]["spanId"] {
				t.Error("the Edit of the second turn sits under the first")
			}
			read := byName["execute_tool Read"][0]
			if status, _ := read["status"].(map[string]any); status == nil || status["code"] != float64(2) || status["message"] != "no such file" {
				t.Errorf("the failed Read has status %v", read["status"])
			}
			if attr(read, "gen_ai.tool.call.id") != "tu2" || attr(read, "gen_ai.operation.name") != "execute_tool" {
				t.Errorf("Read attributes = %v", read["attributes"])
			}
			if sub := byName["invoke_agent Explore"]; len(sub) != 1 {
				t.Errorf("subagent spans = %d, want 1", len(sub))
			}
			if got := attr(root[0], "gen_ai.usage.output_tokens"); got != "310" { // 250 (m1 once) + 60
				t.Errorf("session output tokens = %v, want 310", got)
			}
			first := turns[0]
			if attr(first, "clued.turn.index") != "1" {
				first = turns[1]
			}
			if attr(first, "gen_ai.usage.output_tokens") != "60" || attr(first, "gen_ai.response.model") != "claude-opus-5-5" {
				t.Errorf("the second turn's usage = %v / %v", attr(first, "gen_ai.usage.output_tokens"), attr(first, "gen_ai.response.model"))
			}
		}
		if h := c.headers[0].Get("Authorization"); h != "Bearer otlp-secret" {
			t.Errorf("Authorization = %q", h)
		}
		resource := c.requests[0]["resourceSpans"].([]any)[0].(map[string]any)["resource"].(map[string]any)
		if attr(resource, "service.name") != "claude-code" {
			t.Errorf("resource = %v", resource)
		}

		// Exporting again sends nothing; a resumed session sends only its new spans.
		sent, err := exp.ExportSession(ctx, st, account, "s1")
		must(t, err)
		if sent.Spans != 0 || len(c.requests) != 2 {
			t.Errorf("a second export sent %d spans (%d requests)", sent.Spans, len(c.requests))
		}
		must(t, st.InsertHookEvent(ctx, ev("s1", "UserPromptSubmit", 30*time.Second, nil)))
		must(t, st.InsertHookEvent(ctx, ev("s1", "PreToolUse", 31*time.Second, session.Doc{"tool_name": "Grep", "tool_use_id": "tu4"})))
		must(t, st.InsertHookEvent(ctx, ev("s1", "PostToolUse", 32*time.Second, session.Doc{"tool_name": "Grep", "tool_use_id": "tu4"})))
		sent, err = exp.ExportSession(ctx, st, account, "s1")
		must(t, err)
		if sent.Spans != 2 { // the new turn and its tool
			t.Errorf("a resumed session sent %d spans, want 2", sent.Spans)
		}
	})
}

func TestAFailedSendIsRetriedNextTime(t *testing.T) {
	c, srv := newCollector(t)
	st := memsession.New()
	seed(t, st, "s1")
	exp := &otel.Exporter{Endpoint: srv.URL + "/v1/traces", StateDir: t.TempDir()}
	c.status = http.StatusServiceUnavailable
	if _, err := exp.ExportSession(ctx, st, account, "s1"); err == nil {
		t.Fatal("a 503 was not an error")
	}
	c.status = http.StatusOK
	sent, err := exp.ExportSession(ctx, st, account, "s1")
	must(t, err)
	if sent.Spans != 7 {
		t.Errorf("the retry sent %d spans, want 7", sent.Spans)
	}
	if _, err := exp.ExportSession(ctx, st, account, "../x"); err == nil {
		t.Error("an unsafe session id was accepted")
	}
	if _, err := exp.ExportSession(ctx, st, account, "missing"); err == nil {
		t.Error("a missing session was exported")
	}
}

func TestNewNeedsAnEndpoint(t *testing.T) {
	if otel.New(session.Config{}) != nil {
		t.Error("an exporter without an endpoint")
	}
	if e := otel.New(session.Config{OTelEndpoint: "http://collector:4318", WalPath: "/tmp/x/events.wal"}); e == nil || e.StateDir != "/tmp/x/otel" {
		t.Errorf("exporter = %+v", e)
	}
}
