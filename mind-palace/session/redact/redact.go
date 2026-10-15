// Package redact masks secrets in captured session data before it is
// written: credentials the built-in rules recognise, the user's own
// patterns, and <private>…</private> spans. Store wraps a session.Store so
// every write path (the daemon, the tailer, backfill, the relay's catch-up)
// redacts the same way, and drops what belongs to an excluded path, so
// neither the local store nor a clued server ever holds the original.
package redact

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// Rule is a named pattern whose matches are replaced by [REDACTED:<name>].
type Rule struct {
	Name string
	Re   *regexp.Regexp
}

// Builtin are the credential rules applied at capture: the privacy-redact
// enricher's API-key, AWS-key and JWT patterns plus GitHub and Slack tokens
// and PEM private keys. E-mail addresses are not secrets and stay; a
// redactPatterns entry masks them too when wanted.
var Builtin = []Rule{
	{"private-key", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`)},
	{"api-key", regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`)},
	{"aws-key", regexp.MustCompile(`\b(AKIA[0-9A-Z]{16})\b`)},
	{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`)},
	{"github-token", regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{22,})\b`)},
	{"slack-token", regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}`)},
}

// PrivateRule is the name recorded for a <private> span.
const PrivateRule = "private"

// privateSpan is a <private>…</private> span; an unclosed one runs to the
// end of its line.
var privateSpan = regexp.MustCompile(`<private>(?s:.*?)</private>|<private>[^\n]*`)

// Redactor applies the rules; build it with New.
type Redactor struct {
	rules []Rule
}

// New returns the redactor of the built-in rules plus extra, the user's
// redactPatterns (named pattern-1, pattern-2, … in order). A pattern that
// does not compile is skipped and reported in errs; the rest still apply.
func New(extra []string) (r *Redactor, errs []error) {
	rules := slices.Clone(Builtin)
	for i, p := range extra {
		re, err := regexp.Compile(p)
		if err != nil {
			errs = append(errs, fmt.Errorf("redactPatterns[%d]: %w", i, err))
			continue
		}
		rules = append(rules, Rule{Name: fmt.Sprintf("pattern-%d", i+1), Re: re})
	}
	return &Redactor{rules: rules}, errs
}

// String masks s, returning the masked text and the names of the rules that
// fired (none when nothing matched; the text is then s itself).
func (r *Redactor) String(s string) (string, []string) {
	var fired []string
	if strings.Contains(s, "<private>") {
		s = privateSpan.ReplaceAllLiteralString(s, "[PRIVATE]")
		fired = append(fired, PrivateRule)
	}
	for _, rule := range r.rules {
		if rule.Re.MatchString(s) {
			s = rule.Re.ReplaceAllLiteralString(s, "[REDACTED:"+rule.Name+"]")
			fired = append(fired, rule.Name)
		}
	}
	return s, fired
}

// Value masks every string in a JSON-shaped value (maps, slices, strings;
// map keys are left alone) and returns a copy with the masked strings, and
// the sorted names of the rules that fired. A value with nothing to mask
// is returned as it is.
func (r *Redactor) Value(v any) (any, []string) {
	fired := map[string]bool{}
	out := r.walk(v, fired)
	if len(fired) == 0 {
		return v, nil
	}
	names := make([]string, 0, len(fired))
	for n := range fired {
		names = append(names, n)
	}
	slices.Sort(names)
	return out, names
}

func (r *Redactor) walk(v any, fired map[string]bool) any {
	switch x := v.(type) {
	case string:
		masked, names := r.String(x)
		for _, n := range names {
			fired[n] = true
		}
		return masked
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = r.walk(e, fired)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = r.walk(e, fired)
		}
		return out
	case session.Doc:
		out := make(session.Doc, len(x))
		for k, e := range x {
			out[k] = r.walk(e, fired)
		}
		return out
	}
	return v
}

// Merge adds the names of b to a, sorted and without duplicates.
func Merge(a, b []string) []string {
	if len(b) == 0 {
		return a
	}
	out := append(slices.Clone(a), b...)
	slices.Sort(out)
	return slices.Compact(out)
}

// openPrivate and closePrivate delimit a private span.
const (
	openPrivate  = "<private>"
	closePrivate = "</private>"
)

// StripPrivate removes every <private>…</private> span from text, tags
// included. Spans nest: the text stays private until every opened span is
// closed, and an unclosed span drops the rest of the text. Memory runs it
// before anything reaches an engine or the store.
func StripPrivate(text string) string {
	if !strings.Contains(text, openPrivate) {
		return text
	}
	var b strings.Builder
	depth := 0
	for len(text) > 0 {
		open := strings.Index(text, openPrivate)
		if depth == 0 {
			if open < 0 {
				b.WriteString(text)
				break
			}
			b.WriteString(text[:open])
			text = text[open+len(openPrivate):]
			depth = 1
			continue
		}
		end := strings.Index(text, closePrivate)
		if end < 0 {
			break // unclosed: the rest is private
		}
		if open >= 0 && open < end {
			depth++
			text = text[open+len(openPrivate):]
			continue
		}
		depth--
		text = text[end+len(closePrivate):]
	}
	return b.String()
}
