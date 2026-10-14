// Package sidecar reads the records Claude Code writes beside the messages
// of a transcript (the session's titles, slug, permission mode, pull
// request link and worktree state) and the fields every message line
// carries (how the session was started, the Claude Code version) into
// session fields, and keeps them on the session through
// session.Store.SetSessionMeta. The daemon's tailer, the relay's catch-up
// and the backfill all go through a Tracker.
package sidecar

import (
	"context"
	"errors"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// The session fields the records set.
const (
	FieldTitle          = "title"
	FieldTitleKind      = "title_kind" // "custom" or "ai"
	FieldSlug           = "slug"
	FieldPermissionMode = "permission_mode"
	FieldPRLink         = "pr_link"
	FieldWorktree       = "worktree"
	FieldEntrypoint     = "entrypoint"
	FieldClaudeVersion  = "claude_version"
)

// Fields lists every field a record can set, in a fixed order.
var Fields = []string{FieldTitle, FieldTitleKind, FieldSlug, FieldPermissionMode, FieldPRLink, FieldWorktree, FieldEntrypoint, FieldClaudeVersion}

// Meta is what one transcript line says about its session: the fields it
// sets, none for a line that is not a sidecar record and carries no
// entrypoint or version. The line is the stored line ({"line": …}) or the
// line itself.
func Meta(d session.Doc) session.Doc {
	line := d.Line()
	if line == nil {
		line = d
	}
	out := session.Doc{}
	set := func(field string, keys ...string) {
		for _, k := range keys {
			if v, ok := line.String(k); ok && v != "" {
				out[field] = v
				return
			}
		}
	}
	switch t, _ := line.String("type"); t {
	case "custom-title":
		set(FieldTitle, "title", "customTitle")
		if out[FieldTitle] != nil {
			out[FieldTitleKind] = "custom"
		}
	case "ai-title":
		set(FieldTitle, "title", "aiTitle")
		if out[FieldTitle] != nil {
			out[FieldTitleKind] = "ai"
		}
	case "slug":
		set(FieldSlug, "slug")
	case "permission-mode", "mode":
		set(FieldPermissionMode, "permissionMode", "permission_mode", "mode")
	case "pr-link":
		set(FieldPRLink, "prUrl", "url", "link", "prLink", "pr_link", "href")
	case "worktree-state":
		state := session.Doc{}
		for k, v := range line {
			switch k {
			case "type", "sessionId", "uuid", "timestamp":
				continue
			}
			state[k] = v
		}
		if len(state) > 0 {
			out[FieldWorktree] = state
		}
	}
	set(FieldEntrypoint, "entrypoint")
	set(FieldClaudeVersion, "version")
	if len(out) == 0 {
		return nil
	}
	return out
}

// Tracker remembers what it has set on one session, so each value is
// written once and a custom title is never replaced by a generated one.
// The zero value is ready; Known may be restored from a saved state.
type Tracker struct {
	// Known holds the fields last written, by name.
	Known map[string]any `json:"known,omitempty"`
}

// Lines observes the lines in order and writes the fields they change to
// the session, once per call. A session the store does not have is left
// for the next call: nothing is remembered as written.
func (t *Tracker) Lines(ctx context.Context, store session.Store, accountID, sessionID string, lines []session.TranscriptLine) error {
	if t.Known == nil {
		t.Known = map[string]any{}
	}
	pending := session.Doc{}
	custom := t.Known[FieldTitleKind] == "custom" || pending[FieldTitleKind] == "custom"
	for _, l := range lines {
		meta := Meta(session.Doc{"line": l.Line})
		for k, v := range meta {
			if k == FieldTitle && meta[FieldTitleKind] == "ai" && custom {
				continue
			}
			if k == FieldTitleKind && v == "ai" && custom {
				continue
			}
			if k == FieldTitleKind && v == "custom" {
				custom = true
			}
			if known, ok := t.Known[k]; ok && equal(known, v) && pending[k] == nil {
				continue
			}
			pending[k] = v
		}
	}
	for k, v := range pending {
		if known, ok := t.Known[k]; ok && equal(known, v) {
			delete(pending, k)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	if err := store.SetSessionMeta(ctx, accountID, sessionID, pending); err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return nil
		}
		return err
	}
	for k, v := range pending {
		t.Known[k] = v
	}
	return nil
}

// equal compares two field values: strings by value, anything else never
// (a worktree state is rewritten when seen again).
func equal(a, b any) bool {
	as, aok := a.(string)
	bs, bok := b.(string)
	return aok && bok && as == bs
}
