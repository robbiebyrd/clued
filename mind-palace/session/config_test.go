package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var configEnvKeys = []string{
	"CLUED_MONGO_URL", "CLUED_DB_NAME", "CLUED_PORT", "CLUED_PROJECTS_DIR",
	"CLUED_FILE_HISTORY_DIR", "CLUED_CLAUDE_APP_CONFIG_PATH", "CLUED_WAL_PATH",
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
	h := home(t)
	if cfg.ProjectsDir != filepath.Join(h, ".claude", "projects") {
		t.Fatalf("ProjectsDir = %s", cfg.ProjectsDir)
	}
}

func TestLoadConfigFileOverridesDefaults(t *testing.T) {
	cleanEnv(t)
	p := writeConfig(t, t.TempDir(), "config.json", `{"port": 9999, "dbName": "my_db"}`)
	cfg := LoadConfig(p)
	if cfg.Port != 9999 || cfg.DBName != "my_db" {
		t.Fatalf("port/db = %d/%s", cfg.Port, cfg.DBName)
	}
	if cfg.MongoURL != "mongodb://localhost:27018" {
		t.Fatalf("default MongoURL not preserved: %s", cfg.MongoURL)
	}
}

func TestLoadConfigUnparsableFileMeansDefaults(t *testing.T) {
	cleanEnv(t)
	p := writeConfig(t, t.TempDir(), "config.json", `{"port": 9999, "dbName": not json`)
	cfg := LoadConfig(p)
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
