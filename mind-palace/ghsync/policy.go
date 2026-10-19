package ghsync

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// BranchMode is which branches of a repository sync.
type BranchMode string

const (
	// BranchesActive syncs the default branch plus every active branch:
	// one with an open pull request, or pushed within ActiveDays.
	BranchesActive BranchMode = "active"
	// BranchesDefault syncs the default branch only.
	BranchesDefault BranchMode = "default"
	// BranchesAll syncs every branch, the MaxBranches most recently pushed
	// (the default branch always among them).
	BranchesAll BranchMode = "all"
)

// Policy defaults.
const (
	DefaultBranchMode  = BranchesActive
	DefaultActiveDays  = 14
	DefaultMaxBranches = 200
)

// ErrInvalidPolicy is returned for a branch policy value out of range.
var ErrInvalidPolicy = errors.New("ghsync: invalid branch policy")

// ParseBranchMode parses a mode (case and surrounding space ignored).
func ParseBranchMode(s string) (BranchMode, error) {
	m := BranchMode(strings.ToLower(strings.TrimSpace(s)))
	switch m {
	case BranchesActive, BranchesDefault, BranchesAll:
		return m, nil
	}
	return "", fmt.Errorf("%w: branches must be active, default or all, not %q", ErrInvalidPolicy, s)
}

// BranchPolicy is a repository's branch policy. A zero field is unset and
// takes its default (Normalize).
type BranchPolicy struct {
	Mode        BranchMode `json:"branches,omitempty" yaml:"branches,omitempty"`
	ActiveDays  int        `json:"activeDays,omitempty" yaml:"activeDays,omitempty"`
	MaxBranches int        `json:"maxBranches,omitempty" yaml:"maxBranches,omitempty"`
}

// DefaultBranchPolicy is the policy when nothing is set: active branches,
// 14 days, 200 branches.
func DefaultBranchPolicy() BranchPolicy {
	return BranchPolicy{Mode: DefaultBranchMode, ActiveDays: DefaultActiveDays, MaxBranches: DefaultMaxBranches}
}

// Normalize fills unset fields with the defaults.
func (p BranchPolicy) Normalize() BranchPolicy {
	if p.Mode == "" {
		p.Mode = DefaultBranchMode
	}
	if p.ActiveDays == 0 {
		p.ActiveDays = DefaultActiveDays
	}
	if p.MaxBranches == 0 {
		p.MaxBranches = DefaultMaxBranches
	}
	return p
}

// Validate checks the set fields: a known mode, positive counts.
func (p BranchPolicy) Validate() error {
	switch p.Mode {
	case "", BranchesActive, BranchesDefault, BranchesAll:
	default:
		return fmt.Errorf("%w: branches must be active, default or all, not %q", ErrInvalidPolicy, p.Mode)
	}
	if p.ActiveDays < 0 {
		return fmt.Errorf("%w: activeDays must be positive", ErrInvalidPolicy)
	}
	if p.MaxBranches < 0 {
		return fmt.Errorf("%w: maxBranches must be positive", ErrInvalidPolicy)
	}
	return nil
}

// Over returns p with every field set in o replacing p's: o wins.
func (p BranchPolicy) Over(o BranchPolicy) BranchPolicy {
	if o.Mode != "" {
		p.Mode = o.Mode
	}
	if o.ActiveDays != 0 {
		p.ActiveDays = o.ActiveDays
	}
	if o.MaxBranches != 0 {
		p.MaxBranches = o.MaxBranches
	}
	return p
}

// EffectivePolicy is the policy a repository syncs with: the page setting
// (the repository's, else the installation's, as Claim.Policy carries it)
// with each key the repository's mind-palace.config.yaml sets winning, since
// that file is reviewed with the code; defaults for whatever neither sets.
func EffectivePolicy(page BranchPolicy, file RepoConfig) BranchPolicy {
	return page.Over(file.GitHub).Normalize()
}

// Branch is what the policy knows about a branch.
type Branch struct {
	// Name is the branch name without refs/heads/.
	Name string
	// LastPush is when the branch last received a push (zero: unknown).
	LastPush time.Time
	// OpenPR is true when a pull request from the branch is open.
	OpenPR bool
}

// Wants is the rule for one branch, without the MaxBranches cap of
// BranchesAll (use Select for that): the default branch always; under
// BranchesActive a branch with an open pull request or a push within
// ActiveDays of now; under BranchesAll every branch.
func (p BranchPolicy) Wants(defaultBranch string, b Branch, now time.Time) bool {
	p = p.Normalize()
	if b.Name == defaultBranch {
		return true
	}
	switch p.Mode {
	case BranchesAll:
		return true
	case BranchesActive:
		if b.OpenPR {
			return true
		}
		return !b.LastPush.IsZero() && !b.LastPush.Before(now.Add(-time.Duration(p.ActiveDays)*24*time.Hour))
	}
	return false
}

// Selection splits a repository's branches by the policy.
type Selection struct {
	// Active are the branches to sync: the default branch first, then the
	// rest by name.
	Active []string
	// Inactive are the branches not synced, by name. Their last tree is
	// kept; only a deleted branch loses it.
	Inactive []string
}

// Select applies the policy to every branch of a repository. Under
// BranchesAll the default branch and the most recently pushed others (ties
// by name) fill MaxBranches; the rest are inactive. The default branch is
// active even when branches does not list it.
func (p BranchPolicy) Select(defaultBranch string, branches []Branch, now time.Time) Selection {
	p = p.Normalize()
	var active, inactive []Branch
	for _, b := range branches {
		if b.Name == defaultBranch {
			continue
		}
		if p.Wants(defaultBranch, b, now) {
			active = append(active, b)
		} else {
			inactive = append(inactive, b)
		}
	}
	if p.Mode == BranchesAll {
		limit := p.MaxBranches
		if defaultBranch != "" {
			limit--
		}
		if limit < 0 {
			limit = 0
		}
		if len(active) > limit {
			sort.SliceStable(active, func(i, j int) bool {
				if !active[i].LastPush.Equal(active[j].LastPush) {
					return active[i].LastPush.After(active[j].LastPush)
				}
				return active[i].Name < active[j].Name
			})
			inactive = append(inactive, active[limit:]...)
			active = active[:limit]
		}
	}
	var sel Selection
	if defaultBranch != "" {
		sel.Active = append(sel.Active, defaultBranch)
	}
	sel.Active = append(sel.Active, sortedNames(active)...)
	sel.Inactive = sortedNames(inactive)
	return sel
}

func sortedNames(bs []Branch) []string {
	if len(bs) == 0 {
		return nil
	}
	names := make([]string, len(bs))
	for i, b := range bs {
		names[i] = b.Name
	}
	sort.Strings(names)
	return names
}

// RepoConfig is what the sync reads from a repository's mind-palace config
// file (one of config.DefaultConfigFiles at the commit): the github.* keys,
// codeSync and remote.project. Other keys are ignored here; the client's
// loader reads them.
type RepoConfig struct {
	// GitHub is the github: section; zero fields were not set.
	GitHub BranchPolicy
	// CodeSync is the codeSync key; nil when not set. false opts the
	// repository out of code sync.
	CodeSync *bool
	// Project is remote.project, the slug override ("" when not set).
	Project string
}

// repoConfigFile is the file shape ParseRepoConfig decodes.
type repoConfigFile struct {
	GitHub *struct {
		Branches    *string `yaml:"branches"`
		ActiveDays  *int    `yaml:"activeDays"`
		MaxBranches *int    `yaml:"maxBranches"`
	} `yaml:"github"`
	CodeSync *bool `yaml:"codeSync"`
	Remote   *struct {
		Project string `yaml:"project"`
	} `yaml:"remote"`
}

// ParseRepoConfig reads the sync's keys from a mind-palace config file's
// content (YAML or JSON; an empty file sets nothing). Unknown keys are
// ignored; a set github.* value out of range is an error naming the key.
func ParseRepoConfig(data []byte) (RepoConfig, error) {
	var f repoConfigFile
	if err := yaml.NewDecoder(bytes.NewReader(data)).Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		return RepoConfig{}, fmt.Errorf("ghsync: mind-palace config: %w", err)
	}
	var rc RepoConfig
	if g := f.GitHub; g != nil {
		if g.Branches != nil {
			m, err := ParseBranchMode(*g.Branches)
			if err != nil {
				return RepoConfig{}, fmt.Errorf("github.branches: %w", err)
			}
			rc.GitHub.Mode = m
		}
		if g.ActiveDays != nil {
			if *g.ActiveDays <= 0 {
				return RepoConfig{}, fmt.Errorf("github.activeDays: %w: must be positive, not %d", ErrInvalidPolicy, *g.ActiveDays)
			}
			rc.GitHub.ActiveDays = *g.ActiveDays
		}
		if g.MaxBranches != nil {
			if *g.MaxBranches <= 0 {
				return RepoConfig{}, fmt.Errorf("github.maxBranches: %w: must be positive, not %d", ErrInvalidPolicy, *g.MaxBranches)
			}
			rc.GitHub.MaxBranches = *g.MaxBranches
		}
	}
	rc.CodeSync = f.CodeSync
	if f.Remote != nil {
		rc.Project = strings.TrimSpace(f.Remote.Project)
	}
	return rc, nil
}
