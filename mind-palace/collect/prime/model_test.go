package prime

import (
	"testing"
	"time"
)

func TestSessionBodyNamesTheModel(t *testing.T) {
	snap := &SessionSnapshot{SessionID: "sess-1", Model: "claude-fable-5-1", LiveState: "waiting", Hotness: "warm", LastSeen: time.Date(2026, 3, 4, 6, 6, 7, 0, time.UTC)}
	want := "session: sess-1\nmodel: claude-fable-5-1\nstate: waiting\nhotness: warm\nlast seen: 2026-03-04T06:06:07Z\ncommands: none"
	if got := SessionBody(snap); got != want {
		t.Errorf("SessionBody =\n%q\nwant\n%q", got, want)
	}
}
