package codesearch

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
)

// Score is how a run measured up to its task's ground truth. FileRecall and
// SymbolRecall are the shares of the truth's files and symbols the answer
// named (for an edit, FileRecall is the share of truth files the change
// touched); Success is both at or above the truth's MinRecall and, for an
// edit, the check passing.
type Score struct {
	FileRecall     float64  `json:"file_recall"`
	SymbolRecall   float64  `json:"symbol_recall"`
	MissingFiles   []string `json:"missing_files,omitempty"`
	MissingSymbols []string `json:"missing_symbols,omitempty"`
	Changed        []string `json:"changed,omitempty"`
	CheckPassed    *bool    `json:"check_passed,omitempty"`
	Success        bool     `json:"success"`
}

// minRecall is the truth's MinRecall, 1 when unset.
func (t Truth) minRecall() float64 {
	if t.MinRecall <= 0 {
		return 1
	}
	return t.MinRecall
}

// ScoreAnswer scores an answer to a lookup task: which truth files it names
// (the whole repository-relative path, or a tail of it of at least two
// components, e.g. "service/documents.go:310" for
// "mind-palace/service/documents.go") and which truth symbols it names
// (whole identifiers; "A.b", "A::b", "A#b", "A->b" and "A:b" are the same,
// and a qualified symbol also counts when each of its parts is named).
func ScoreAnswer(t Truth, answer string) Score {
	var s Score
	s.FileRecall, s.MissingFiles = recall(t.Files, func(f string) bool { return mentionsFile(answer, f) })
	s.SymbolRecall, s.MissingSymbols = recall(t.Symbols, func(sym string) bool { return mentionsSymbol(answer, sym) })
	min := t.minRecall()
	s.Success = s.FileRecall >= min-1e-9 && s.SymbolRecall >= min-1e-9
	return s
}

// ScoreEdit scores an edit: the share of truth files among the changed
// files, and the check's result (nil when there is none).
func ScoreEdit(t Truth, changed []string, checkPassed *bool) Score {
	s := Score{Changed: changed, CheckPassed: checkPassed, SymbolRecall: 1}
	s.FileRecall, s.MissingFiles = recall(t.Files, func(f string) bool { return slices.Contains(changed, f) })
	s.Success = s.FileRecall >= t.minRecall()-1e-9 && (checkPassed == nil || *checkPassed)
	return s
}

func recall(want []string, found func(string) bool) (float64, []string) {
	if len(want) == 0 {
		return 1, nil
	}
	var missing []string
	for _, w := range want {
		if !found(w) {
			missing = append(missing, w)
		}
	}
	return float64(len(want)-len(missing)) / float64(len(want)), missing
}

// mentionsFile reports whether text names the file p: p itself or a tail
// of it with at least two components (one when p has one), not glued to
// other path characters before (a "/" is fine: absolute paths) or after.
func mentionsFile(text, p string) bool {
	parts := strings.Split(p, "/")
	minParts := min(2, len(parts))
	for i := 0; i <= len(parts)-minParts; i++ {
		tail := strings.Join(parts[i:], "/")
		if containsBounded(text, tail, isPathBefore, isPathAfter) {
			return true
		}
	}
	return false
}

func isPathBefore(r byte) bool { return isWordByte(r) || r == '.' || r == '-' }
func isPathAfter(r byte) bool  { return isWordByte(r) || r == '/' || r == '-' }

func isWordByte(r byte) bool {
	return r == '_' || r == '$' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= 0x80
}

// qualifierRe finds the separators that join a qualifier to a name.
var qualifierRe = regexp.MustCompile(`([A-Za-z0-9_$\]>)])(::|->|#|:|\.)([A-Za-z_$~])`)

func normalizeQualifiers(s string) string {
	// Twice: adjacent matches share a character (a::b::c).
	for range 2 {
		s = qualifierRe.ReplaceAllString(s, "$1.$3")
	}
	return s
}

// mentionsSymbol reports whether text names sym as a whole identifier.
// Dashes count as identifier characters when sym has one (Lisp names).
func mentionsSymbol(text, sym string) bool {
	word := isWordByte
	if strings.Contains(sym, "-") {
		word = func(r byte) bool { return isWordByte(r) || r == '-' }
	}
	norm := normalizeQualifiers(sym)
	if containsBounded(text, sym, word, word) {
		return true
	}
	ntext := normalizeQualifiers(text)
	if containsBounded(ntext, norm, word, word) {
		return true
	}
	parts := strings.Split(norm, ".")
	if len(parts) < 2 {
		return false
	}
	for _, p := range parts {
		if p == "" || !containsBounded(ntext, p, word, word) {
			return false
		}
	}
	return true
}

// containsBounded reports whether needle occurs in text with neither the
// byte before rejected by before nor the byte after rejected by after. A
// needle that itself starts or ends with a non-word byte (a "-reduce", a
// "route!") is checked only on the sides where it has a word byte.
func containsBounded(text, needle string, before, after func(byte) bool) bool {
	if needle == "" {
		return false
	}
	checkBefore := isWordByte(needle[0])
	checkAfter := isWordByte(needle[len(needle)-1])
	if needle[0] == '-' {
		checkBefore = true
	}
	for from := 0; ; {
		i := strings.Index(text[from:], needle)
		if i < 0 {
			return false
		}
		i += from
		end := i + len(needle)
		okBefore := !checkBefore || i == 0 || !before(text[i-1])
		okAfter := !checkAfter || end == len(text) || !after(text[end])
		if okBefore && okAfter {
			return true
		}
		from = i + 1
	}
}

// Stat is one arm in one group (a family or a language): the runs and
// tasks it covers, the share of successful runs, and the medians over the
// group's tasks of each task's median over its runs.
type Stat struct {
	Group           string  `json:"group"`
	Arm             string  `json:"arm"`
	Tasks           int     `json:"tasks"`
	Runs            int     `json:"runs"`
	Errors          int     `json:"errors"`
	SuccessRate     float64 `json:"success_rate"`
	MedianTokens    float64 `json:"median_tokens"`
	MedianTurns     float64 `json:"median_turns"`
	MedianToolCalls float64 `json:"median_tool_calls"`
	MedianCodeCalls float64 `json:"median_code_tool_calls"`
	MedianCostUSD   float64 `json:"median_cost_usd"`
}

// Report is the evaluation's summary: by family, by language, and over
// every task, each per arm.
type Report struct {
	Arms     []string `json:"arms"`
	ByFamily []Stat   `json:"by_family"`
	ByLang   []Stat   `json:"by_lang"`
	Overall  []Stat   `json:"overall"`
}

// Summarize builds the report of a set of runs.
func Summarize(runs []Run) Report {
	var arms []string
	for _, r := range runs {
		if !slices.Contains(arms, r.Arm) {
			arms = append(arms, r.Arm)
		}
	}
	slices.SortStableFunc(arms, func(a, b string) int { return armOrder(a) - armOrder(b) })
	rep := Report{Arms: arms}
	rep.ByFamily = group(runs, arms, func(r Run) string { return r.Family }, Families)
	var langs []string
	for _, r := range runs {
		if !slices.Contains(langs, r.Lang) {
			langs = append(langs, r.Lang)
		}
	}
	slices.Sort(langs)
	rep.ByLang = group(runs, arms, func(r Run) string { return r.Lang }, langs)
	rep.Overall = group(runs, arms, func(Run) string { return "all" }, []string{"all"})
	return rep
}

func armOrder(name string) int {
	if i := slices.IndexFunc(Arms, func(a Arm) bool { return a.Name == name }); i >= 0 {
		return i
	}
	return len(Arms)
}

func group(runs []Run, arms []string, key func(Run) string, order []string) []Stat {
	var out []Stat
	for _, g := range order {
		for _, arm := range arms {
			byTask := map[string][]Run{}
			var taskOrder []string
			st := Stat{Group: g, Arm: arm}
			ok := 0
			for _, r := range runs {
				if key(r) != g || r.Arm != arm {
					continue
				}
				if _, seen := byTask[r.Task]; !seen {
					taskOrder = append(taskOrder, r.Task)
				}
				byTask[r.Task] = append(byTask[r.Task], r)
				st.Runs++
				if r.Score.Success {
					ok++
				}
				if r.Error != "" {
					st.Errors++
				}
			}
			if st.Runs == 0 {
				continue
			}
			st.Tasks = len(taskOrder)
			st.SuccessRate = float64(ok) / float64(st.Runs)
			var tokens, turns, calls, code, cost []float64
			for _, id := range taskOrder {
				rs := byTask[id]
				tokens = append(tokens, medianOf(rs, func(r Run) float64 { return float64(r.Measures.Tokens()) }))
				turns = append(turns, medianOf(rs, func(r Run) float64 { return float64(r.Measures.Turns) }))
				calls = append(calls, medianOf(rs, func(r Run) float64 { return float64(r.Measures.ToolCalls) }))
				code = append(code, medianOf(rs, func(r Run) float64 { return float64(r.Measures.CodeToolCalls) }))
				cost = append(cost, medianOf(rs, func(r Run) float64 {
					if r.Measures.CostUSD == nil {
						return 0
					}
					return *r.Measures.CostUSD
				}))
			}
			st.MedianTokens, st.MedianTurns, st.MedianToolCalls = Median(tokens), Median(turns), Median(calls)
			st.MedianCodeCalls, st.MedianCostUSD = Median(code), Median(cost)
			out = append(out, st)
		}
	}
	return out
}

func medianOf(runs []Run, f func(Run) float64) float64 {
	vals := make([]float64, 0, len(runs))
	for _, r := range runs {
		vals = append(vals, f(r))
	}
	return Median(vals)
}

// Median is the middle value (the mean of the two middle ones for an even
// count); 0 for none.
func Median(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	s := slices.Clone(vals)
	slices.Sort(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// Markdown renders the report as tables for the plan's work log: one row
// per group and arm, tokens also as a change against the grep arm of the
// same group.
func (r Report) Markdown() string {
	var b strings.Builder
	section := func(title, col string, stats []Stat) {
		if len(stats) == 0 {
			return
		}
		fmt.Fprintf(&b, "### %s\n\n", title)
		fmt.Fprintf(&b, "| %s | arm | tasks | runs | success | median tokens | vs grep | median turns | median tool calls | median code tool calls | median cost (USD) |\n", col)
		b.WriteString("|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
		base := map[string]float64{}
		for _, s := range stats {
			if s.Arm == "grep" {
				base[s.Group] = s.MedianTokens
			}
		}
		for _, s := range stats {
			vs := "-"
			if g, ok := base[s.Group]; ok && g > 0 && s.Arm != "grep" {
				vs = fmt.Sprintf("%+.0f%%", (s.MedianTokens/g-1)*100)
			}
			errs := ""
			if s.Errors > 0 {
				errs = fmt.Sprintf(" (%d errors)", s.Errors)
			}
			fmt.Fprintf(&b, "| %s | %s | %d | %d%s | %.0f%% | %s | %s | %s | %s | %s | %.3f |\n",
				s.Group, s.Arm, s.Tasks, s.Runs, errs, s.SuccessRate*100, num(s.MedianTokens), vs,
				num(s.MedianTurns), num(s.MedianToolCalls), num(s.MedianCodeCalls), s.MedianCostUSD)
		}
		b.WriteString("\n")
	}
	section("Overall", "tasks", r.Overall)
	section("By family", "family", r.ByFamily)
	section("By language", "language", r.ByLang)
	return b.String()
}

func num(v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.1f", v)
}
