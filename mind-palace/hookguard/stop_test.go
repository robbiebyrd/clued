package hookguard

import (
	"strings"
	"testing"
)

func TestCheckStopAllowsWhenAlreadyBlockedThisTurn(t *testing.T) {
	if d := CheckStop(StopInput{StopHookActive: true, LastAssistantMessage: "This is pre-existing."}); d != nil {
		t.Errorf("got %+v, want nil so the assistant is not trapped in a loop", d)
	}
}

func TestCheckStopAllowsEmptyMessage(t *testing.T) {
	if d := CheckStop(StopInput{}); d != nil {
		t.Errorf("got %+v, want nil", d)
	}
}

func TestCheckStopAllowsCleanMessage(t *testing.T) {
	if d := CheckStop(StopInput{LastAssistantMessage: "All tests pass and the branch is pushed."}); d != nil {
		t.Errorf("got %+v, want nil", d)
	}
}

func TestCheckStopBlocksOwnershipDodging(t *testing.T) {
	d := CheckStop(StopInput{LastAssistantMessage: "The lint failure is Pre-Existing and unrelated to my changes."})
	if d == nil || d.Decision != "block" {
		t.Fatalf("got %+v, want a block", d)
	}
	if !strings.HasPrefix(d.Reason, StopReasonPrefix) || !strings.Contains(d.Reason, "NOTHING IS PRE-EXISTING (CLAUDE.md golden rule)") {
		t.Errorf("reason = %q", d.Reason)
	}
}

func TestCheckStopIsCaseInsensitive(t *testing.T) {
	d := CheckStop(StopInput{LastAssistantMessage: "WOULD YOU LIKE ME TO CONTINUE?"})
	if d == nil || !strings.Contains(d.Reason, "Do not ask. Continue.") {
		t.Errorf("got %+v", d)
	}
}

func TestCheckStopFirstRuleWins(t *testing.T) {
	// "known limitation" is listed after the ownership rules, so the
	// ownership correction is the one sent.
	d := CheckStop(StopInput{LastAssistantMessage: "This is a known limitation that already existed before."})
	if d == nil || !strings.Contains(d.Reason, "If you found it broken, fix it") {
		t.Errorf("got %+v", d)
	}
}

func TestCheckStopMatchesWantToContinueOrAcrossLines(t *testing.T) {
	d := CheckStop(StopInput{LastAssistantMessage: "Do you want to continue with the migration,\nor should we stop?"})
	if d == nil || !strings.Contains(d.Reason, "The task is not done. Continue working.") {
		t.Errorf("got %+v", d)
	}
}

func TestCheckStopCoversEveryRule(t *testing.T) {
	// Each rule's literal text (with the regex fragment spelled out) must
	// trigger that rule or an earlier one.
	for i, r := range StopRules {
		text := strings.NewReplacer(`.*`, " ", `(?is)`, "").Replace(r.Pattern.String())
		got, ok := MatchStop("Well, " + text + " then.")
		if !ok {
			t.Errorf("rule %d (%s) does not match its own text", i, r.Name())
			continue
		}
		var idx int
		for j, rr := range StopRules {
			if rr.Name() == got.Name() {
				idx = j
				break
			}
		}
		if idx > i {
			t.Errorf("rule %d (%s) was shadowed by later rule %d (%s)", i, r.Name(), idx, got.Name())
		}
	}
}

func TestCheckStopJSON(t *testing.T) {
	d, err := CheckStopJSON([]byte(`{"session_id":"abc","stop_hook_active":false,"last_assistant_message":"I'll pick this up later."}`))
	if err != nil || d == nil || !strings.Contains(d.Reason, "There is no 'later.'") {
		t.Errorf("got %+v, %v", d, err)
	}
	d, err = CheckStopJSON([]byte(`{"stop_hook_active":true,"last_assistant_message":"I'll pick this up later."}`))
	if err != nil || d != nil {
		t.Errorf("got %+v, %v, want nil", d, err)
	}
	if _, err := CheckStopJSON([]byte(`not json`)); err == nil {
		t.Error("invalid JSON should error")
	}
}
