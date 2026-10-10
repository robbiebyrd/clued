package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config is the capture configuration. The JSON names match the config file
// the plugin has always read.
type Config struct {
	MongoURL            string   `json:"mongoUrl"`
	DBName              string   `json:"dbName"`
	Port                int      `json:"port"`
	ProjectsDir         string   `json:"projectsDir"`
	FileHistoryDir      string   `json:"fileHistoryDir"`
	DisabledEnrichers   []string `json:"disabledEnrichers"`
	ClaudeAppConfigPath string   `json:"claudeAppConfigPath"`
	WalPath             string   `json:"walPath"`
}

func userHome() string {
	h, _ := os.UserHomeDir()
	return h
}

// DefaultConfigPath is ~/.claude/plugins/data/clued/config.json.
func DefaultConfigPath() string {
	return filepath.Join(userHome(), ".claude", "plugins", "data", "clued", "config.json")
}

func defaultConfig() Config {
	h := userHome()
	return Config{
		MongoURL:            "mongodb://localhost:27018",
		DBName:              "claude_sessions",
		Port:                8085,
		ProjectsDir:         filepath.Join(h, ".claude", "projects"),
		FileHistoryDir:      filepath.Join(h, ".claude", "file-history"),
		DisabledEnrichers:   []string{},
		ClaudeAppConfigPath: filepath.Join(h, "Library", "Application Support", "Claude", "config.json"),
	}
}

func expandHome(val string) string {
	if rest, ok := strings.CutPrefix(val, "~/"); ok {
		return filepath.Join(userHome(), rest)
	}
	return val
}

// LoadConfig builds the configuration from defaults, then the JSON file at
// path, then the CLUED_* environment variables, and expands a leading ~/. It
// never fails: an absent or unparsable file means defaults. WalPath defaults
// to events.wal next to the config file unless CLUED_WAL_PATH is set.
func LoadConfig(path string) Config {
	cfg := defaultConfig()
	if raw, err := os.ReadFile(path); err == nil {
		fromFile := cfg
		if json.Unmarshal(raw, &fromFile) == nil {
			cfg = fromFile
		}
	}
	if cfg.DisabledEnrichers == nil {
		cfg.DisabledEnrichers = []string{}
	}

	setFromEnv(&cfg.MongoURL, "CLUED_MONGO_URL")
	setFromEnv(&cfg.DBName, "CLUED_DB_NAME")
	setFromEnv(&cfg.ProjectsDir, "CLUED_PROJECTS_DIR")
	setFromEnv(&cfg.FileHistoryDir, "CLUED_FILE_HISTORY_DIR")
	setFromEnv(&cfg.ClaudeAppConfigPath, "CLUED_CLAUDE_APP_CONFIG_PATH")
	if v := os.Getenv("CLUED_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.Port = port
		}
	}

	cfg.ProjectsDir = expandHome(cfg.ProjectsDir)
	cfg.FileHistoryDir = expandHome(cfg.FileHistoryDir)
	cfg.MongoURL = expandHome(cfg.MongoURL)
	cfg.ClaudeAppConfigPath = expandHome(cfg.ClaudeAppConfigPath)

	cfg.WalPath = filepath.Join(filepath.Dir(path), "events.wal")
	setFromEnv(&cfg.WalPath, "CLUED_WAL_PATH")
	return cfg
}

func setFromEnv(dst *string, name string) {
	if v := os.Getenv(name); v != "" {
		*dst = v
	}
}
