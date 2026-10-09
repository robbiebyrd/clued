// Package model defines the Plan data types shared by every storage plugin
// and entrypoint of the plan service. Stories will live alongside Plans as an
// architectural equal; nothing here is stubbed out for them yet.
package model

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Relations a Plan can have to another Plan or Story.
const (
	RelationParent   = "parent"
	RelationIncluded = "included"
	RelationDepends  = "depends"
	RelationBlocks   = "blocks"
)

// PlanRelations lists the relations valid for plan→plan links.
var PlanRelations = []string{RelationParent, RelationIncluded, RelationDepends, RelationBlocks}

// StoryRelations lists the relations valid for plan→story links.
var StoryRelations = []string{RelationIncluded, RelationDepends, RelationBlocks}

// Identifier patterns.
var (
	PlanIDRe        = regexp.MustCompile(`^[0-9]{4}-[a-z0-9]{3}$`)
	StoryIDRe       = regexp.MustCompile(`^[0-9]{3,4}-[a-z0-9]{3}$`)
	SectionNumberRe = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)
	SlugRe          = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	// FileNameRe matches a plan file name: AAAA-BBB-CCCC-slug.md. The type part is
	// 2–8 alphanumerics so configured plan types can be longer than four letters.
	FileNameRe = regexp.MustCompile(`^([0-9]{4}-[a-z0-9]{3})-([a-zA-Z0-9]{2,8})-([a-z0-9]+(?:-[a-z0-9]+)*)\.md$`)
)

// Plan is a stored plan: where it lives, its front matter and its Markdown
// content (everything below the front matter).
type Plan struct {
	// Path is the plan's location relative to the plans root, e.g.
	// "0002-a3f-dsgn-description-of-plan.md" or
	// "archive/0002-a3f-dsgn-description-of-plan.md". Every storage plugin
	// records it so copies stay interchangeable.
	Path        string      `json:"path"`
	FrontMatter FrontMatter `json:"frontMatter"`
	Content     string      `json:"content"`
}

// ID returns the plan's identifier.
func (p *Plan) ID() string { return p.FrontMatter.ID }

// Clone returns a deep copy.
func (p *Plan) Clone() *Plan {
	if p == nil {
		return nil
	}
	c := *p
	c.FrontMatter = p.FrontMatter.Clone()
	return &c
}

// FrontMatter is the YAML header of a plan file.
type FrontMatter struct {
	ID        string   `yaml:"id" json:"id"`
	Title     string   `yaml:"title" json:"title"`
	Type      string   `yaml:"type" json:"type"`
	Status    string   `yaml:"status" json:"status"`
	Priority  string   `yaml:"priority" json:"priority"`
	Effort    string   `yaml:"effort,omitempty" json:"effort,omitempty"`
	Created   string   `yaml:"created" json:"created"`
	Updated   string   `yaml:"updated" json:"updated"`
	Completed string   `yaml:"completed" json:"completed"`
	Plans     []Link   `yaml:"plans,omitempty" json:"plans,omitempty"`
	Links     *Links   `yaml:"links,omitempty" json:"links,omitempty"`
	Progress  Progress `yaml:"progress,omitempty" json:"progress,omitempty"`
}

// Clone returns a deep copy.
func (f FrontMatter) Clone() FrontMatter {
	c := f
	if f.Plans != nil {
		c.Plans = append([]Link(nil), f.Plans...)
	}
	if f.Links != nil {
		l := *f.Links
		if f.Links.Repo != nil {
			r := *f.Links.Repo
			l.Repo = &r
		}
		if f.Links.Specs != nil {
			l.Specs = append([]string(nil), f.Links.Specs...)
		}
		if f.Links.Web != nil {
			l.Web = make(map[string]string, len(f.Links.Web))
			for k, v := range f.Links.Web {
				l.Web[k] = v
			}
		}
		if f.Links.Stories != nil {
			l.Stories = append([]Link(nil), f.Links.Stories...)
		}
		c.Links = &l
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

// Link is a [targetID, relation] tuple. It serialises as a two-element array
// in both YAML (flow style) and JSON.
type Link struct {
	ID       string
	Relation string
}

func (l Link) MarshalJSON() ([]byte, error) {
	return json.Marshal([2]string{l.ID, l.Relation})
}

func (l *Link) UnmarshalJSON(b []byte) error {
	var arr []string
	if err := json.Unmarshal(b, &arr); err != nil {
		// Also accept {"id": ..., "relation": ...}.
		var obj struct {
			ID       string `json:"id"`
			Relation string `json:"relation"`
		}
		if err2 := json.Unmarshal(b, &obj); err2 != nil || obj.ID == "" {
			return fmt.Errorf("link must be a [id, relation] pair: %w", err)
		}
		l.ID, l.Relation = obj.ID, obj.Relation
		return nil
	}
	if len(arr) != 2 {
		return fmt.Errorf("link must be a [id, relation] pair, got %d items", len(arr))
	}
	l.ID, l.Relation = arr[0], arr[1]
	return nil
}

func (l Link) MarshalYAML() (any, error) {
	return &yaml.Node{
		Kind:  yaml.SequenceNode,
		Style: yaml.FlowStyle,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Style: yaml.DoubleQuotedStyle, Value: l.ID},
			{Kind: yaml.ScalarNode, Style: yaml.DoubleQuotedStyle, Value: l.Relation},
		},
	}, nil
}

func (l *Link) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.SequenceNode {
		if len(n.Content) != 2 {
			return fmt.Errorf("line %d: link must be a [id, relation] pair", n.Line)
		}
		l.ID, l.Relation = n.Content[0].Value, n.Content[1].Value
		return nil
	}
	if n.Kind == yaml.MappingNode {
		var obj struct {
			ID       string `yaml:"id"`
			Relation string `yaml:"relation"`
		}
		if err := n.Decode(&obj); err != nil {
			return err
		}
		l.ID, l.Relation = obj.ID, obj.Relation
		return nil
	}
	return fmt.Errorf("line %d: link must be a [id, relation] pair", n.Line)
}

// Links groups the outbound connections of a plan.
type Links struct {
	Repo    *Repo             `yaml:"repo,omitempty" json:"repo,omitempty"`
	Specs   []string          `yaml:"specs,omitempty" json:"specs,omitempty"`
	Web     map[string]string `yaml:"web,omitempty" json:"web,omitempty"`
	Stories []Link            `yaml:"stories,omitempty" json:"stories,omitempty"`
}

// IsEmpty reports whether no link of any kind is set.
func (l *Links) IsEmpty() bool {
	return l == nil || (l.Repo == nil && len(l.Specs) == 0 && len(l.Web) == 0 && len(l.Stories) == 0)
}

// Repo points at the git repository the plan's work happens in.
type Repo struct {
	Remote string `yaml:"remote,omitempty" json:"remote,omitempty"`
	Local  string `yaml:"local,omitempty" json:"local,omitempty"`
}

// ProgressEntry records the state of one numbered phase or section.
type ProgressEntry struct {
	Status  string   `yaml:"status" json:"status"`
	Stories []string `yaml:"stories,omitempty" json:"stories,omitempty"`
}

// Progress maps section numbers ("2", "1.1") to their state. Keys are always
// written as quoted YAML strings and ordered numerically.
type Progress map[string]ProgressEntry

// SortedKeys returns the section numbers in numeric order (1, 1.1, 1.2, 2, 10).
func (p Progress) SortedKeys() []string {
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return CompareSections(keys[i], keys[j]) < 0 })
	return keys
}

// CompareSections orders section numbers numerically part by part.
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
		out[n.Content[i].Value] = e
	}
	*p = out
	return nil
}

// Template is a plan content template (a Go text/template over the plan body).
type Template struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Updated string `json:"updated,omitempty"`
}

// ParseFileName splits a plan file name into id, type and slug.
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

// FileName builds the canonical file name for a plan.
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
		s = "plan"
	}
	return s
}

// SequenceOf returns the numeric sequence part of a plan ID.
func SequenceOf(id string) int {
	if len(id) < 4 {
		return 0
	}
	n, _ := strconv.Atoi(id[:4])
	return n
}
