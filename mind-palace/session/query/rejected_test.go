package query_test

import (
	"context"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
	"github.com/robbiebyrd/clued/mind-palace/session/query"
)

// rejectLine stores a tool result line error-flag marked as rejected.
func rejectLine(t *testing.T, st session.Store, acc, id string, seq int, toolUseID string) {
	t.Helper()
	ctx := context.Background()
	line := session.TranscriptLine{SessionID: id, Seq: seq, AccountID: acc, Line: map[string]any{"type": "user"}}
	if err := st.UpsertTranscriptLines(ctx, []session.TranscriptLine{line}); err != nil {
		t.Fatal(err)
	}
	pending, err := st.Unenriched(ctx, "transcript_lines", "error-flag", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range pending {
		if d["session_id"] == id && seqOf(d["seq"]) == seq {
			flag := session.Doc{"is_error": true, "error_type": "tool_failure", "rejected": true, "rejection": "permission_denied", "tool_use_id": toolUseID}
			if err := st.SetEnrichment(ctx, "transcript_lines", d["_id"], "error-flag", flag); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestRejectedCallsAreLeftOutUnlessAsked(t *testing.T) {
	st := memsession.New()
	ctx := context.Background()
	now := time.Now()
	seedSession(t, st, account, "s1", "/p/one", "", now)
	for _, ev := range []session.Doc{
		{"session_id": "s1", "account_id": account, "hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_use_id": "tu-ok", "tool_input": session.Doc{"command": "go test ./..."}, "created_at": now.Add(-2 * time.Minute)},
		{"session_id": "s1", "account_id": account, "hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_use_id": "tu-no", "tool_input": session.Doc{"command": "rm -rf /"}, "created_at": now.Add(-time.Minute)},
		{"session_id": "s1", "account_id": account, "hook_event_name": "PreToolUse", "tool_name": "Edit", "tool_use_id": "tu-edit", "tool_input": session.Doc{"file_path": "/p/one/a.go"}, "created_at": now,
			"enrichments": session.Doc{"file-tracker": session.Doc{"file_path": "/p/one/a.go", "operation": "edit"}}},
	} {
		if err := st.InsertHookEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	rejectLine(t, st, account, "s1", 0, "tu-no")
	rejectLine(t, st, account, "s1", 1, "tu-edit")
	seedActivity(t, st, account, "s1", 2, session.Doc{"kind": "bash", "label": "Bash", "detail": "rm -rf /", "tool_name": "Bash", "tool_use_id": "tu-no"})
	seedActivity(t, st, account, "s1", 3, session.Doc{"kind": "result", "label": "result", "detail": "Permission for this action was denied", "tool_use_id": "tu-no"})
	seedActivity(t, st, account, "s1", 4, session.Doc{"kind": "bash", "label": "Bash", "detail": "go test ./...", "tool_name": "Bash", "tool_use_id": "tu-ok"})
	svc := &query.Service{Store: st, AccountID: account}

	hits, err := svc.SearchCommands(ctx, query.SearchCommandsArgs{Pattern: "."})
	if err != nil || len(hits) != 1 || hits[0].Command != "go test ./..." {
		t.Errorf("search_commands = %+v (%v)", hits, err)
	}
	hits, err = svc.SearchCommands(ctx, query.SearchCommandsArgs{Pattern: ".", IncludeRejected: true})
	if err != nil || len(hits) != 2 {
		t.Errorf("search_commands with rejected = %+v (%v)", hits, err)
	}

	files, err := svc.FileHistory(ctx, query.FileHistoryArgs{Path: "a.go"})
	if err != nil || len(files) != 0 {
		t.Errorf("file_history = %+v (%v)", files, err)
	}
	files, err = svc.FileHistory(ctx, query.FileHistoryArgs{Path: "a.go", IncludeRejected: true})
	if err != nil || len(files) != 1 || files[0].Operations["edit"] != 1 {
		t.Errorf("file_history with rejected = %+v (%v)", files, err)
	}

	acts, err := svc.Activities(ctx, query.ActivitiesArgs{})
	if err != nil || len(acts) != 1 || acts[0].ToolUseID != "tu-ok" {
		t.Errorf("activities = %+v (%v)", acts, err)
	}
	acts, err = svc.Activities(ctx, query.ActivitiesArgs{IncludeRejected: true})
	if err != nil || len(acts) != 3 {
		t.Errorf("activities with rejected = %+v (%v)", acts, err)
	}
}
