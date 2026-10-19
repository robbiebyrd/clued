package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaults(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	c := &cfg.Plans
	for in, want := range map[string]string{"approved": "ready", "Ready": "ready", "In Progress": "in_progress", "in-progress": "in_progress", "wontfix": "rejected", "parked": "archived", "draft": "pending"} {
		if got, ok := c.NormalizeStatus(in); !ok || got != want {
			t.Errorf("status %q = %q,%v want %q", in, got, ok, want)
		}
	}
	if _, ok := c.NormalizeStatus("bogus"); ok {
		t.Error("bogus status accepted")
	}
	for in, want := range map[any]string{"P1": "1", "critical": "1", 3: "3", "3": "3", float64(0): "0", "Feature Request": "5"} {
		if got, ok := c.NormalizePriority(in); !ok || got != want {
			t.Errorf("priority %v = %q,%v want %q", in, got, ok, want)
		}
	}
	for in, want := range map[any]string{"M": "M", "medium": "M", 5: "M", "13": "XL", "extra small": "XS"} {
		if got, ok := c.NormalizeEffort(in); !ok || got != want {
			t.Errorf("effort %v = %q,%v want %q", in, got, ok, want)
		}
	}
	if got, ok := c.NormalizeType("Implementation"); !ok || got != "impl" {
		t.Errorf("type = %q", got)
	}
	if !c.CanTransition("pending", "validated") || c.CanTransition("pending", "complete") {
		t.Error("workflow defaults wrong")
	}
	if len(c.Transitions("complete")) != 2 {
		t.Errorf("transitions from complete: %v", c.Transitions("complete"))
	}
}

func TestLoadOverlay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.config.yaml")
	os.WriteFile(path, []byte(`
plansDir: plans
statuses:
  - {name: pending, label: Pending, synonyms: [new]}
  - {name: done, label: Done}
workflow:
  pending: [done]
storage:
  - {name: db, kind: sqlite, options: {dsn: ":memory:"}}
  - {name: file, kind: file}
`), 0o644)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Plans.Dir != filepath.Join(dir, "plans") || len(c.Plans.Statuses) != 2 || len(c.Plans.Types) != 3 {
		t.Errorf("overlay: %+v", c)
	}
	if got := c.EnabledStorage(); len(got) != 2 || got[0].Name != "db" || got[1].Name != "file" {
		t.Errorf("enabled storage: %+v", got)
	}
	if c.Source != path {
		t.Errorf("source = %q", c.Source)
	}
	// Missing default status is rejected.
	os.WriteFile(path, []byte("statuses:\n  - {name: done, label: Done}\n"), 0o644)
	if _, err := Load(path); err == nil {
		t.Error("expected error when pending is missing")
	}
}

func TestStrictConfigAndNormalisationEdges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.config.yaml")
	os.WriteFile(path, []byte("plansDirr: x\n"), 0o644)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "plansDirr") {
		t.Errorf("unknown YAML key not reported: %v", err)
	}
	jpath := filepath.Join(dir, "plan.config.json")
	os.WriteFile(jpath, []byte(`{"typos": 1}`), 0o644)
	if _, err := Load(jpath); err == nil {
		t.Error("unknown JSON key not reported")
	}
	os.WriteFile(path, []byte("types:\n  - {name: 'a-b', label: X}\n"), 0o644)
	if _, err := Load(path); err == nil {
		t.Error("type with punctuation accepted")
	}
	os.WriteFile(path, []byte(""), 0o644)
	if c, err := Load(path); err != nil || len(c.Plans.Types) != 3 {
		t.Errorf("empty config file: %v", err)
	}
	c := &Default().Plans
	for _, in := range []any{1.5, float32(2.5), "1.5", true, nil} {
		if got, ok := c.NormalizePriority(in); ok {
			t.Errorf("priority %v accepted as %q", in, got)
		}
		if got, ok := c.NormalizeEffort(in); ok {
			t.Errorf("effort %v accepted as %q", in, got)
		}
	}
	if got, ok := c.NormalizePriority(2.0); !ok || got != "2" {
		t.Errorf("integral float rejected: %q %v", got, ok)
	}
}

func TestLoadFindsDefaultConfigInParentDir(t *testing.T) {
	t.Setenv(EnvConfig, "")
	root := t.TempDir()
	t.Setenv("HOME", root)
	os.WriteFile(filepath.Join(root, "plan.config.yaml"), []byte("plansDir: from-parent\n"), 0o644)
	nested := filepath.Join(root, "a", "b")
	os.MkdirAll(nested, 0o755)
	t.Chdir(nested)
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(c.Plans.Dir) != "from-parent" {
		t.Errorf("plansDir = %q, want config from parent dir", c.Plans.Dir)
	}
	if filepath.Base(c.Source) != "plan.config.yaml" || !filepath.IsAbs(c.Source) {
		t.Errorf("source = %q, want absolute path to parent plan.config.yaml", c.Source)
	}
}

func TestLoadPrefersNearestDefaultConfig(t *testing.T) {
	t.Setenv(EnvConfig, "")
	root := t.TempDir()
	t.Setenv("HOME", root)
	os.WriteFile(filepath.Join(root, "plan.config.yaml"), []byte("plansDir: from-parent\n"), 0o644)
	nested := filepath.Join(root, "a")
	os.MkdirAll(nested, 0o755)
	os.WriteFile(filepath.Join(nested, ".plan.yaml"), []byte("plansDir: from-cwd\n"), 0o644)
	t.Chdir(nested)
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(c.Plans.Dir) != "from-cwd" {
		t.Errorf("plansDir = %q, want the working directory's config to win over a parent's", c.Plans.Dir)
	}
}

func TestLoadStopsSearchingAboveHomeDir(t *testing.T) {
	t.Setenv(EnvConfig, "")
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	os.WriteFile(filepath.Join(root, "plan.config.yaml"), []byte("plansDir: above-home\n"), 0o644)
	nested := filepath.Join(home, "a")
	os.MkdirAll(nested, 0o755)
	t.Chdir(nested)
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Source != "" || c.Plans.Dir != DefaultPlansDir {
		t.Errorf("source = %q plansDir = %q, want a config above the home dir to be ignored", c.Source, c.Plans.Dir)
	}
}

func TestLoadFindsDefaultConfigInHomeDir(t *testing.T) {
	t.Setenv(EnvConfig, "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.WriteFile(filepath.Join(home, "plan.config.yaml"), []byte("plansDir: from-home\n"), 0o644)
	nested := filepath.Join(home, "a")
	os.MkdirAll(nested, 0o755)
	t.Chdir(nested)
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(c.Plans.Dir) != "from-home" {
		t.Errorf("plansDir = %q, want the home dir itself to be searched", c.Plans.Dir)
	}
}

func TestLoadOutsideHomeSearchesOnlyWorkingDir(t *testing.T) {
	t.Setenv(EnvConfig, "")
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	os.WriteFile(filepath.Join(root, "plan.config.yaml"), []byte("plansDir: outside-home\n"), 0o644)
	nested := filepath.Join(root, "elsewhere", "a")
	os.MkdirAll(nested, 0o755)
	t.Chdir(nested)
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Source != "" {
		t.Errorf("source = %q, want no parent search outside the home dir", c.Source)
	}
}

func TestLoadSkipsUnreadableDefaultConfig(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read any file")
	}
	t.Setenv(EnvConfig, "")
	root := t.TempDir()
	t.Setenv("HOME", root)
	os.WriteFile(filepath.Join(root, "plan.config.yaml"), []byte("plansDir: from-parent\n"), 0o644)
	nested := filepath.Join(root, "a")
	os.MkdirAll(nested, 0o755)
	os.WriteFile(filepath.Join(nested, "plan.config.yaml"), []byte("plansDir: unreadable\n"), 0o000)
	t.Chdir(nested)
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(c.Plans.Dir) != "from-parent" {
		t.Errorf("plansDir = %q, want an unreadable config to be skipped", c.Plans.Dir)
	}
}

func TestLoadResolvesRelativePathsAgainstConfigDir(t *testing.T) {
	t.Setenv(EnvConfig, "")
	root := t.TempDir()
	t.Setenv("HOME", root)
	os.WriteFile(filepath.Join(root, "plan.config.yaml"), []byte(`
plansDir: plans
storage:
  - {name: file, kind: file, options: {dir: data/plans}}
`), 0o644)
	nested := filepath.Join(root, "a", "b")
	os.MkdirAll(nested, 0o755)
	t.Chdir(nested)
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Dir(c.Source)
	if c.Plans.Dir != filepath.Join(configDir, "plans") {
		t.Errorf("plansDir = %q, want it resolved against %s", c.Plans.Dir, configDir)
	}
	if got := c.Storage[0].Options["dir"]; got != filepath.Join(configDir, "data", "plans") {
		t.Errorf("file store dir = %q, want it resolved against %s", got, configDir)
	}
}

func TestLoadLeavesAbsolutePathsAndDefaultsAlone(t *testing.T) {
	t.Setenv(EnvConfig, "")
	root := t.TempDir()
	t.Setenv("HOME", root)
	abs := filepath.Join(root, "elsewhere")
	path := filepath.Join(root, "plan.config.yaml")
	os.WriteFile(path, []byte("plansDir: "+abs+"\n"), 0o644)
	t.Chdir(root)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Plans.Dir != abs {
		t.Errorf("plansDir = %q, want absolute path kept as %s", c.Plans.Dir, abs)
	}
	os.Remove(path)
	c, err = Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Plans.Dir != DefaultPlansDir {
		t.Errorf("plansDir = %q, want default %s relative to the working directory", c.Plans.Dir, DefaultPlansDir)
	}
}

func TestPerKindConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mind-palace.config.yaml")
	os.WriteFile(path, []byte(`
plans:
  dir: p
stories:
  dir: s
  types:
    - {name: bugs, label: Bug, synonyms: [defect]}
`), 0o644)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Plans.Dir != filepath.Join(dir, "p") || c.Stories.Dir != filepath.Join(dir, "s") {
		t.Errorf("dirs: %q %q", c.Plans.Dir, c.Stories.Dir)
	}
	if len(c.Stories.Types) != 1 || len(c.Plans.Types) != 3 {
		t.Errorf("types: %+v %+v", c.Stories.Types, c.Plans.Types)
	}
	if got, ok := c.Stories.NormalizeType("defect"); !ok || got != "bugs" {
		t.Errorf("story type synonym: %q %v", got, ok)
	}
	if got, ok := Default().Stories.NormalizeType("refactor"); !ok || got != "impr" {
		t.Errorf("default story synonym: %q %v", got, ok)
	}
	if c.Kind("story") != &c.Stories || c.Kind("plans") != &c.Plans || c.Kind("nope") != nil {
		t.Error("Kind lookup")
	}
	if d := c.Dirs(); d["plan"] != c.Plans.Dir || d["story"] != c.Stories.Dir {
		t.Errorf("dirs map: %v", d)
	}
	// Legacy env var still selects the file.
	t.Setenv(EnvConfig, "")
	t.Setenv(LegacyEnvConfig, path)
	if c, err := Load(""); err != nil || c.Source != path {
		t.Errorf("legacy env: %v %q", err, c.Source)
	}
}

func TestFileStoreCannotBeDisabled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.config.yaml")
	for name, storage := range map[string]string{
		"file disabled next to another store": `
storage:
  - {name: file, kind: file, enabled: false}
  - {name: db, kind: sqlite, options: {dsn: ":memory:"}}
`,
		"file omitted": `
storage:
  - {name: db, kind: sqlite, options: {dsn: ":memory:"}}
`,
		"file disabled alone": `
storage:
  - {name: file, kind: file, enabled: false}
`,
	} {
		os.WriteFile(path, []byte(storage), 0o644)
		_, err := Load(path)
		if err == nil || !strings.Contains(err.Error(), "file store") {
			t.Errorf("%s: want file-store error, got %v", name, err)
		}
	}
}

func TestValidateAllowNoFileStore(t *testing.T) {
	cfg := Default()
	cfg.Storage = []StorageDef{{Name: "db", Kind: "sqlite", Options: map[string]string{"dsn": ":memory:"}}}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "kind file") {
		t.Errorf("zero-option Validate without a file store: err = %v, want the file-store rule", err)
	}
	if err := Validate(cfg); err == nil {
		t.Error("package-level Validate without options must keep the file-store rule")
	}
	if err := cfg.Validate(AllowNoFileStore); err != nil {
		t.Errorf("Validate(AllowNoFileStore) = %v", err)
	}
	if err := Validate(cfg, AllowNoFileStore); err != nil {
		t.Errorf("Validate(cfg, AllowNoFileStore) = %v", err)
	}
	// A disabled file store counts as none.
	off := false
	cfg.Storage = append(cfg.Storage, StorageDef{Name: "file", Kind: "file", Enabled: &off})
	if err := cfg.Validate(); err == nil {
		t.Error("disabled file store must still fail without the option")
	}
	if err := cfg.Validate(AllowNoFileStore); err != nil {
		t.Errorf("disabled file store with option: %v", err)
	}
	// The option waives only the file-store rule.
	cfg.Storage = []StorageDef{{Name: "", Kind: "sqlite"}}
	if err := cfg.Validate(AllowNoFileStore); err == nil || !strings.Contains(err.Error(), "name and a kind") {
		t.Errorf("nameless storage with option: err = %v", err)
	}
	// The default configuration still validates both ways.
	if err := Default().Validate(AllowNoFileStore); err != nil {
		t.Errorf("default with option: %v", err)
	}
}

func TestRemoteProjectKey(t *testing.T) {
	if Default().Remote.Project != "" {
		t.Error("the default config names a remote project")
	}
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "mind-palace.config.yaml")
	os.WriteFile(yamlPath, []byte("remote:\n  project: ' team__thing '\n"), 0o644)
	c, err := Load(yamlPath)
	if err != nil {
		t.Fatal(err)
	}
	if c.Remote.Project != "team__thing" {
		t.Errorf("remote.project from YAML = %q", c.Remote.Project)
	}
	jsonPath := filepath.Join(dir, "mind-palace.config.json")
	os.WriteFile(jsonPath, []byte(`{"remote": {"project": "owner__repo"}}`), 0o644)
	if c, err = Load(jsonPath); err != nil || c.Remote.Project != "owner__repo" {
		t.Errorf("remote.project from JSON = %q, %v", c.Remote.Project, err)
	}
	// Other remote keys are not known.
	os.WriteFile(yamlPath, []byte("remote:\n  url: https://clued.example\n"), 0o644)
	if _, err := Load(yamlPath); err == nil {
		t.Error("an unknown remote key must be rejected")
	}
}

func TestCodeSyncKey(t *testing.T) {
	if Default().CodeSync != nil || !Default().CodeSyncAllowed() {
		t.Error("the default config opts out of code sync")
	}
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "mind-palace.config.yaml")
	for _, tc := range []struct {
		body    string
		allowed bool
		set     bool
	}{
		{"codeSync: false\n", false, true},
		{"codeSync: true\n", true, true},
		{"remote:\n  project: owner__repo\n", true, false},
	} {
		os.WriteFile(yamlPath, []byte(tc.body), 0o644)
		c, err := Load(yamlPath)
		if err != nil {
			t.Fatalf("%q: %v", tc.body, err)
		}
		if c.CodeSyncAllowed() != tc.allowed || (c.CodeSync != nil) != tc.set {
			t.Errorf("%q: allowed %v, set %v; want %v, %v", tc.body, c.CodeSyncAllowed(), c.CodeSync != nil, tc.allowed, tc.set)
		}
	}
	jsonPath := filepath.Join(dir, "mind-palace.config.json")
	os.WriteFile(jsonPath, []byte(`{"codeSync": false}`), 0o644)
	if c, err := Load(jsonPath); err != nil || c.CodeSyncAllowed() {
		t.Errorf("codeSync false from JSON: allowed %v, %v", c != nil && c.CodeSyncAllowed(), err)
	}
	// A value that is not a boolean is rejected.
	os.WriteFile(yamlPath, []byte("codeSync: sometimes\n"), 0o644)
	if _, err := Load(yamlPath); err == nil {
		t.Error("a non-boolean codeSync must be rejected")
	}
	// Overlaying a file without the key keeps an earlier opt-out.
	c := Default()
	if err := c.Overlay([]byte("codeSync: false\n"), ".yaml"); err != nil {
		t.Fatal(err)
	}
	if err := c.Overlay([]byte("server:\n  addr: 127.0.0.1:9\n"), ".yaml"); err != nil {
		t.Fatal(err)
	}
	if c.CodeSyncAllowed() {
		t.Error("a later overlay without codeSync cleared the opt-out")
	}
}
