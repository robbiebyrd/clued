package sessionmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/query"
)

// budgetArgs are the arguments every budgeted tool takes besides its own.
type budgetArgs struct {
	MaxTokens int    `json:"max_tokens"`
	Cursor    string `json:"cursor"`
}

func budgetOf(req *mcp.CallToolRequest) budgetArgs {
	var b budgetArgs
	if req != nil && len(req.Params.Arguments) > 0 {
		_ = json.Unmarshal(req.Params.Arguments, &b)
	}
	return b
}

// budgetSchema adds max_tokens and cursor to a tool's input schema and
// says in its description what the default budget is.
func budgetSchema(spec toolSpec) toolSpec {
	if spec.budget == 0 {
		return spec
	}
	var schema map[string]any
	if err := json.Unmarshal([]byte(spec.schema), &schema); err != nil {
		return spec
	}
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
		schema["properties"] = props
	}
	props["max_tokens"] = map[string]any{"type": "number", "description": fmt.Sprintf("Approximate size limit of the result in tokens (default %d, at least %d)", spec.budget, query.MinTokens)}
	if spec.name != "get_session_context" { // its next is a read_transcript cursor
		props["cursor"] = map[string]any{"type": "string", "description": "The next value of a truncated result, to continue after it (the other arguments must stay the same)"}
	}
	raw, _ := json.Marshal(schema)
	spec.schema = string(raw)
	spec.description += fmt.Sprintf(" The result is cut to about %d tokens (max_tokens): a cut result says \"truncated\": true (a list becomes {\"items\", \"truncated\", \"next\"}), and its next, passed as cursor, continues it.", spec.budget)
	return spec
}

// listTool runs a tool whose result is a list, and cuts it to the budget:
// the list itself when everything fits, else a query.Page whose next
// continues after the items it holds.
func listTool[A any](t *Tools, name string, budget int, run func(ctx context.Context, q *query.Service, args A) (any, error)) mcp.ToolHandler {
	return tool(t, func(ctx context.Context, q *query.Service, args A, req *mcp.CallToolRequest) (any, error) {
		b := budgetOf(req)
		cur, err := query.DecodeCursor(b.Cursor, name)
		if err != nil {
			return nil, err
		}
		out, err := run(ctx, q, args)
		if err != nil {
			return nil, err
		}
		return page(name, out, cur.Offset, 0, query.Budget(b.MaxTokens, budget)), nil
	})
}

// page cuts list (from offset) to the budget; base is added to the next
// offset, for tools whose list starts at an offset of their own.
func page(name string, list any, offset, base, budget int) any {
	items, _ := query.Generic(list).([]any)
	if items == nil {
		items = []any{}
	}
	offset = min(offset, len(items))
	kept, truncated := query.Fit(items[offset:], budget)
	if !truncated {
		if offset == 0 {
			return list
		}
		return kept
	}
	p := query.Page{Items: kept, Truncated: true}
	if next := offset + len(kept); next < len(items) {
		p.Next = query.Cursor{Tool: name, Offset: base + next}.Encode()
	}
	return p
}

// readTranscript reads lines from the cursor's offset (else the args') and
// cuts them to the budget, the next cursor continuing at the first line
// left out.
func (t *Tools) readTranscript() mcp.ToolHandler {
	const name = "read_transcript"
	return tool(t, func(ctx context.Context, q *query.Service, args query.ReadTranscriptArgs, req *mcp.CallToolRequest) (any, error) {
		b := budgetOf(req)
		cur, err := query.DecodeCursor(b.Cursor, name)
		if err != nil {
			return nil, err
		}
		if b.Cursor != "" {
			args.Offset = cur.Offset
		}
		lines, err := q.ReadTranscript(ctx, args, progressReporter(ctx, req))
		if err != nil {
			return nil, err
		}
		return page(name, lines, 0, max(args.Offset, 0), query.Budget(b.MaxTokens, query.DefaultReadTokens)), nil
	})
}

// usage cuts the report's groups to the budget; the total still covers
// every group.
func (t *Tools) usage() mcp.ToolHandler {
	const name = "usage"
	return tool(t, func(ctx context.Context, q *query.Service, args query.UsageArgs, req *mcp.CallToolRequest) (any, error) {
		b := budgetOf(req)
		cur, err := query.DecodeCursor(b.Cursor, name)
		if err != nil {
			return nil, err
		}
		report, err := q.Usage(ctx, args)
		if err != nil {
			return nil, err
		}
		budget := query.Budget(b.MaxTokens, query.DefaultSearchTokens)
		rest := report
		rest.Groups = nil
		paged := page(name, report.Groups, cur.Offset, 0, max(budget-query.Tokens(rest), query.MinTokens))
		p, cut := paged.(query.Page)
		if !cut && cur.Offset == 0 {
			return report, nil
		}
		out, _ := query.Generic(rest).(map[string]any)
		if cut {
			out["groups"], out["truncated"] = p.Items, true
			if p.Next != "" {
				out["next"] = p.Next
			}
		} else {
			out["groups"] = paged
		}
		return out, nil
	})
}

// sessionContext trims the first and last lines of a session context that
// is over the budget, inner ends first; next is a read_transcript cursor
// at the first line left out.
func (t *Tools) sessionContext() mcp.ToolHandler {
	return tool(t, func(ctx context.Context, q *query.Service, args sessionArgs, req *mcp.CallToolRequest) (any, error) {
		c, err := q.SessionContext(ctx, args.SessionID)
		if err != nil {
			return nil, err
		}
		budget := query.Budget(budgetOf(req).MaxTokens, query.DefaultReadTokens)
		if query.Tokens(c) <= budget {
			return c, nil
		}
		first, last := c.FirstLines, c.LastLines
		for query.Tokens(c) > budget && len(c.FirstLines)+len(c.LastLines) > 0 {
			if len(c.LastLines) >= len(c.FirstLines) && len(c.LastLines) > 0 {
				c.LastLines = c.LastLines[1:]
			} else {
				c.FirstLines = c.FirstLines[:len(c.FirstLines)-1]
			}
		}
		out, _ := query.Generic(c).(map[string]any)
		if query.Tokens(out) > budget {
			clipped, _ := query.Clip(out, budget)
			out, _ = clipped.(map[string]any)
		}
		out["truncated"] = true
		if len(c.FirstLines) < len(first) || len(c.LastLines) < len(last) {
			out["next"] = query.Cursor{Tool: "read_transcript", Offset: len(c.FirstLines)}.Encode()
		}
		return out, nil
	})
}

// fullSections are the joined lists of get_full_session, in order.
var fullSections = []string{"transcript_lines", "subagent_lines", "blobs", "hook_events"}

// sectionNames maps the sections argument to the joined lists.
var sectionNames = map[string]string{
	"transcript": "transcript_lines", "subagents": "subagent_lines", "blobs": "blobs", "hook_events": "hook_events",
}

type fullArgs struct {
	SessionID string   `json:"session_id"`
	Sections  []string `json:"sections"`
}

// fullSession returns the session with the sections asked for, each cut to
// an equal share of the budget; next continues every section that was cut.
func (t *Tools) fullSession() mcp.ToolHandler {
	const name = "get_full_session"
	return tool(t, func(ctx context.Context, q *query.Service, args fullArgs, req *mcp.CallToolRequest) (any, error) {
		b := budgetOf(req)
		cur, err := query.DecodeCursor(b.Cursor, name)
		if err != nil {
			return nil, err
		}
		wanted := fullSections
		if len(args.Sections) > 0 {
			wanted = nil
			for _, s := range args.Sections {
				list, ok := sectionNames[s]
				if !ok {
					return nil, fmt.Errorf("section %q is not transcript, subagents, blobs or hook_events", s)
				}
				wanted = append(wanted, list)
			}
		}
		if b.Cursor != "" {
			wanted = nil
			for _, s := range fullSections {
				if _, ok := cur.Sections[s]; ok {
					wanted = append(wanted, s)
				}
			}
		}
		doc, err := q.FullSession(ctx, args.SessionID)
		if err != nil {
			return nil, err
		}
		budget := query.Budget(b.MaxTokens, query.DefaultReadTokens)
		meta := session.Doc{}
		for k, v := range doc {
			if !slices.Contains(fullSections, k) {
				meta[k] = v
			}
		}
		out, _ := query.Generic(meta).(map[string]any)
		share := max((budget-query.Tokens(meta))/max(len(wanted), 1), query.MinTokens)
		next, cut := map[string]int{}, false
		for _, s := range wanted {
			items, _ := query.Generic(doc[s]).([]any)
			if items == nil {
				items = []any{}
			}
			offset := min(cur.Sections[s], len(items))
			kept, truncated := query.Fit(items[offset:], share)
			out[s] = kept
			cut = cut || truncated
			if truncated && offset+len(kept) < len(items) {
				next[s] = offset + len(kept)
			}
		}
		if cut {
			out["truncated"] = true
		}
		if len(next) > 0 {
			out["next"] = query.Cursor{Tool: name, Sections: next}.Encode()
		}
		return out, nil
	})
}
