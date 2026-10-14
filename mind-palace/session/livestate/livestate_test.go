package livestate

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
)

func TestStateOf(t *testing.T) {
	cases := []struct {
		ev            session.Doc
		state, reason string
	}{
		{session.Doc{"hook_event_name": "UserPromptSubmit"}, Working, "UserPromptSubmit"},
		{session.Doc{"hook_event_name": "PreToolUse", "tool_name": "Bash"}, Working, "PreToolUse"},
		{session.Doc{"hook_event_name": "PreToolUse", "tool_name": "AskUserQuestion"}, Waiting, "PreToolUse:AskUserQuestion"},
		{session.Doc{"hook_event_name": "PostToolUse", "tool_name": "AskUserQuestion"}, Working, "PostToolUse"},
		{session.Doc{"hook_event_name": "PostToolUseFailure"}, Working, "PostToolUseFailure"},
		{session.Doc{"hook_event_name": "SubagentStart"}, Working, "SubagentStart"},
		{session.Doc{"hook_event_name": "Stop"}, Waiting, "Stop"},
		{session.Doc{"hook_event_name": "StopFailure"}, Waiting, "StopFailure"},
		{session.Doc{"hook_event_name": "PermissionRequest"}, Waiting, "PermissionRequest"},
		{session.Doc{"hook_event_name": "Notification", "notification_type": "permission_prompt"}, Waiting, "Notification:permission_prompt"},
		{session.Doc{"hook_event_name": "Notification", "notification_type": "idle_prompt"}, Waiting, "Notification:idle_prompt"},
		{session.Doc{"hook_event_name": "Notification", "notification_type": "elicitation_dialog"}, Waiting, "Notification:elicitation_dialog"},
		{session.Doc{"hook_event_name": "Notification", "notification_type": "auth_success"}, "", ""},
		{session.Doc{"hook_event_name": "SessionEnd"}, Ended, "SessionEnd"},
		{session.Doc{"hook_event_name": "SessionStart"}, "", ""},
		{session.Doc{"hook_event_name": "SubagentStop"}, "", ""},
		{session.Doc{"hook_event_name": "PreCompact"}, "", ""},
		{session.Doc{"hook_event_name": session.LimitsEventName}, "", ""},
		{session.Doc{}, "", ""},
	}
	for _, c := range cases {
		if state, reason := StateOf(c.ev); state != c.state || reason != c.reason {
			t.Errorf("StateOf(%v) = %q/%q, want %q/%q", c.ev, state, reason, c.state, c.reason)
		}
	}
}

func TestObserveRecordsEachChangeOnce(t *testing.T) {
	ctx := context.Background()
	st := memsession.New()
	t0 := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	if _, err := Observe(ctx, st, "acc", "s1", Working, "UserPromptSubmit", t0); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("missing session: err = %v", err)
	}
	if err := st.UpsertSession(ctx, session.Session{SessionID: "s1", AccountID: "acc", LastSeen: t0}); err != nil {
		t.Fatal(err)
	}
	steps := []struct {
		state, reason string
		at            time.Time
		changed       bool
	}{
		{Working, "UserPromptSubmit", t0, true},
		{Working, "PreToolUse", t0.Add(time.Second), false},
		{Waiting, "Stop", t0.Add(2 * time.Second), true},
		{Working, "UserPromptSubmit", t0.Add(3 * time.Second), true},
		{Waiting, "PreToolUse:AskUserQuestion", t0.Add(4 * time.Second), true},
		{Ended, "SessionEnd", t0.Add(5 * time.Second), true},
		{"", "x", t0.Add(6 * time.Second), false},
	}
	for _, s := range steps {
		changed, err := Observe(ctx, st, "acc", "s1", s.state, s.reason, s.at)
		if err != nil || changed != s.changed {
			t.Errorf("%s/%s: changed %v err %v, want %v", s.state, s.reason, changed, err, s.changed)
		}
	}
	doc, _ := st.GetSession(ctx, "acc", "s1")
	if doc["live_state"] != Ended || doc["live_state_reason"] != "SessionEnd" {
		t.Errorf("session = %v", doc)
	}
	changes, err := st.StateChanges(ctx, session.ActivityQuery{AccountID: "acc", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for i := len(changes) - 1; i >= 0; i-- {
		from, _ := changes[i].String("from")
		to, _ := changes[i].String("to")
		got = append(got, from+">"+to)
	}
	want := []string{">working", "working>waiting", "waiting>working", "working>waiting", "waiting>ended"}
	if len(got) != len(want) {
		t.Fatalf("changes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("change %d = %q, want %q", i, got[i], want[i])
		}
	}
	if changes[0]["reason"] != "SessionEnd" || !changes[0]["at"].(time.Time).Equal(t0.Add(5*time.Second)) {
		t.Errorf("last change = %v", changes[0])
	}
}

func TestHotnessAndWindow(t *testing.T) {
	now := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		ago  time.Duration
		want string
	}{
		{0, Hot}, {10 * time.Minute, Hot}, {30 * time.Minute, Hot},
		{31 * time.Minute, Warm}, {50 * time.Minute, Warm}, {time.Hour, Warm},
		{61 * time.Minute, Cool}, {9 * time.Hour, Cool}, {10 * time.Hour, Cool},
		{10*time.Hour + time.Second, Cold}, {24 * time.Hour, Cold}, {30 * 24 * time.Hour, Cold},
	}
	for _, c := range cases {
		lastSeen := now.Add(-c.ago)
		if got := Hotness(lastSeen, now); got != c.want {
			t.Errorf("Hotness(%v ago) = %q, want %q", c.ago, got, c.want)
		}
		from, before, ok := Window(c.want, now)
		if !ok {
			t.Fatalf("Window(%q) unknown", c.want)
		}
		in := (from.IsZero() || !lastSeen.Before(from)) && (before.IsZero() || lastSeen.Before(before))
		if !in {
			t.Errorf("Window(%q) = [%v, %v) excludes a session seen %v ago", c.want, from, before, c.ago)
		}
	}
	if Hotness(time.Time{}, now) != "" {
		t.Error("a zero last_seen has no hotness")
	}
	// A session in a different local zone is graded on the UTC day.
	if got := Hotness(now.Add(-10*time.Hour).In(time.FixedZone("x", -8*3600)), now); got != Cool {
		t.Errorf("zone: %q", got)
	}
	if _, _, ok := Window("tepid", now); ok {
		t.Error("an unknown grade has no window")
	}
	// Just after midnight, the cool window is empty.
	early := time.Date(2026, 10, 14, 0, 20, 0, 0, time.UTC)
	if from, before, _ := Window(Cool, early); from.Before(before) {
		t.Errorf("cool window at 00:20 = [%v, %v), want empty", from, before)
	}
}
