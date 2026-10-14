package sessionmcp_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/query"
)

// bigSession seeds a session of 60 lines of about 400 characters, one line
// of 20,000 characters (seq 60), 40 Bash commands, subagent lines and hook
// events.
func bigSession(t *testing.T) *mcp.ClientSession {
	t.Helper()
	st := storeUnderTest
	ctx := context.Background()
	seedSession(t, st, account, "big", "/home/user/big", "https://github.com/user/big.git")
	lines := make([]session.TranscriptLine, 61)
	for i := range lines {
		text := fmt.Sprintf("line %d ", i) + strings.Repeat("lorem ipsum ", 33)
		if i == 60 {
			text = strings.Repeat("x", 20000)
		}
		lines[i] = session.TranscriptLine{SessionID: "big", Seq: i, AccountID: account, Line: map[string]any{"type": "user", "text": text}}
	}
	must(t, st.UpsertTranscriptLines(ctx, lines))
	for i := range 40 {
		seedBash(t, st, account, "big", fmt.Sprintf("make target-%02d %s", i, strings.Repeat("-v ", 30)), t0.Add(time.Duration(i)*time.Second))
	}
	for i := range 30 {
		must(t, st.UpsertSubagentLine(ctx, session.SubagentLine{SessionID: "big", SubagentID: "agent", Seq: i, AccountID: account,
			Line: map[string]any{"text": strings.Repeat("sub ", 100)}}))
	}
	return connectClient(t, newTools(st, session.Config{}).Server(), nil)
}

// follow calls tool from its first page until next runs out, checking each
// page against the budget, and returns every item in order.
func follow(t *testing.T, cs *mcp.ClientSession, tool string, args obj, budget int) []any {
	t.Helper()
	var all []any
	for page := 0; ; page++ {
		if page > 100 {
			t.Fatalf("%s: no end after 100 pages", tool)
		}
		args["max_tokens"] = budget
		res := call(t, cs, tool, args)
		raw := text(t, res)
		if res.IsError {
			t.Fatalf("%s: %s", tool, raw)
		}
		if got := (len(raw) + 3) / 4; got > budget+budget/10 {
			t.Errorf("%s page %d: about %d tokens, over the budget of %d", tool, page, got, budget)
		}
		var list []any
		if okList := strings.HasPrefix(raw, "["); okList {
			list = okJSON[[]any](t, res)
			return append(all, list...)
		}
		p := okJSON[obj](t, res)
		if p["truncated"] != true {
			t.Fatalf("%s: an object page that is not truncated: %s", tool, raw)
		}
		items, _ := p["items"].([]any)
		if len(items) == 0 {
			t.Fatalf("%s: a truncated page without items", tool)
		}
		all = append(all, items...)
		next, _ := p["next"].(string)
		if next == "" {
			return all
		}
		args["cursor"] = next
		t.Logf("%s page %d: %d items, %d bytes", tool, page, len(items), len(raw))
	}
}

func TestReadTranscriptHonoursTheBudgetAndContinuesWithTheCursor(t *testing.T) {
	eachStore(t, func(t *testing.T) {
		cs := bigSession(t)
		lines := follow(t, cs, "read_transcript", obj{"session_id": "big", "limit": 500}, 1000)
		if len(lines) != 61 {
			t.Fatalf("got %d lines over the pages, want 61", len(lines))
		}
		for i, l := range lines {
			if seq := l.(obj)["seq"]; seq != float64(i) {
				t.Fatalf("line %d has seq %v: a page skipped or repeated lines", i, seq)
			}
		}
		// The 20,000-character line comes back clipped rather than not at all.
		last := lines[60].(obj)["line"].(obj)["text"].(string)
		if len(last) >= 20000 || !strings.Contains(last, "characters clipped]") {
			t.Errorf("the oversized line was not clipped: %d characters", len(last))
		}
		// Everything fits the default budget? No: the default cuts too.
		res := okJSON[obj](t, call(t, cs, "read_transcript", obj{"session_id": "big", "limit": 500}))
		if res["truncated"] != true || res["next"] == nil {
			t.Errorf("the default budget did not cut 61 long lines: %v", res["truncated"])
		}
	})
}

func TestListToolsPageUnderTheBudget(t *testing.T) {
	eachStore(t, func(t *testing.T) {
		cs := bigSession(t)
		commands := follow(t, cs, "search_commands", obj{"pattern": "make", "limit": 40}, 250)
		if len(commands) != 40 {
			t.Fatalf("got %d commands over the pages, want 40", len(commands))
		}
		seen := map[string]bool{}
		for _, c := range commands {
			cmd := c.(obj)["command"].(string)
			if seen[cmd] {
				t.Fatalf("command %q came twice", cmd)
			}
			seen[cmd] = true
		}
		// A list that fits keeps its plain shape.
		if raw := text(t, call(t, cs, "search_commands", obj{"pattern": "target-01"})); !strings.HasPrefix(raw, "[") {
			t.Errorf("a result that fits is not a list: %s", raw)
		}
	})
}

func TestACursorFromAnotherToolIsRefused(t *testing.T) {
	eachStore(t, func(t *testing.T) {
		cs := bigSession(t)
		p := okJSON[obj](t, call(t, cs, "search_commands", obj{"pattern": "make", "limit": 40, "max_tokens": 250}))
		res := call(t, cs, "read_transcript", obj{"session_id": "big", "cursor": p["next"]})
		wantError(t, res, query.ErrBadCursor.Error())
		wantError(t, call(t, cs, "read_transcript", obj{"session_id": "big", "cursor": "not-a-cursor"}), query.ErrBadCursor.Error())
	})
}

func TestGetFullSessionCutsEachSectionAndContinuesThem(t *testing.T) {
	eachStore(t, func(t *testing.T) {
		cs := bigSession(t)
		first := okJSON[obj](t, call(t, cs, "get_full_session", obj{"session_id": "big", "sections": []string{"transcript", "subagents"}, "max_tokens": 2000}))
		if first["truncated"] != true || first["next"] == nil || first["hook_events"] != nil {
			t.Fatalf("first page: truncated %v, next %v, hook_events present %v", first["truncated"], first["next"] != nil, first["hook_events"] != nil)
		}
		got := map[string]int{
			"transcript_lines": len(first["transcript_lines"].([]any)),
			"subagent_lines":   len(first["subagent_lines"].([]any)),
		}
		args := obj{"session_id": "big", "max_tokens": 2000, "cursor": first["next"]}
		for page := 0; args["cursor"] != nil && page < 100; page++ {
			p := okJSON[obj](t, call(t, cs, "get_full_session", args))
			for _, s := range []string{"transcript_lines", "subagent_lines"} {
				if list, ok := p[s].([]any); ok {
					got[s] += len(list)
				}
			}
			args["cursor"] = p["next"]
		}
		if got["transcript_lines"] != 61 || got["subagent_lines"] != 30 {
			t.Errorf("sections over the pages = %v, want 61 lines and 30 subagent lines", got)
		}
		wantError(t, call(t, cs, "get_full_session", obj{"session_id": "big", "sections": []string{"nope"}}),
			`section "nope" is not transcript, subagents, blobs or hook_events`)
	})
}

func TestGetSessionContextTrimsLinesAndPointsToReadTranscript(t *testing.T) {
	eachStore(t, func(t *testing.T) {
		cs := bigSession(t)
		c := okJSON[obj](t, call(t, cs, "get_session_context", obj{"session_id": "big", "max_tokens": 1500}))
		if c["truncated"] != true {
			t.Fatalf("not truncated: %v", c)
		}
		first, _ := c["first_lines"].([]any)
		if len(first) == 20 {
			t.Errorf("first_lines were not trimmed")
		}
		next, _ := c["next"].(string)
		cur, err := query.DecodeCursor(next, "read_transcript")
		if err != nil || cur.Offset != len(first) {
			t.Errorf("next = %+v, %v; want a read_transcript cursor at %d", cur, err, len(first))
		}
		rest := okJSON[any](t, call(t, cs, "read_transcript", obj{"session_id": "big", "cursor": next, "max_tokens": 400}))
		if rest == nil {
			t.Error("the context's cursor does not continue in read_transcript")
		}
	})
}

func TestGetLinesFetchesBySeq(t *testing.T) {
	eachStore(t, func(t *testing.T) {
		cs := bigSession(t)
		lines := okJSON[[]obj](t, call(t, cs, "get_lines", obj{"session_id": "big", "seqs": []int{42, 3, 3, 59, 1000}}))
		var seqs []float64
		for _, l := range lines {
			seqs = append(seqs, l["seq"].(float64))
		}
		if fmt.Sprint(seqs) != "[3 42 59]" {
			t.Errorf("seqs = %v, want [3 42 59]", seqs)
		}
		wantError(t, call(t, cs, "get_lines", obj{"session_id": "big", "seqs": []int{}}), query.ErrNoSeqs.Error())
		if res := call(t, cs, "get_lines", obj{"session_id": "nope", "seqs": []int{1}}); !res.IsError {
			t.Error("get_lines of a missing session succeeded")
		}
	})
}

func TestUsageAndFindSessionsHonourTheBudget(t *testing.T) {
	eachStore(t, func(t *testing.T) {
		st := storeUnderTest
		for i := range 30 {
			seedSession(t, st, account, fmt.Sprintf("s-%02d", i), "/home/user/"+strings.Repeat("deep/", 20)+fmt.Sprint(i), "")
		}
		cs := connectClient(t, newTools(st, session.Config{}).Server(), nil)
		sessions := follow(t, cs, "find_sessions", obj{"limit": 30}, 400)
		if len(sessions) != 30 {
			t.Errorf("find_sessions over the pages = %d, want 30", len(sessions))
		}
		report := okJSON[obj](t, call(t, cs, "usage", obj{"max_tokens": 200}))
		if _, ok := report["total"]; !ok {
			t.Errorf("usage lost its total: %v", report)
		}
	})
}

func TestExportSessionWritesARedactedFileOnce(t *testing.T) {
	eachStore(t, func(t *testing.T) {
		st := storeUnderTest
		seedSession(t, st, account, "exp", "/home/user/exp", "")
		must(t, st.UpsertTranscriptLines(context.Background(), []session.TranscriptLine{{SessionID: "exp", Seq: 0, AccountID: account,
			Line: map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "key sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123"}}}}))
		cs := connectClient(t, newTools(st, session.Config{}).Server(), nil)
		out := t.TempDir() + "/exp.html"
		res := okJSON[obj](t, call(t, cs, "export_session", obj{"session_id": "exp", "format": "html", "out": out}))
		if res["path"] != out || res["redacted"] != true {
			t.Errorf("export_session = %v", res)
		}
		raw, err := os.ReadFile(out)
		must(t, err)
		if strings.Contains(string(raw), "sk-ant-api03") || !strings.Contains(string(raw), "<!doctype html>") {
			t.Errorf("the exported page is not a redacted HTML page")
		}
		if again := call(t, cs, "export_session", obj{"session_id": "exp", "format": "html", "out": out}); !again.IsError {
			t.Error("export_session replaced an existing file")
		}
		wantError(t, call(t, cs, "export_session", obj{"session_id": "exp", "format": "zip"}), `format "zip" is not md, html or jsonl`)
	})
}
