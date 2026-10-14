package sidecar

import (
	"context"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
)

func line(sid string, seq int, l map[string]any) session.TranscriptLine {
	return session.TranscriptLine{SessionID: sid, Seq: seq, AccountID: "acc", Line: l}
}

func TestMetaReadsTheRecords(t *testing.T) {
	cases := []struct {
		name string
		line map[string]any
		want session.Doc
	}{
		{"ai title", map[string]any{"type": "ai-title", "title": "Fix the build", "sessionId": "s"}, session.Doc{"title": "Fix the build", "title_kind": "ai"}},
		{"custom title", map[string]any{"type": "custom-title", "title": "Release work"}, session.Doc{"title": "Release work", "title_kind": "custom"}},
		{"slug", map[string]any{"type": "slug", "slug": "sunny-twirling-fiddle"}, session.Doc{"slug": "sunny-twirling-fiddle"}},
		{"permission mode", map[string]any{"type": "permission-mode", "permissionMode": "plan"}, session.Doc{"permission_mode": "plan"}},
		{"mode", map[string]any{"type": "mode", "mode": "normal"}, session.Doc{"permission_mode": "normal"}},
		{"pr link", map[string]any{"type": "pr-link", "prUrl": "https://github.com/o/r/pull/7"}, session.Doc{"pr_link": "https://github.com/o/r/pull/7"}},
		{"worktree", map[string]any{"type": "worktree-state", "sessionId": "s", "path": "/w", "branch": "b"}, session.Doc{"worktree": session.Doc{"path": "/w", "branch": "b"}}},
		{"message line", map[string]any{"type": "user", "entrypoint": "sdk-cli", "version": "2.1.296", "message": map[string]any{}}, session.Doc{"entrypoint": "sdk-cli", "claude_version": "2.1.296"}},
		{"nothing", map[string]any{"type": "last-prompt", "lastPrompt": "x"}, nil},
		{"empty title", map[string]any{"type": "ai-title", "title": ""}, nil},
	}
	for _, c := range cases {
		got := Meta(session.Doc{"line": c.line})
		if len(got) != len(c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
			continue
		}
		for k, v := range c.want {
			if w, ok := v.(session.Doc); ok {
				g := got.Map(k)
				if len(g) != len(w) || g["path"] != w["path"] {
					t.Errorf("%s: %s = %v, want %v", c.name, k, got[k], v)
				}
			} else if got[k] != v {
				t.Errorf("%s: %s = %v, want %v", c.name, k, got[k], v)
			}
		}
	}
}

func TestTrackerWritesEachChangeOnceAndKeepsACustomTitle(t *testing.T) {
	ctx := context.Background()
	st := memsession.New()
	tr := &Tracker{}
	first := []session.TranscriptLine{
		line("s1", 0, map[string]any{"type": "user", "entrypoint": "cli", "version": "2.1.0", "message": map[string]any{}}),
		line("s1", 1, map[string]any{"type": "ai-title", "title": "Draft"}),
	}
	// No session yet: nothing is written and nothing remembered.
	if err := tr.Lines(ctx, st, "acc", "s1", first); err != nil || len(tr.Known) != 0 {
		t.Fatalf("missing session: err %v known %v", err, tr.Known)
	}
	if err := st.UpsertSession(ctx, session.Session{SessionID: "s1", AccountID: "acc", LastSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := tr.Lines(ctx, st, "acc", "s1", first); err != nil {
		t.Fatal(err)
	}
	doc, _ := st.GetSession(ctx, "acc", "s1")
	if doc["title"] != "Draft" || doc["title_kind"] != "ai" || doc["entrypoint"] != "cli" || doc["claude_version"] != "2.1.0" {
		t.Errorf("session = %v", doc)
	}
	more := []session.TranscriptLine{
		line("s1", 2, map[string]any{"type": "assistant", "entrypoint": "cli", "version": "2.1.0", "message": map[string]any{}}),
		line("s1", 3, map[string]any{"type": "custom-title", "title": "Mine"}),
		line("s1", 4, map[string]any{"type": "ai-title", "title": "Generated again"}),
		line("s1", 5, map[string]any{"type": "pr-link", "prUrl": "https://example.com/pull/1"}),
	}
	counting := &countingStore{Store: st}
	if err := tr.Lines(ctx, counting, "acc", "s1", more); err != nil {
		t.Fatal(err)
	}
	doc, _ = st.GetSession(ctx, "acc", "s1")
	if doc["title"] != "Mine" || doc["title_kind"] != "custom" || doc["pr_link"] != "https://example.com/pull/1" || counting.writes != 1 {
		t.Errorf("session = %v, writes %d", doc, counting.writes)
	}
	// A later generated title never replaces the custom one; unchanged
	// values write nothing.
	if err := tr.Lines(ctx, counting, "acc", "s1", []session.TranscriptLine{
		line("s1", 6, map[string]any{"type": "ai-title", "title": "Another"}),
		line("s1", 7, map[string]any{"type": "user", "entrypoint": "cli", "version": "2.1.0", "message": map[string]any{}}),
	}); err != nil {
		t.Fatal(err)
	}
	doc, _ = st.GetSession(ctx, "acc", "s1")
	if doc["title"] != "Mine" || counting.writes != 1 {
		t.Errorf("after a later ai-title: title %v, writes %d", doc["title"], counting.writes)
	}
	// A restored tracker knows the custom title too.
	restored := &Tracker{Known: tr.Known}
	if err := restored.Lines(ctx, counting, "acc", "s1", []session.TranscriptLine{line("s1", 8, map[string]any{"type": "ai-title", "title": "Late"})}); err != nil || counting.writes != 1 {
		t.Errorf("restored: err %v writes %d", err, counting.writes)
	}
}

type countingStore struct {
	session.Store
	writes int
}

func (c *countingStore) SetSessionMeta(ctx context.Context, accountID, sessionID string, fields session.Doc) error {
	c.writes++
	return c.Store.SetSessionMeta(ctx, accountID, sessionID, fields)
}
