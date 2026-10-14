package structpatch

import "testing"

func TestParseSummarisesHunks(t *testing.T) {
	patch := []any{
		map[string]any{"oldStart": 10.0, "oldLines": 3.0, "newStart": 10.0, "newLines": 5.0, "lines": []any{" a", "-b", "+c", "+d", "+e", " f"}},
		map[string]any{"oldStart": 40, "oldLines": 1, "newStart": 42, "newLines": 1, "lines": []any{"-x", "+y"}},
	}
	s, ok := Parse(patch)
	if !ok {
		t.Fatal("not a patch")
	}
	if len(s.Hunks) != 2 || s.Hunks[0] != (Hunk{10, 3, 10, 5}) || s.Hunks[1] != (Hunk{40, 1, 42, 1}) {
		t.Errorf("hunks = %+v", s.Hunks)
	}
	if s.LinesAdded != 4 || s.LinesRemoved != 2 || s.Span != "L10-42" {
		t.Errorf("added %d removed %d span %q", s.LinesAdded, s.LinesRemoved, s.Span)
	}
	want := "@@ -10,3 +10,5 @@\n a\n-b\n+c\n+d\n+e\n f\n@@ -40,1 +42,1 @@\n-x\n+y\n"
	if s.Diff != want {
		t.Errorf("diff =\n%s\nwant\n%s", s.Diff, want)
	}

	one, _ := Parse([]any{map[string]any{"oldStart": 7, "oldLines": 1, "newStart": 7, "newLines": 1, "lines": []any{"-a", "+b"}}})
	if one.Span != "L7" {
		t.Errorf("one-line span = %q", one.Span)
	}
	if empty, ok := Parse([]any{}); !ok || empty.Span != "" || empty.Diff != "" {
		t.Errorf("empty patch = %+v, %v", empty, ok)
	}
	if _, ok := Parse("not a list"); ok {
		t.Error("a string is not a patch")
	}
	if _, ok := Parse(nil); ok {
		t.Error("nil is not a patch")
	}
	if SpanOf(3, 3) != "L3" || SpanOf(3, 9) != "L3-9" {
		t.Error("SpanOf")
	}
}
