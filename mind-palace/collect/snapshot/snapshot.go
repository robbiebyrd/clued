// Package snapshot posts a collected context report to the clued daemon as a
// synthetic ContextCollected hook event, so the report lands in MongoDB next
// to the session it describes.
package snapshot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/collect"
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
