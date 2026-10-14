// Package otel sends captured sessions to OpenTelemetry tooling (Grafana,
// Langfuse, Phoenix, any OTLP collector) as traces: one trace per session,
// a root span for the session, a span per prompt turn, and below each turn
// a span per tool use and per subagent, with token usage on the turns and
// the session. Spans follow the OpenTelemetry GenAI semantic conventions
// (gen_ai.*). They are sent as OTLP/HTTP JSON to <endpoint>/v1/traces; ids
// derive from the account and session, and a state file records the spans
// sent, so exporting a session again sends only what is new.
package otel

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// Span is one span of a session's trace.
type Span struct {
	ID       string
	ParentID string
	Name     string
	Start    time.Time
	End      time.Time
	Attrs    map[string]any
	Error    string // the status message of a failed span; empty when it did not fail
}

// Trace is a session's spans; Spans[0] is the session's root span.
type Trace struct {
	ID    string
	Spans []Span
}

// id derives a hex id of n bytes from parts.
func id(n int, parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:n])
}

// event is a hook event with its time.
type event struct {
	doc session.Doc
	at  time.Time
}

func (e event) name() string { s, _ := e.doc.String("hook_event_name"); return s }
func (e event) str(k string) string {
	s, _ := e.doc.String(k)
	return s
}

// Build maps a session (as Store.FullSession returns it) to its trace. A
// turn runs from its UserPromptSubmit to the next one, or to the session's
// last event; a tool span from PreToolUse to the PostToolUse (or
// PostToolUseFailure) with the same tool_use_id; a subagent span from its
// SubagentStart, when there is one, to its SubagentStop.
func Build(accountID string, full session.Doc) Trace {
	sid, _ := full.String("session_id")
	traceID := id(16, "trace", accountID, sid)
	events := hookEvents(full)
	root := Span{ID: id(8, traceID, "session"), Name: "invoke_agent claude-code", Attrs: map[string]any{
		"gen_ai.operation.name":  "invoke_agent",
		"gen_ai.agent.name":      "claude-code",
		"gen_ai.conversation.id": sid,
	}}
	for _, k := range []string{"project_path", "git_origin", "git_branch", "cwd"} {
		if v, ok := full.String(k); ok && v != "" {
			root.Attrs["clued."+k] = v
		}
	}
	if len(events) > 0 {
		root.Start, root.End = events[0].at, events[len(events)-1].at
	} else {
		root.Start = timeOf(full["started_at"])
		root.End = timeOf(full["last_seen"])
	}
	tr := Trace{ID: traceID, Spans: []Span{root}}

	// Turns.
	type turn struct {
		span  Span
		index int
	}
	var turns []*turn
	for i, e := range events {
		if e.name() != "UserPromptSubmit" {
			continue
		}
		end := root.End
		for _, later := range events[i+1:] {
			if later.name() == "UserPromptSubmit" {
				end = later.at
				break
			}
		}
		n := len(turns)
		prompt := e.str("prompt")
		turns = append(turns, &turn{index: n, span: Span{
			ID: id(8, traceID, "turn", fmt.Sprint(n)), ParentID: root.ID, Name: "turn", Start: e.at, End: end,
			Attrs: map[string]any{"gen_ai.conversation.id": sid, "clued.turn.index": n, "clued.prompt.chars": len([]rune(prompt))},
		}})
	}
	parentAt := func(at time.Time) string {
		parent := root.ID
		for _, t := range turns {
			if !at.Before(t.span.Start) {
				parent = t.span.ID
			}
		}
		return parent
	}

	// Tool uses.
	starts := map[string]event{}
	for _, e := range events {
		switch e.name() {
		case "PreToolUse":
			if use := e.str("tool_use_id"); use != "" {
				starts[use] = e
			}
		case "PostToolUse", "PostToolUseFailure":
			use := e.str("tool_use_id")
			start, ok := starts[use]
			if !ok {
				start = e
			}
			delete(starts, use)
			name := e.str("tool_name")
			s := Span{ID: id(8, traceID, "tool", use, e.at.String()), ParentID: parentAt(start.at), Name: "execute_tool " + name,
				Start: start.at, End: e.at, Attrs: map[string]any{
					"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": name, "gen_ai.tool.call.id": use,
				}}
			if msg := toolError(e); msg != "" {
				s.Error = msg
				s.Attrs["error.type"] = "tool_error"
			}
			tr.Spans = append(tr.Spans, s)
		}
	}
	// A PreToolUse never answered (the session ended mid-call).
	for use, start := range starts {
		name := start.str("tool_name")
		tr.Spans = append(tr.Spans, Span{ID: id(8, traceID, "tool", use, "open"), ParentID: parentAt(start.at), Name: "execute_tool " + name,
			Start: start.at, End: start.at, Error: "no result", Attrs: map[string]any{
				"gen_ai.operation.name": "execute_tool", "gen_ai.tool.name": name, "gen_ai.tool.call.id": use, "error.type": "no_result",
			}})
	}

	// Subagents.
	var subStarts []event
	for _, e := range events {
		switch e.name() {
		case "SubagentStart":
			subStarts = append(subStarts, e)
		case "SubagentStop":
			start := e
			if n := len(subStarts); n > 0 {
				start, subStarts = subStarts[n-1], subStarts[:n-1]
			}
			agent := e.str("agent_type")
			if agent == "" {
				agent = "subagent"
			}
			tr.Spans = append(tr.Spans, Span{ID: id(8, traceID, "subagent", e.str("agent_id"), e.at.String()), ParentID: parentAt(start.at),
				Name: "invoke_agent " + agent, Start: start.at, End: e.at, Attrs: map[string]any{
					"gen_ai.operation.name": "invoke_agent", "gen_ai.agent.name": agent, "gen_ai.conversation.id": sid,
				}})
		}
	}

	// Usage, each message once (its largest counters), on its turn and the
	// session.
	type msgUsage struct {
		at                                    time.Time
		model                                 string
		input, output, cacheRead, cacheCreate int64
	}
	messages := map[string]*msgUsage{}
	for _, l := range docs(full["transcript_lines"]) {
		u := l.Map("enrichments").Map("usage")
		mid, _ := u.String("message_id")
		if mid == "" {
			continue
		}
		m := messages[mid]
		if m == nil {
			m = &msgUsage{at: timeOf(l.Line()["timestamp"])}
			if m.at.IsZero() {
				m.at = timeOf(l["created_at"])
			}
			messages[mid] = m
		}
		m.model, _ = u.String("model")
		m.input = max(m.input, num(u["input_tokens"]))
		m.output = max(m.output, num(u["output_tokens"]))
		m.cacheRead = max(m.cacheRead, num(u["cache_read_input_tokens"]))
		m.cacheCreate = max(m.cacheCreate, num(u["cache_creation_input_tokens"]))
	}
	add := func(attrs map[string]any, m *msgUsage) {
		attrs["gen_ai.usage.input_tokens"] = numAttr(attrs["gen_ai.usage.input_tokens"]) + m.input
		attrs["gen_ai.usage.output_tokens"] = numAttr(attrs["gen_ai.usage.output_tokens"]) + m.output
		attrs["gen_ai.usage.cache_read.input_tokens"] = numAttr(attrs["gen_ai.usage.cache_read.input_tokens"]) + m.cacheRead
		attrs["gen_ai.usage.cache_creation.input_tokens"] = numAttr(attrs["gen_ai.usage.cache_creation.input_tokens"]) + m.cacheCreate
		if m.model != "" {
			attrs["gen_ai.response.model"] = m.model
		}
	}
	for _, m := range messages {
		add(tr.Spans[0].Attrs, m)
		for _, t := range turns {
			if !m.at.Before(t.span.Start) && (m.at.Before(t.span.End) || t.span.End.Equal(root.End)) {
				add(t.span.Attrs, m)
			}
		}
	}
	for _, t := range turns {
		tr.Spans = append(tr.Spans, t.span)
	}
	return tr
}

func toolError(e event) string {
	if e.name() == "PostToolUseFailure" {
		if msg := e.str("error"); msg != "" {
			return msg
		}
		return "tool failed"
	}
	resp := e.doc.Map("tool_response")
	if isErr, _ := resp["is_error"].(bool); isErr {
		return "tool failed"
	}
	if msg, ok := resp.String("error"); ok && msg != "" {
		return msg
	}
	return ""
}

func hookEvents(full session.Doc) []event {
	var out []event
	for _, d := range docs(full["hook_events"]) {
		at := timeOf(d["created_at"])
		if at.IsZero() {
			continue
		}
		out = append(out, event{doc: d, at: at})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].at.Before(out[j].at) })
	return out
}

func docs(v any) []session.Doc {
	switch list := v.(type) {
	case []session.Doc:
		return list
	case []any:
		out := make([]session.Doc, 0, len(list))
		for _, e := range list {
			switch d := e.(type) {
			case session.Doc:
				out = append(out, d)
			case map[string]any:
				out = append(out, d)
			}
		}
		return out
	}
	return nil
}

func timeOf(v any) time.Time {
	switch t := v.(type) {
	case time.Time:
		return t
	case string:
		at, _ := time.Parse(time.RFC3339Nano, t)
		return at
	}
	return time.Time{}
}

func num(v any) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int32:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return 0
}

func numAttr(v any) int64 { n, _ := v.(int64); return n }
