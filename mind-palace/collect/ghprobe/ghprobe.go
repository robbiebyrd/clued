// Package ghprobe answers questions about the GitHub side of a repository
// through the gh CLI: the open pull request of a branch, and any read-only
// gh command a context collector wants to quote.
//
// Allowed is the gate: only nouns and verbs that read state pass, so a probe
// can never merge, create, delete, log in or open a browser. Everything that
// goes wrong (gh missing, not authenticated, offline, a disallowed command)
// maps to a sentinel from package collect; no probe returns an error.
package ghprobe

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/collect"
)

// DefaultTimeout bounds one gh call when the Probe has no Runner: gh talks to
// the network, so it gets more room than collect.DefaultTimeout.
const DefaultTimeout = 15 * time.Second

// Probe runs gh in a repository.
type Probe struct {
	Root   string         // repository directory; "" means the working directory
	Runner collect.Runner // nil means collect.ExecRunner{Timeout: DefaultTimeout}
}

func (p Probe) runner() collect.Runner {
	if p.Runner != nil {
		return p.Runner
	}
	return collect.ExecRunner{Timeout: DefaultTimeout}
}

// readVerbs are the verbs that only read state, for the nouns in readNouns.
var readVerbs = map[string]bool{
	"list":   true,
	"view":   true,
	"status": true,
	"checks": true,
	"diff":   true,
	"ls":     true,
}

// readNouns are the nouns allowed with any verb of readVerbs. The value
// narrows the verbs further when non-nil.
var readNouns = map[string]map[string]bool{
	"pr":       nil,
	"issue":    nil,
	"run":      nil,
	"release":  nil,
	"repo":     nil,
	"workflow": nil,
	"label":    nil,
	"cache":    nil,
	"gist":     nil,
	"project":  nil,
	"ruleset":  nil,
	"variable": {"list": true},
	"secret":   {"list": true},
}

// Allowed reports whether gh args is a read-only command a probe may run.
//
// The first argument is the noun and must not be a flag. Resource nouns (pr,
// issue, run, ...) need a read verb (list, view, status, checks, diff, ls;
// secret and variable only list) and must not carry --web or -w. search is
// allowed with any verb, auth only as auth status, and api only without a
// method or body flag other than GET. Every other noun is refused.
func Allowed(args []string) bool {
	if len(args) == 0 {
		return false
	}
	noun := args[0]
	if noun == "" || strings.HasPrefix(noun, "-") {
		return false
	}
	rest := args[1:]
	switch noun {
	case "search":
		return len(rest) > 0
	case "auth":
		return len(rest) == 1 && rest[0] == "status"
	case "api":
		return apiAllowed(rest)
	}
	verbs, ok := readNouns[noun]
	if !ok {
		return false
	}
	if len(rest) == 0 {
		return false
	}
	verb := rest[0]
	if verbs == nil {
		verbs = readVerbs
	}
	if !verbs[verb] {
		return false
	}
	for _, a := range rest[1:] {
		if a == "--web" || a == "-w" {
			return false
		}
	}
	return true
}

// apiAllowed accepts gh api arguments that neither choose a method other
// than GET nor attach a body.
func apiAllowed(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-X" || a == "--method":
			if i+1 >= len(args) || !isGet(args[i+1]) {
				return false
			}
			i++
		case strings.HasPrefix(a, "--method="):
			if !isGet(strings.TrimPrefix(a, "--method=")) {
				return false
			}
		case strings.HasPrefix(a, "-X") && len(a) > 2:
			if !isGet(a[2:]) {
				return false
			}
		case a == "-f" || a == "-F" || a == "--field" || a == "--raw-field" || a == "--input":
			return false
		case strings.HasPrefix(a, "--field=") || strings.HasPrefix(a, "--raw-field=") || strings.HasPrefix(a, "--input="):
			return false
		}
	}
	return true
}

func isGet(method string) bool {
	return strings.EqualFold(method, "GET")
}

// GH runs gh args and returns the first limit lines of its output (limit <= 0
// keeps everything). A command Allowed refuses is not run and answers with
// the fallback; a failed command answers with whatever it printed, or with
// the fallback when it printed nothing.
func (p Probe) GH(ctx context.Context, args []string, limit int) string {
	if !Allowed(args) {
		return collect.Fallback("gh", args)
	}
	out, err := p.runner().Run(ctx, p.Root, "gh", args...)
	if err != nil && out == "" {
		return collect.Fallback("gh", args)
	}
	return collect.Truncate(out, limit)
}

// PRForBranch returns "#<number>: <title>" of the first open pull request
// whose head is branch, or collect.None when there is none, gh fails, or the
// branch is empty.
func (p Probe) PRForBranch(ctx context.Context, branch string) string {
	if branch == "" {
		return collect.None
	}
	out, err := p.runner().Run(ctx, p.Root, "gh",
		"pr", "list", "--head", branch, "--state", "open", "--json", "number,title")
	if err != nil {
		return collect.None
	}
	var prs []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
	}
	if err := json.Unmarshal([]byte(out), &prs); err != nil || len(prs) == 0 {
		return collect.None
	}
	return "#" + strconv.Itoa(prs[0].Number) + ": " + prs[0].Title
}
