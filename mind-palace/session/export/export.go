// Package export writes captured sessions out of the store, as Markdown,
// HTML (session/render), Claude Code's JSONL, or a zip of many, and reads a
// JSONL file or a zip back in, so an export is also a backup. An export is
// redacted unless asked for raw: what capture-time redaction would mask is
// masked in it too.
package export

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/redact"
	"github.com/robbiebyrd/clued/mind-palace/session/render"
)

// Format names the bundle layout a zip holds.
const Format = "clued-session/1"

// Bundle is one session with everything captured for it, blob content
// included: what a zip export holds per session and import reads back.
type Bundle struct {
	Format          string        `json:"format"`
	Session         session.Doc   `json:"session"`
	TranscriptLines []session.Doc `json:"transcript_lines"`
	SubagentLines   []session.Doc `json:"subagent_lines"`
	Blobs           []session.Doc `json:"blobs"`
	HookEvents      []session.Doc `json:"hook_events"`
}

// ID is the bundle's session id.
func (b Bundle) ID() string {
	id, _ := b.Session.String("session_id")
	return id
}

// Load reads one of the account's sessions from the store.
func Load(ctx context.Context, store session.Store, accountID, sessionID string) (Bundle, error) {
	full, err := store.FullSession(ctx, accountID, sessionID)
	if err != nil {
		return Bundle{}, fmt.Errorf("session %s: %w", sessionID, err)
	}
	blobs, err := store.Blobs(ctx, accountID, sessionID)
	if err != nil {
		return Bundle{}, err
	}
	b := Bundle{Format: Format, Session: session.Doc{}, Blobs: blobs}
	for k, v := range full {
		switch k {
		case "transcript_lines":
			b.TranscriptLines = docs(v)
		case "subagent_lines":
			b.SubagentLines = docs(v)
		case "hook_events":
			b.HookEvents = docs(v)
		case "blobs", "_id":
		default:
			b.Session[k] = v
		}
	}
	return b, nil
}

func docs(v any) []session.Doc {
	switch list := v.(type) {
	case []session.Doc:
		return list
	case []any:
		out := make([]session.Doc, 0, len(list))
		for _, e := range list {
			switch d := e.(type) {
			case session.Doc:
				out = append(out, d)
			case map[string]any:
				out = append(out, d)
			}
		}
		return out
	}
	return []session.Doc{}
}

// Redact masks the bundle with r: every string of every document.
func (b Bundle) Redact(r *redact.Redactor) Bundle {
	mask := func(list []session.Doc) []session.Doc {
		out := make([]session.Doc, len(list))
		for i, d := range list {
			masked, _ := r.Value(d)
			out[i] = masked.(session.Doc)
		}
		return out
	}
	masked, _ := r.Value(b.Session)
	b.Session = masked.(session.Doc)
	b.TranscriptLines = mask(b.TranscriptLines)
	b.SubagentLines = mask(b.SubagentLines)
	b.HookEvents = mask(b.HookEvents)
	blobs := make([]session.Doc, len(b.Blobs))
	for i, d := range b.Blobs {
		if enc, _ := d.String("encoding"); enc == "base64" {
			blobs[i] = d
			continue
		}
		m, _ := r.Value(d)
		blobs[i] = m.(session.Doc)
	}
	b.Blobs = blobs
	return b
}

// Model is the bundle as session/render reads it, usage given.
func (b Bundle) Model(usage *render.Usage) render.Model {
	return render.Build(render.Input{Session: b.Session, Lines: b.TranscriptLines, Usage: usage})
}

// WriteJSONL writes the transcript in Claude Code's layout, one stored line
// per row, HTML not escaped, as restore writes it.
func (b Bundle) WriteJSONL(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for _, l := range b.TranscriptLines {
		if err := enc.Encode(l["line"]); err != nil {
			return err
		}
	}
	return nil
}

// WriteZip writes the bundles into one zip: <id>/session.json (the bundle,
// what import reads), <id>/<id>.jsonl and <id>/<id>.md. models gives the
// rendered Markdown of each bundle, by id.
func WriteZip(w io.Writer, bundles []Bundle, models map[string]render.Model) error {
	zw := zip.NewWriter(w)
	for _, b := range bundles {
		id := b.ID()
		if !filepath.IsLocal(id) || strings.ContainsAny(id, `/\`) {
			return fmt.Errorf("session id %q is not a single path element", id)
		}
		raw, err := json.MarshalIndent(session.JSONTimes(b), "", "  ")
		if err != nil {
			return err
		}
		var jsonl bytes.Buffer
		if err := b.WriteJSONL(&jsonl); err != nil {
			return err
		}
		for name, content := range map[string][]byte{
			id + "/session.json":     raw,
			id + "/" + id + ".jsonl": jsonl.Bytes(),
			id + "/" + id + ".md":    render.Markdown(models[id]),
		} {
			f, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Now()})
			if err != nil {
				return err
			}
			if _, err := f.Write(content); err != nil {
				return err
			}
		}
	}
	return zw.Close()
}

// Imported counts what Import wrote.
type Imported struct {
	Sessions   []string `json:"sessions"`
	Lines      int      `json:"lines"`
	Subagents  int      `json:"subagent_lines"`
	Blobs      int      `json:"blobs"`
	HookEvents int      `json:"hook_events"`
}

// Import reads a JSONL transcript (a session id from its file name or its
// lines' sessionId, the project from their cwd) or a zip export into the
// store under accountID. Lines and blobs are upserted, so importing twice
// changes nothing; hook events are inserted only for a session the store
// did not hold yet.
func Import(ctx context.Context, store session.Store, accountID, path string) (Imported, error) {
	out := Imported{Sessions: []string{}}
	if strings.EqualFold(filepath.Ext(path), ".zip") {
		zr, err := zip.OpenReader(path)
		if err != nil {
			return out, err
		}
		defer zr.Close()
		var names []string
		files := map[string]*zip.File{}
		for _, f := range zr.File {
			if filepath.Base(f.Name) == "session.json" {
				names = append(names, f.Name)
				files[f.Name] = f
			}
		}
		sort.Strings(names)
		if len(names) == 0 {
			return out, errors.New("the zip holds no session.json")
		}
		for _, name := range names {
			b, err := readBundle(files[name])
			if err != nil {
				return out, fmt.Errorf("%s: %w", name, err)
			}
			if err := importBundle(ctx, store, accountID, b, &out); err != nil {
				return out, fmt.Errorf("%s: %w", name, err)
			}
		}
		return out, nil
	}
	b, err := bundleFromJSONL(path)
	if err != nil {
		return out, err
	}
	return out, importBundle(ctx, store, accountID, b, &out)
}

func readBundle(f *zip.File) (Bundle, error) {
	rc, err := f.Open()
	if err != nil {
		return Bundle{}, err
	}
	defer rc.Close()
	var b Bundle
	if err := json.NewDecoder(io.LimitReader(rc, 1<<30)).Decode(&b); err != nil {
		return Bundle{}, err
	}
	if b.Format != Format {
		return Bundle{}, fmt.Errorf("format %q is not %s", b.Format, Format)
	}
	return b, nil
}

// bundleFromJSONL reads a Claude Code transcript file.
func bundleFromJSONL(path string) (Bundle, error) {
	f, err := os.Open(path)
	if err != nil {
		return Bundle{}, err
	}
	defer f.Close()
	b := Bundle{Format: Format, Session: session.Doc{}}
	id := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	seq := 0
	for sc.Scan() {
		raw := bytes.TrimSpace(sc.Bytes())
		if len(raw) == 0 {
			continue
		}
		var line any
		if err := json.Unmarshal(raw, &line); err != nil {
			line = string(raw)
		}
		if m, ok := line.(map[string]any); ok {
			if s, _ := m["sessionId"].(string); s != "" && b.Session["session_id"] == nil {
				b.Session["session_id"] = s
			}
			if c, _ := m["cwd"].(string); c != "" && b.Session["cwd"] == nil {
				b.Session["cwd"], b.Session["project_path"] = c, c
			}
		}
		b.TranscriptLines = append(b.TranscriptLines, session.Doc{"seq": seq, "line": line})
		seq++
	}
	if err := sc.Err(); err != nil {
		return Bundle{}, err
	}
	if b.Session["session_id"] == nil {
		b.Session["session_id"] = id
	}
	return b, nil
}

func importBundle(ctx context.Context, store session.Store, accountID string, b Bundle, out *Imported) error {
	id := b.ID()
	if id == "" || !filepath.IsLocal(id) || strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("session id %q is not a single path element", id)
	}
	_, err := store.GetSession(ctx, accountID, id)
	existed := err == nil
	if err != nil && !errors.Is(err, session.ErrNotFound) {
		return err
	}
	s := session.Session{SessionID: id, AccountID: accountID}
	s.ProjectPath, _ = b.Session.String("project_path")
	s.Cwd, _ = b.Session.String("cwd")
	s.TranscriptPath, _ = b.Session.String("transcript_path")
	s.GitOrigin, _ = b.Session.String("git_origin")
	s.GitBranch, _ = b.Session.String("git_branch")
	s.LastSeen = timeOf(b.Session["last_seen"])
	if s.LastSeen.IsZero() {
		s.LastSeen = time.Now()
	}
	if err := store.UpsertSession(ctx, s); err != nil {
		return err
	}
	lines := make([]session.TranscriptLine, 0, len(b.TranscriptLines))
	for _, l := range b.TranscriptLines {
		lines = append(lines, session.TranscriptLine{SessionID: id, Seq: intOf(l["seq"]), Line: l["line"], AccountID: accountID, Redacted: strs(l["redacted"])})
	}
	for start := 0; start < len(lines); start += 500 {
		if err := store.UpsertTranscriptLines(ctx, lines[start:min(start+500, len(lines))]); err != nil {
			return err
		}
	}
	for _, l := range b.SubagentLines {
		agent, _ := l.String("subagent_id")
		if err := store.UpsertSubagentLine(ctx, session.SubagentLine{SessionID: id, SubagentID: agent, Seq: intOf(l["seq"]), Line: l["line"], AccountID: accountID, Redacted: strs(l["redacted"])}); err != nil {
			return err
		}
	}
	for _, d := range b.Blobs {
		blob := session.Blob{SessionID: id, AccountID: accountID, Redacted: strs(d["redacted"])}
		blob.BlobType, _ = d.String("blob_type")
		blob.Name, _ = d.String("name")
		blob.Content, _ = d.String("content")
		blob.Encoding, _ = d.String("encoding")
		if err := store.UpsertBlob(ctx, blob); err != nil {
			return err
		}
	}
	if !existed {
		for _, ev := range b.HookEvents {
			doc := session.Doc{}
			for k, v := range ev {
				if k != "_id" && k != "enrichments" {
					doc[k] = v
				}
			}
			doc["session_id"], doc["account_id"] = id, accountID
			if at := timeOf(doc["created_at"]); !at.IsZero() {
				doc["created_at"] = at
			}
			if err := store.InsertHookEvent(ctx, doc); err != nil {
				return err
			}
			out.HookEvents++
		}
	}
	out.Sessions = append(out.Sessions, id)
	out.Lines += len(lines)
	out.Subagents += len(b.SubagentLines)
	out.Blobs += len(b.Blobs)
	return nil
}

func timeOf(v any) time.Time {
	switch t := v.(type) {
	case time.Time:
		return t
	case string:
		at, _ := time.Parse(time.RFC3339Nano, t)
		return at
	}
	return time.Time{}
}

func intOf(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

func strs(v any) []string {
	switch list := v.(type) {
	case []string:
		return list
	case []any:
		out := make([]string, 0, len(list))
		for _, e := range list {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
