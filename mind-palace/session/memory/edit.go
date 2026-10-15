package memory

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/summary"
)

// EditReport says what ApplyMarkdown changed.
type EditReport struct {
	Changed   []string `json:"changed"`
	Removed   []string `json:"removed"`
	Unchanged int      `json:"unchanged"`
}

// blockLine opens a record's block in the Markdown EditMarkdown renders.
var blockLine = regexp.MustCompile(`^<!-- memory id="([^"]+)" kind="([^"]*)"( private)?( edited)? -->$`)

// editHeader starts the Markdown; it is a comment, so it is no block.
const editHeader = `<!-- clued memory of %s
Each block below is one memory record, newest first. Change a block's text
to rewrite the record (it is then marked edited and an engine never
rewrites it); delete a block, or empty its text, to forget the record. Keep
each "<!-- memory id=… -->" line as it is: a block whose id is not one of
these records is refused and nothing is changed. -->
`

// EditMarkdown renders the project's native records, private ones
// included, as Markdown blocks for a person to edit (ApplyMarkdown reads
// the result back).
func (s *Service) EditMarkdown(ctx context.Context, project string) (string, error) {
	if project == "" {
		return "", errors.New("a project is required")
	}
	events, err := s.records(ctx, project, true)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, editHeader, project)
	for _, n := range notesOf(events) {
		flags := ""
		if n.Private {
			flags += " private"
		}
		if n.Edited {
			flags += " edited"
		}
		fmt.Fprintf(&b, "\n<!-- memory id=%q kind=%q%s -->\n%s\n", n.ID, n.Kind, flags, strings.TrimSpace(n.Text))
	}
	return b.String(), nil
}

// parseBlocks reads the blocks of edited Markdown, by id, in order.
func parseBlocks(md string) (ids []string, texts map[string]string, err error) {
	texts = map[string]string{}
	var cur string
	var body []string
	inHeader := false
	flush := func() {
		if cur != "" {
			texts[cur] = strings.TrimSpace(strings.Join(body, "\n"))
		}
		body = nil
	}
	for _, line := range strings.Split(md, "\n") {
		trimmed := strings.TrimSpace(line)
		if cur == "" && strings.HasPrefix(trimmed, "<!-- clued memory") {
			inHeader = !strings.HasSuffix(trimmed, "-->")
			continue
		}
		if inHeader {
			inHeader = !strings.HasSuffix(trimmed, "-->")
			continue
		}
		if m := blockLine.FindStringSubmatch(trimmed); m != nil {
			flush()
			cur = m[1]
			if _, dup := texts[cur]; dup {
				return nil, nil, fmt.Errorf("the record %q appears twice", cur)
			}
			texts[cur] = ""
			ids = append(ids, cur)
			continue
		}
		if strings.HasPrefix(trimmed, "<!-- memory") {
			return nil, nil, fmt.Errorf("unreadable block line %q", trimmed)
		}
		if cur == "" {
			if trimmed != "" {
				return nil, nil, fmt.Errorf("text outside any record: %q; add notes with remember", trimmed)
			}
			continue
		}
		body = append(body, line)
	}
	flush()
	return ids, texts, nil
}

// ApplyMarkdown applies Markdown EditMarkdown rendered and a person edited:
// a block whose text changed replaces its record (edited: true), a block
// removed or emptied forgets its record, and a block whose id is not one
// of the project's records refuses the whole edit.
func (s *Service) ApplyMarkdown(ctx context.Context, project, edited string) (EditReport, error) {
	rep := EditReport{Changed: []string{}, Removed: []string{}}
	if project == "" {
		return rep, errors.New("a project is required")
	}
	ids, texts, err := parseBlocks(edited)
	if err != nil {
		return rep, err
	}
	events, err := s.records(ctx, project, true)
	if err != nil {
		return rep, err
	}
	byID := map[string]session.Doc{}
	for _, ev := range events {
		byID[noteOf(ev).ID] = ev
	}
	for _, id := range ids {
		if _, known := byID[id]; !known {
			return rep, fmt.Errorf("unknown memory id %q: nothing was changed", id)
		}
	}
	var remove []string
	for _, ev := range events {
		n := noteOf(ev)
		text, kept := texts[n.ID]
		switch {
		case !kept || text == "":
			remove = append(remove, n.Hash)
			rep.Removed = append(rep.Removed, n.ID)
		case text == strings.TrimSpace(n.Text):
			rep.Unchanged++
		default:
			if !n.Private {
				text = s.clean(text)
			} else {
				text, _ = s.redactor().String(text)
			}
			if err := s.rewrite(ctx, ev, text); err != nil {
				return rep, err
			}
			rep.Changed = append(rep.Changed, n.ID)
		}
	}
	if len(remove) > 0 {
		if _, err := s.Store.DeleteMemory(ctx, session.MemoryDelete{AccountID: s.AccountID, Hashes: remove}); err != nil {
			return rep, err
		}
	}
	sort.Strings(rep.Changed)
	sort.Strings(rep.Removed)
	return rep, nil
}

// rewrite replaces a record's text, marking it edited.
func (s *Service) rewrite(ctx context.Context, old session.Doc, text string) error {
	mem := session.Doc{}
	for k, v := range old.Map("memory") {
		mem[k] = v
	}
	now := s.now()
	id, _ := mem.String("id")
	mem["text"] = text
	mem["edited"] = true
	mem["edited_at"] = now.UTC().Format(time.RFC3339Nano)
	mem["hash"] = summary.Hash(id, "edited "+now.UTC().Format(time.RFC3339Nano), text)
	ev := session.Doc{
		"hook_event_name": session.MemoryEventName,
		"account_id":      s.AccountID,
		"saved_at":        now.UTC().Format(time.RFC3339Nano),
		"created_at":      old["created_at"],
		"memory":          mem,
	}
	if sid, _ := old.String("session_id"); sid != "" {
		ev["session_id"] = sid
	}
	if cwd, _ := old.String("cwd"); cwd != "" {
		ev["cwd"] = cwd
	}
	if _, ok := ev["created_at"].(time.Time); !ok {
		ev["created_at"] = now
	}
	_, err := summary.Replace(ctx, s.Store, s.AccountID, ev)
	return err
}

// exported is one line of an export.
type exported struct {
	HookEventName string      `json:"hook_event_name"`
	SessionID     string      `json:"session_id,omitempty"`
	Cwd           string      `json:"cwd,omitempty"`
	CreatedAt     time.Time   `json:"created_at"`
	Memory        session.Doc `json:"memory"`
}

// Export writes the project's memory records (every record of the
// account when project is ""; legacy and digest records included then) as
// JSONL, oldest first. Private records are never exported.
func (s *Service) Export(ctx context.Context, w io.Writer, project string) error {
	q := session.MemoryQuery{AccountID: s.AccountID}
	if project != "" {
		q.Projects = []string{project}
	}
	events, err := s.Store.MemoryEvents(ctx, q)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		mem := ev.Map("memory")
		if private, _ := mem["private"].(bool); private {
			continue
		}
		line := exported{HookEventName: session.MemoryEventName, Memory: mem}
		line.SessionID, _ = ev.String("session_id")
		line.Cwd, _ = ev.String("cwd")
		line.CreatedAt, _ = ev["created_at"].(time.Time)
		if err := enc.Encode(line); err != nil {
			return err
		}
	}
	return nil
}

// Import reads an export and stores its records under the service's
// account, each once: a record the store already holds (same id and hash,
// or the same hash for a record without id) is skipped, and private
// records are refused. It reports how many records it stored.
func (s *Service) Import(ctx context.Context, r io.Reader) (int, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	known := map[string]map[string]bool{} // kind → hashes held
	held := func(kind, hash string) (bool, error) {
		if known[kind] == nil {
			events, err := s.Store.MemoryEvents(ctx, session.MemoryQuery{AccountID: s.AccountID, Kinds: []string{kind}, IncludePrivate: true})
			if err != nil {
				return false, err
			}
			known[kind] = map[string]bool{}
			for _, ev := range events {
				h, _ := ev.Map("memory").String("hash")
				known[kind][h] = true
			}
		}
		return known[kind][hash], nil
	}
	n := 0
	for lineNo := 1; sc.Scan(); lineNo++ {
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var in struct {
			HookEventName string         `json:"hook_event_name"`
			SessionID     string         `json:"session_id"`
			Cwd           string         `json:"cwd"`
			CreatedAt     time.Time      `json:"created_at"`
			Memory        map[string]any `json:"memory"`
		}
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			return n, fmt.Errorf("line %d: %w", lineNo, err)
		}
		mem := session.Doc(in.Memory)
		hash, _ := mem.String("hash")
		kind, _ := mem.String("kind")
		text, isText := mem.String("text")
		if in.HookEventName != session.MemoryEventName || hash == "" || kind == "" || !isText {
			return n, fmt.Errorf("line %d: not a memory record (MemorySaved with memory.kind, memory.text and memory.hash)", lineNo)
		}
		if private, _ := mem["private"].(bool); private {
			continue
		}
		if dup, err := held(kind, hash); err != nil {
			return n, err
		} else if dup {
			continue
		}
		mem["text"] = s.clean(text)
		at := in.CreatedAt
		if at.IsZero() {
			at = s.now()
		}
		ev := session.Doc{
			"hook_event_name": session.MemoryEventName,
			"account_id":      s.AccountID,
			"saved_at":        s.now().UTC().Format(time.RFC3339Nano),
			"created_at":      at,
			"memory":          mem,
		}
		if in.SessionID != "" {
			ev["session_id"] = in.SessionID
		}
		if in.Cwd != "" {
			ev["cwd"] = in.Cwd
		}
		if id, _ := mem.String("id"); id != "" {
			wrote, err := summary.Replace(ctx, s.Store, s.AccountID, ev)
			if err != nil {
				return n, err
			}
			if !wrote {
				continue
			}
		} else if err := s.Store.InsertHookEvent(ctx, ev); err != nil {
			return n, err
		}
		known[kind][hash] = true
		n++
	}
	return n, sc.Err()
}
