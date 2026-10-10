// Package config holds the plan service configuration: plan types, statuses
// (with synonyms and workflow), priorities, effort levels and storage plugins.
// Every list ships with a default that a config file can replace.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultPlansDir is where file-based storage keeps plans, relative to the
// working directory the service was called from.
const DefaultPlansDir = "docs/plans"

// DefaultStatus is the status a new plan gets when none is provided.
const DefaultStatus = "pending"

// Env var and file names consulted when no --config flag is given.
const (
	EnvConfig = "PLAN_CONFIG"
)

// typeNameRe is the shape a plan type must have to fit in a file name.
var typeNameRe = regexp.MustCompile(`^[a-z0-9]{2,8}$`)

// DefaultConfigFiles are looked up in the working directory and then each
// parent directory up to the user's home directory, in order.
var DefaultConfigFiles = []string{"plan.config.yaml", "plan.config.yml", "plan.config.json", ".plan.yaml", ".plan.json"}

// TypeDef is a plan type.
type TypeDef struct {
	Name  string `yaml:"name" json:"name"`
	Label string `yaml:"label" json:"label"`
}

// StatusDef is a plan status: primary system name, user label and synonyms.
type StatusDef struct {
	Name     string   `yaml:"name" json:"name"`
	Label    string   `yaml:"label" json:"label"`
	Synonyms []string `yaml:"synonyms,omitempty" json:"synonyms,omitempty"`
}

// PriorityDef is a numeric priority level and the labels that map to it.
type PriorityDef struct {
	Value  string   `yaml:"value" json:"value"`
	Labels []string `yaml:"labels" json:"labels"`
}

// EffortDef is an effort level: size, labels and story points.
type EffortDef struct {
	Name   string   `yaml:"name" json:"name"`
	Labels []string `yaml:"labels,omitempty" json:"labels,omitempty"`
	Points int      `yaml:"points" json:"points"`
}

// StorageDef configures one storage plugin instance.
type StorageDef struct {
	// Name identifies this store in sync commands and error messages.
	Name string `yaml:"name" json:"name"`
	// Kind selects the plugin: file, sqlite, postgres, mysql, clickhouse, mongodb, firestore.
	Kind string `yaml:"kind" json:"kind"`
	// Enabled defaults to true when omitted.
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	// Options are plugin-specific (dsn, dir, database, collection, project…).
	Options map[string]string `yaml:"options,omitempty" json:"options,omitempty"`
}

// IsEnabled reports whether the store is enabled (default true).
func (s StorageDef) IsEnabled() bool { return s.Enabled == nil || *s.Enabled }

// ServerConfig holds listen addresses for the network entrypoints.
type ServerConfig struct {
	Addr string `yaml:"addr" json:"addr"`
}

// Config is the effective service configuration.
type Config struct {
	// PlansDir is the root for file-based storage and the base for relative
	// paths. A relative value from a config file is anchored to that file's
	// directory; the default is relative to the working directory.
	PlansDir   string              `yaml:"plansDir" json:"plansDir"`
	Types      []TypeDef           `yaml:"types" json:"types"`
	Statuses   []StatusDef         `yaml:"statuses" json:"statuses"`
	Workflow   map[string][]string `yaml:"workflow" json:"workflow"`
	Priorities []PriorityDef       `yaml:"priorities" json:"priorities"`
	Efforts    []EffortDef         `yaml:"efforts" json:"efforts"`
	Storage    []StorageDef        `yaml:"storage" json:"storage"`
	Server     ServerConfig        `yaml:"server" json:"server"`
	// Source is the config file the values came from ("" for defaults).
	Source string `yaml:"-" json:"source,omitempty"`
}

// Default returns the built-in configuration.
func Default() *Config {
	return &Config{
		PlansDir: DefaultPlansDir,
		Types: []TypeDef{
			{Name: "drft", Label: "Draft"},
			{Name: "dsgn", Label: "Design"},
			{Name: "impl", Label: "Implementation"},
		},
		Statuses: []StatusDef{
			{Name: "pending", Label: "Pending", Synonyms: []string{"new", "draft", "todo"}},
			{Name: "validated", Label: "Validated", Synonyms: []string{"validate", "valid"}},
			{Name: "ready", Label: "Ready", Synonyms: []string{"approve", "approved"}},
			{Name: "in_progress", Label: "In Progress", Synonyms: []string{"start", "started", "active", "working", "progress", "in-progress"}},
			{Name: "complete", Label: "Complete", Synonyms: []string{"done", "completed", "finish", "finished", "close", "closed"}},
			{Name: "blocked", Label: "Blocked", Synonyms: []string{"block", "hold", "on_hold"}},
			{Name: "rejected", Label: "Rejected", Synonyms: []string{"reject", "skip", "skipped", "cancel", "cancelled", "wontdo", "wontfix"}},
			{Name: "archived", Label: "Archived", Synonyms: []string{"archive", "shelve", "shelved", "park", "parked"}},
		},
		Workflow: map[string][]string{
			"pending":     {"blocked", "rejected", "validated"},
			"validated":   {"ready", "pending", "blocked", "rejected", "archived"},
			"ready":       {"in_progress", "validated", "blocked", "rejected"},
			"in_progress": {"blocked", "rejected", "complete", "ready"},
			"blocked":     {"pending", "ready", "rejected"},
			"rejected":    {"blocked", "archived", "pending", "ready"},
			"complete":    {"archived", "in_progress"},
			"archived":    {"pending", "ready"},
		},
		Priorities: []PriorityDef{
			{Value: "0", Labels: []string{"P0", "Emergency"}},
			{Value: "1", Labels: []string{"P1", "Critical", "Block"}},
			{Value: "2", Labels: []string{"P2", "Major", "High"}},
			{Value: "3", Labels: []string{"P3", "Moderate", "Medium"}},
			{Value: "4", Labels: []string{"P4", "Minor", "Low"}},
			{Value: "5", Labels: []string{"P5", "Feature Request", "Informational"}},
		},
		Efforts: []EffortDef{
			{Name: "XS", Labels: []string{"Extra Small"}, Points: 2},
			{Name: "S", Labels: []string{"Small"}, Points: 3},
			{Name: "M", Labels: []string{"Medium"}, Points: 5},
			{Name: "L", Labels: []string{"Large"}, Points: 8},
			{Name: "XL", Labels: []string{"Extra Large"}, Points: 13},
		},
		Storage: []StorageDef{{Name: "file", Kind: "file"}},
		Server:  ServerConfig{Addr: "127.0.0.1:8087"},
	}
}

// fileConfig mirrors Config with every field optional so a file can override
// only part of the defaults.
type fileConfig struct {
	PlansDir   *string             `yaml:"plansDir" json:"plansDir"`
	Types      []TypeDef           `yaml:"types" json:"types"`
	Statuses   []StatusDef         `yaml:"statuses" json:"statuses"`
	Workflow   map[string][]string `yaml:"workflow" json:"workflow"`
	Priorities []PriorityDef       `yaml:"priorities" json:"priorities"`
	Efforts    []EffortDef         `yaml:"efforts" json:"efforts"`
	Storage    []StorageDef        `yaml:"storage" json:"storage"`
	Server     *ServerConfig       `yaml:"server" json:"server"`
}

// Load reads a config file (YAML or JSON) and overlays it on the defaults.
// An empty path means: use $PLAN_CONFIG, then the first DefaultConfigFiles
// entry found in the working directory or its ancestors (up to the home
// directory), then defaults alone.
func Load(path string) (*Config, error) {
	cfg := Default()
	if path == "" {
		path = os.Getenv(EnvConfig)
	}
	if path == "" {
		path = findDefaultConfig()
	}
	if path == "" {
		return cfg, cfg.Validate()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	if err := cfg.Overlay(data, filepath.Ext(path)); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	cfg.Source = path
	cfg.resolvePaths(filepath.Dir(path))
	return cfg, cfg.Validate()
}

// resolvePaths anchors relative directories from a config file to that
// file's directory, so storage lands in the same place no matter which
// subdirectory the service was invoked from.
func (c *Config) resolvePaths(baseDir string) {
	c.PlansDir = resolveAgainst(baseDir, c.PlansDir)
	for i := range c.Storage {
		def := &c.Storage[i]
		if def.Kind == "file" && def.Options["dir"] != "" {
			def.Options["dir"] = resolveAgainst(baseDir, def.Options["dir"])
		}
	}
}

// resolveAgainst joins a relative path onto baseDir; absolute paths and
// empty strings are returned unchanged.
func resolveAgainst(baseDir, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(baseDir, p)
}

// findDefaultConfig returns the nearest readable DefaultConfigFiles entry,
// searching the working directory and then each parent directory, or ""
// when none exists. The search never goes above the user's home directory,
// so a config planted in a shared ancestor such as /tmp is not honoured;
// outside the home directory only the working directory is searched.
func findDefaultConfig() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	dir = resolvePath(dir)
	home := ""
	if h, err := os.UserHomeDir(); err == nil {
		home = resolvePath(h)
	}
	withinHome := home != "" && (dir == home || strings.HasPrefix(dir, home+string(filepath.Separator)))
	for {
		for _, name := range DefaultConfigFiles {
			candidate := filepath.Join(dir, name)
			if isReadableFile(candidate) {
				return candidate
			}
		}
		parent := filepath.Dir(dir)
		if !withinHome || dir == home || parent == dir {
			return ""
		}
		dir = parent
	}
}

// resolvePath follows symlinks so that paths compare equal regardless of how
// they were spelled (e.g. /var vs /private/var on macOS).
func resolvePath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// isReadableFile reports whether path is a regular file the process can open.
func isReadableFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	return err == nil && info.Mode().IsRegular()
}

// Overlay applies a YAML or JSON document on top of the receiver.
func (c *Config) Overlay(data []byte, ext string) error {
	var fc fileConfig
	switch strings.ToLower(ext) {
	case ".json":
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&fc); err != nil {
			return err
		}
	default:
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(&fc); err != nil && !errors.Is(err, io.EOF) {
			return err
		}
	}
	if fc.PlansDir != nil {
		c.PlansDir = *fc.PlansDir
	}
	if fc.Types != nil {
		c.Types = fc.Types
	}
	if fc.Statuses != nil {
		c.Statuses = fc.Statuses
	}
	if fc.Workflow != nil {
		c.Workflow = fc.Workflow
	}
	if fc.Priorities != nil {
		c.Priorities = fc.Priorities
	}
	if fc.Efforts != nil {
		c.Efforts = fc.Efforts
	}
	if fc.Storage != nil {
		c.Storage = fc.Storage
	}
	if fc.Server != nil {
		if fc.Server.Addr != "" {
			c.Server.Addr = fc.Server.Addr
		}
	}
	return nil
}

// Validate checks internal consistency.
func (c *Config) Validate() error {
	var errs []error
	if c.PlansDir == "" {
		errs = append(errs, errors.New("plansDir must not be empty"))
	}
	if len(c.Types) == 0 {
		errs = append(errs, errors.New("at least one plan type is required"))
	}
	for _, t := range c.Types {
		if !typeNameRe.MatchString(t.Name) {
			errs = append(errs, fmt.Errorf("plan type %q must be 2–8 lowercase alphanumerics", t.Name))
		}
	}
	if len(c.Statuses) == 0 {
		errs = append(errs, errors.New("at least one status is required"))
	}
	if _, ok := c.Status(DefaultStatus); !ok {
		errs = append(errs, fmt.Errorf("statuses must include the default status %q", DefaultStatus))
	}
	for from, tos := range c.Workflow {
		if _, ok := c.Status(from); !ok {
			errs = append(errs, fmt.Errorf("workflow source %q is not a status", from))
		}
		for _, to := range tos {
			if _, ok := c.Status(to); !ok {
				errs = append(errs, fmt.Errorf("workflow target %q (from %q) is not a status", to, from))
			}
		}
	}
	if len(c.Priorities) == 0 {
		errs = append(errs, errors.New("at least one priority is required"))
	}
	enabled := 0
	for _, s := range c.Storage {
		if s.Name == "" || s.Kind == "" {
			errs = append(errs, errors.New("every storage entry needs a name and a kind"))
		}
		if s.IsEnabled() {
			enabled++
		}
	}
	if enabled == 0 {
		errs = append(errs, errors.New("at least one storage plugin must be enabled"))
	}
	return errors.Join(errs...)
}

// ArchiveDir is where archived plans live.
func (c *Config) ArchiveDir() string { return filepath.Join(c.PlansDir, "archive") }

// EnabledStorage returns the enabled storage definitions in order.
func (c *Config) EnabledStorage() []StorageDef {
	var out []StorageDef
	for _, s := range c.Storage {
		if s.IsEnabled() {
			out = append(out, s)
		}
	}
	return out
}

// Status looks up a status by primary name.
func (c *Config) Status(name string) (StatusDef, bool) {
	for _, s := range c.Statuses {
		if s.Name == name {
			return s, true
		}
	}
	return StatusDef{}, false
}

// StatusNames returns the primary status names in configured order.
func (c *Config) StatusNames() []string {
	out := make([]string, len(c.Statuses))
	for i, s := range c.Statuses {
		out[i] = s.Name
	}
	return out
}

// TypeNames returns the plan type names in configured order.
func (c *Config) TypeNames() []string {
	out := make([]string, len(c.Types))
	for i, t := range c.Types {
		out[i] = t.Name
	}
	return out
}

// PriorityValues returns the priority values in configured order.
func (c *Config) PriorityValues() []string {
	out := make([]string, len(c.Priorities))
	for i, p := range c.Priorities {
		out[i] = p.Value
	}
	return out
}

// EffortNames returns the effort sizes in configured order.
func (c *Config) EffortNames() []string {
	out := make([]string, len(c.Efforts))
	for i, e := range c.Efforts {
		out[i] = e.Name
	}
	return out
}

func norm(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// NormalizeStatus maps a primary name, label or synonym to the primary name.
func (c *Config) NormalizeStatus(in string) (string, bool) {
	n := norm(in)
	if n == "" {
		return "", false
	}
	for _, s := range c.Statuses {
		if n == norm(s.Name) || n == norm(s.Label) {
			return s.Name, true
		}
	}
	for _, s := range c.Statuses {
		for _, syn := range s.Synonyms {
			if n == norm(syn) {
				return s.Name, true
			}
		}
	}
	return "", false
}

// NormalizeType maps a type name or label to the type name.
func (c *Config) NormalizeType(in string) (string, bool) {
	n := norm(in)
	for _, t := range c.Types {
		if n == norm(t.Name) || n == norm(t.Label) {
			return t.Name, true
		}
	}
	return "", false
}

// scalarString renders a string or integral number as text; fractional
// numbers and other types are rejected.
func scalarString(in any) (string, bool) {
	switch v := in.(type) {
	case string:
		return v, true
	case float64:
		if v != float64(int64(v)) {
			return "", false
		}
		return fmt.Sprintf("%d", int64(v)), true
	case float32:
		if v != float32(int64(v)) {
			return "", false
		}
		return fmt.Sprintf("%d", int64(v)), true
	case int:
		return fmt.Sprintf("%d", v), true
	case int64:
		return fmt.Sprintf("%d", v), true
	case json.Number:
		return v.String(), true
	}
	return "", false
}

// NormalizePriority maps a number ("1", 1) or label ("P1", "Critical") to the value.
func (c *Config) NormalizePriority(in any) (string, bool) {
	s, ok := scalarString(in)
	if !ok {
		return "", false
	}
	n := norm(s)
	for _, p := range c.Priorities {
		if n == norm(p.Value) {
			return p.Value, true
		}
	}
	for _, p := range c.Priorities {
		for _, l := range p.Labels {
			if n == norm(l) {
				return p.Value, true
			}
		}
	}
	return "", false
}

// NormalizeEffort maps a size ("M"), label ("Medium") or points ("5", 5) to the size.
func (c *Config) NormalizeEffort(in any) (string, bool) {
	s, ok := scalarString(in)
	if !ok {
		return "", false
	}
	n := norm(s)
	for _, e := range c.Efforts {
		if n == norm(e.Name) {
			return e.Name, true
		}
	}
	for _, e := range c.Efforts {
		if n == fmt.Sprintf("%d", e.Points) {
			return e.Name, true
		}
		for _, l := range e.Labels {
			if n == norm(l) {
				return e.Name, true
			}
		}
	}
	return "", false
}

// Transitions returns the statuses reachable from the given one.
func (c *Config) Transitions(from string) []string {
	return append([]string(nil), c.Workflow[from]...)
}

// CanTransition reports whether the workflow allows from→to.
func (c *Config) CanTransition(from, to string) bool {
	for _, t := range c.Workflow[from] {
		if t == to {
			return true
		}
	}
	return false
}
