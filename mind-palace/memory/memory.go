// Package memory reads the memory store that the claude-remember plugin
// (github.com/Digital-Process-Tools/claude-remember) keeps for a project and
// turns it into records clued can store and search: one per entry of
// now.md and today-*.md, one per version of recent.md, archive.md and their
// rotated slices, one per handoff note and one for identity.md. Nothing is
// ever written into the store; a state file remembers which records were
// already posted so an ingest can run as often as the hooks fire.
package memory

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Kinds of record.
const (
	KindEntry    = "entry"    // one ## HH:MM | branch entry of now.md
	KindDay      = "day"      // one entry of a today-YYYY-MM-DD.md file
	KindRecent   = "recent"   // recent.md or a rotated recent-YYYY-MM-DD.md, whole file
	KindArchive  = "archive"  // archive.md or a rotated archive-YYYY-MM-DD.md, whole file
	KindHandoff  = "handoff"  // remember.md or remember.<session>.md
	KindIdentity = "identity" // identity.md
)

// Kinds lists every kind, in the order a search presents them.
var Kinds = []string{KindEntry, KindDay, KindRecent, KindArchive, KindHandoff, KindIdentity}

// Record is one unit of memory.
type Record struct {
	Kind string `json:"kind"`
	// File is the name of the file inside the store.
	File string `json:"file"`
	// Time is the entry's wall-clock time as written ("14:30" or "2:30 PM"); empty for whole files.
	Time string `json:"time,omitempty"`
	// Day is the YYYY-MM-DD a day entry or rotated slice belongs to; empty when unknown.
	Day string `json:"day,omitempty"`
	// Branch is the git branch named in an entry header.
	Branch string `json:"branch,omitempty"`
	// SessionID is set for a per-session handoff (remember.<session>.md).
	SessionID string `json:"session_id,omitempty"`
	Text      string `json:"text"`
	// Hash identifies the text: sha256 of the trimmed text, so an entry that
	// moves from now.md into a today file unchanged is the same record.
	Hash string `json:"hash"`
}

var (
	// entryHeader is claude-remember's own ENTRY_HEADER_ERE: a 24-hour or
	// 12-hour time, then the branch behind a pipe.
	entryHeader = regexp.MustCompile(`^## ([0-9]{2}:[0-9]{2}|[0-9]{1,2}:[0-9]{2} (?:AM|PM)) \|[ \t]*(.*?)[ \t]*$`)
	dayFile     = regexp.MustCompile(`^today-([0-9]{4}-[0-9]{2}-[0-9]{2})\.md$`)
	sliceFile   = regexp.MustCompile(`^(recent|archive)-([0-9]{4}-[0-9]{2}-[0-9]{2})(?:-[0-9]+)?\.md$`)
	handoffFile = regexp.MustCompile(`^remember(?:\.([A-Za-z0-9_-]+))?\.md$`)
	nonAlnum    = regexp.MustCompile(`[^A-Za-z0-9]`)
)

// HashText returns the hash of a record's text.
func HashText(text string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(text)))
	return hex.EncodeToString(sum[:])
}

// Slug is the directory name Claude Code (and claude-remember's {slug})
// derive from a project path: every non-alphanumeric character becomes "-".
func Slug(path string) string {
	return nonAlnum.ReplaceAllString(path, "-")
}

// Locate returns the claude-remember stores that may hold a project's
// memory, in the order the plugin resolves them: <cwd>/.remember (legacy
// mode) and <home>/.remember/<slug> (external storage mode). Only
// directories that exist are returned.
func Locate(cwd, home string) []string {
	var out []string
	for _, dir := range []string{filepath.Join(cwd, ".remember"), filepath.Join(home, ".remember", Slug(cwd))} {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			out = append(out, dir)
		}
	}
	return out
}

// Scan reads a store directory and returns its records, files in name order
// and entries in file order. Files the plugin does not write (logs/, tmp/,
// config.json) are ignored, as are unreadable files.
func Scan(dir string) ([]Record, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var out []Record
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		out = append(out, parseFile(name, string(data))...)
	}
	return out, nil
}

// parseFile turns one file into records according to its name.
func parseFile(name, text string) []Record {
	switch {
	case name == "now.md":
		return parseEntries(KindEntry, name, "", text)
	case dayFile.MatchString(name):
		return parseEntries(KindDay, name, dayFile.FindStringSubmatch(name)[1], text)
	case name == "recent.md":
		return whole(KindRecent, name, "", text)
	case name == "archive.md":
		return whole(KindArchive, name, "", text)
	case sliceFile.MatchString(name):
		m := sliceFile.FindStringSubmatch(name)
		return whole(m[1], name, m[2], text)
	case name == "identity.md":
		return whole(KindIdentity, name, "", text)
	case handoffFile.MatchString(name):
		recs := whole(KindHandoff, name, "", text)
		if sid := handoffFile.FindStringSubmatch(name)[1]; sid != "" {
			for i := range recs {
				recs[i].SessionID = sid
			}
		}
		return recs
	}
	return nil
}

func whole(kind, name, day, text string) []Record {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	return []Record{{Kind: kind, File: name, Day: day, Text: text, Hash: HashText(text)}}
}

// parseEntries splits a now.md or today file on its entry headers. Text
// before the first header is ignored (a memory file has none).
func parseEntries(kind, name, day, text string) []Record {
	var out []Record
	var cur *Record
	var body []string
	flush := func() {
		if cur == nil {
			return
		}
		content := strings.TrimSpace(strings.Join(body, "\n"))
		full := strings.TrimSpace("## " + cur.Time + " | " + cur.Branch + "\n" + content)
		if content != "" {
			cur.Text, cur.Hash = full, HashText(full)
			out = append(out, *cur)
		}
		cur, body = nil, nil
	}
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if m := entryHeader.FindStringSubmatch(line); m != nil {
			flush()
			cur = &Record{Kind: kind, File: name, Day: day, Time: m[1], Branch: m[2]}
			continue
		}
		if cur != nil {
			body = append(body, line)
		}
	}
	flush()
	return out
}

// State remembers which record hashes of a store were already posted.
type State struct {
	Dir    string   `json:"dir"`
	Hashes []string `json:"hashes"`
	// Updated is when the state was last written.
	Updated string `json:"updated,omitempty"`
}

// StatePath is where the state of a store is kept: one file per store
// under stateDir, named by a hash of the store's absolute path.
func StatePath(stateDir, dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	sum := sha256.Sum256([]byte(abs))
	return filepath.Join(stateDir, hex.EncodeToString(sum[:8])+".json")
}

// LoadState reads the state of a store; a missing file is an empty state.
func LoadState(stateDir, dir string) (*State, error) {
	st := &State{Dir: dir}
	data, err := os.ReadFile(StatePath(stateDir, dir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return st, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, st); err != nil {
		return nil, fmt.Errorf("%s: %w", StatePath(stateDir, dir), err)
	}
	return st, nil
}

// Has reports whether the hash was already posted.
func (st *State) Has(hash string) bool {
	for _, h := range st.Hashes {
		if h == hash {
			return true
		}
	}
	return false
}

// Add records a posted hash.
func (st *State) Add(hash string) {
	if !st.Has(hash) {
		st.Hashes = append(st.Hashes, hash)
	}
}

// Save writes the state atomically with owner-only permissions.
func (st *State) Save(stateDir string) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	st.Updated = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	path := StatePath(stateDir, st.Dir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// New returns the records of a scan that the state has not seen.
func New(records []Record, st *State) []Record {
	var out []Record
	seen := map[string]bool{}
	for _, r := range records {
		if st.Has(r.Hash) || seen[r.Hash] {
			continue
		}
		seen[r.Hash] = true
		out = append(out, r)
	}
	return out
}

// NewestSession returns the id of the most recently modified transcript of
// the project under projectsDir (<projectsDir>/<slug>/<id>.jsonl), the way
// claude-remember itself picks the session when none is given. It is empty
// when there is none.
func NewestSession(projectsDir, cwd string) string {
	dir := filepath.Join(projectsDir, Slug(cwd))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var best string
	var bestTime time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if best == "" || info.ModTime().After(bestTime) {
			best, bestTime = strings.TrimSuffix(e.Name(), ".jsonl"), info.ModTime()
		}
	}
	return best
}
