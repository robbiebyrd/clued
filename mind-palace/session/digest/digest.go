package digest

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/query"
)

//go:embed templates/daily.md templates/range.md
var templates embed.FS

// Kind is the memory kind a digest is stored under.
const Kind = "digest"

// The scopes of a digest.
const (
	ScopeDay   = "day"
	ScopeRange = "range"
)

// Digest is one stored digest: the retro text and how it was made.
type Digest struct {
	Scope        string    `json:"scope"`
	From         string    `json:"from"`
	To           string    `json:"to"`
	Text         string    `json:"text"`
	Engine       string    `json:"engine"`
	Model        string    `json:"model,omitempty"`
	CostUSD      float64   `json:"cost_usd"`
	ReportTokens int       `json:"report_tokens"`
	Cached       bool      `json:"cached"`
	CreatedAt    time.Time `json:"created_at"`
}

// An Engine turns a prompt into a digest.
type Engine interface {
	// Name is the engine's name as stored with the digest.
	Name() string
	// Run answers the prompt with the model (or its default when empty),
	// reporting what it cost.
	Run(ctx context.Context, prompt, model string) (text string, costUSD float64, err error)
}

// ClaudeEngine runs the Claude Code CLI non-interactively, with
// CLUED_NO_CAPTURE=1 in its environment so the clued hooks of that run
// capture, relay and prime nothing: an engine's own run is never a
// session.
type ClaudeEngine struct {
	// Command is the executable, "claude" by default.
	Command string
}

func (ClaudeEngine) Name() string { return "claude" }

// Run pipes the prompt to `claude -p --no-session-persistence
// --output-format stream-json [--model m]` and reads the result event's
// text and cost.
func (e ClaudeEngine) Run(ctx context.Context, prompt, model string) (string, float64, error) {
	command := e.Command
	if command == "" {
		command = "claude"
	}
	args := []string{"-p", "--no-session-persistence", "--output-format", "stream-json", "--verbose"}
	if model != "" {
		args = append(args, "--model", model)
	}
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = append(os.Environ(), session.NoCaptureEnv+"=1")
	cmd.Stdin = strings.NewReader(prompt)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", 0, fmt.Errorf("%s: %s", command, msg)
	}
	return parseStream(out)
}

// parseStream reads a stream-json transcript: the "result" event's text and
// cost, else the assistant messages' text.
func parseStream(out []byte) (string, float64, error) {
	var text strings.Builder
	var cost float64
	var result string
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var ev struct {
			Type    string  `json:"type"`
			Result  string  `json:"result"`
			Cost    float64 `json:"total_cost_usd"`
			IsError bool    `json:"is_error"`
			Message struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "result":
			if ev.IsError {
				return "", ev.Cost, fmt.Errorf("claude reported an error: %s", ev.Result)
			}
			result, cost = ev.Result, ev.Cost
		case "assistant":
			for _, c := range ev.Message.Content {
				if c.Type == "text" {
					text.WriteString(c.Text)
				}
			}
		}
	}
	if result == "" {
		result = text.String()
	}
	if strings.TrimSpace(result) == "" {
		return "", cost, errors.New("claude returned no text")
	}
	return strings.TrimSpace(result), cost, nil
}

// Service generates and reads digests for one account.
type Service struct {
	Store     session.Store
	AccountID string
	// Query answers the activity queries the report is built from; its
	// Store and AccountID are the service's.
	Query *query.Service
	// TemplateDir holds prompt templates that override the built-in ones
	// (daily.md, range.md); empty means the built-in ones only.
	TemplateDir string
	// Now is the clock that decides whether a day is over; nil is time.Now.
	Now func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Cached returns the stored digest of the period, or nil.
func (s *Service) Cached(ctx context.Context, p Period) (*Digest, error) {
	events, err := s.Store.MemoryEvents(ctx, session.MemoryQuery{AccountID: s.AccountID, Kinds: []string{Kind}, Limit: sessionScan})
	if err != nil {
		return nil, err
	}
	for _, ev := range events {
		d := digestOf(ev)
		if d.From == p.FirstDay() && d.To == p.LastDay() {
			d.Cached = true
			return &d, nil
		}
	}
	return nil, nil
}

// digestOf reads a stored digest event.
func digestOf(ev session.Doc) Digest {
	mem := ev.Map("memory")
	str := func(k string) string { v, _ := mem.String(k); return v }
	cost, _ := mem["cost_usd"].(float64)
	tokens := 0
	switch n := mem["report_tokens"].(type) {
	case int:
		tokens = n
	case int32:
		tokens = int(n)
	case int64:
		tokens = int(n)
	case float64:
		tokens = int(n)
	}
	createdAt, _ := ev["created_at"].(time.Time)
	return Digest{Scope: str("scope"), From: str("from"), To: str("to"), Text: str("text"), Engine: str("engine"), Model: str("model"), CostUSD: cost, ReportTokens: tokens, CreatedAt: createdAt}
}

// Prompt renders the period's prompt: the daily template over the
// report, or the range template over the days' digests.
func (s *Service) Prompt(ctx context.Context, p Period, opts Options) (prompt string, rep Report, err error) {
	if p.IsDay() {
		rep, err = Build(ctx, s.Query, p, opts)
		if err != nil {
			return "", rep, err
		}
		prompt, err = s.render("daily.md", map[string]any{"Day": p.FirstDay(), "Report": rep.Markdown()})
		return prompt, rep, err
	}
	var days []map[string]string
	for _, day := range p.Days() {
		dp, _ := Day(day)
		d, err := s.Cached(ctx, dp)
		if err != nil {
			return "", rep, err
		}
		if d != nil {
			days = append(days, map[string]string{"Day": day, "Text": d.Text})
		}
	}
	prompt, err = s.render("range.md", map[string]any{"From": p.FirstDay(), "To": p.LastDay(), "Days": days})
	return prompt, rep, err
}

// render executes the named template, from TemplateDir when it holds one.
func (s *Service) render(name string, data any) (string, error) {
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
	var b strings.Builder
	if err := tmpl.Execute(&b, data); err != nil {
		return "", fmt.Errorf("template %s: %w", name, err)
	}
	return b.String(), nil
}

// Generate returns the period's digest: the stored one when the period is
// over and force is false, else a new one from the engine, stored. A range
// digest is built from its days' digests, generating the missing ones
// first; days with no sessions yield no daily digest and are skipped.
func (s *Service) Generate(ctx context.Context, p Period, opts Options, engine Engine, model string, force bool) (Digest, error) {
	over := !p.To.After(s.now().UTC())
	if !force && over {
		if d, err := s.Cached(ctx, p); err != nil || d != nil {
			if d != nil {
				return *d, err
			}
			return Digest{}, err
		}
	}
	if !p.IsDay() {
		for _, day := range p.Days() {
			dp, _ := Day(day)
			if dp.From.After(s.now().UTC()) {
				continue
			}
			if _, err := s.Generate(ctx, dp, opts, engine, model, false); err != nil && !errors.Is(err, ErrNoSessions) {
				return Digest{}, fmt.Errorf("day %s: %w", day, err)
			}
		}
	}
	prompt, rep, err := s.Prompt(ctx, p, opts)
	if err != nil {
		return Digest{}, err
	}
	if p.IsDay() && len(rep.Sessions) == 0 {
		return Digest{}, ErrNoSessions
	}
	if !p.IsDay() && !strings.Contains(prompt, "<day ") {
		return Digest{}, ErrNoSessions
	}
	text, cost, err := engine.Run(ctx, prompt, model)
	if err != nil {
		return Digest{}, err
	}
	d := Digest{Scope: ScopeDay, From: p.FirstDay(), To: p.LastDay(), Text: text, Engine: engine.Name(), Model: model, CostUSD: cost, ReportTokens: rep.Tokens, CreatedAt: s.now()}
	if !p.IsDay() {
		d.Scope = ScopeRange
	}
	return d, s.store(ctx, d)
}

// ErrNoSessions is returned when the period holds nothing to digest.
var ErrNoSessions = errors.New("no sessions were captured in the period")

// store keeps the digest as a MemorySaved event of kind "digest".
func (s *Service) store(ctx context.Context, d Digest) error {
	sum := sha256.Sum256([]byte(d.Scope + "|" + d.From + "|" + d.To + "|" + d.Text))
	mem := session.Doc{
		"kind": Kind, "file": Kind + "/" + d.From + ".md", "text": d.Text, "hash": hex.EncodeToString(sum[:]),
		"scope": d.Scope, "from": d.From, "to": d.To, "day": d.To, "engine": d.Engine, "cost_usd": d.CostUSD, "report_tokens": d.ReportTokens,
	}
	if d.Scope == ScopeRange {
		mem["file"] = Kind + "/" + d.From + ".." + d.To + ".md"
	}
	if d.Model != "" {
		mem["model"] = d.Model
	}
	return s.Store.InsertHookEvent(ctx, session.Doc{
		"hook_event_name": session.MemoryEventName,
		"account_id":      s.AccountID,
		"saved_at":        d.CreatedAt.UTC().Format(time.RFC3339Nano),
		"memory":          mem,
		"created_at":      d.CreatedAt,
	})
}
