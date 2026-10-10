package enrichers

import (
	"context"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

func bashOutcomeOf(t *testing.T, response map[string]any) bashOutcomeResult {
	t.Helper()
	got, err := BashOutcome.Enrich(context.Background(), session.Doc{
		"hook_event_name": "PostToolUse",
		"tool_name":       "Bash",
		"tool_response":   response,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return got.(bashOutcomeResult)
}

func TestBashOutcomeNameAndCollection(t *testing.T) {
	if BashOutcome.Name != "bash-outcome" || BashOutcome.Collection != "hook_events" || !BashOutcome.Enabled {
		t.Errorf("unexpected metadata: %+v", BashOutcome)
	}
}

func TestBashOutcomeMatchesPostToolUseBashOnly(t *testing.T) {
	cases := []struct {
		doc  session.Doc
		want bool
	}{
		{session.Doc{"hook_event_name": "PostToolUse", "tool_name": "Bash"}, true},
		{session.Doc{"hook_event_name": "PreToolUse", "tool_name": "Bash"}, false},
		{session.Doc{"hook_event_name": "PostToolUse", "tool_name": "Read"}, false},
		{session.Doc{}, false},
	}
	for _, c := range cases {
		if got := BashOutcome.Matches(c.doc); got != c.want {
			t.Errorf("%v: got %v, want %v", c.doc, got, c.want)
		}
	}
}

func TestBashOutcomeSuccessWhenStderrIsEmptyString(t *testing.T) {
	r := bashOutcomeOf(t, map[string]any{"stdout": "hello\nworld", "stderr": ""})
	want := bashOutcomeResult{Success: true, HasError: false, OutputLines: 2, Truncated: false}
	if r != want {
		t.Errorf("got %+v, want %+v", r, want)
	}
}

func TestBashOutcomeSuccessWhenStderrIsAbsent(t *testing.T) {
	if r := bashOutcomeOf(t, map[string]any{"stdout": "ok"}); !r.Success {
		t.Errorf("got %+v", r)
	}
}

func TestBashOutcomeFailureWhenStderrIsNonEmpty(t *testing.T) {
	r := bashOutcomeOf(t, map[string]any{"stdout": "", "stderr": "command not found"})
	if r.Success || !r.HasError {
		t.Errorf("got %+v", r)
	}
}

func TestBashOutcomeTruncatedWhenStdoutContainsMarker(t *testing.T) {
	if r := bashOutcomeOf(t, map[string]any{"stdout": "line1\n[truncated]", "stderr": ""}); !r.Truncated {
		t.Errorf("got %+v", r)
	}
}

func TestBashOutcomeOutputLinesZeroWhenStdoutAbsent(t *testing.T) {
	if r := bashOutcomeOf(t, map[string]any{}); r.OutputLines != 0 {
		t.Errorf("got %+v", r)
	}
}

func TestBashOutcomeCountsLinesForMultiLineOutput(t *testing.T) {
	if r := bashOutcomeOf(t, map[string]any{"stdout": "a\nb\nc", "stderr": ""}); r.OutputLines != 3 {
		t.Errorf("got %+v", r)
	}
}

func TestBashOutcomeSuccessWhenStderrIsNull(t *testing.T) {
	if r := bashOutcomeOf(t, map[string]any{"stdout": "ok", "stderr": nil}); !r.Success {
		t.Errorf("got %+v", r)
	}
}

func TestBashOutcomeMissingToolResponseIsSuccessWithNoOutput(t *testing.T) {
	got, err := BashOutcome.Enrich(context.Background(), session.Doc{"hook_event_name": "PostToolUse", "tool_name": "Bash"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := bashOutcomeResult{Success: true}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestBashOutcomeResultShape(t *testing.T) {
	hookEventTagsMatch(t, bashOutcomeResult{})
	j := hookEventJSON(t, bashOutcomeResult{Success: true, OutputLines: 2})
	if j != `{"success":true,"has_error":false,"output_lines":2,"truncated":false}` {
		t.Errorf("got %s", j)
	}
}
