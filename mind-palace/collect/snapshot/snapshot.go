// Package snapshot stores a collected context report as a synthetic
// ContextCollected hook event next to the session it describes: posted to
// the clued daemon in local mode (Post), inserted through the session store
// in remote mode (Insert).
package snapshot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/collect"
	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/ingest"
)

// EventName is the hook_event_name of a posted snapshot.
const EventName = "ContextCollected"

// Timeout bounds one post.
const Timeout = 2 * time.Second

// Event is one snapshot. SessionID may be empty when no session is known;
// the daemon then stores the event without tracking a session.
type Event struct {
	SessionID string
	Cwd       string
	Report    collect.Report
	At        time.Time // zero means now
}

// payload is the JSON body the daemon receives. Field names follow the hook
// payloads Claude Code sends (snake_case, hook_event_name, session_id, cwd).
type payload struct {
	HookEventName string            `json:"hook_event_name"`
	SessionID     string            `json:"session_id,omitempty"`
	Cwd           string            `json:"cwd"`
	CollectedAt   string            `json:"collected_at"`
	Report        string            `json:"report"`
	Sections      map[string]string `json:"sections"`
}

// JSON renders the event as the daemon's event body.
func (ev Event) JSON() ([]byte, error) {
	at := ev.At
	if at.IsZero() {
		at = time.Now()
	}
	sections := make(map[string]string, len(ev.Report.Sections))
	for _, s := range ev.Report.Sections {
		sections[s.Name] = s.Body
	}
	return json.Marshal(payload{
		HookEventName: EventName,
		SessionID:     ev.SessionID,
		Cwd:           ev.Cwd,
		CollectedAt:   at.UTC().Format(time.RFC3339Nano),
		Report:        ev.Report.String(),
		Sections:      sections,
	})
}

// Doc renders the event as the hook-event document a store receives: the
// same fields as JSON, as a session.Doc.
func (ev Event) Doc() (session.Doc, error) {
	raw, err := ev.JSON()
	if err != nil {
		return nil, err
	}
	var doc session.Doc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("snapshot: decoding event: %w", err)
	}
	return doc, nil
}

// ErrNoSession is returned by Insert for an event whose SessionID is empty
// or not a single path element: the store has no session to attach it to.
var ErrNoSession = errors.New("snapshot: no session to attach the event to")

// Insert stores the event through svc as one hook event, the remote-mode
// counterpart of Post: the service stamps it with its account, host and
// time, tracks its session as the daemon would, and buffers it in the WAL
// when the store refuses it, which counts as stored (the next relay
// replays it). Any other failure is returned; nothing is retried. The call
// is bounded by Timeout.
//
// Unlike the daemon, the store needs a session id that is a single path
// element, so an event without one is refused with ErrNoSession before
// anything is sent: sending it would be refused for good, and buffering it
// in the WAL would replay that refusal on every hook.
func Insert(ctx context.Context, svc *ingest.Service, ev Event) error {
	if !filepath.IsLocal(ev.SessionID) || filepath.Base(ev.SessionID) != ev.SessionID {
		return fmt.Errorf("%w: session id %q", ErrNoSession, ev.SessionID)
	}
	doc, err := ev.Doc()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	if _, err := svc.Handle(ctx, doc); err != nil && !errors.Is(err, ingest.ErrBuffered) {
		return err
	}
	return nil
}

// Post sends the event to the daemon on 127.0.0.1:port. Any failure
// (daemon down, non-2xx answer, timeout) is returned; nothing is retried.
func Post(ctx context.Context, port int, ev Event) error {
	if port <= 0 {
		return fmt.Errorf("snapshot: no daemon port configured")
	}
	body, err := ev.JSON()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1:"+strconv.Itoa(port)+"/event", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("snapshot: daemon answered %s", resp.Status)
	}
	return nil
}
