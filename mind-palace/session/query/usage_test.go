package query_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/enrich"
	"github.com/robbiebyrd/clued/mind-palace/session/enrichers"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
	"github.com/robbiebyrd/clued/mind-palace/session/query"
)

// assistant is a transcript line as Claude Code writes it: one per content
// block, each repeating the message's id, model and usage.
func assistant(id, model, timestamp string, input, output float64) map[string]any {
	return map[string]any{
		"type": "assistant", "timestamp": timestamp,
		"message": map[string]any{"id": id, "model": model, "role": "assistant",
			"usage": map[string]any{"input_tokens": input, "cache_read_input_tokens": 1000.0, "output_tokens": output}},
	}
}

func seedUsage(t *testing.T) session.Store {
	t.Helper()
	st := memsession.New()
	seedSession(t, st, account, "s1", "/repo/a", "git@github.com:o/a.git", t0)
	seedSession(t, st, account, "s2", "/repo/b", "git@github.com:o/b.git", t0)
	seedSession(t, st, foreign, "s3", "/repo/a", "git@github.com:o/a.git", t0)
	lines := []session.TranscriptLine{
		// One message over three lines (thinking, text, tool use); the
		// output count grows as it streams.
		{SessionID: "s1", Seq: 0, AccountID: account, Line: assistant("m1", "claude-sonnet-4-5", "2026-10-01T10:00:00Z", 100, 1)},
		{SessionID: "s1", Seq: 1, AccountID: account, Line: assistant("m1", "claude-sonnet-4-5", "2026-10-01T10:00:01Z", 100, 1)},
		{SessionID: "s1", Seq: 2, AccountID: account, Line: assistant("m1", "claude-sonnet-4-5", "2026-10-01T10:00:02Z", 100, 400)},
		{SessionID: "s1", Seq: 3, AccountID: account, Line: map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "go on"}}},
		{SessionID: "s1", Seq: 4, AccountID: account, Line: assistant("m2", "claude-future-9", "2026-10-02T09:00:00Z", 50, 60)},
		{SessionID: "s2", Seq: 0, AccountID: account, Line: assistant("m3", "claude-haiku-4-5", "2026-10-02T11:00:00Z", 1000, 2000)},
		{SessionID: "s3", Seq: 0, AccountID: foreign, Line: assistant("m4", "claude-sonnet-4-5", "2026-10-02T11:00:00Z", 1e6, 1e6)},
	}
	must(t, st.UpsertTranscriptLines(ctx, lines))
	must(t, enrich.Pass(ctx, st, []session.Enricher{enrichers.Usage}, slog.New(slog.NewTextHandler(io.Discard, nil))))
	return st
}

func usage(t *testing.T, svc *query.Service, args query.UsageArgs) query.UsageReport {
	t.Helper()
	r, err := svc.Usage(ctx, args)
	must(t, err)
	return r
}

func cost(c *float64) any {
	if c == nil {
		return nil
	}
	return *c
}

func TestUsageCountsASplitMessageOnce(t *testing.T) {
	svc := &query.Service{Store: seedUsage(t), AccountID: account}
	r := usage(t, svc, query.UsageArgs{SessionID: "s1"})
	if len(r.Groups) != 1 || r.Groups[0].Key != "s1" {
		t.Fatalf("groups = %+v", r.Groups)
	}
	g := r.Groups[0]
	if g.Messages != 2 || g.InputTokens != 150 || g.CacheReadInputTokens != 2000 || g.OutputTokens != 460 || g.PeakContextTokens != 1100 {
		t.Errorf("s1 = %+v", g.UsageCounts)
	}
	// Only the sonnet message is priced: (100 + 0.1×1000)×$3 + 400×$15 per million.
	if got := cost(g.CostUSD); got != 0.0066 {
		t.Errorf("s1 cost = %v, want 0.0066", got)
	}
	if len(g.Models) != 2 || g.Models[0].Model != "claude-future-9" || g.Models[0].CostUSD != nil || g.Models[1].Messages != 1 {
		t.Errorf("s1 models = %+v", g.Models)
	}
	if len(r.UnpricedModels) != 1 || r.UnpricedModels[0] != "claude-future-9" {
		t.Errorf("unpriced = %v", r.UnpricedModels)
	}
}

func TestUsageGroupsBySessionModelAndDay(t *testing.T) {
	svc := &query.Service{Store: seedUsage(t), AccountID: account}
	keys := func(r query.UsageReport) []string {
		var out []string
		for _, g := range r.Groups {
			out = append(out, g.Key)
		}
		return out
	}
	bySession := usage(t, svc, query.UsageArgs{})
	if got := keys(bySession); len(got) != 2 || got[0] != "s2" || got[1] != "s1" { // costliest first: $0.0111, $0.0066
		t.Errorf("by session = %v", got)
	}
	if bySession.Total.Messages != 3 || bySession.Total.OutputTokens != 2460 {
		t.Errorf("total = %+v", bySession.Total)
	}
	byModel := usage(t, svc, query.UsageArgs{GroupBy: "model"})
	if got := keys(byModel); len(got) != 3 || got[0] != "claude-haiku-4-5" || got[2] != "claude-future-9" {
		t.Errorf("by model = %v", got)
	}
	if byModel.Groups[0].Models != nil {
		t.Errorf("by model carries a model breakdown: %+v", byModel.Groups[0].Models)
	}
	byDay := usage(t, svc, query.UsageArgs{GroupBy: "day"})
	if got := keys(byDay); len(got) != 2 || got[0] != "2026-10-01" || got[1] != "2026-10-02" || byDay.Groups[1].Messages != 2 {
		t.Errorf("by day = %v / %+v", got, byDay.Groups)
	}
	since := usage(t, svc, query.UsageArgs{GroupBy: "day", Since: "2026-10-02", GitOrigin: "o/b"})
	if got := keys(since); len(got) != 1 || got[0] != "2026-10-02" || since.Groups[0].Messages != 1 {
		t.Errorf("since and repository = %+v", since.Groups)
	}
	if got := usage(t, svc, query.UsageArgs{Limit: 1}); len(got.Groups) != 1 || got.Total.Messages != 3 {
		t.Errorf("limit = %+v", got)
	}
	if got := usage(t, svc, query.UsageArgs{SessionID: "s3"}); len(got.Groups) != 0 || got.Total.Messages != 0 {
		t.Errorf("another account's session = %+v", got)
	}
}

func TestUsagePricesFromTheConfigAndRefusesBadArguments(t *testing.T) {
	svc := &query.Service{Store: seedUsage(t), AccountID: account, Prices: map[string]session.ModelPrice{"claude-future": {Input: 10, Output: 100}}}
	r := usage(t, svc, query.UsageArgs{GroupBy: "model"})
	if len(r.UnpricedModels) != 0 {
		t.Errorf("unpriced = %v", r.UnpricedModels)
	}
	for _, g := range r.Groups {
		if g.Key == "claude-future-9" && cost(g.CostUSD) != 0.0075 { // (50 + 0.1×1000)×$10 + 60×$100 per million
			t.Errorf("configured price: cost = %v", cost(g.CostUSD))
		}
	}
	if _, err := svc.Usage(ctx, query.UsageArgs{GroupBy: "week"}); err == nil {
		t.Error("group_by week accepted")
	}
	if _, err := svc.Usage(ctx, query.UsageArgs{Since: "yesterday"}); err == nil {
		t.Error("since yesterday accepted")
	}
}

func TestSessionContextCarriesTheSessionsUsage(t *testing.T) {
	svc := &query.Service{Store: seedUsage(t), AccountID: account}
	c, err := svc.SessionContext(ctx, "s2")
	must(t, err)
	if c.Usage == nil || c.Usage.Key != "s2" || c.Usage.OutputTokens != 2000 {
		t.Fatalf("usage = %+v", c.Usage)
	}
	seedSession(t, svc.Store, account, "s4", "/repo/c", "", t0)
	c, err = svc.SessionContext(ctx, "s4")
	must(t, err)
	b, _ := json.Marshal(c)
	var out map[string]any
	must(t, json.Unmarshal(b, &out))
	if v, present := out["usage"]; !present || v != nil {
		t.Errorf("usage of a session without usage = %v (present %v)", v, present)
	}
}
