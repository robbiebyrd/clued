package query

import (
	"context"
	"errors"
	"sort"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/otlp"
)

// telemetryScan bounds the OpenTelemetry events summed for one session.
const telemetryScan = 10000

// telemetryTools bounds the tools a telemetry summary lists.
const telemetryTools = 10

// Telemetry sums the OpenTelemetry events Claude Code exported for a
// session (session.OtelEvent): its API requests with their cost, tokens
// and time, its API errors, and its tool calls by tool. Truncated is set
// when the session holds more events than were summed.
type Telemetry struct {
	Events              int     `json:"events"`
	Prompts             int     `json:"prompts"`
	APIRequests         int     `json:"api_requests"`
	APIErrors           int     `json:"api_errors"`
	CostUSD             float64 `json:"cost_usd"`
	InputTokens         int64   `json:"input_tokens"`
	OutputTokens        int64   `json:"output_tokens"`
	CacheReadTokens     int64   `json:"cache_read_tokens"`
	CacheCreationTokens int64   `json:"cache_creation_tokens"`
	// APIMs is the time spent in API requests, in milliseconds.
	APIMs  int64    `json:"api_ms"`
	Models []string `json:"models"`
	// Tools are the tools called, most calls first (at most ten).
	Tools []ToolTelemetry `json:"tools"`
	// LastError is the newest API error: model, status_code, error and
	// timestamp.
	LastError session.Doc `json:"last_error,omitempty"`
	Truncated bool        `json:"truncated,omitempty"`
}

// ToolTelemetry is one tool's calls in a session's telemetry.
type ToolTelemetry struct {
	Name     string `json:"name"`
	Calls    int    `json:"calls"`
	Failures int    `json:"failures"`
	TotalMs  int64  `json:"total_ms"`
}

// telemetry sums the session's OpenTelemetry events; nil when there are
// none, or when the store has no such events to offer (a clued server
// older than the ingest answers not found).
func (s *Service) telemetry(ctx context.Context, sessionID string) (*Telemetry, error) {
	events, err := s.Store.OtelEvents(ctx, session.OtelQuery{AccountID: s.AccountID, SessionIDs: []string{sessionID}, Limit: telemetryScan})
	if errors.Is(err, session.ErrNotFound) || errors.Is(err, session.ErrUnsupported) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, nil
	}
	t := &Telemetry{Events: len(events), Models: []string{}, Tools: []ToolTelemetry{}, Truncated: len(events) >= telemetryScan}
	prompts, models, tools := map[string]bool{}, map[string]bool{}, map[string]*ToolTelemetry{}
	// Events come newest first, so the first error seen is the last one.
	for _, e := range events {
		if p, _ := e.String("prompt_id"); p != "" {
			prompts[p] = true
		}
		a := e.Map("attributes")
		if m, _ := a.String("model"); m != "" {
			models[m] = true
		}
		switch name, _ := e.String("name"); name {
		case otlp.EventAPIRequest:
			t.APIRequests++
			t.CostUSD += toFloat64(a["cost_usd"])
			t.InputTokens += toInt64(a["input_tokens"])
			t.OutputTokens += toInt64(a["output_tokens"])
			t.CacheReadTokens += toInt64(a["cache_read_tokens"])
			t.CacheCreationTokens += toInt64(a["cache_creation_tokens"])
			t.APIMs += toInt64(a["duration_ms"])
		case otlp.EventAPIError:
			t.APIErrors++
			if t.LastError == nil {
				t.LastError = session.Doc{"model": a["model"], "status_code": a["status_code"], "error": a["error"], "timestamp": e["timestamp"]}
			}
		case otlp.EventToolResult:
			tool, _ := a.String("tool_name")
			if tool == "" {
				continue
			}
			tt := tools[tool]
			if tt == nil {
				tt = &ToolTelemetry{Name: tool}
				tools[tool] = tt
			}
			tt.Calls++
			if ok, isBool := a["success"].(bool); isBool && !ok {
				tt.Failures++
			}
			tt.TotalMs += toInt64(a["duration_ms"])
		}
	}
	t.Prompts = len(prompts)
	for m := range models {
		t.Models = append(t.Models, m)
	}
	sort.Strings(t.Models)
	for _, tt := range tools {
		t.Tools = append(t.Tools, *tt)
	}
	sort.Slice(t.Tools, func(i, j int) bool {
		if t.Tools[i].Calls != t.Tools[j].Calls {
			return t.Tools[i].Calls > t.Tools[j].Calls
		}
		return t.Tools[i].Name < t.Tools[j].Name
	})
	if len(t.Tools) > telemetryTools {
		t.Tools = t.Tools[:telemetryTools]
	}
	return t, nil
}

// toFloat64 reads a stored number whatever its decoded type.
func toFloat64(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int32:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}
