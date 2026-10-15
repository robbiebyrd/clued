package session

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

var configEnvKeys = []string{
	"CLUED_MONGO_URL", "CLUED_DB_NAME", "CLUED_PORT", "CLUED_PROJECTS_DIR",
	"CLUED_FILE_HISTORY_DIR", "CLUED_CLAUDE_APP_CONFIG_PATH", "CLUED_WAL_PATH",
	"CLUED_SERVER_URL", "CLUED_SERVER_TOKEN", "CLUED_SERVER_CA_FILE", "CLUED_SERVER_INSECURE",
	"CLUED_SERVER_KEY", "CLUED_CAPTURE_CONTENT",
}

// cleanEnv unsets every CLUED_* variable for the test and restores them after.
func cleanEnv(t *testing.T) {
	t.Helper()
	for _, k := range configEnvKeys {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

func writeConfig(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func home(t *testing.T) string {
	t.Helper()
	h, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestLoadConfigDefaults(t *testing.T) {
	cleanEnv(t)
	cfg := LoadConfig(filepath.Join(t.TempDir(), "nonexistent.json"))
	if cfg.Port != 8085 || cfg.DBName != "claude_sessions" {
		t.Fatalf("port/db = %d/%s", cfg.Port, cfg.DBName)
	}
	if cfg.MongoURL != "mongodb://localhost:27018" {
		t.Fatalf("MongoURL = %s", cfg.MongoURL)
	}
	if cfg.DisabledEnrichers == nil || len(cfg.DisabledEnrichers) != 0 {
		t.Fatalf("DisabledEnrichers = %#v, want empty non-nil", cfg.DisabledEnrichers)
	}
	if cfg.EnabledEnrichers == nil || len(cfg.EnabledEnrichers) != 0 {
		t.Fatalf("EnabledEnrichers = %#v, want empty non-nil", cfg.EnabledEnrichers)
	}
	h := home(t)
	if cfg.ProjectsDir != filepath.Join(h, ".claude", "projects") {
		t.Fatalf("ProjectsDir = %s", cfg.ProjectsDir)
	}
}

func TestLoadConfigFileOverridesDefaults(t *testing.T) {
	cleanEnv(t)
	p := writeConfig(t, t.TempDir(), "config.json", `{"port": 9999, "dbName": "my_db", "enabledEnrichers": ["privacy-redact"]}`)
	cfg := LoadConfig(p)
	if cfg.Port != 9999 || cfg.DBName != "my_db" {
		t.Fatalf("port/db = %d/%s", cfg.Port, cfg.DBName)
	}
	if len(cfg.EnabledEnrichers) != 1 || cfg.EnabledEnrichers[0] != "privacy-redact" {
		t.Fatalf("EnabledEnrichers = %#v", cfg.EnabledEnrichers)
	}
	if cfg.MongoURL != "mongodb://localhost:27018" {
		t.Fatalf("default MongoURL not preserved: %s", cfg.MongoURL)
	}
}

func TestLoadConfigWrongFieldTypeWarnsAndMeansDefaults(t *testing.T) {
	cleanEnv(t)
	logs := captureLog(t)
	p := writeConfig(t, t.TempDir(), "config.json", `{"port": "9999", "dbName": "my_db"}`)
	cfg := LoadConfig(p)
	if cfg.Port != 8085 || cfg.DBName != "claude_sessions" {
		t.Fatalf("port/db = %d/%s, want defaults", cfg.Port, cfg.DBName)
	}
	if got := logs.String(); !strings.Contains(got, p) || !strings.Contains(got, "port") {
		t.Errorf("log = %q, want a warning naming %s and the bad field", got, p)
	}
}

func TestLoadConfigUnparsableFileMeansDefaults(t *testing.T) {
	cleanEnv(t)
	logs := captureLog(t)
	p := writeConfig(t, t.TempDir(), "config.json", `{"port": 9999, "dbName": not json`)
	cfg := LoadConfig(p)
	if !strings.Contains(logs.String(), p) {
		t.Errorf("log = %q, want a warning naming %s", logs.String(), p)
	}
	if cfg.Port != 8085 || cfg.DBName != "claude_sessions" {
		t.Fatalf("port/db = %d/%s, want defaults", cfg.Port, cfg.DBName)
	}
}

func TestLoadConfigEnvOverridesFile(t *testing.T) {
	cleanEnv(t)
	p := writeConfig(t, t.TempDir(), "config.json", `{"port": 9999}`)
	t.Setenv("CLUED_PORT", "7777")
	t.Setenv("CLUED_DB_NAME", "env_db")
	cfg := LoadConfig(p)
	if cfg.Port != 7777 || cfg.DBName != "env_db" {
		t.Fatalf("port/db = %d/%s", cfg.Port, cfg.DBName)
	}
}

func TestLoadConfigEnvOverridesEachPath(t *testing.T) {
	cleanEnv(t)
	t.Setenv("CLUED_MONGO_URL", "mongodb://elsewhere:1")
	t.Setenv("CLUED_PROJECTS_DIR", "/custom/projects")
	t.Setenv("CLUED_FILE_HISTORY_DIR", "/custom/file-history")
	t.Setenv("CLUED_CLAUDE_APP_CONFIG_PATH", "/custom/path/config.json")
	cfg := LoadConfig(filepath.Join(t.TempDir(), "nonexistent.json"))
	if cfg.MongoURL != "mongodb://elsewhere:1" || cfg.ProjectsDir != "/custom/projects" ||
		cfg.FileHistoryDir != "/custom/file-history" || cfg.ClaudeAppConfigPath != "/custom/path/config.json" {
		t.Fatalf("env overrides not applied: %+v", cfg)
	}
}

func TestLoadConfigExpandsHome(t *testing.T) {
	cleanEnv(t)
	p := writeConfig(t, t.TempDir(), "config.json", `{
		"projectsDir": "~/.claude/projects",
		"mongoUrl": "~/mongo/local.db",
		"claudeAppConfigPath": "~/Library/Application Support/Claude/config.json",
		"fileHistoryDir": "~/.claude/file-history"
	}`)
	cfg := LoadConfig(p)
	h := home(t)
	for name, v := range map[string]string{
		"ProjectsDir": cfg.ProjectsDir, "MongoURL": cfg.MongoURL,
		"ClaudeAppConfigPath": cfg.ClaudeAppConfigPath, "FileHistoryDir": cfg.FileHistoryDir,
	} {
		if !strings.HasPrefix(v, h) || strings.Contains(v, "~") {
			t.Errorf("%s = %s, want ~ expanded to %s", name, v, h)
		}
	}
}

func TestLoadConfigDefaultsContainExpectedNames(t *testing.T) {
	cleanEnv(t)
	cfg := LoadConfig(filepath.Join(t.TempDir(), "nonexistent.json"))
	if !strings.Contains(cfg.ClaudeAppConfigPath, "Claude") || !strings.HasSuffix(cfg.ClaudeAppConfigPath, "config.json") {
		t.Fatalf("ClaudeAppConfigPath = %s", cfg.ClaudeAppConfigPath)
	}
	if !strings.Contains(cfg.FileHistoryDir, "file-history") || strings.Contains(cfg.FileHistoryDir, "~") {
		t.Fatalf("FileHistoryDir = %s", cfg.FileHistoryDir)
	}
}

func TestLoadConfigWalPathNextToConfigFile(t *testing.T) {
	cleanEnv(t)
	dir := t.TempDir()
	cfg := LoadConfig(filepath.Join(dir, "subdir", "config.json"))
	if want := filepath.Join(dir, "subdir", "events.wal"); cfg.WalPath != want {
		t.Fatalf("WalPath = %s, want %s", cfg.WalPath, want)
	}
}

func TestLoadConfigWalPathEnvOverride(t *testing.T) {
	cleanEnv(t)
	t.Setenv("CLUED_WAL_PATH", "/custom/events.wal")
	cfg := LoadConfig(filepath.Join(t.TempDir(), "config.json"))
	if cfg.WalPath != "/custom/events.wal" {
		t.Fatalf("WalPath = %s", cfg.WalPath)
	}
}

func TestDefaultConfigPath(t *testing.T) {
	want := filepath.Join(home(t), ".claude", "plugins", "data", "clued", "config.json")
	if got := DefaultConfigPath(); got != want {
		t.Fatalf("DefaultConfigPath = %s, want %s", got, want)
	}
}

func TestLoadConfigServerFieldsDefaultEmptyAndNotRemote(t *testing.T) {
	cleanEnv(t)
	cfg := LoadConfig(filepath.Join(t.TempDir(), "nonexistent.json"))
	if cfg.ServerURL != "" || cfg.ServerToken != "" || cfg.ServerCAFile != "" || cfg.ServerInsecure {
		t.Fatalf("server fields not empty by default: %+v", cfg)
	}
	if cfg.Remote() {
		t.Fatal("Remote() = true with no serverUrl")
	}
}

func TestLoadConfigServerFieldsFromFile(t *testing.T) {
	cleanEnv(t)
	p := writeConfig(t, t.TempDir(), "config.json", `{
		"serverUrl": "https://clued.example.com",
		"serverToken": "secret-token",
		"serverCaFile": "/etc/clued/ca.pem",
		"serverInsecure": true
	}`)
	cfg := LoadConfig(p)
	if cfg.ServerURL != "https://clued.example.com" || cfg.ServerToken != "secret-token" ||
		cfg.ServerCAFile != "/etc/clued/ca.pem" || !cfg.ServerInsecure {
		t.Fatalf("server fields not read from file: %+v", cfg)
	}
	if !cfg.Remote() {
		t.Fatal("Remote() = false with serverUrl set")
	}
	if cfg.MongoURL != "mongodb://localhost:27018" || cfg.Port != 8085 {
		t.Fatalf("existing defaults changed: %+v", cfg)
	}
}

func TestLoadConfigServerEnvOverridesFile(t *testing.T) {
	cleanEnv(t)
	p := writeConfig(t, t.TempDir(), "config.json", `{
		"serverUrl": "https://file.example.com",
		"serverToken": "file-token",
		"serverCaFile": "/file/ca.pem",
		"serverInsecure": false
	}`)
	t.Setenv("CLUED_SERVER_URL", "https://env.example.com")
	t.Setenv("CLUED_SERVER_TOKEN", "env-token")
	t.Setenv("CLUED_SERVER_CA_FILE", "/env/ca.pem")
	t.Setenv("CLUED_SERVER_INSECURE", "1")
	cfg := LoadConfig(p)
	if cfg.ServerURL != "https://env.example.com" || cfg.ServerToken != "env-token" ||
		cfg.ServerCAFile != "/env/ca.pem" || !cfg.ServerInsecure {
		t.Fatalf("env overrides not applied: %+v", cfg)
	}
}

func TestLoadConfigServerEnvOnly(t *testing.T) {
	cleanEnv(t)
	t.Setenv("CLUED_SERVER_URL", "https://env.example.com")
	t.Setenv("CLUED_SERVER_TOKEN", "env-token")
	cfg := LoadConfig(filepath.Join(t.TempDir(), "nonexistent.json"))
	if cfg.ServerURL != "https://env.example.com" || cfg.ServerToken != "env-token" {
		t.Fatalf("env values not applied without a file: %+v", cfg)
	}
	if !cfg.Remote() {
		t.Fatal("Remote() = false with CLUED_SERVER_URL set")
	}
}

func TestLoadConfigServerInsecureEnvValues(t *testing.T) {
	cases := []struct {
		env  string
		file bool
		want bool
	}{
		{env: "1", want: true},
		{env: "true", want: true},
		{env: "TRUE", want: true},
		{env: "0", file: true, want: false},
		{env: "false", file: true, want: false},
		{env: "", file: true, want: true},
		{env: "", file: false, want: false},
		{env: "banana", file: true, want: true},
		{env: "banana", file: false, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.env+"/file="+strconv.FormatBool(tc.file), func(t *testing.T) {
			cleanEnv(t)
			p := writeConfig(t, t.TempDir(), "config.json", `{"serverInsecure": `+strconv.FormatBool(tc.file)+`}`)
			if tc.env != "" {
				t.Setenv("CLUED_SERVER_INSECURE", tc.env)
			}
			if cfg := LoadConfig(p); cfg.ServerInsecure != tc.want {
				t.Fatalf("ServerInsecure = %v, want %v", cfg.ServerInsecure, tc.want)
			}
		})
	}
}

func TestLoadConfigExpandsHomeInServerCAFile(t *testing.T) {
	cleanEnv(t)
	p := writeConfig(t, t.TempDir(), "config.json", `{"serverCaFile": "~/.clued/ca.pem"}`)
	cfg := LoadConfig(p)
	if want := filepath.Join(home(t), ".clued", "ca.pem"); cfg.ServerCAFile != want {
		t.Fatalf("ServerCAFile = %s, want %s", cfg.ServerCAFile, want)
	}
	t.Setenv("CLUED_SERVER_CA_FILE", "~/env/ca.pem")
	cfg = LoadConfig(p)
	if want := filepath.Join(home(t), "env", "ca.pem"); cfg.ServerCAFile != want {
		t.Fatalf("ServerCAFile from env = %s, want %s", cfg.ServerCAFile, want)
	}
}

// TestLoadConfigOldKeysOnlyLoadsIdentically is the compatibility gate: a
// config.json written before the server fields existed must load exactly as
// before, and Remote() must be false.
func TestLoadConfigOldKeysOnlyLoadsIdentically(t *testing.T) {
	cleanEnv(t)
	dir := t.TempDir()
	p := writeConfig(t, dir, "config.json", `{
		"mongoUrl": "mongodb://db.example.com:27017",
		"dbName": "my_db",
		"port": 9001,
		"projectsDir": "/p/projects",
		"fileHistoryDir": "/p/file-history",
		"disabledEnrichers": ["a"],
		"enabledEnrichers": ["b"],
		"claudeAppConfigPath": "/p/claude/config.json",
		"devRepoPath": "/p/clued"
	}`)
	cfg := LoadConfig(p)
	want := Config{
		MongoURL:            "mongodb://db.example.com:27017",
		DBName:              "my_db",
		Port:                9001,
		ProjectsDir:         "/p/projects",
		FileHistoryDir:      "/p/file-history",
		DisabledEnrichers:   []string{"a"},
		EnabledEnrichers:    []string{"b"},
		ClaudeAppConfigPath: "/p/claude/config.json",
		WalPath:             filepath.Join(dir, "events.wal"),
		TokenCache:          filepath.Join(dir, "server-token.json"),
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("LoadConfig = %+v\nwant        %+v", cfg, want)
	}
	if cfg.Remote() {
		t.Fatal("Remote() = true for a config with only the old keys")
	}
}

func TestConfigRemote(t *testing.T) {
	if (Config{}).Remote() {
		t.Fatal("Remote() = true on the zero Config")
	}
	if !(Config{ServerURL: "https://x"}).Remote() {
		t.Fatal("Remote() = false with ServerURL set")
	}
}

func TestLoadConfigCaptureContent(t *testing.T) {
	cleanEnv(t)
	dir := t.TempDir()
	if cfg := LoadConfig(writeConfig(t, dir, "none.json", `{}`)); cfg.CaptureContent != nil || !cfg.CapturesContent() {
		t.Errorf("unset captureContent: %v, captures %v; want nil and true", cfg.CaptureContent, cfg.CapturesContent())
	}
	off := writeConfig(t, dir, "off.json", `{"captureContent": false}`)
	if cfg := LoadConfig(off); cfg.CaptureContent == nil || cfg.CapturesContent() {
		t.Errorf("captureContent false: captures %v, want false", cfg.CapturesContent())
	}
	if cfg := LoadConfig(writeConfig(t, dir, "on.json", `{"captureContent": true}`)); !cfg.CapturesContent() {
		t.Error("captureContent true: captures false")
	}
	t.Setenv("CLUED_CAPTURE_CONTENT", "true")
	if cfg := LoadConfig(off); !cfg.CapturesContent() {
		t.Error("CLUED_CAPTURE_CONTENT=true did not override the file")
	}
}
