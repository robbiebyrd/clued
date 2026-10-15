package memory

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/digest"
	"github.com/robbiebyrd/clued/mind-palace/session/summary"
)

//go:embed templates/day.md templates/recent.md templates/archive.md
var templates embed.FS

// RunReport says what a run did.
type RunReport struct {
	Project string `json:"project,omitempty"`
	Engine  string `json:"engine"`
	// Notes, Days, Recent and Archives count the records written.
	Notes    int `json:"notes"`
	Days     int `json:"days"`
	Recent   int `json:"recent"`
	Archives int `json:"archives"`
	// EngineRuns counts the calls to the configured engine; Fallbacks the
	// work done by the extract engine instead, because the daily cap was
	// reached (CapReached) or the engine failed (Errors).
	EngineRuns int      `json:"engine_runs"`
	Fallbacks  int      `json:"fallbacks"`
	CapReached bool     `json:"cap_reached,omitempty"`
	CostUSD    float64  `json:"cost_usd"`
	Skipped    []string `json:"skipped,omitempty"`
	Errors     []string `json:"errors,omitempty"`
}

// pendingWindow is how far back a run looks for sessions without a note,
// and pendingIdle how long a session must be idle (unless ended).
const (
	pendingWindow = 7 * 24 * time.Hour
	pendingIdle   = 30 * time.Minute
	pendingLimit  = 100
)

// Run brings the project's memory up to date (every project when project
// is ""): the notes of its sessions that need one, then one day record per
// finished UTC day holding notes, the recent record over the last seven
// days and one archive per month of days older than that. Each roll-up
// records the hashes of its sources, so a run with nothing new makes no
// engine call; records the user edited are kept. Past MaxRunsPerDay the
// rest of the work falls back to ExtractEngine, as does an engine error.
// A run holds the project's lock; a concurrent one returns ErrLocked.
func (s *Service) Run(ctx context.Context, project string, engine digest.Engine, model string) (RunReport, error) {
	rep := RunReport{Project: project, Engine: engine.Name()}
	unlock, err := s.lock(project)
	if err != nil {
		return rep, err
	}
	defer unlock()
	r := &runner{s: s, engine: engine, model: model, rep: &rep}
	if err := r.notes(ctx, project, ""); err != nil {
		return rep, err
	}
	projects := []string{project}
	if project == "" {
		if projects, err = s.projects(ctx); err != nil {
			return rep, err
		}
	}
	for _, p := range projects {
		if err := r.layers(ctx, p); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

// RunSession writes the note of one session at once (the SessionEnd hook:
// the session may not look idle yet), then runs its project.
func (s *Service) RunSession(ctx context.Context, sessionID string, engine digest.Engine, model string) (RunReport, error) {
	doc, err := s.Store.GetSession(ctx, s.AccountID, sessionID)
	if err != nil {
		return RunReport{Engine: engine.Name()}, err
	}
	project := projectOf(doc)
	rep := RunReport{Project: project, Engine: engine.Name()}
	unlock, err := s.lock(project)
	if err != nil {
		return rep, err
	}
	r := &runner{s: s, engine: engine, model: model, rep: &rep}
	err = r.notes(ctx, project, sessionID)
	if err == nil {
		err = r.layers(ctx, project)
	}
	unlock()
	return rep, err
}

// Checkpoint stores a running session's note as its memory-checkpoint
// (PreCompact), within the daily cap; the session's note replaces it.
func (s *Service) Checkpoint(ctx context.Context, sessionID string, engine digest.Engine, model string) error {
	if err := s.summary().Eligible(ctx, sessionID); err != nil {
		return err
	}
	rep := RunReport{Engine: engine.Name()}
	r := &runner{s: s, engine: engine, model: model, rep: &rep}
	_, err := r.withEngine(ctx, func(ctx context.Context, e digest.Engine) (float64, error) {
		sum, err := s.summary().Checkpoint(ctx, sessionID, e, r.modelFor(e))
		return sum.CostUSD, err
	})
	return err
}

// summary is the Summary service with the limits' MinPrompts.
func (s *Service) summary() *summary.Service {
	sum := summary.Service{Store: s.Store, AccountID: s.AccountID, Redact: s.Redact, Now: s.Now}
	if s.Summary != nil {
		sum = *s.Summary
	}
	if s.Limits.MinPrompts > 0 {
		sum.MinPrompts = s.Limits.MinPrompts
	}
	return &sum
}

func projectOf(doc session.Doc) string {
	origin, _ := doc.String("git_origin")
	path, _ := doc.String("project_path")
	if path == "" {
		path, _ = doc.String("cwd")
	}
	return ProjectKey(origin, path)
}

// projects lists the projects holding session notes.
func (s *Service) projects(ctx context.Context) ([]string, error) {
	events, err := s.Store.MemoryEvents(ctx, session.MemoryQuery{AccountID: s.AccountID, Kinds: []string{KindSession}, IncludePrivate: true})
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, ev := range events {
		if p, _ := ev.Map("memory").String("project"); p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}

// runner is one run's engine, budget and report.
type runner struct {
	s      *Service
	engine digest.Engine
	model  string
	rep    *RunReport
}

func (r *runner) modelFor(e digest.Engine) string {
	if e.Name() == r.engine.Name() {
		return r.model
	}
	return ""
}

// withEngine runs fn with the configured engine while the day's cap
// allows, else (or when it fails) with ExtractEngine.
func (r *runner) withEngine(ctx context.Context, fn func(ctx context.Context, e digest.Engine) (float64, error)) (digest.Engine, error) {
	free := r.engine.Name() == (summary.ExtractEngine{}).Name()
	if !free {
		if r.s.reserveRun() {
			cctx, cancel := context.WithTimeout(ctx, r.s.timeout())
			cost, err := fn(cctx, r.engine)
			cancel()
			r.rep.EngineRuns++
			r.rep.CostUSD += cost
			if err == nil || errors.Is(err, summary.ErrSkipped) || errors.Is(err, session.ErrNotFound) {
				return r.engine, err
			}
			r.rep.Errors = append(r.rep.Errors, r.engine.Name()+": "+err.Error())
		} else {
			r.rep.CapReached = true
		}
		r.rep.Fallbacks++
	}
	_, err := fn(ctx, summary.ExtractEngine{})
	return summary.ExtractEngine{}, err
}

func (s *Service) timeout() time.Duration {
	if s.Limits.Timeout > 0 {
		return s.Limits.Timeout
	}
	return DefaultTimeout
}

// notes writes the missing session notes of the project (every project
// when ""), or of the one session given.
func (r *runner) notes(ctx context.Context, project, sessionID string) error {
	sum := r.s.summary()
	ids := []string{sessionID}
	if sessionID == "" {
		var err error
		if ids, err = sum.Pending(ctx, r.s.now().Add(-pendingWindow), pendingIdle, pendingLimit); err != nil {
			return err
		}
	}
	for _, id := range ids {
		if project != "" && sessionID == "" {
			doc, err := r.s.Store.GetSession(ctx, r.s.AccountID, id)
			if err != nil || projectOf(doc) != project {
				continue
			}
		}
		err := sum.Eligible(ctx, id)
		if err == nil {
			_, err = r.withEngine(ctx, func(ctx context.Context, e digest.Engine) (float64, error) {
				n, err := sum.Summarize(ctx, id, e, r.modelFor(e))
				return n.CostUSD, err
			})
		}
		switch {
		case errors.Is(err, summary.ErrSkipped):
			r.rep.Skipped = append(r.rep.Skipped, id+": "+strings.TrimPrefix(err.Error(), summary.ErrSkipped.Error()+": "))
		case err != nil && sessionID != "":
			return err
		case err != nil:
			r.rep.Errors = append(r.rep.Errors, id+": "+err.Error())
		default:
			r.rep.Notes++
		}
	}
	return nil
}

// layers rebuilds the project's day, recent and archive records.
func (r *runner) layers(ctx context.Context, project string) error {
	if project == "" {
		return nil
	}
	events, err := r.s.records(ctx, project, false, KindSession, KindNote, KindHandoff, KindDay, KindRecent, KindArchive)
	if err != nil {
		return err
	}
	today := r.s.now().UTC().Format(time.DateOnly)
	byDay := map[string][]Note{}
	existing := map[string]session.Doc{}
	for _, ev := range events {
		n := noteOf(ev)
		switch n.Kind {
		case KindSession, KindNote, KindHandoff:
			day := n.Day
			if day == "" {
				day = n.CreatedAt.UTC().Format(time.DateOnly)
			}
			if day < today {
				byDay[day] = append(byDay[day], n)
			}
		default:
			existing[n.ID] = ev
		}
	}
	days := sortedKeys(byDay)
	for _, day := range days {
		notes := byDay[day]
		sortOldestFirst(notes)
		wrote, err := r.rollup(ctx, project, KindDay, KindDay+"/"+project+"/"+day, "day.md", day, notes, existing, session.Doc{"day": day})
		if err != nil {
			return err
		}
		r.rep.Days += wrote
	}
	// The day records as they now stand.
	dayEvents, err := r.s.records(ctx, project, false, KindDay)
	if err != nil {
		return err
	}
	cutoff := r.s.now().UTC().AddDate(0, 0, -RecentDays).Format(time.DateOnly)
	var recent []Note
	byMonth := map[string][]Note{}
	for _, ev := range dayEvents {
		n := noteOf(ev)
		switch {
		case n.Day >= cutoff && n.Day < today:
			recent = append(recent, n)
		case n.Day < cutoff && len(n.Day) >= 7:
			byMonth[n.Day[:7]] = append(byMonth[n.Day[:7]], n)
		}
	}
	if len(recent) > 0 {
		sortByDay(recent)
		label := recent[0].Day + ".." + recent[len(recent)-1].Day
		wrote, err := r.rollup(ctx, project, KindRecent, KindRecent+"/"+project, "recent.md", label, recent, existing, session.Doc{"from": recent[0].Day, "to": recent[len(recent)-1].Day, "day": recent[len(recent)-1].Day})
		if err != nil {
			return err
		}
		r.rep.Recent += wrote
	}
	for _, month := range sortedKeys(byMonth) {
		notes := byMonth[month]
		sortByDay(notes)
		wrote, err := r.rollup(ctx, project, KindArchive, KindArchive+"/"+project+"/"+month, "archive.md", month, notes, existing, session.Doc{"month": month, "day": notes[len(notes)-1].Day})
		if err != nil {
			return err
		}
		r.rep.Archives += wrote
	}
	return nil
}

// rollup writes one roll-up from its sources unless the stored record was
// built from the same sources or the user edited it; it reports 1 when it
// wrote.
func (r *runner) rollup(ctx context.Context, project, kind, id, tmpl, label string, sources []Note, existing map[string]session.Doc, extra session.Doc) (int, error) {
	hashes := make([]string, len(sources))
	for i, n := range sources {
		hashes[i] = n.Hash
	}
	sort.Strings(hashes)
	sum := sha256.Sum256([]byte(strings.Join(hashes, ",")))
	version := hex.EncodeToString(sum[:])
	if old, ok := existing[id]; ok {
		mem := old.Map("memory")
		if edited, _ := mem["edited"].(bool); edited {
			return 0, nil
		}
		if v, _ := mem.String("sources"); v == version {
			return 0, nil
		}
	}
	prompt, err := r.s.render(tmpl, project, label, sources)
	if err != nil {
		return 0, err
	}
	var text string
	var cost float64
	used, err := r.withEngine(ctx, func(ctx context.Context, e digest.Engine) (float64, error) {
		var err error
		text, cost, err = e.Run(ctx, prompt, r.modelFor(e))
		if err == nil && strings.TrimSpace(r.s.clean(text)) == "" {
			err = fmt.Errorf("%s returned an empty %s", e.Name(), kind)
		}
		return cost, err
	})
	if err != nil {
		return 0, err
	}
	extra["sources"] = version
	extra["engine"] = used.Name()
	extra["cost_usd"] = cost
	if m := r.modelFor(used); m != "" {
		extra["model"] = m
	}
	ev := r.s.record(kind, id, project, r.s.clean(text), r.s.now(), extra)
	wrote, err := summary.Replace(ctx, r.s.Store, r.s.AccountID, ev)
	if err != nil || !wrote {
		return 0, err
	}
	return 1, nil
}

// render executes a roll-up template over its sources as <note> blocks.
func (s *Service) render(name, project, label string, sources []Note) (string, error) {
	var text []byte
	var err error
	if s.TemplateDir != "" {
		text, err = os.ReadFile(filepath.Join(s.TemplateDir, name))
	}
	if s.TemplateDir == "" || err != nil {
		if text, err = templates.ReadFile("templates/" + name); err != nil {
			return "", err
		}
	}
	tmpl, err := template.New(name).Parse(string(text))
	if err != nil {
		return "", fmt.Errorf("template %s: %w", name, err)
	}
	var in strings.Builder
	for i, n := range sources {
		if i > 0 {
			in.WriteString("\n")
		}
		fmt.Fprintf(&in, "<note id=%q label=%q>\n%s\n</note>", n.ID, noteLabel(n), s.clean(n.Text))
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, map[string]string{"Project": project, "Label": label, "Input": in.String()}); err != nil {
		return "", fmt.Errorf("template %s: %w", name, err)
	}
	return b.String(), nil
}

// noteLabel names a source in a roll-up prompt.
func noteLabel(n Note) string {
	switch n.Kind {
	case KindSession:
		return "session " + n.SessionID
	case KindDay:
		return n.Day
	case KindHandoff:
		return "handoff " + n.CreatedAt.UTC().Format("2006-01-02 15:04")
	case KindNote:
		return "note " + n.CreatedAt.UTC().Format("2006-01-02 15:04")
	}
	return n.Kind
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortOldestFirst(notes []Note) {
	sort.SliceStable(notes, func(i, j int) bool {
		if !notes[i].CreatedAt.Equal(notes[j].CreatedAt) {
			return notes[i].CreatedAt.Before(notes[j].CreatedAt)
		}
		return notes[i].ID < notes[j].ID
	})
}

func sortByDay(notes []Note) {
	sort.SliceStable(notes, func(i, j int) bool { return notes[i].Day < notes[j].Day })
}

// --- the run lock and the daily run count ---

// lock takes the project's run lock: in this process, and as a lock file
// under StateDir when one is set (a lock file older than the stale age is
// taken over, so a crashed run does not block memory for ever).
func (s *Service) lock(project string) (func(), error) {
	key := s.AccountID + "\x00" + project
	mu, _ := locks.LoadOrStore(key, &sync.Mutex{})
	m := mu.(*sync.Mutex)
	if !m.TryLock() {
		return nil, ErrLocked
	}
	if s.StateDir == "" {
		return m.Unlock, nil
	}
	if err := os.MkdirAll(s.StateDir, 0o700); err != nil {
		m.Unlock()
		return nil, err
	}
	sum := sha256.Sum256([]byte(key))
	path := filepath.Join(s.StateDir, "run-"+hex.EncodeToString(sum[:8])+".lock")
	stale := 4 * s.timeout()
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = fmt.Fprintf(f, "%d %s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
			_ = f.Close()
			return func() { _ = os.Remove(path); m.Unlock() }, nil
		}
		info, serr := os.Stat(path)
		if serr != nil || time.Since(info.ModTime()) < stale {
			break
		}
		_ = os.Remove(path)
	}
	m.Unlock()
	return nil, ErrLocked
}

// countMu serialises the reads and writes of the run counts.
var countMu sync.Mutex

// runsFile holds the engine runs of each day.
func (s *Service) runsFile() string { return filepath.Join(s.StateDir, "runs.json") }

func (s *Service) maxRuns() int {
	if s.Limits.MaxRunsPerDay == 0 {
		return DefaultMaxRunsPerDay
	}
	return s.Limits.MaxRunsPerDay
}

// RunsToday is how many engine runs the day (UTC) has used.
func (s *Service) RunsToday() int {
	countMu.Lock()
	defer countMu.Unlock()
	return s.loadRuns()[s.now().UTC().Format(time.DateOnly)]
}

func (s *Service) loadRuns() map[string]int {
	counts := map[string]int{}
	if s.StateDir == "" {
		for k, n := range s.runs {
			counts[k] = n
		}
		return counts
	}
	if raw, err := os.ReadFile(s.runsFile()); err == nil {
		_ = json.Unmarshal(raw, &counts)
	}
	return counts
}

func (s *Service) saveRuns(counts map[string]int) {
	if s.StateDir == "" {
		s.runs = counts
		return
	}
	raw, err := json.Marshal(counts)
	if err != nil {
		return
	}
	_ = os.MkdirAll(s.StateDir, 0o700)
	tmp := s.runsFile() + ".tmp"
	if os.WriteFile(tmp, raw, 0o600) == nil {
		_ = os.Rename(tmp, s.runsFile())
	}
}

// reserveRun counts one engine run against the day's cap and reports
// whether the cap allowed it.
func (s *Service) reserveRun() bool {
	countMu.Lock()
	defer countMu.Unlock()
	counts := s.loadRuns()
	today := s.now().UTC().Format(time.DateOnly)
	if max := s.maxRuns(); max >= 0 && counts[today] >= max {
		return false
	}
	counts[today]++
	// Keep a week of counts.
	cutoff := s.now().UTC().AddDate(0, 0, -7).Format(time.DateOnly)
	for day := range counts {
		if day < cutoff {
			delete(counts, day)
		}
	}
	s.saveRuns(counts)
	return true
}

// Status is the state of a project's memory (every project's when the
// project is ""): the records per kind, the legacy records, the engine
// runs of the day against the cap and what the records made this month
// cost.
type Status struct {
	Project       string         `json:"project,omitempty"`
	Records       map[string]int `json:"records"`
	Private       int            `json:"private"`
	Edited        int            `json:"edited"`
	RunsToday     int            `json:"runs_today"`
	MaxRunsPerDay int            `json:"max_runs_per_day"`
	CostMonthUSD  float64        `json:"cost_month_usd"`
	LastBuilt     *time.Time     `json:"last_built,omitempty"`
}

// Status reports the project's memory.
func (s *Service) Status(ctx context.Context, project string) (Status, error) {
	st := Status{Project: project, Records: map[string]int{}, RunsToday: s.RunsToday(), MaxRunsPerDay: s.maxRuns()}
	events, err := s.records(ctx, project, true)
	if err != nil {
		return st, err
	}
	now := s.now().UTC()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	for _, n := range notesOf(events) {
		st.Records[n.Kind]++
		if n.Private {
			st.Private++
		}
		if n.Edited {
			st.Edited++
		}
		if !n.CreatedAt.Before(month) {
			st.CostMonthUSD += n.CostUSD
		}
		if n.Kind == KindDay || n.Kind == KindRecent || n.Kind == KindArchive {
			if st.LastBuilt == nil || n.CreatedAt.After(*st.LastBuilt) {
				at := n.CreatedAt
				st.LastBuilt = &at
			}
		}
	}
	legacy, err := s.Store.MemoryEvents(ctx, session.MemoryQuery{AccountID: s.AccountID, Kinds: LegacyKinds})
	if err != nil {
		return st, err
	}
	if len(legacy) > 0 {
		st.Records["legacy"] = len(legacy)
	}
	return st, nil
}
