package ghprobe

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/collect"
)

// scriptRunner records every argv it is asked to run and answers from a
// script keyed on the joined args (the name is not part of the key).
type scriptRunner struct {
	calls   [][]string
	dirs    []string
	names   []string
	stdout  map[string]string
	errs    map[string]error
	fallErr error // returned for every key not in errs when non-nil
}

func (r *scriptRunner) Run(_ context.Context, dir, name string, args ...string) (string, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	r.dirs = append(r.dirs, dir)
	r.names = append(r.names, name)
	key := strings.Join(args, " ")
	if err, ok := r.errs[key]; ok {
		return r.stdout[key], err
	}
	if r.fallErr != nil {
		return r.stdout[key], r.fallErr
	}
	return r.stdout[key], nil
}

func TestAllowed(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		// allowed read-only verbs
		{[]string{"pr", "list"}, true},
		{[]string{"pr", "view", "12"}, true},
		{[]string{"pr", "checks"}, true},
		{[]string{"pr", "diff"}, true},
		{[]string{"pr", "status"}, true},
		{[]string{"issue", "list"}, true},
		{[]string{"issue", "view", "3"}, true},
		{[]string{"run", "list"}, true},
		{[]string{"run", "view", "1", "--log"}, true},
		{[]string{"release", "list"}, true},
		{[]string{"release", "view", "v1.0"}, true},
		{[]string{"repo", "view"}, true},
		{[]string{"workflow", "list"}, true},
		{[]string{"label", "list"}, true},
		{[]string{"cache", "list"}, true},
		{[]string{"gist", "list"}, true},
		{[]string{"project", "list"}, true},
		{[]string{"ruleset", "list"}, true},
		{[]string{"variable", "list"}, true},
		{[]string{"secret", "list"}, true},
		{[]string{"search", "prs", "x"}, true},
		{[]string{"search", "issues", "bug"}, true},
		{[]string{"search", "repos", "clued"}, true},
		{[]string{"auth", "status"}, true},
		{[]string{"api", "repos/o/r/pulls"}, true},
		{[]string{"api", "-X", "GET", "x"}, true},
		{[]string{"api", "--method", "GET", "x"}, true},
		{[]string{"api", "--method=GET", "x"}, true},
		{[]string{"api", "-XGET", "x"}, true},
		{[]string{"api", "--paginate", "repos/o/r/issues"}, true},

		// disallowed: mutating verbs
		{[]string{"pr", "merge", "1"}, false},
		{[]string{"pr", "create"}, false},
		{[]string{"pr", "close", "1"}, false},
		{[]string{"pr", "edit", "1"}, false},
		{[]string{"issue", "create"}, false},
		{[]string{"issue", "close", "1"}, false},
		{[]string{"repo", "delete"}, false},
		{[]string{"repo", "clone", "x"}, false},
		{[]string{"release", "create"}, false},
		{[]string{"run", "cancel", "1"}, false},
		{[]string{"workflow", "run"}, false},
		{[]string{"label", "create", "x"}, false},
		{[]string{"cache", "delete", "x"}, false},
		{[]string{"gist", "create"}, false},
		{[]string{"ruleset", "check"}, false},
		{[]string{"secret", "set"}, false},
		{[]string{"secret", "view", "X"}, false},
		{[]string{"variable", "view", "X"}, false},
		{[]string{"variable", "set", "X"}, false},

		// disallowed: opens a browser
		{[]string{"pr", "view", "--web"}, false},
		{[]string{"pr", "view", "12", "-w"}, false},
		{[]string{"issue", "list", "--web"}, false},
		{[]string{"repo", "view", "-w"}, false},

		// disallowed: api with a method or body
		{[]string{"api", "-X", "DELETE", "x"}, false},
		{[]string{"api", "-XDELETE", "x"}, false},
		{[]string{"api", "--method", "POST", "x"}, false},
		{[]string{"api", "--method=PATCH", "x"}, false},
		{[]string{"api", "-f", "a=b", "x"}, false},
		{[]string{"api", "-F", "a=b", "x"}, false},
		{[]string{"api", "--field", "a=b", "x"}, false},
		{[]string{"api", "--field=a=b", "x"}, false},
		{[]string{"api", "--raw-field", "a=b", "x"}, false},
		{[]string{"api", "--raw-field=a=b", "x"}, false},
		{[]string{"api", "--input", "f", "x"}, false},
		{[]string{"api", "--input=f", "x"}, false},
		{[]string{"api", "-X"}, false},

		// disallowed: other nouns
		{[]string{"auth", "login"}, false},
		{[]string{"auth", "logout"}, false},
		{[]string{"auth"}, false},
		{[]string{"browse"}, false},
		{[]string{"config", "set", "x", "y"}, false},
		{[]string{"config", "get", "x"}, false},
		{[]string{"status"}, false},
		{[]string{"extension", "list"}, false},
		{[]string{"alias", "list"}, false},
		{[]string{"codespace", "list"}, false},
		{[]string{"ssh-key", "list"}, false},
		{[]string{"gpg-key", "list"}, false},
		{[]string{"completion"}, false},
		{[]string{"nonsense", "list"}, false},

		// disallowed: shape
		{[]string{"-R", "o/r", "pr", "list"}, false},
		{[]string{"--help"}, false},
		{[]string{"pr"}, false},
		{[]string{"search"}, false},
		{[]string{}, false},
		{nil, false},
	}
	for _, tc := range cases {
		name := strings.Join(tc.args, " ")
		if name == "" {
			name = "<empty>"
		}
		t.Run(name, func(t *testing.T) {
			if got := Allowed(tc.args); got != tc.want {
				t.Fatalf("Allowed(%q) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

func TestGHPassesArgvThroughAndTruncates(t *testing.T) {
	r := &scriptRunner{stdout: map[string]string{
		"pr list": "#1 one\n#2 two\n#3 three",
	}}
	p := Probe{Root: "/repo", Runner: r}
	got := p.GH(context.Background(), []string{"pr", "list"}, 2)
	if got != "#1 one\n#2 two" {
		t.Fatalf("GH = %q", got)
	}
	if len(r.calls) != 1 {
		t.Fatalf("calls = %v, want exactly one", r.calls)
	}
	if strings.Join(r.calls[0], " ") != "pr list" {
		t.Fatalf("argv = %q", r.calls[0])
	}
	if r.names[0] != "gh" {
		t.Fatalf("name = %q, want gh", r.names[0])
	}
	if r.dirs[0] != "/repo" {
		t.Fatalf("dir = %q, want /repo", r.dirs[0])
	}
}

func TestGHNoLimitKeepsEverything(t *testing.T) {
	r := &scriptRunner{stdout: map[string]string{
		"issue list": "a\nb\nc",
	}}
	p := Probe{Runner: r}
	if got := p.GH(context.Background(), []string{"issue", "list"}, 0); got != "a\nb\nc" {
		t.Fatalf("GH = %q", got)
	}
}

func TestGHDisallowedReturnsFallbackWithoutRunning(t *testing.T) {
	r := &scriptRunner{stdout: map[string]string{"pr merge 1": "merged"}}
	p := Probe{Runner: r}
	got := p.GH(context.Background(), []string{"pr", "merge", "1"}, 0)
	if got != "[FALLBACK: gh pr merge 1]" {
		t.Fatalf("GH = %q", got)
	}
	if len(r.calls) != 0 {
		t.Fatalf("runner was called: %v", r.calls)
	}
}

func TestGHEmptyArgsReturnsFallbackWithoutRunning(t *testing.T) {
	r := &scriptRunner{}
	p := Probe{Runner: r}
	if got := p.GH(context.Background(), nil, 0); got != collect.Fallback("gh", nil) {
		t.Fatalf("GH = %q", got)
	}
	if len(r.calls) != 0 {
		t.Fatalf("runner was called: %v", r.calls)
	}
}

func TestGHErrorWithPartialStdoutReturnsStdout(t *testing.T) {
	r := &scriptRunner{
		stdout: map[string]string{"run list": "one\ntwo\nthree"},
		errs:   map[string]error{"run list": errors.New("gh: exit status 1")},
	}
	p := Probe{Runner: r}
	if got := p.GH(context.Background(), []string{"run", "list"}, 2); got != "one\ntwo" {
		t.Fatalf("GH = %q", got)
	}
}

func TestGHErrorWithoutStdoutReturnsFallback(t *testing.T) {
	r := &scriptRunner{errs: map[string]error{"pr list": errors.New("gh: not authenticated")}}
	p := Probe{Runner: r}
	if got := p.GH(context.Background(), []string{"pr", "list"}, 5); got != "[FALLBACK: gh pr list]" {
		t.Fatalf("GH = %q", got)
	}
}

func TestGHDefaultRunnerIsExecRunner(t *testing.T) {
	// A nil Runner must not panic; the real gh is likely absent or unauthenticated
	// so the answer is either real output or the fallback. We only check the
	// disallowed path, which never touches the Runner, plus that a nil Runner
	// resolves to something.
	p := Probe{}
	if got := p.GH(context.Background(), []string{"browse"}, 0); got != "[FALLBACK: gh browse]" {
		t.Fatalf("GH = %q", got)
	}
	if _, ok := p.runner().(collect.ExecRunner); !ok {
		t.Fatalf("runner() = %T, want collect.ExecRunner", p.runner())
	}
}

const prListArgs = "pr list --head feat --state open --json number,title"

func TestPRForBranchParsesFirstPR(t *testing.T) {
	r := &scriptRunner{stdout: map[string]string{
		prListArgs: `[{"number":12,"title":"Add x"},{"number":13,"title":"Other"}]`,
	}}
	p := Probe{Root: "/repo", Runner: r}
	if got := p.PRForBranch(context.Background(), "feat"); got != "#12: Add x" {
		t.Fatalf("PRForBranch = %q", got)
	}
	if len(r.calls) != 1 {
		t.Fatalf("calls = %v, want exactly one", r.calls)
	}
	if strings.Join(r.calls[0], " ") != prListArgs {
		t.Fatalf("argv = %q", r.calls[0])
	}
	if r.names[0] != "gh" || r.dirs[0] != "/repo" {
		t.Fatalf("name/dir = %q/%q", r.names[0], r.dirs[0])
	}
}

func TestPRForBranchEmptyArrayIsNone(t *testing.T) {
	r := &scriptRunner{stdout: map[string]string{prListArgs: "[]"}}
	p := Probe{Runner: r}
	if got := p.PRForBranch(context.Background(), "feat"); got != collect.None {
		t.Fatalf("PRForBranch = %q", got)
	}
}

func TestPRForBranchInvalidJSONIsNone(t *testing.T) {
	r := &scriptRunner{stdout: map[string]string{prListArgs: "not json"}}
	p := Probe{Runner: r}
	if got := p.PRForBranch(context.Background(), "feat"); got != collect.None {
		t.Fatalf("PRForBranch = %q", got)
	}
}

func TestPRForBranchRunnerErrorIsNone(t *testing.T) {
	r := &scriptRunner{
		stdout: map[string]string{prListArgs: `[{"number":12,"title":"Add x"}]`},
		errs:   map[string]error{prListArgs: errors.New("gh: exit status 4")},
	}
	p := Probe{Runner: r}
	if got := p.PRForBranch(context.Background(), "feat"); got != collect.None {
		t.Fatalf("PRForBranch = %q", got)
	}
}

func TestPRForBranchEmptyBranchIsNoneWithoutRunning(t *testing.T) {
	r := &scriptRunner{}
	p := Probe{Runner: r}
	if got := p.PRForBranch(context.Background(), ""); got != collect.None {
		t.Fatalf("PRForBranch = %q", got)
	}
	if len(r.calls) != 0 {
		t.Fatalf("runner was called: %v", r.calls)
	}
}
