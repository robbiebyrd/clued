package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/wal"
)

// EventName is the hook_event_name of a posted record.
const EventName = session.MemoryEventName

// PostTimeout bounds one post to the daemon.
const PostTimeout = 2 * time.Second

// Event is one record on its way to the store, with where it came from.
type Event struct {
	Record    Record
	Cwd       string
	Dir       string // the store the record was read from
	SessionID string // empty when no session is known
	At        time.Time
}

// Doc renders the event as the daemon's event body. Field names follow the
// hook payloads Claude Code sends; the record sits under "memory".
func (ev Event) Doc() session.Doc {
	at := ev.At
	if at.IsZero() {
		at = time.Now()
	}
	mem := session.Doc{
		"kind": ev.Record.Kind, "file": ev.Record.File, "text": ev.Record.Text, "hash": ev.Record.Hash,
		"remember_dir": ev.Dir, "slug": Slug(ev.Cwd),
	}
	for k, v := range map[string]string{"time": ev.Record.Time, "day": ev.Record.Day, "branch": ev.Record.Branch} {
		if v != "" {
			mem[k] = v
		}
	}
	d := session.Doc{
		"hook_event_name": EventName,
		"cwd":             ev.Cwd,
		"saved_at":        at.UTC().Format(time.RFC3339Nano),
		"memory":          mem,
	}
	sid := ev.SessionID
	if ev.Record.SessionID != "" {
		sid = ev.Record.SessionID
	}
	if sid != "" {
		d["session_id"] = sid
	}
	return d
}

// A Sink accepts the events an ingest produces. DaemonPoster is the local
// one; in remote mode the CLI stores the events on the clued server itself.
type Sink interface {
	Post(ctx context.Context, ev Event) error
}

// DaemonPoster delivers events to the daemon, falling back to the write-ahead
// log the daemon replays when it is not reachable (the same path the hooks
// use).
type DaemonPoster struct {
	Port    int
	WalPath string
	// Client overrides the HTTP client (tests).
	Client *http.Client
}

// Post delivers one event. It returns an error only when neither the daemon
// nor the WAL accepted it.
func (p DaemonPoster) Post(ctx context.Context, ev Event) error {
	doc := ev.Doc()
	err := p.send(ctx, doc)
	if err == nil {
		return nil
	}
	if p.WalPath == "" {
		return err
	}
	if werr := wal.Append(p.WalPath, doc); werr != nil {
		return fmt.Errorf("daemon: %v; wal: %w", err, werr)
	}
	return nil
}

func (p DaemonPoster) send(ctx context.Context, doc session.Doc) error {
	if p.Port <= 0 {
		return fmt.Errorf("memory: no daemon port configured")
	}
	body, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, PostTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1:"+strconv.Itoa(p.Port)+"/event", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("memory: daemon answered %s", resp.Status)
	}
	return nil
}

// IngestOptions configures one ingest run.
type IngestOptions struct {
	// Dirs are the stores to read; empty means Locate(Cwd, Home).
	Dirs []string
	Cwd  string
	Home string
	// StateDir holds the per-store state files.
	StateDir string
	// SessionID is attached to every event; empty means the newest transcript
	// of the project under ProjectsDir, or none.
	SessionID   string
	ProjectsDir string
	// Poster receives every new record; nil means nothing can be delivered.
	Poster Sink
}

// IngestResult says what an ingest did.
type IngestResult struct {
	Dirs    []string `json:"dirs"`
	Scanned int      `json:"scanned"`
	Posted  int      `json:"posted"`
	// Session is the session id the events carried, if any.
	Session string `json:"session_id,omitempty"`
}

// Ingest scans each store, posts the records the state has not seen and
// records them. Records are posted oldest file first; a record that could
// not be delivered stops the run so the state never claims it.
func Ingest(ctx context.Context, o IngestOptions) (*IngestResult, error) {
	dirs := o.Dirs
	if len(dirs) == 0 {
		dirs = Locate(o.Cwd, o.Home)
	}
	res := &IngestResult{Dirs: []string{}}
	sid := o.SessionID
	if sid == "" && o.ProjectsDir != "" {
		sid = NewestSession(o.ProjectsDir, o.Cwd)
	}
	res.Session = sid
	for _, dir := range dirs {
		records, err := Scan(dir)
		if err != nil {
			return res, err
		}
		res.Dirs = append(res.Dirs, dir)
		res.Scanned += len(records)
		st, err := LoadState(o.StateDir, dir)
		if err != nil {
			return res, err
		}
		fresh := New(records, st)
		if len(fresh) == 0 {
			continue
		}
		for _, r := range fresh {
			if err := o.Poster.Post(ctx, Event{Record: r, Cwd: o.Cwd, Dir: dir, SessionID: sid}); err != nil {
				_ = st.Save(o.StateDir)
				return res, err
			}
			st.Add(r.Hash)
			res.Posted++
		}
		if err := st.Save(o.StateDir); err != nil {
			return res, err
		}
	}
	return res, nil
}
