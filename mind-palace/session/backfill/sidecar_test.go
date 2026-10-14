package backfill_test

import (
	"context"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
)

func TestRunSetsTheSessionFieldsTheRecordsCarry(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "-Users-test-myproject", sessionID,
		`{"type":"user","entrypoint":"sdk-cli","version":"2.0.0","message":{"role":"user","content":"x"}}`+"\n"+
			`{"type":"slug","slug":"sunny-fiddle"}`+"\n"+
			`{"type":"custom-title","title":"Mine"}`+"\n")
	store := memsession.New()
	run(t, store, dir, "acc", nil)
	sess, err := store.GetSession(context.Background(), "acc", sessionID)
	if err != nil || sess["title"] != "Mine" || sess["slug"] != "sunny-fiddle" || sess["entrypoint"] != "sdk-cli" || sess["claude_version"] != "2.0.0" {
		t.Fatalf("session = %v (%v)", sess, err)
	}
}
