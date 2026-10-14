package query_test

import (
	"context"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
	"github.com/robbiebyrd/clued/mind-palace/session/query"
)

func TestSessionContextSumsTelemetry(t *testing.T) {
	st := memsession.New()
	must(t, st.UpsertSession(ctx, session.Session{SessionID: "s", AccountID: account}))
	svc := &query.Service{Store: st, AccountID: account}
	got, err := svc.SessionContext(ctx, "s")
	must(t, err)
	if got.Telemetry != nil {
		t.Fatalf("a session without events has telemetry %+v", got.Telemetry)
	}
	at := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	ev := func(seq int64, prompt, name string, attrs session.Doc) session.OtelEvent {
		return session.OtelEvent{AccountID: account, SessionID: "s", PromptID: prompt, Name: name, Sequence: seq, Timestamp: at.Add(time.Duration(seq) * time.Second), Attributes: attrs}
	}
	_, err = st.InsertOtelEvents(ctx, []session.OtelEvent{
		ev(1, "p1", "api_request", session.Doc{"model": "claude-opus-5-5", "cost_usd": 0.5, "input_tokens": int64(100), "output_tokens": int64(10), "cache_read_tokens": int64(1000), "duration_ms": int64(2000)}),
		ev(2, "p1", "tool_result", session.Doc{"tool_name": "Bash", "success": false, "duration_ms": int64(5)}),
		ev(3, "p1", "tool_result", session.Doc{"tool_name": "Bash", "success": true, "duration_ms": int64(7)}),
		ev(4, "p2", "tool_result", session.Doc{"tool_name": "Read", "success": true}),
		ev(5, "p2", "api_error", session.Doc{"model": "claude-fable-5-1", "status_code": int64(529), "error": "overloaded"}),
		ev(6, "p2", "api_error", session.Doc{"model": "claude-fable-5-1", "status_code": int64(500), "error": "server"}),
		ev(7, "p2", "api_request", session.Doc{"model": "claude-fable-5-1", "cost_usd": 0.25, "input_tokens": int64(50)}),
	})
	must(t, err)
	got, err = svc.SessionContext(ctx, "s")
	must(t, err)
	tel := got.Telemetry
	if tel == nil {
		t.Fatal("no telemetry")
	}
	if tel.Events != 7 || tel.Prompts != 2 || tel.APIRequests != 2 || tel.APIErrors != 2 || tel.CostUSD != 0.75 ||
		tel.InputTokens != 150 || tel.OutputTokens != 10 || tel.CacheReadTokens != 1000 || tel.APIMs != 2000 {
		t.Errorf("totals = %+v", tel)
	}
	if len(tel.Models) != 2 || tel.Models[0] != "claude-fable-5-1" {
		t.Errorf("models = %v", tel.Models)
	}
	want := []query.ToolTelemetry{{Name: "Bash", Calls: 2, Failures: 1, TotalMs: 12}, {Name: "Read", Calls: 1}}
	if len(tel.Tools) != 2 || tel.Tools[0] != want[0] || tel.Tools[1] != want[1] {
		t.Errorf("tools = %+v, want %+v", tel.Tools, want)
	}
	if tel.LastError["status_code"] != int64(500) || tel.LastError["error"] != "server" {
		t.Errorf("last error = %v, want the newest", tel.LastError)
	}

	// A store that offers no telemetry (an older clued server) leaves it out.
	svc = &query.Service{Store: noTelemetry{st}, AccountID: account}
	got, err = svc.SessionContext(ctx, "s")
	must(t, err)
	if got.Telemetry != nil {
		t.Errorf("telemetry from a store without it: %+v", got.Telemetry)
	}
}

type noTelemetry struct{ *memsession.Store }

func (noTelemetry) OtelEvents(context.Context, session.OtelQuery) ([]session.Doc, error) {
	return nil, session.ErrNotFound
}
