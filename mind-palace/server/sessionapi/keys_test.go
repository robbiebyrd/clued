package sessionapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// The query bodies use camel-case keys; the Go field names older clients
// sent still decode, and the structs never carry the account.
func TestQueryKeysAreCamelCaseAndLegacyNamesStillDecode(t *testing.T) {
	h, _ := newHandler(t, Options{})
	putSession(t, h, "acct", session.Session{SessionID: "s1", ProjectPath: "/p/one"})
	putSession(t, h, "acct", session.Session{SessionID: "s2", ProjectPath: "/q/two"})
	for _, body := range []string{`{"projectPathRe": "^/p/", "limit": 10}`, `{"ProjectPathRe": "^/p/", "Limit": 10}`} {
		docs := docsOf(t, h, "acct", "POST", "/sessions/find", body)
		if len(docs) != 1 || docs[0]["session_id"] != "s1" {
			t.Errorf("find with %s: %v", body, docs)
		}
	}

	mustOK(t, h, "acct", "PUT", "/transcript-lines", []session.TranscriptLine{line("s1", 1), line("s1", 2)}, nil)
	for _, body := range []string{`{"sessionId": "s1", "limit": 1}`, `{"SessionID": "s1", "Limit": 1}`} {
		docs := docsOf(t, h, "acct", "POST", "/transcript-lines/query", body)
		if len(docs) != 1 {
			t.Errorf("transcript query with %s: %d lines", body, len(docs))
		}
	}
	mustFail(t, h, "acct", "POST", "/transcript-lines/query", `{"limit": 1}`, 400, KindInvalid)

	b, err := json.Marshal(session.LineQuery{AccountID: "never-sent", SessionID: "s1", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); strings.Contains(got, "never-sent") || !strings.Contains(got, `"sessionId":"s1"`) {
		t.Errorf("LineQuery encodes as %s", got)
	}
	b, _ = json.Marshal(session.MemoryQuery{PatternRe: "x", Kinds: []string{"entry"}, SessionIDs: []string{"s1"}})
	if got := string(b); got != `{"patternRe":"x","kinds":["entry"],"sessionIds":["s1"],"limit":0}` {
		t.Errorf("MemoryQuery encodes as %s", got)
	}
}
