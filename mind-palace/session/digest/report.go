// Package digest tells the story of a day, or a range of days, of captured
// sessions: it renders their activity timelines as a report (one section
// per session, one row per activity, sub-agents with what they returned)
// and has a model summarise the report into a short first-person retro,
// which it keeps as a memory record of kind "digest" so every machine of
// the account shares it and search_memory and the prime report find it.
package digest

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/query"
)

// Period is a span of whole UTC days: From is the first day's midnight, To
// the midnight after the last day.
type Period struct {
	From time.Time
	To   time.Time
}

// Day is a period of one UTC day.
func Day(day string) (Period, error) {
	d, err := time.Parse(time.DateOnly, day)
	if err != nil {
		return Period{}, fmt.Errorf("day must be YYYY-MM-DD: %q", day)
	}
	return Period{From: d, To: d.AddDate(0, 0, 1)}, nil
}

// Range is a period from one day to another, inclusive.
func Range(from, to string) (Period, error) {
	f, err := Day(from)
	if err != nil {
		return Period{}, err
	}
	t, err := Day(to)
	if err != nil {
		return Period{}, err
	}
	if t.From.Before(f.From) {
		return Period{}, fmt.Errorf("the range ends (%s) before it starts (%s)", to, from)
	}
	return Period{From: f.From, To: t.To}, nil
}

// IsDay reports whether the period is one day.
func (p Period) IsDay() bool { return p.To.Equal(p.From.AddDate(0, 0, 1)) }

// FirstDay and LastDay are the period's bounds as YYYY-MM-DD.
func (p Period) FirstDay() string { return p.From.Format(time.DateOnly) }
func (p Period) LastDay() string  { return p.To.AddDate(0, 0, -1).Format(time.DateOnly) }

// Days lists the period's days as YYYY-MM-DD.
func (p Period) Days() []string {
	var out []string
	for d := p.From; d.Before(p.To); d = d.AddDate(0, 0, 1) {
		out = append(out, d.Format(time.DateOnly))
	}
	return out
}

// String names the period: the day, or "from..to".
func (p Period) String() string {
	if p.IsDay() {
		return p.FirstDay()
	}
	return p.FirstDay() + ".." + p.LastDay()
}

// DefaultKinds are the activity kinds a report shows unless told otherwise.
var DefaultKinds = []string{"user", "response", "bash", "edit", "thinking", "task"}

// Options select what a report covers.
type Options struct {
	// Kinds are the activity kinds to show; nil means DefaultKinds. The
	// "result" entries that carry what a sub-agent returned are always read.
	Kinds []string
	// ProjectPath and GitOrigin restrict the sessions (regexes).
	ProjectPath string
	GitOrigin   string
}

// Report is the activity report of a period.
type Report struct {
	Period   string          `json:"period"`
	From     string          `json:"from"`
	To       string          `json:"to"`
	Sessions []SessionReport `json:"sessions"`
	// Tokens estimates the rendered report's size (one token per four
	// characters); Warning is set when it is past the WarnTokens.
	Tokens  int    `json:"tokens"`
	Warning string `json:"warning,omitempty"`
}

// SessionReport is one session's part of the report.
type SessionReport struct {
	SessionID   string    `json:"session_id"`
	ProjectPath string    `json:"project_path,omitempty"`
	GitOrigin   string    `json:"git_origin,omitempty"`
	Title       string    `json:"title,omitempty"`
	Model       string    `json:"model,omitempty"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	Rows        []Row     `json:"rows"`
}

// Row is one activity: when, what kind, its label and detail, how many
// consecutive repeats it stands for, and for a sub-agent what it returned.
type Row struct {
	At       time.Time `json:"at"`
	Kind     string    `json:"kind"`
	Label    string    `json:"label"`
	Detail   string    `json:"detail"`
	Count    int       `json:"count,omitempty"`
	Returned string    `json:"returned,omitempty"`
}

const (
	// DetailChars bounds a row's detail in the rendered report.
	DetailChars = 120
	// WarnTokens is the estimated size past which a report is flagged as
	// too large for one prompt.
	WarnTokens = 300000
	// sessionScan bounds the sessions and the activities read per session.
	sessionScan = 500
)

// Build reads the period's sessions and their activities into a report.
func Build(ctx context.Context, svc *query.Service, p Period, opts Options) (Report, error) {
	kinds := opts.Kinds
	if len(kinds) == 0 {
		kinds = DefaultKinds
	}
	wanted := map[string]bool{}
	for _, k := range kinds {
		wanted[k] = true
	}
	// Sessions last seen in or after the period; those started after it
	// are dropped below.
	sessions, err := svc.Store.FindSessions(ctx, session.SessionQuery{
		AccountID: svc.AccountID, ProjectPathRe: opts.ProjectPath, GitOriginRe: opts.GitOrigin,
		LastSeenFrom: p.From, Limit: sessionScan,
	})
	if err != nil {
		return Report{}, err
	}
	rep := Report{Period: p.String(), From: p.FirstDay(), To: p.LastDay(), Sessions: []SessionReport{}}
	for _, s := range sessions {
		id, _ := s.String("session_id")
		if started, ok := s["started_at"].(time.Time); ok && !started.Before(p.To) {
			continue
		}
		acts, err := svc.Activities(ctx, query.ActivitiesArgs{SessionID: id, Kinds: append(append([]string{}, kinds...), "result"), Since: p.From.Format(time.RFC3339), Limit: sessionScan})
		if err != nil {
			return Report{}, err
		}
		sr := sessionReport(s, acts, p, wanted)
		if len(sr.Rows) == 0 {
			continue
		}
		rep.Sessions = append(rep.Sessions, sr)
	}
	sort.SliceStable(rep.Sessions, func(i, j int) bool { return rep.Sessions[i].Start.Before(rep.Sessions[j].Start) })
	rep.Tokens = len(rep.Markdown()) / 4
	if rep.Tokens > WarnTokens {
		rep.Warning = fmt.Sprintf("the report is about %d tokens, past the %d a single prompt should carry; narrow the period or the kinds", rep.Tokens, WarnTokens)
	}
	return rep, nil
}

// sessionReport turns one session's activities (newest first) into rows
// (oldest first) within the period, attaching each sub-agent's result to
// its task.
func sessionReport(s session.Doc, acts []query.Activity, p Period, wanted map[string]bool) SessionReport {
	sr := SessionReport{SessionID: s["session_id"].(string), Rows: []Row{}}
	sr.ProjectPath, _ = s.String("project_path")
	sr.GitOrigin, _ = s.String("git_origin")
	sr.Title, _ = s.String("title")
	sr.Model, _ = s.String("model")
	returned := map[string]string{}
	for _, a := range acts {
		if a.Kind == "result" && a.ToolUseID != "" {
			returned[a.ToolUseID] = a.Detail
		}
	}
	for i := len(acts) - 1; i >= 0; i-- {
		a := acts[i]
		if !wanted[a.Kind] {
			continue
		}
		at := activityTime(a)
		if at.Before(p.From) || !at.Before(p.To) {
			continue
		}
		row := Row{At: at, Kind: a.Kind, Label: a.Label, Detail: a.Detail, Count: a.Count}
		if a.Kind == "task" {
			row.Returned = returned[a.ToolUseID]
		}
		if sr.Start.IsZero() || at.Before(sr.Start) {
			sr.Start = at
		}
		if at.After(sr.End) {
			sr.End = at
		}
		sr.Rows = append(sr.Rows, row)
	}
	return sr
}

// activityTime is the activity's own timestamp, else when it was stored.
func activityTime(a query.Activity) time.Time {
	if at, err := time.Parse(time.RFC3339Nano, a.Timestamp); err == nil {
		return at
	}
	return a.CreatedAt
}

// icons mark the kinds in the rendered report.
var icons = map[string]string{
	"user": "👤", "response": "🤖", "thinking": "💭", "bash": "$", "commit": "✔", "edit": "✏", "read": "📖",
	"task": "🧵", "tool": "🔧", "result": "↩", "state": "⏱", "compaction": "🗜", "api_error": "⚠", "turn": "⏲", "other": "·",
}

// Markdown renders the report: a heading per session with its project,
// repository, title, model and span, then one "[HH:MM] <icon> <label>:
// <detail>" row per activity (details cut at DetailChars, repeats marked
// "×N") and what each sub-agent returned indented under its task.
func (r Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Activity report %s\n", r.Period)
	if len(r.Sessions) == 0 {
		b.WriteString("\nNo sessions were captured in this period.\n")
		return b.String()
	}
	for _, s := range r.Sessions {
		fmt.Fprintf(&b, "\n## Session %s\n", s.SessionID)
		var meta []string
		if s.Title != "" {
			meta = append(meta, "title: "+s.Title)
		}
		if s.ProjectPath != "" {
			meta = append(meta, "project: "+s.ProjectPath)
		}
		if s.GitOrigin != "" {
			meta = append(meta, "repository: "+s.GitOrigin)
		}
		if s.Model != "" {
			meta = append(meta, "model: "+s.Model)
		}
		meta = append(meta, fmt.Sprintf("from %s to %s UTC", s.Start.UTC().Format("2006-01-02 15:04"), s.End.UTC().Format("15:04")))
		b.WriteString(strings.Join(meta, " · ") + "\n\n")
		for _, row := range s.Rows {
			icon := icons[row.Kind]
			if icon == "" {
				icon = "·"
			}
			detail := cut(row.Detail, DetailChars)
			fmt.Fprintf(&b, "[%s] %s %s: %s", row.At.UTC().Format("15:04"), icon, row.Label, detail)
			if row.Count > 1 {
				fmt.Fprintf(&b, " ×%d", row.Count)
			}
			b.WriteString("\n")
			if row.Returned != "" {
				fmt.Fprintf(&b, "    ↳ returned: %s\n", cut(row.Returned, DetailChars*3))
			}
		}
	}
	return b.String()
}

// cut keeps at most n characters of one-line text, marking the cut.
func cut(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}
