// Package model defines the document types shared by every storage plugin
// and entrypoint of the mind-palace. Plans and Stories share one Document
// shape: the front matter is the union of both kinds' fields, and each
// kind's JSON Schema decides which fields it may use.
package model

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Relations a document can have to another plan or story.
const (
	RelationParent   = "parent"
	RelationIncluded = "included"
	RelationDepends  = "depends"
	RelationBlocks   = "blocks"
)

// Identifier patterns.
var (
	// IDRe matches a plan or story id: AAAA-BBB.
	IDRe = regexp.MustCompile(`^[0-9]{4}-[a-z0-9]{3}$`)
	// PlanIDRe and StoryIDRe are the same shape; kept for readability.
	PlanIDRe  = IDRe
	StoryIDRe = IDRe
	// SectionNumberRe matches a plan phase/section number ("2", "1.1").
	SectionNumberRe = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)
	// StepNumberRe matches a story step number at any depth ("2.1.3").
	StepNumberRe = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*$`)
	SlugRe       = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	// FileNameRe matches a document file name: AAAA-BBB-CCCC-slug.md. The type
	// part is 2–8 alphanumerics so configured types can vary in length.
	FileNameRe = regexp.MustCompile(`^([0-9]{4}-[a-z0-9]{3})-([a-zA-Z0-9]{2,8})-([a-z0-9]+(?:-[a-z0-9]+)*)\.md$`)
	// shortIDRe matches an id whose sequence part has fewer than four digits.
	shortIDRe = regexp.MustCompile(`^([0-9]{1,3})-([a-z0-9]{3})$`)
)

// Document is a stored plan or story: its kind, where it lives, its front
// matter and its Markdown content (everything below the front matter).
type Document struct {
	// Kind is "plan" or "story".
	Kind string `json:"kind"`
	// Path is the document's location relative to its kind's root, e.g.
	// "0002-a3f-dsgn-description-of-plan.md" or "archive/…". Every storage
	// plugin records it so copies stay interchangeable.
	Path        string      `json:"path"`
	FrontMatter FrontMatter `json:"frontMatter"`
	Content     string      `json:"content"`
}

// ID returns the document's identifier.
func (d *Document) ID() string { return d.FrontMatter.ID }

// Clone returns a deep copy.
func (d *Document) Clone() *Document {
	if d == nil {
		return nil
	}
	c := *d
	c.FrontMatter = d.FrontMatter.Clone()
	return &c
}

// FrontMatter is the YAML header of a document. Fields a kind does not use
// are omitted when empty and rejected by that kind's schema when set.
type FrontMatter struct {
	ID       string `yaml:"id" json:"id"`
	Title    string `yaml:"title" json:"title"`
	Purpose  string `yaml:"purpose,omitempty" json:"purpose,omitempty"`
	Type     string `yaml:"type" json:"type"`
	Status   string `yaml:"status" json:"status"`
	Priority string `yaml:"priority" json:"priority"`
	Effort   string `yaml:"effort,omitempty" json:"effort,omitempty"`
	Created  string `yaml:"created" json:"created"`
	Updated  string `yaml:"updated" json:"updated"`
	// Started is omitted until the document first enters in_progress.
	Started string `yaml:"started,omitempty" json:"started,omitempty"`
	// Completed is omitted until the document first reaches complete; it is
	// never written as an empty string.
	Completed string   `yaml:"completed,omitempty" json:"completed,omitempty"`
	Links     *Links   `yaml:"links,omitempty" json:"links,omitempty"`
	Progress  Progress `yaml:"progress,omitempty" json:"progress,omitempty"`
}

// Clone returns a deep copy.
func (f FrontMatter) Clone() FrontMatter {
	c := f
	if f.Links != nil {
		c.Links = f.Links.Clone()
	}
	if f.Progress != nil {
		c.Progress = make(Progress, len(f.Progress))
		for k, v := range f.Progress {
			e := v
			if v.Stories != nil {
				e.Stories = append([]string(nil), v.Stories...)
			}
			c.Progress[k] = e
		}
	}
	return c
}

// PlanLinks returns the links to plans (nil when there are none).
func (f *FrontMatter) PlanLinks() []Link {
	if f.Links == nil {
		return nil
	}
	return f.Links.Plans
}

// SetPlanLinks replaces the links to plans, dropping an empty Links object.
func (f *FrontMatter) SetPlanLinks(links []Link) {
	if len(links) == 0 {
		if f.Links != nil {
			f.Links.Plans = nil
			if f.Links.IsEmpty() {
				f.Links = nil
			}
		}
		return
	}
	if f.Links == nil {
		f.Links = &Links{}
	}
	f.Links.Plans = links
}

// StoryLinks returns the links to stories (nil when there are none).
func (f *FrontMatter) StoryLinks() []Link {
	if f.Links == nil {
		return nil
	}
	return f.Links.Stories
}

// SetStoryLinks replaces the links to stories, dropping an empty Links object.
func (f *FrontMatter) SetStoryLinks(links []Link) {
	if len(links) == 0 {
		if f.Links != nil {
			f.Links.Stories = nil
			if f.Links.IsEmpty() {
				f.Links = nil
			}
		}
		return
	}
	if f.Links == nil {
		f.Links = &Links{}
	}
	f.Links.Stories = links
}

// Link is a [targetID, relation] tuple, optionally carrying a third element
// listing the plan sections a story implements. It serialises as a two- or
// three-element array in both YAML (flow style) and JSON.
type Link struct {
	ID       string
	Relation string
	Sections []string
}

// Same reports whether two links refer to the same target and relation.
func (l Link) Same(o Link) bool { return l.ID == o.ID && l.Relation == o.Relation }

func (l Link) MarshalJSON() ([]byte, error) {
	if len(l.Sections) > 0 {
		return json.Marshal([]any{l.ID, l.Relation, l.Sections})
	}
	return json.Marshal([2]string{l.ID, l.Relation})
}

func (l *Link) UnmarshalJSON(b []byte) error {
	var arr []json.RawMessage
	if err := json.Unmarshal(b, &arr); err != nil {
		// Also accept {"id": ..., "relation": ..., "sections": [...]}.
		var obj struct {
			ID       string   `json:"id"`
			Relation string   `json:"relation"`
			Sections []string `json:"sections"`
		}
		if err2 := json.Unmarshal(b, &obj); err2 != nil {
			return fmt.Errorf("link must be a [id, relation] pair: %w", err)
		}
		return l.set(obj.ID, obj.Relation, obj.Sections)
	}
	if len(arr) < 2 || len(arr) > 3 {
		return fmt.Errorf("link must be a [id, relation] pair (optionally with a sections list), got %d items", len(arr))
	}
	var id, rel string
	if err := json.Unmarshal(arr[0], &id); err != nil {
		return fmt.Errorf("link id must be a string: %w", err)
	}
	if err := json.Unmarshal(arr[1], &rel); err != nil {
		return fmt.Errorf("link relation must be a string: %w", err)
	}
	var sections []string
	if len(arr) == 3 {
		if err := json.Unmarshal(arr[2], &sections); err != nil {
			return fmt.Errorf("link sections must be a list of strings: %w", err)
		}
	}
	return l.set(id, rel, sections)
}

// set assigns a link after checking both parts are present.
func (l *Link) set(id, relation string, sections []string) error {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(relation) == "" {
		return fmt.Errorf("link must be a [id, relation] pair with both parts set, got [%q, %q]", id, relation)
	}
	for _, s := range sections {
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("link %s: sections must be non-empty strings", id)
		}
	}
	l.ID, l.Relation, l.Sections = id, relation, sections
	return nil
}

func (l Link) MarshalYAML() (any, error) {
	node := &yaml.Node{
		Kind:  yaml.SequenceNode,
		Style: yaml.FlowStyle,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Style: yaml.DoubleQuotedStyle, Value: l.ID},
			{Kind: yaml.ScalarNode, Style: yaml.DoubleQuotedStyle, Value: l.Relation},
		},
	}
	if len(l.Sections) > 0 {
		seq := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
		for _, s := range l.Sections {
			seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Style: yaml.DoubleQuotedStyle, Value: s})
		}
		node.Content = append(node.Content, seq)
	}
	return node, nil
}

func (l *Link) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.SequenceNode {
		if len(n.Content) < 2 || len(n.Content) > 3 || n.Content[0].Kind != yaml.ScalarNode || n.Content[1].Kind != yaml.ScalarNode {
			return fmt.Errorf("line %d: link must be a [id, relation] pair of strings, optionally with a sections list", n.Line)
		}
		var sections []string
		if len(n.Content) == 3 {
			if n.Content[2].Kind != yaml.SequenceNode {
				return fmt.Errorf("line %d: link sections must be a list", n.Line)
			}
			for _, s := range n.Content[2].Content {
				if s.Kind != yaml.ScalarNode {
					return fmt.Errorf("line %d: link sections must be strings", n.Line)
				}
				sections = append(sections, s.Value)
			}
		}
		if err := l.set(n.Content[0].Value, n.Content[1].Value, sections); err != nil {
			return fmt.Errorf("line %d: %w", n.Line, err)
		}
		return nil
	}
	if n.Kind == yaml.MappingNode {
		var obj struct {
			ID       string   `yaml:"id"`
			Relation string   `yaml:"relation"`
			Sections []string `yaml:"sections"`
		}
		if err := n.Decode(&obj); err != nil {
			return err
		}
		if err := l.set(obj.ID, obj.Relation, obj.Sections); err != nil {
			return fmt.Errorf("line %d: %w", n.Line, err)
		}
		return nil
	}
	return fmt.Errorf("line %d: link must be a [id, relation] pair", n.Line)
}

// Links groups the outbound connections of a document: the repository it
// lives in, spec documents, named web links, and [id, relation] tuples to
// stories and to plans.
type Links struct {
	Repo    *Repo             `yaml:"repo,omitempty" json:"repo,omitempty"`
	Specs   []string          `yaml:"specs,omitempty" json:"specs,omitempty"`
	Web     map[string]string `yaml:"web,omitempty" json:"web,omitempty"`
	Stories []Link            `yaml:"stories,omitempty" json:"stories,omitempty"`
	Plans   []Link            `yaml:"plans,omitempty" json:"plans,omitempty"`
}

// Clone returns a deep copy.
func (l *Links) Clone() *Links {
	if l == nil {
		return nil
	}
	c := *l
	if l.Repo != nil {
		r := *l.Repo
		if l.Repo.Files != nil {
			r.Files = append([]string(nil), l.Repo.Files...)
		}
		c.Repo = &r
	}
	if l.Specs != nil {
		c.Specs = append([]string(nil), l.Specs...)
	}
	if l.Web != nil {
		c.Web = make(map[string]string, len(l.Web))
		for k, v := range l.Web {
			c.Web[k] = v
		}
	}
	c.Stories = cloneLinks(l.Stories)
	c.Plans = cloneLinks(l.Plans)
	return &c
}

func cloneLinks(in []Link) []Link {
	if in == nil {
		return nil
	}
	out := make([]Link, len(in))
	for i, l := range in {
		out[i] = l
		if l.Sections != nil {
			out[i].Sections = append([]string(nil), l.Sections...)
		}
	}
	return out
}

// IsEmpty reports whether no link of any kind is set.
func (l *Links) IsEmpty() bool {
	return l == nil || (l.Repo == nil && len(l.Specs) == 0 && len(l.Web) == 0 && len(l.Stories) == 0 && len(l.Plans) == 0)
}

// Repo points at the git repository the work happens in. PullRequest and
// Files are used by stories.
type Repo struct {
	Remote      string   `yaml:"remote,omitempty" json:"remote,omitempty"`
	Local       string   `yaml:"local,omitempty" json:"local,omitempty"`
	PullRequest string   `yaml:"pull_request,omitempty" json:"pull_request,omitempty"`
	Files       []string `yaml:"files,omitempty" json:"files,omitempty"`
}

// IsEmpty reports whether nothing is set.
func (r *Repo) IsEmpty() bool {
	return r == nil || (r.Remote == "" && r.Local == "" && r.PullRequest == "" && len(r.Files) == 0)
}

// ProgressEntry records the state of one numbered phase, section or step.
type ProgressEntry struct {
	Status  string   `yaml:"status" json:"status"`
	Stories []string `yaml:"stories,omitempty" json:"stories,omitempty"`
}

// Progress maps numbers ("2", "1.1", "2.1.3") to their state. Keys are
// always written as quoted YAML strings and ordered numerically.
type Progress map[string]ProgressEntry

// SortedKeys returns the keys in numeric order (1, 1.1, 1.2, 2, 10).
func (p Progress) SortedKeys() []string {
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return CompareSections(keys[i], keys[j]) < 0 })
	return keys
}

// CompareSections orders numbers numerically part by part.
func CompareSections(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		na, ea := strconv.Atoi(pa[i])
		nb, eb := strconv.Atoi(pb[i])
		if ea != nil || eb != nil {
			if c := strings.Compare(pa[i], pb[i]); c != 0 {
				return c
			}
			continue
		}
		if na != nb {
			if na < nb {
				return -1
			}
			return 1
		}
	}
	return len(pa) - len(pb)
}

func (p Progress) MarshalYAML() (any, error) {
	node := &yaml.Node{Kind: yaml.MappingNode}
	for _, k := range p.SortedKeys() {
		var val yaml.Node
		if err := val.Encode(p[k]); err != nil {
			return nil, err
		}
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Style: yaml.DoubleQuotedStyle, Value: k},
			&val,
		)
	}
	return node, nil
}

func (p *Progress) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: progress must be a mapping", n.Line)
	}
	out := make(Progress, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		var e ProgressEntry
		if err := n.Content[i+1].Decode(&e); err != nil {
			return err
		}
		// Keys are read from the raw scalar so an unquoted 1.10 stays "1.10".
		key := n.Content[i].Value
		if _, dup := out[key]; dup {
			return fmt.Errorf("line %d: duplicate progress key %q", n.Content[i].Line, key)
		}
		out[key] = e
	}
	*p = out
	return nil
}

// MarshalJSON emits the entries in numeric order.
func (p Progress) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range p.SortedKeys() {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		vb, err := json.Marshal(p[k])
		if err != nil {
			return nil, err
		}
		b.Write(kb)
		b.WriteByte(':')
		b.Write(vb)
	}
	b.WriteByte('}')
	return []byte(b.String()), nil
}

// Template is a content template (a Go text/template over the body) for one kind.
type Template struct {
	Kind    string `json:"kind,omitempty"`
	ID      string `json:"id"`
	Content string `json:"content"`
	Updated string `json:"updated,omitempty"`
}

// ParseFileName splits a document file name into id, type and slug.
func ParseFileName(name string) (id, typ, slug string, ok bool) {
	base := name
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	m := FileNameRe.FindStringSubmatch(base)
	if m == nil {
		return "", "", "", false
	}
	return m[1], strings.ToLower(m[2]), m[3], true
}

// FileName builds the canonical file name for a document.
func FileName(id, typ, slug string) string {
	return fmt.Sprintf("%s-%s-%s.md", id, strings.ToLower(typ), slug)
}

// Slugify derives a filename slug from a title: lowercase, runs of
// non-alphanumerics collapsed to a single hyphen, trimmed.
func Slugify(title string) string {
	var b strings.Builder
	lastHyphen := true
	for _, r := range strings.ToLower(title) {
		isAlnum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlnum {
			b.WriteRune(r)
			lastHyphen = false
		} else if !lastHyphen {
			b.WriteByte('-')
			lastHyphen = true
		}
	}
	s := strings.TrimRight(b.String(), "-")
	if s == "" {
		s = "untitled"
	}
	return s
}

// TimestampLayout is the canonical front matter timestamp: RFC 3339, UTC,
// millisecond precision.
const TimestampLayout = "2006-01-02T15:04:05.000Z"

// NormalizeTimestamp converts an accepted input form to the canonical layout:
// RFC 3339 in any zone and precision, "YYYY-MM-DD HH:MM:SS UTC", or a bare
// "YYYY-MM-DD" date. Unparsable input is returned unchanged with an error so
// schema validation can report it.
func NormalizeTimestamp(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	in := s
	// RFC 3339 allows lowercase date/time and zone separators; Go does not.
	if len(in) > 10 && in[10] == 't' {
		in = in[:10] + "T" + in[11:]
	}
	if strings.HasSuffix(in, "z") {
		in = in[:len(in)-1] + "Z"
	}
	layouts := []string{time.RFC3339Nano, "2006-01-02 15:04:05 UTC", "2006-01-02 15:04:05", "2006-01-02"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, in); err == nil {
			return t.UTC().Format(TimestampLayout), nil
		}
	}
	return s, fmt.Errorf("unrecognised timestamp %q", s)
}

// NormalizeTimestamps rewrites created, updated, started and completed into
// the canonical layout where they parse, leaving unparsable values for
// validation.
func (f *FrontMatter) NormalizeTimestamps() {
	for _, p := range []*string{&f.Created, &f.Updated, &f.Started, &f.Completed} {
		if n, err := NormalizeTimestamp(*p); err == nil {
			*p = n
		}
	}
}

// NormalizeID zero-pads an id's sequence number to four digits
// ("001-abc" → "0001-abc"). Anything else is returned unchanged so
// validation can report it.
func NormalizeID(id string) string {
	if m := shortIDRe.FindStringSubmatch(id); m != nil {
		return fmt.Sprintf("%04s-%s", m[1], m[2])
	}
	return id
}

// NormalizeStoryID is NormalizeID; kept for readability at call sites.
func NormalizeStoryID(id string) string { return NormalizeID(id) }

// NormalizeIDs zero-pads every linked id in links and progress.
func (f *FrontMatter) NormalizeIDs() {
	if f.Links != nil {
		for i := range f.Links.Stories {
			f.Links.Stories[i].ID = NormalizeID(f.Links.Stories[i].ID)
		}
		for i := range f.Links.Plans {
			f.Links.Plans[i].ID = NormalizeID(f.Links.Plans[i].ID)
		}
	}
	for k, e := range f.Progress {
		for i := range e.Stories {
			e.Stories[i] = NormalizeID(e.Stories[i])
		}
		f.Progress[k] = e
	}
}

// SequenceOf returns the numeric sequence part of an id.
func SequenceOf(id string) int {
	if len(id) < 4 {
		return 0
	}
	n, _ := strconv.Atoi(id[:4])
	return n
}
