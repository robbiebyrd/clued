package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestSidecarRecordsSetSessionFieldsWhileTailing(t *testing.T) {
	eachStore(t, func(t *testing.T) {
		f := newFixture(t)
		transcript := filepath.Join(f.tmp, "projects", "sess-meta.jsonl")
		if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(transcript, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if code, _ := f.post(t, fmt.Sprintf(`{"session_id":"sess-meta","transcript_path":%q,"cwd":"/tmp"}`, transcript)); code != 200 {
			t.Fatalf("status %d", code)
		}
		eventually(t, "session doc", func() bool { return sessionDoc(f.store, "sess-meta") != nil })
		content := `{"type":"user","entrypoint":"sdk-cli","version":"2.1.296","message":{"role":"user","content":"hi"}}` + "\n" +
			`{"type":"ai-title","title":"Fix the build","sessionId":"sess-meta"}` + "\n" +
			`{"type":"permission-mode","permissionMode":"plan","sessionId":"sess-meta"}` + "\n" +
			`{"type":"pr-link","prUrl":"https://github.com/o/r/pull/7","sessionId":"sess-meta"}` + "\n"
		fh, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fh.WriteString(content); err != nil {
			t.Fatal(err)
		}
		fh.Close()
		eventually(t, "pr link", func() bool { return sessionDoc(f.store, "sess-meta")["pr_link"] == "https://github.com/o/r/pull/7" })
		doc := sessionDoc(f.store, "sess-meta")
		if doc["title"] != "Fix the build" || doc["title_kind"] != "ai" || doc["permission_mode"] != "plan" || doc["entrypoint"] != "sdk-cli" || doc["claude_version"] != "2.1.296" {
			t.Fatalf("session = %v", doc)
		}
	})
}
