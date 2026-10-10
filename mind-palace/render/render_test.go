package render

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestRenderDefault(t *testing.T) {
	for _, f := range []string{"../testdata/create-impl.json", "../testdata/create-dsgn.json"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var in struct {
			FrontMatter struct{ Title, Type string } `json:"frontMatter"`
			Body        Body                         `json:"body"`
		}
		if err := json.Unmarshal(b, &in); err != nil {
			t.Fatal(err)
		}
		out, err := Render(DefaultTemplate("plan"), Data{ID: "0001-abc", Title: in.FrontMatter.Title, Type: in.FrontMatter.Type, Body: in.Body})
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if !strings.HasPrefix(out, "# "+in.FrontMatter.Title+"\n\n## Summary\n") {
			t.Errorf("%s: bad start:\n%s", f, out)
		}
		if strings.Contains(out, "\n\n\n") {
			t.Errorf("%s: triple blank line:\n%s", f, out)
		}
		if in.FrontMatter.Type == "impl" {
			for _, want := range []string{"# Part 2 — Implementation", "## Phase 1: Core", "### 1.1: Model", "- [ ] **Step 1: Write the failing test** — round-trips YAML", "`go test ./model`", "## Phase 2: Wrap up", "- [ ] Full test suite passes", "## Acceptance Criteria", "1. Add stories", "| `plan/service/service.go` | create | Business rules |", "**Depends on:** none"} {
				if !strings.Contains(out, want) {
					t.Errorf("impl missing %q:\n%s", want, out)
				}
			}
			if strings.Contains(out, "Part 1") {
				t.Error("impl should not render Part 1")
			}
		} else {
			for _, want := range []string{"# Part 1 — Design", "## Decisions", "- **Markdown files are the primary store** — They diff well in git.", "### Service", "- **Security:** No new exposure.", "**Out of scope:**", "- Stories — tracked separately", "- [ ] Which DBs first? — robbie"} {
				if !strings.Contains(out, want) {
					t.Errorf("dsgn missing %q:\n%s", want, out)
				}
			}
		}
	}
}

func TestSectionNumbers(t *testing.T) {
	b := Body{Implementation: &Implementation{Phases: []Phase{{Number: "1", Sections: []Section{{Number: "1.1"}, {Number: "1.2"}}}, {Number: "2"}}}}
	if got := strings.Join(b.SectionNumbers(), ","); got != "1,1.1,1.2,2" {
		t.Errorf("got %s", got)
	}
	if err := Validate("{{ .Title "); err == nil {
		t.Error("broken template should fail")
	}
}

func TestRenderPreservesBodyWhitespace(t *testing.T) {
	body := Body{Summary: Summary{Goal: "line one  \nline two", Problem: "Intro\n\n```\ncode\n\n\n  indented  \n```"}}
	out, err := Render(DefaultTemplate("plan"), Data{Title: "T", Body: body})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "line one  \nline two") {
		t.Errorf("hard break lost:\n%q", out)
	}
	if !strings.Contains(out, "```\ncode\n\n\n  indented  \n```") {
		t.Errorf("code block changed:\n%q", out)
	}
	if strings.Contains(strings.SplitN(out, "```", 2)[0], "\n\n\n") {
		t.Errorf("template blank lines not collapsed outside fences:\n%q", out)
	}
}

func TestRenderStory(t *testing.T) {
	raw := `{"problemStatement":{"statement":"It breaks.","issue":"x.go:1","impact":"Bad."},
"steps":[{"number":"1","name":"Guard","description":"Add a guard.","steps":[{"number":"1.1","name":"Test","description":"Add a test."}]},{"number":"2","name":"Ship","description":"Release."}],
"acceptanceCriteria":[{"text":"go test ./...","kind":"verify"},{"text":"Works","state":"done"},{"text":"Manual check","kind":"manual","state":"not_applicable"}],
"files":[{"path":"x.go","change":"guard"}],"proof":[{"dimension":"input-validation","state":"proven","evidence":"tests"}],"qa":"Ran it.",
"workLog":[{"timestamp":"2026-09-09T14:07:05.352Z","entry":"Started"}]}`
	body, err := DecodeBody("story", json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(body.(Sectioned).SectionNumbers(), ","); got != "1,1.1,2" {
		t.Errorf("story sections: %s", got)
	}
	out, err := Render(DefaultTemplate("story"), Data{ID: "0001-abc", Title: "Fix it", Type: "bugs", Body: body})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Fix it\n\n## Problem Statement\n\nIt breaks.\n\n**Issue:** x.go:1\n\n**Impact:** Bad.\n\n## Steps\n\n### 1: Guard\n\nAdd a guard.\n\n#### 1.1: Test\n\nAdd a test.\n\n### 2: Ship\n\nRelease.\n\n## Acceptance Criteria\n\n- [ ] VERIFY: go test ./...\n- [x] Works\n- [~] [MANUAL] Manual check\n\n## Files\n\n- `x.go` - guard\n\n## Proof\n\n- [x] [input-validation] Input Validation (tests)\n\n## QA\n\nRan it.\n\n## Work Log\n\n### 2026-09-09T14:07:05.352Z - Started\n"} {
		if out != want {
			t.Errorf("story render:\n%q\nwant\n%q", out, want)
		}
	}
	if _, err := DecodeBody("story", json.RawMessage(`{"bogus":1}`)); err == nil {
		t.Error("unknown body field accepted")
	}
	if _, err := DecodeBody("plan", json.RawMessage(`{"summary":{"goal":"g","problem":"p"}}`)); err != nil {
		t.Errorf("plan body: %v", err)
	}
}
