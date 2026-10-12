package enrichers

import (
	"context"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

func editDiffStatsOf(t *testing.T, tool string, input map[string]any) diffStatsResult {
	t.Helper()
	got, err := EditDiffStats.Enrich(context.Background(), session.Doc{
		"hook_event_name": "PostToolUse",
		"tool_name":       tool,
		"tool_input":      input,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return got.(diffStatsResult)
}

func TestEditDiffStatsNameAndCollection(t *testing.T) {
	if EditDiffStats.Name != "edit-diff-stats" || EditDiffStats.Collection != "hook_events" || !EditDiffStats.Enabled {
		t.Errorf("unexpected metadata: %+v", EditDiffStats)
	}
}

func TestEditDiffStatsMatchesPostToolUseEditOrWrite(t *testing.T) {
	cases := []struct {
		doc  session.Doc
		want bool
	}{
		{session.Doc{"hook_event_name": "PostToolUse", "tool_name": "Edit"}, true},
		{session.Doc{"hook_event_name": "PostToolUse", "tool_name": "Write"}, true},
		{session.Doc{"hook_event_name": "PostToolUse", "tool_name": "Read"}, false},
		{session.Doc{"hook_event_name": "PreToolUse", "tool_name": "Edit"}, false},
		{session.Doc{}, false},
	}
	for _, c := range cases {
		if got := EditDiffStats.Matches(c.doc); got != c.want {
			t.Errorf("%v: got %v, want %v", c.doc, got, c.want)
		}
	}
}

func TestEditDiffStatsEditCountsLinesAddedAndRemoved(t *testing.T) {
	r := editDiffStatsOf(t, "Edit", map[string]any{"old_string": "line1\nline2\nline3", "new_string": "line1\nline2"})
	want := diffStatsResult{LinesAdded: 2, LinesRemoved: 3, NetChange: -1}
	if r != want {
		t.Errorf("got %+v, want %+v", r, want)
	}
}

func TestEditDiffStatsEditNetChangePositiveWhenAddingLines(t *testing.T) {
	r := editDiffStatsOf(t, "Edit", map[string]any{"old_string": "a", "new_string": "a\nb\nc"})
	want := diffStatsResult{LinesAdded: 3, LinesRemoved: 1, NetChange: 2}
	if r != want {
		t.Errorf("got %+v, want %+v", r, want)
	}
}

func TestEditDiffStatsEditEmptyStringsGiveZeroLines(t *testing.T) {
	if r := editDiffStatsOf(t, "Edit", map[string]any{"old_string": "", "new_string": ""}); r != (diffStatsResult{}) {
		t.Errorf("got %+v", r)
	}
}

func TestEditDiffStatsWriteLinesRemovedIsAlwaysZero(t *testing.T) {
	r := editDiffStatsOf(t, "Write", map[string]any{"file_path": "/x.ts", "content": "a\nb\nc"})
	want := diffStatsResult{LinesAdded: 3, LinesRemoved: 0, NetChange: 3}
	if r != want {
		t.Errorf("got %+v, want %+v", r, want)
	}
}

func TestEditDiffStatsWriteEmptyContentAddsNothing(t *testing.T) {
	r := editDiffStatsOf(t, "Write", map[string]any{"file_path": "/x.ts", "content": ""})
	if r.LinesAdded != 0 || r.NetChange != 0 {
		t.Errorf("got %+v", r)
	}
}

func TestEditDiffStatsMissingToolInputStringsTreatedAsEmpty(t *testing.T) {
	if r := editDiffStatsOf(t, "Edit", map[string]any{}); r != (diffStatsResult{}) {
		t.Errorf("got %+v", r)
	}
}

func TestEditDiffStatsTrailingNewlineCountsAnExtraLine(t *testing.T) {
	if r := editDiffStatsOf(t, "Write", map[string]any{"content": "a\n"}); r.LinesAdded != 2 {
		t.Errorf("got %+v", r)
	}
}

func TestEditDiffStatsMissingToolInputIsZero(t *testing.T) {
	got, err := EditDiffStats.Enrich(context.Background(), session.Doc{"hook_event_name": "PostToolUse", "tool_name": "Write"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != (diffStatsResult{}) {
		t.Errorf("got %+v", got)
	}
}

func TestEditDiffStatsResultShape(t *testing.T) {
	hookEventTagsMatch(t, diffStatsResult{})
	j := hookEventJSON(t, diffStatsResult{LinesAdded: 2, LinesRemoved: 3, NetChange: -1})
	if j != `{"lines_added":2,"lines_removed":3,"net_change":-1}` {
		t.Errorf("got %s", j)
	}
}
