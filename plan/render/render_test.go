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
			Body        Body                          `json:"body"`
		}
		if err := json.Unmarshal(b, &in); err != nil {
			t.Fatal(err)
		}
		out, err := Render(DefaultTemplate(), Data{ID: "0001-abc", Title: in.FrontMatter.Title, Type: in.FrontMatter.Type, Body: in.Body})
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
