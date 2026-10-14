package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/otlp"
)

// claudeCodeExport is a logs export as Claude Code's OTLP exporter sends it
// (http/json): one api_request and one tool_result of session sid.
func claudeCodeExport(sid string) string {
	attr := func(k, v string) string { return `{"key":"` + k + `","value":{"stringValue":"` + v + `"}}` }
	common := attr("session.id", sid) + "," + attr("prompt.id", "p-1") + "," + attr("user.email", "dev@example.com")
	return `{"resourceLogs":[{"resource":{"attributes":[` + attr("service.name", "claude-code") + `]},"scopeLogs":[{"logRecords":[` +
		`{"body":{"stringValue":"claude_code.api_request"},"attributes":[` + common + `,` + attr("event.name", "api_request") + `,` +
		attr("event.timestamp", "2026-10-14T10:00:00.000Z") + `,{"key":"event.sequence","value":{"intValue":"1"}},` +
		attr("model", "claude-opus-5-5") + `,` + attr("cost_usd", "0.02") + `,` + attr("input_tokens", "1000") + `,` +
		attr("output_tokens", "200") + `,` + attr("duration_ms", "900") + `]},` +
		`{"body":{"stringValue":"claude_code.tool_result"},"attributes":[` + common + `,` + attr("event.name", "tool_result") + `,` +
		attr("event.timestamp", "2026-10-14T10:00:01.000Z") + `,{"key":"event.sequence","value":{"intValue":"2"}},` +
		attr("tool_name", "Bash") + `,` + attr("success", "true") + `,` + attr("duration_ms", "30") + `]}` +
		`]}]}]}`
}

func postOTLP(t *testing.T, url, token, body string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

func TestCluedIngestsClaudeCodeTelemetryUnderTheTokensAccount(t *testing.T) {
	ts, _, sessions, _ := newCluedServer(t)
	ctx := context.Background()
	if err := sessions.UpsertSession(ctx, session.Session{SessionID: "s-ab", AccountID: "acct-ab", ProjectPath: "/home/ab/repo", LastSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if code := postOTLP(t, ts.URL+otlp.PathLogs, "", claudeCodeExport("s-ab")); code != http.StatusUnauthorized {
		t.Fatalf("without a token: %d", code)
	}
	if code := postOTLP(t, ts.URL+otlp.PathLogs, tokenAB, claudeCodeExport("s-ab")); code != http.StatusOK {
		t.Fatalf("export: %d", code)
	}
	// Another account names the same session: it holds no such session,
	// so nothing is stored under it.
	if code := postOTLP(t, ts.URL+otlp.PathLogs, tokenStar, claudeCodeExport("s-ab")); code != http.StatusOK {
		t.Fatalf("export as star: %d", code)
	}
	if code := postOTLP(t, ts.URL+otlp.PathMetrics, tokenAB, `{"resourceMetrics":[]}`); code != http.StatusOK {
		t.Fatalf("metrics: %d", code)
	}
	stored, err := sessions.OtelEvents(ctx, session.OtelQuery{AccountID: "acct-ab"})
	if err != nil || len(stored) != 2 {
		t.Fatalf("acct-ab holds %d events (%v), want 2", len(stored), err)
	}
	if other, _ := sessions.OtelEvents(ctx, session.OtelQuery{AccountID: "acct-star"}); len(other) != 0 {
		t.Errorf("acct-star holds %d events", len(other))
	}
	raw, _ := json.Marshal(stored)
	if strings.Contains(string(raw), "dev@example.com") {
		t.Errorf("the e-mail address was stored: %s", raw)
	}

	// get_session_context over the server's MCP endpoint reports them.
	sa, err := connectMCP(t, ts.URL+PathMCP, tokenAB, "a")
	if err != nil {
		t.Fatal(err)
	}
	res, text := toolCall(t, sa, "get_session_context", map[string]any{"session_id": "s-ab"})
	if res.IsError {
		t.Fatalf("get_session_context: %s", text)
	}
	var ctxReport struct {
		Telemetry *struct {
			Events      int     `json:"events"`
			Prompts     int     `json:"prompts"`
			APIRequests int     `json:"api_requests"`
			CostUSD     float64 `json:"cost_usd"`
			InputTokens int64   `json:"input_tokens"`
			Models      []string
			Tools       []struct {
				Name  string `json:"name"`
				Calls int    `json:"calls"`
			} `json:"tools"`
		} `json:"telemetry"`
	}
	if err := json.Unmarshal([]byte(text), &ctxReport); err != nil {
		t.Fatalf("%v: %s", err, text)
	}
	tel := ctxReport.Telemetry
	if tel == nil || tel.Events != 2 || tel.Prompts != 1 || tel.APIRequests != 1 || tel.CostUSD != 0.02 || tel.InputTokens != 1000 ||
		len(tel.Models) != 1 || tel.Models[0] != "claude-opus-5-5" || len(tel.Tools) != 1 || tel.Tools[0].Name != "Bash" || tel.Tools[0].Calls != 1 {
		t.Errorf("telemetry = %+v in %s", tel, text)
	}
}
