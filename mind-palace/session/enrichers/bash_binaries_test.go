package enrichers

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// hookEventTagsMatch fails when a result struct has a field whose bson tag
// differs from its json tag, or lacks either.
func hookEventTagsMatch(t *testing.T, result any) {
	t.Helper()
	typ := reflect.TypeOf(result)
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		j, b := f.Tag.Get("json"), f.Tag.Get("bson")
		if j == "" || j != b {
			t.Errorf("field %s: json tag %q, bson tag %q", f.Name, j, b)
		}
	}
}

func hookEventJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func bashBinariesOf(t *testing.T, command string) []string {
	t.Helper()
	got, err := BashBinaries.Enrich(context.Background(), session.Doc{
		"tool_name":  "Bash",
		"tool_input": map[string]any{"command": command},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return got.(binariesResult).Binaries
}

func expectBinaries(t *testing.T, command string, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	if got := bashBinariesOf(t, command); !reflect.DeepEqual(got, want) {
		t.Errorf("%q: got %q, want %q", command, got, want)
	}
}

func TestBashBinariesNameAndCollection(t *testing.T) {
	if BashBinaries.Name != "bash-binaries" || BashBinaries.Collection != "hook_events" || !BashBinaries.Enabled {
		t.Errorf("unexpected metadata: %+v", BashBinaries)
	}
}

func TestBashBinariesMatchesBashWithStringCommand(t *testing.T) {
	cases := []struct {
		name string
		doc  session.Doc
		want bool
	}{
		{"bash with command", session.Doc{"tool_name": "Bash", "tool_input": map[string]any{"command": "ls"}}, true},
		{"empty command is still a string", session.Doc{"tool_name": "Bash", "tool_input": map[string]any{"command": ""}}, true},
		{"non-string command", session.Doc{"tool_name": "Bash", "tool_input": map[string]any{"command": 5.0}}, false},
		{"missing command", session.Doc{"tool_name": "Bash", "tool_input": map[string]any{}}, false},
		{"missing tool_input", session.Doc{"tool_name": "Bash"}, false},
		{"other tool", session.Doc{"tool_name": "Read", "tool_input": map[string]any{"command": "ls"}}, false},
		{"empty doc", session.Doc{}, false},
	}
	for _, c := range cases {
		if got := BashBinaries.Matches(c.doc); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestBashBinariesSingleCommand(t *testing.T) {
	expectBinaries(t, "git status", "git")
}

func TestBashBinariesPipeline(t *testing.T) {
	expectBinaries(t, "cat a.txt | grep foo | wc -l", "cat", "grep", "wc")
}

func TestBashBinariesAndOrSemicolonSeparators(t *testing.T) {
	expectBinaries(t, "make build && go test || git stash; ls", "make", "go", "git", "ls")
}

func TestBashBinariesNewlineSeparator(t *testing.T) {
	expectBinaries(t, "git add .\ngit commit -m x\n\n  npm test", "git", "npm")
}

func TestBashBinariesEmptyStagesAreSkipped(t *testing.T) {
	expectBinaries(t, ";; ls ;  | && grep", "ls", "grep")
}

func TestBashBinariesEnvVarPrefixIsSkipped(t *testing.T) {
	expectBinaries(t, "FOO=bar BAZ_1=2 make all", "make")
}

func TestBashBinariesOnlyEnvAssignmentsYieldsNothing(t *testing.T) {
	expectBinaries(t, "FOO=bar")
}

func TestBashBinariesEmptyCommandYieldsEmptyList(t *testing.T) {
	got := bashBinariesOf(t, "")
	if got == nil || len(got) != 0 {
		t.Fatalf("want empty non-nil slice, got %#v", got)
	}
	if j := hookEventJSON(t, binariesResult{Binaries: got}); j != `{"binaries":[]}` {
		t.Errorf("got %s", j)
	}
}

func TestBashBinariesStripsLeadingAndTrailingQuotes(t *testing.T) {
	expectBinaries(t, `"git" status && 'ls' -la`, "git", "ls")
}

func TestBashBinariesLoneQuoteTokenIsDropped(t *testing.T) {
	expectBinaries(t, `" foo`)
}

func TestBashBinariesQuotedSeparatorsStillSplit(t *testing.T) {
	expectBinaries(t, `echo "a && b"`, "b")
}

func TestBashBinariesStripsDirectoryFromPath(t *testing.T) {
	expectBinaries(t, "/usr/local/bin/node app.js && ./scripts/run.sh", "node", "run.sh")
}

func TestBashBinariesTrailingSlashLeavesNothing(t *testing.T) {
	expectBinaries(t, "somedir/ && ls", "ls")
}

func TestBashBinariesFiltersBuiltinsAndKeywords(t *testing.T) {
	expectBinaries(t, "cd /tmp && export A=1 && source env.sh && echo hi && ls", "ls")
	expectBinaries(t, "if [ -f x ]; then echo y; else echo n; fi")
	expectBinaries(t, "for f in *; do cat f; done; while true; do sleep 1; done; case x in x) ls;; esac")
	expectBinaries(t, ". ./env.sh; [[ -n x ]] && ]]")
}

func TestBashBinariesDeduplicatesKeepingFirstOccurrence(t *testing.T) {
	expectBinaries(t, "ls; grep a | ls && /bin/ls && grep b", "ls", "grep")
}

func TestBashBinariesUnicodeWhitespaceSeparatesLikeJavaScript(t *testing.T) {
	expectBinaries(t, "\u00A0make\u00A0all\u3000&&\uFEFFls", "make", "ls")
}

func TestBashBinariesResultShape(t *testing.T) {
	hookEventTagsMatch(t, binariesResult{})
	if j := hookEventJSON(t, binariesResult{Binaries: []string{"a", "b"}}); j != `{"binaries":["a","b"]}` {
		t.Errorf("got %s", j)
	}
}
