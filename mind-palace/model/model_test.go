package model

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestLinkYAMLRoundTrip(t *testing.T) {
	fm := FrontMatter{
		ID: "0002-a3f", Title: "T", Type: "dsgn", Status: "ready", Priority: "1",
		Created: "2026-09-09T14:07:05.352Z", Updated: "2026-09-09T14:35:37.046Z",
		Links: &Links{
			Repo:    &Repo{Remote: "git@github.com:robbiebyrd/Project.git", Local: "~/Projects/project"},
			Specs:   []string{"./docs/design/design-doc-overview.md"},
			Web:     map[string]string{"jira": "https://jira.atlassian.net/browse/ACME-123"},
			Stories: []Link{{ID: "0001-abc", Relation: "included"}},
			Plans:   []Link{{ID: "0031-34c", Relation: "blocks"}, {ID: "0001-3sd", Relation: "depends"}},
		},
		Progress: Progress{"1.1": {Status: "completed", Stories: []string{"0001-abc"}}, "10": {Status: "blocked"}, "2": {Status: "completed"}, "1.10": {Status: "pending"}},
	}
	out, err := yaml.Marshal(fm)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{`- ["0031-34c", "blocks"]`, `priority: "1"`, `"1.1":`, `"1.10":`, `"10":`, `- ["0001-abc", "included"]`, "  stories:\n", "  plans:\n"} {
		if !strings.Contains(s, want) {
			t.Errorf("yaml missing %q:\n%s", want, s)
		}
	}
	// Numeric ordering: 1.1 < 1.10 < 2 < 10
	if strings.Index(s, `"1.1":`) > strings.Index(s, `"1.10":`) || strings.Index(s, `"2":`) > strings.Index(s, `"10":`) {
		t.Errorf("progress keys not numerically ordered:\n%s", s)
	}
	var back FrontMatter
	if err := yaml.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.Links.Plans, fm.Links.Plans) || !reflect.DeepEqual(back.Links.Stories, fm.Links.Stories) {
		t.Errorf("links did not round-trip: %+v", back.Links)
	}
	if !strings.Contains(s, `- ["0001-3sd", "depends"]`) {
		t.Errorf("second plan link missing from yaml:\n%s", s)
	}
	if strings.Index(s, "stories:") > strings.Index(s, "plans:") {
		t.Errorf("plans should follow stories under links:\n%s", s)
	}
	if back.Progress["1.10"].Status != "pending" || back.Progress["1.1"].Stories[0] != "0001-abc" {
		t.Errorf("progress did not round-trip: %+v", back.Progress)
	}
}

func TestUnquotedProgressKeysAndPriority(t *testing.T) {
	src := "id: 0001-abc\npriority: 1\nprogress:\n  1.10:\n    status: pending\n  2:\n    status: done\n"
	var fm FrontMatter
	if err := yaml.Unmarshal([]byte(src), &fm); err != nil {
		t.Fatal(err)
	}
	if fm.Priority != "1" {
		t.Errorf("priority = %q", fm.Priority)
	}
	if _, ok := fm.Progress["1.10"]; !ok {
		t.Errorf("unquoted 1.10 key lost: %v", fm.Progress)
	}
	if _, ok := fm.Progress["2"]; !ok {
		t.Errorf("unquoted 2 key lost: %v", fm.Progress)
	}
}

func TestLinkJSON(t *testing.T) {
	b, _ := json.Marshal(Link{ID: "0001-abc", Relation: "parent"})
	if string(b) != `["0001-abc","parent"]` {
		t.Errorf("got %s", b)
	}
	var l Link
	if err := json.Unmarshal([]byte(`["0001-abc","parent"]`), &l); err != nil || l.ID != "0001-abc" {
		t.Errorf("unmarshal: %v %+v", err, l)
	}
	if err := json.Unmarshal([]byte(`{"id":"0001-abc","relation":"parent"}`), &l); err != nil || l.Relation != "parent" {
		t.Errorf("unmarshal object: %v %+v", err, l)
	}
}

func TestFileNameAndSlug(t *testing.T) {
	id, typ, slug, ok := ParseFileName("docs/plans/0913-b33-IMPL-add-new-feature.md")
	if !ok || id != "0913-b33" || typ != "impl" || slug != "add-new-feature" {
		t.Errorf("parse: %v %s %s %s", ok, id, typ, slug)
	}
	if _, _, _, ok := ParseFileName("README.md"); ok {
		t.Error("README.md should not parse")
	}
	if got := Slugify("  Hello, World! -- Plan #2 "); got != "hello-world-plan-2" {
		t.Errorf("slug = %q", got)
	}
	if got := Slugify("!!!"); got != "untitled" {
		t.Errorf("empty slug = %q", got)
	}
	if FileName("0002-a3f", "DSGN", "x") != "0002-a3f-dsgn-x.md" {
		t.Error("FileName")
	}
}

func TestCompareSections(t *testing.T) {
	cases := [][3]any{{"1", "1.1", -1}, {"1.2", "1.10", -1}, {"2", "10", -1}, {"3.3", "3.3", 0}, {"10", "9", 1}}
	for _, c := range cases {
		got := CompareSections(c[0].(string), c[1].(string))
		if (got < 0) != (c[2].(int) < 0) || (got == 0) != (c[2].(int) == 0) {
			t.Errorf("Compare(%s,%s)=%d want %d", c[0], c[1], got, c[2])
		}
	}
}

func TestMalformedLinks(t *testing.T) {
	for _, src := range []string{`["", "parent"]`, `["0001-abc", ""]`, `[null, "parent"]`, `["0001-abc"]`, `{"id": "0001-abc"}`, `[1, 2]`} {
		var l Link
		if err := json.Unmarshal([]byte(src), &l); err == nil {
			t.Errorf("JSON %s accepted", src)
		}
	}
	for _, src := range []string{`- ["", "parent"]`, `- ["0001-abc", ""]`, `- [[x], "parent"]`, `- {id: 0001-abc}`, `- 0001-abc`} {
		var links []Link
		if err := yaml.Unmarshal([]byte(src), &links); err == nil {
			t.Errorf("YAML %s accepted", src)
		}
	}
	var links []Link
	if err := yaml.Unmarshal([]byte("- {id: 0001-abc, relation: parent}\n- [0002-def, blocks]\n"), &links); err != nil || len(links) != 2 {
		t.Errorf("valid links rejected: %v", err)
	}
}

func TestProgressDuplicateKeysAndJSONOrder(t *testing.T) {
	var fm FrontMatter
	err := yaml.Unmarshal([]byte("progress:\n  \"1.1\":\n    status: pending\n  1.1:\n    status: complete\n"), &fm)
	if err == nil || !strings.Contains(err.Error(), "duplicate progress key") {
		t.Errorf("duplicate key accepted: %v", err)
	}
	b, _ := json.Marshal(Progress{"10": {Status: "a"}, "2": {Status: "b"}, "1.10": {Status: "c"}, "1.2": {Status: "d"}})
	if string(b) != `{"1.2":{"status":"d"},"1.10":{"status":"c"},"2":{"status":"b"},"10":{"status":"a"}}` {
		t.Errorf("json order: %s", b)
	}
	var back Progress
	if err := json.Unmarshal(b, &back); err != nil || len(back) != 4 {
		t.Errorf("json round trip: %v", err)
	}
	if b, _ := json.Marshal(Progress{}); string(b) != "{}" {
		t.Errorf("empty progress: %s", b)
	}
}

func TestNormalizeTimestampAndStoryID(t *testing.T) {
	cases := map[string]string{
		"2026-09-09T14:07:05.352Z":       "2026-09-09T14:07:05.352Z",
		"2026-09-09T14:07:05Z":           "2026-09-09T14:07:05.000Z",
		"2026-09-09T16:07:05.3521+02:00": "2026-09-09T14:07:05.352Z",
		"":                               "",
		"2026-09-09t14:07:05z":           "2026-09-09T14:07:05.000Z",
	}
	for in, want := range cases {
		if got, err := NormalizeTimestamp(in); err != nil || got != want {
			t.Errorf("NormalizeTimestamp(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"yesterday", "  ", " 2026-09-09T14:07:05Z"} {
		if got, err := NormalizeTimestamp(bad); err == nil || got != bad {
			t.Errorf("unparsable timestamp %q: %q %v", bad, got, err)
		}
	}
	for in, want := range map[string]string{"1-abc": "0001-abc", "001-abc": "0001-abc", "0001-abc": "0001-abc", "abc": "abc", "00001-abc": "00001-abc", "1": "1"} {
		if got := NormalizeStoryID(in); got != want {
			t.Errorf("NormalizeStoryID(%q) = %q want %q", in, got, want)
		}
	}
	for _, ok := range []string{"0001-abc", "9999-z9z"} {
		if !StoryIDRe.MatchString(ok) {
			t.Errorf("%q should be a story id", ok)
		}
	}
	for _, bad := range []string{"001-abc", "0001", "abc", "0001-ABC", "00001-abc"} {
		if StoryIDRe.MatchString(bad) {
			t.Errorf("%q should not be a story id", bad)
		}
	}
	// completed is omitted from YAML and JSON until set.
	fm := FrontMatter{ID: "0001-abc", Title: "t", Type: "dsgn", Status: "pending", Priority: "1", Created: "2026-01-01T00:00:00.000Z", Updated: "2026-01-01T00:00:00.000Z"}
	y, _ := yaml.Marshal(fm)
	j, _ := json.Marshal(fm)
	if strings.Contains(string(y), "completed") || strings.Contains(string(j), "completed") {
		t.Errorf("completed should be omitted when empty:\n%s\n%s", y, j)
	}
}
