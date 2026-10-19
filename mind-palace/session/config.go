package session

import (
	"encoding/json"
	"log"
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
	EnabledEnrichers    []string `json:"enabledEnrichers"`
	ClaudeAppConfigPath string   `json:"claudeAppConfigPath"`
	WalPath             string   `json:"walPath"`

	// ServerURL switches the plugin into remote mode: when set, sessions,
	// plans and stories are sent to a clued server over HTTPS instead of a
	// local MongoDB (see Remote). ServerToken is the bearer token the server
	// minted; ServerCAFile is an optional PEM bundle that is trusted in
	// addition to the system roots (a private CA); ServerInsecure allows a
	// plain http:// ServerURL on a host that is not loopback.
	// StatusLine turns on the status-line sampler: register-hooks then
	// installs `mind-palace hook status` as Claude Code's status line,
	// which posts LimitsSampled events (CLUED_STATUS_LINE).
	StatusLine bool `json:"statusLine"`

	ServerURL      string `json:"serverUrl"`
	ServerToken    string `json:"serverToken"`
	ServerCAFile   string `json:"serverCaFile"`
	ServerInsecure bool   `json:"serverInsecure"`
	// ServerKey is an environment key (CLUED_SERVER_KEY): with no
	// ServerToken, the client trades it at the server's token exchange for
	// a short-lived token, cached in TokenCache and renewed before it
	// expires. A Claude Code cloud environment keeps it in its settings.
	ServerKey string `json:"serverKey,omitempty"`
	// TokenCache is where an exchanged token is kept (server-token.json
	// next to the config file); not read from the file.
	TokenCache string `json:"-"`

	// PrimeOnStart, when false, stops `hook prime` from adding the prime
	// report to new, resumed, cleared and compacted sessions; unset means
	// on. CLUED_PRIME_ON_START (a boolean) overrides it.
	PrimeOnStart *bool `json:"primeOnStart,omitempty"`

	// ModelPrices adds or overrides model prices (US dollars per million
	// input and output tokens, by model id prefix) for the usage cost
	// estimates; see DefaultModelPrices.
	ModelPrices map[string]ModelPrice `json:"modelPrices,omitempty"`

	// RedactAtCapture masks credentials and <private>…</private> spans in
	// everything captured before it is written (session/redact); unset
	// means off, so configurations written before it keep their behaviour,
	// and /clued-setup writes it on. CLUED_REDACT_AT_CAPTURE overrides it.
	// RedactPatterns are extra regular expressions masked with it.
	RedactAtCapture *bool    `json:"redactAtCapture,omitempty"`
	RedactPatterns  []string `json:"redactPatterns,omitempty"`
	// ExcludePaths are globs over a session's cwd and project path (a
	// directory and everything below it); such sessions are never stored.
	ExcludePaths []string `json:"excludePaths,omitempty"`
	// CaptureContent, when false, makes capture metadata-only: transcript
	// lines, subagent lines and artifact files are not sent and hook events
	// lose their content (session.WithoutContent) before they leave the
	// machine; sessions, hook event names, tools, file paths, timestamps,
	// models and limit samples are still captured. Unset means true. In
	// remote mode it is the server's hint: `session config-pull` copies the
	// server's captureContent here (and removes it when the server sends
	// none), so a server that captures metadata only (a clued cloud
	// workspace on zero retention) is not sent content it would drop. The
	// server enforces its policy either way. CLUED_CAPTURE_CONTENT (a
	// boolean) overrides it.
	CaptureContent *bool `json:"captureContent,omitempty"`

	// CodeSync turns on code collection (session/codesync): in remote mode,
	// the files of each worktree a session works in are mirrored to the
	// server. Unset means off; CLUED_CODE_SYNC (a boolean) overrides it, and
	// a repository's `codeSync: false` in mind-palace.config.yaml opts that
	// repository out. CodeSyncMaxFileBytes caps the content uploaded per
	// file (CLUED_CODE_SYNC_MAX_FILE_BYTES; DefaultCodeSyncMaxFileBytes when
	// unset or not positive), and CodeSyncExclude are extra globs, relative
	// to the worktree root, whose files are never uploaded. A server or
	// clued cloud may set all three through `session config-pull`.
	CodeSync             *bool    `json:"codeSync,omitempty"`
	CodeSyncMaxFileBytes int64    `json:"codeSyncMaxFileBytes,omitempty"`
	CodeSyncExclude      []string `json:"codeSyncExclude,omitempty"`

	// RetentionDays, when positive, has MongoDB delete captured documents
	// that many days after they were stored (a TTL index on created_at,
	// last_seen for sessions).
	RetentionDays int `json:"retentionDays,omitempty"`

	// OTelEndpoint, when set, is the OTLP/HTTP collector (e.g.
	// http://localhost:4318) each session's trace is sent to when it ends
	// (session/otel); CLUED_OTEL_ENDPOINT overrides it. OTelHeaders are sent
	// with every request (an Authorization header); they are secrets and
	// never logged.
	OTelEndpoint string            `json:"otelEndpoint,omitempty"`
	OTelHeaders  map[string]string `json:"otelHeaders,omitempty"`

	// Memory configures native memory (session/memory); the key is
	// "memory".
	Memory MemoryConfig `json:"memory"`
}

// The memory engines: extract (free, deterministic, the default), claude
// (the user's own `claude -p`), hosted (a clued cloud's allowance, then
// the user's own key, then extract) and off (no memory runs).
const (
	MemoryEngineExtract = "extract"
	MemoryEngineClaude  = "claude"
	MemoryEngineHosted  = "hosted"
	MemoryEngineOff     = "off"
)

// MemoryEngines lists the engine names in the order /clued-setup offers
// them.
var MemoryEngines = []string{MemoryEngineExtract, MemoryEngineClaude, MemoryEngineHosted, MemoryEngineOff}

// MemoryConfig is the "memory" block of the config file. Engine is
// overridden by CLUED_MEMORY_ENGINE and BudgetTokens by
// CLUED_MEMORY_BUDGET; Exclude are project path globs (excludePaths
// syntax) whose sessions get no memory.
type MemoryConfig struct {
	Engine        string   `json:"engine,omitempty"`
	Model         string   `json:"model,omitempty"`
	MaxRunsPerDay int      `json:"maxRunsPerDay,omitempty"`
	MinPrompts    int      `json:"minPrompts,omitempty"`
	BudgetTokens  int      `json:"budgetTokens,omitempty"`
	Exclude       []string `json:"exclude,omitempty"`
}

// DefaultCodeSyncMaxFileBytes is the content cap per file of code sync
// (1 MiB); larger files are recorded by size only.
const DefaultCodeSyncMaxFileBytes int64 = 1 << 20

// Defaults of the memory block.
const (
	DefaultMemoryModel         = "haiku"
	DefaultMemoryMaxRunsPerDay = 20
	DefaultMemoryMinPrompts    = 2
	DefaultMemoryBudgetTokens  = 1500
)

// NoCaptureEnv is the variable every memory engine run sets
// (CLUED_NO_CAPTURE=1): the hooks of that `claude -p` capture, relay and
// prime nothing, so an engine's own run never becomes a session.
const NoCaptureEnv = "CLUED_NO_CAPTURE"

// NoCapture reports whether this process runs inside a memory engine run.
func NoCapture() bool { return os.Getenv(NoCaptureEnv) == "1" }

// MemoryOffEnv set to "off" in a session's environment (CLUED_MEMORY=off)
// keeps that session out of memory.
const MemoryOffEnv = "CLUED_MEMORY"

// RedactsAtCapture reports whether captured data is redacted before it is
// written.
func (c Config) RedactsAtCapture() bool { return c.RedactAtCapture != nil && *c.RedactAtCapture }

// CapturesContent reports whether capture keeps content (transcript lines,
// artifact files, the content of hook events), which is the default.
func (c Config) CapturesContent() bool { return c.CaptureContent == nil || *c.CaptureContent }

// CodeSyncEnabled reports whether code sync may run on this machine: it
// is opted in (CodeSync set and true), the plugin is in remote mode, and
// capture keeps content (captureContent false turns it off). A
// repository's own opt-out (config.Config.CodeSyncAllowed) is checked
// separately.
func (c Config) CodeSyncEnabled() bool {
	return c.CodeSync != nil && *c.CodeSync && c.Remote() && c.CapturesContent()
}

// PrimesOnStart reports whether sessions start with the prime report.
func (c Config) PrimesOnStart() bool { return c.PrimeOnStart == nil || *c.PrimeOnStart }

// Remote reports whether the plugin runs in remote mode, which is the case
// exactly when a server URL is configured.
func (c Config) Remote() bool {
	return c.ServerURL != ""
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
		EnabledEnrichers:    []string{},
		ClaudeAppConfigPath: filepath.Join(h, "Library", "Application Support", "Claude", "config.json"),
		Memory: MemoryConfig{
			Engine: MemoryEngineExtract, Model: DefaultMemoryModel, MaxRunsPerDay: DefaultMemoryMaxRunsPerDay,
			MinPrompts: DefaultMemoryMinPrompts, BudgetTokens: DefaultMemoryBudgetTokens,
		},
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
// never fails: an absent file means defaults, and an unparsable one means
// defaults plus a warning on the log. WalPath defaults
// to events.wal next to the config file unless CLUED_WAL_PATH is set.
func LoadConfig(path string) Config {
	cfg := defaultConfig()
	if raw, err := os.ReadFile(path); err == nil {
		fromFile := cfg
		if err := json.Unmarshal(raw, &fromFile); err != nil {
			log.Printf("clued: ignoring config file %s, using defaults: %v", path, err)
		} else {
			cfg = fromFile
		}
	}
	if cfg.DisabledEnrichers == nil {
		cfg.DisabledEnrichers = []string{}
	}
	if cfg.EnabledEnrichers == nil {
		cfg.EnabledEnrichers = []string{}
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
	if v := os.Getenv("CLUED_STATUS_LINE"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.StatusLine = b
		}
	}
	setFromEnv(&cfg.ServerURL, "CLUED_SERVER_URL")
	setFromEnv(&cfg.ServerToken, "CLUED_SERVER_TOKEN")
	setFromEnv(&cfg.ServerCAFile, "CLUED_SERVER_CA_FILE")
	setFromEnv(&cfg.ServerKey, "CLUED_SERVER_KEY")
	if v := os.Getenv("CLUED_SERVER_INSECURE"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.ServerInsecure = b
		}
	}
	if v := os.Getenv("CLUED_PRIME_ON_START"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.PrimeOnStart = &b
		}
	}
	if v := os.Getenv("CLUED_CAPTURE_CONTENT"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.CaptureContent = &b
		}
	}
	if v := os.Getenv("CLUED_CODE_SYNC"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.CodeSync = &b
		}
	}
	if v := os.Getenv("CLUED_CODE_SYNC_MAX_FILE_BYTES"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			cfg.CodeSyncMaxFileBytes = n
		}
	}
	if cfg.CodeSyncMaxFileBytes <= 0 {
		cfg.CodeSyncMaxFileBytes = DefaultCodeSyncMaxFileBytes
	}
	setFromEnv(&cfg.OTelEndpoint, "CLUED_OTEL_ENDPOINT")
	if v := os.Getenv("CLUED_REDACT_AT_CAPTURE"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.RedactAtCapture = &b
		}
	}

	setFromEnv(&cfg.Memory.Engine, "CLUED_MEMORY_ENGINE")
	if v := os.Getenv("CLUED_MEMORY_BUDGET"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Memory.BudgetTokens = n
		}
	}
	cfg.Memory = cfg.Memory.withDefaults()

	cfg.ProjectsDir = expandHome(cfg.ProjectsDir)
	cfg.FileHistoryDir = expandHome(cfg.FileHistoryDir)
	cfg.MongoURL = expandHome(cfg.MongoURL)
	cfg.ClaudeAppConfigPath = expandHome(cfg.ClaudeAppConfigPath)
	cfg.ServerCAFile = expandHome(cfg.ServerCAFile)

	cfg.WalPath = filepath.Join(filepath.Dir(path), "events.wal")
	cfg.TokenCache = filepath.Join(filepath.Dir(path), "server-token.json")
	setFromEnv(&cfg.WalPath, "CLUED_WAL_PATH")
	return cfg
}

func setFromEnv(dst *string, name string) {
	if v := os.Getenv(name); v != "" {
		*dst = v
	}
}

// withDefaults fills the fields a config file left empty or zero.
func (m MemoryConfig) withDefaults() MemoryConfig {
	if m.Engine == "" {
		m.Engine = MemoryEngineExtract
	}
	if m.Model == "" {
		m.Model = DefaultMemoryModel
	}
	if m.MaxRunsPerDay == 0 {
		m.MaxRunsPerDay = DefaultMemoryMaxRunsPerDay
	}
	if m.MinPrompts <= 0 {
		m.MinPrompts = DefaultMemoryMinPrompts
	}
	if m.BudgetTokens <= 0 {
		m.BudgetTokens = DefaultMemoryBudgetTokens
	}
	return m
}

// ValidPulledValue reports whether v, decoded from JSON, is a value
// config.json may hold under key, so `session config-pull` never writes a
// value that would make the file unparsable: captureContent and codeSync
// are booleans, codeSyncMaxFileBytes a positive whole number and
// codeSyncExclude a list of strings. Other keys are not checked here.
func ValidPulledValue(key string, v any) bool {
	switch key {
	case "captureContent", "codeSync":
		_, ok := v.(bool)
		return ok
	case "codeSyncMaxFileBytes":
		switch n := v.(type) {
		case float64:
			return n > 0 && n <= 1<<53 && n == float64(int64(n))
		case json.Number:
			i, err := n.Int64()
			return err == nil && i > 0
		}
		return false
	case "codeSyncExclude":
		list, ok := v.([]any)
		if !ok {
			return false
		}
		for _, item := range list {
			if _, ok := item.(string); !ok {
				return false
			}
		}
		return true
	}
	return true
}
