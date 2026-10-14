// Package limits turns the JSON Claude Code pipes to its status line into
// LimitsSampled events: the context window's fill, the subscription's
// five-hour and seven-day usage windows, the model and the cost of the
// session at that moment. The status line runs after every assistant
// message and on its refresh interval, so Due decides which samples are
// worth storing: the first one, any that moves a percentage, and a
// heartbeat while nothing moves. The entrypoint is `mind-palace hook
// status` (cli/hook_status.go); the events are queried with
// session.Store.LimitSamples.
package limits

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// EventName is the hook_event_name of a sample.
const EventName = session.LimitsEventName

const (
	// MinInterval is the shortest gap between two stored samples of a
	// session, whatever changed.
	MinInterval = 15 * time.Second
	// Heartbeat is the longest gap while the status line keeps refreshing:
	// after it a sample is stored even when nothing moved.
	Heartbeat = 5 * time.Minute
	// ContextStep is the change in the context's used percentage that is
	// worth a sample on its own.
	ContextStep = 1.0
)

// Input is the status line JSON, reduced to what the sample keeps. The
// maps are stored as Claude Code sent them.
type Input struct {
	SessionID      string         `json:"session_id"`
	Cwd            string         `json:"cwd"`
	TranscriptPath string         `json:"transcript_path"`
	Version        string         `json:"version"`
	Model          map[string]any `json:"model"`
	ContextWindow  map[string]any `json:"context_window"`
	RateLimits     map[string]any `json:"rate_limits"`
	Cost           map[string]any `json:"cost"`
}

// Parse reads the status line JSON; an empty body is an empty Input.
func Parse(raw []byte) (Input, error) {
	var in Input
	if strings.TrimSpace(string(raw)) == "" {
		return in, nil
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return Input{}, fmt.Errorf("limits: status line input: %w", err)
	}
	return in, nil
}

// Event renders the input as the event document the daemon stores (it
// stamps account_id, host and created_at). Maps the input lacks are left
// out.
func (in Input) Event(at time.Time) session.Doc {
	d := session.Doc{
		"hook_event_name": EventName,
		"session_id":      in.SessionID,
		"cwd":             in.Cwd,
		"sampled_at":      at.UTC().Format(time.RFC3339Nano),
	}
	if in.Version != "" {
		d["version"] = in.Version
	}
	for k, m := range map[string]map[string]any{"model": in.Model, "context_window": in.ContextWindow, "rate_limits": in.RateLimits, "cost": in.Cost} {
		if len(m) > 0 {
			d[k] = session.Doc(m)
		}
	}
	return d
}

// Sample is the part of an input that decides whether it is stored.
// Percentages are -1 when the input lacks them.
type Sample struct {
	ContextUsed    float64   `json:"context_used"`
	FiveHourUsed   float64   `json:"five_hour_used"`
	FiveHourResets float64   `json:"five_hour_resets"`
	SevenDayUsed   float64   `json:"seven_day_used"`
	SevenDayResets float64   `json:"seven_day_resets"`
	At             time.Time `json:"at"`
}

// Sample extracts the numbers that decide a post.
func (in Input) Sample(at time.Time) Sample {
	five := asDoc(in.RateLimits["five_hour"])
	seven := asDoc(in.RateLimits["seven_day"])
	return Sample{
		ContextUsed:    numOr(in.ContextWindow["used_percentage"], -1),
		FiveHourUsed:   numOr(five["used_percentage"], -1),
		FiveHourResets: numOr(five["resets_at"], -1),
		SevenDayUsed:   numOr(seven["used_percentage"], -1),
		SevenDayResets: numOr(seven["resets_at"], -1),
		At:             at,
	}
}

// Due reports whether s should be stored given prev, the last stored sample
// of the session (nil when none).
func Due(prev *Sample, s Sample) bool {
	if prev == nil {
		return true
	}
	gap := s.At.Sub(prev.At)
	switch {
	case gap < MinInterval:
		return false
	case gap >= Heartbeat:
		return true
	case math.Abs(s.ContextUsed-prev.ContextUsed) >= ContextStep:
		return true
	case s.FiveHourUsed != prev.FiveHourUsed, s.SevenDayUsed != prev.SevenDayUsed:
		return true
	case s.FiveHourResets != prev.FiveHourResets, s.SevenDayResets != prev.SevenDayResets:
		return true
	}
	return false
}

// Line renders the default status line: the context's fill and window,
// the usage windows and the model, for a status line with no other
// command to run.
func (in Input) Line() string {
	var parts []string
	if used, ok := num(in.ContextWindow["used_percentage"]); ok {
		s := fmt.Sprintf("ctx %.0f%%", used)
		if size, ok := num(in.ContextWindow["context_window_size"]); ok && size > 0 {
			s += " of " + window(size)
		}
		parts = append(parts, s)
	}
	for _, w := range []struct{ key, label string }{{"five_hour", "5h"}, {"seven_day", "7d"}} {
		if used, ok := num(asDoc(in.RateLimits[w.key])["used_percentage"]); ok {
			parts = append(parts, fmt.Sprintf("%s %.0f%%", w.label, used))
		}
	}
	if name, ok := in.Model["display_name"].(string); ok && name != "" {
		parts = append(parts, name)
	} else if id, ok := in.Model["id"].(string); ok && id != "" {
		parts = append(parts, id)
	}
	return strings.Join(parts, " · ")
}

// window renders a context window size: 200k, 1M.
func window(size float64) string {
	switch {
	case size >= 1_000_000 && math.Mod(size, 1_000_000) == 0:
		return fmt.Sprintf("%.0fM", size/1_000_000)
	case size >= 1_000 && math.Mod(size, 1_000) == 0:
		return fmt.Sprintf("%.0fk", size/1_000)
	}
	return fmt.Sprintf("%.0f", size)
}

// StatePath is the file the last stored sample of a session lives in: the
// id is hashed, so any session id makes a safe file name.
func StatePath(dir, sessionID string) string {
	sum := sha256.Sum256([]byte(sessionID))
	return filepath.Join(dir, hex.EncodeToString(sum[:8])+".json")
}

// LoadState reads the last stored sample of a session; nil when there is
// none or the file is unreadable.
func LoadState(dir, sessionID string) *Sample {
	b, err := os.ReadFile(StatePath(dir, sessionID))
	if err != nil {
		return nil
	}
	var s Sample
	if err := json.Unmarshal(b, &s); err != nil || s.At.IsZero() {
		return nil
	}
	return &s
}

// SaveState records s as the last stored sample of a session.
func SaveState(dir, sessionID string, s Sample) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	path := StatePath(dir, sessionID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// ErrNoSession is returned by Validate for an input without a session id.
var ErrNoSession = errors.New("limits: the status line input names no session")

// Validate checks the input can be stored: it must name a session that is a
// single path element, as the daemon tracks sessions by id.
func (in Input) Validate() error {
	if in.SessionID == "" {
		return ErrNoSession
	}
	if strings.ContainsAny(in.SessionID, `/\`) || in.SessionID == "." || in.SessionID == ".." {
		return fmt.Errorf("limits: session id %q is not a single path element", in.SessionID)
	}
	return nil
}

func asDoc(v any) map[string]any {
	switch m := v.(type) {
	case map[string]any:
		return m
	case session.Doc:
		return m
	}
	return nil
}

func num(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func numOr(v any, def float64) float64 {
	if f, ok := num(v); ok {
		return f
	}
	return def
}
