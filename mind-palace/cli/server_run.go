package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/remote"
	"github.com/robbiebyrd/clued/mind-palace/server"
	"github.com/robbiebyrd/clued/mind-palace/server/auth"
	"github.com/robbiebyrd/clued/mind-palace/server/device"
	"github.com/robbiebyrd/clued/mind-palace/server/projects"
	"github.com/robbiebyrd/clued/mind-palace/service"
	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
)

// Environment variables `server run` falls back to when a flag is not set.
const (
	envServerAddr        = "CLUED_SERVER_ADDR"
	envServerTLSCert     = "CLUED_SERVER_TLS_CERT"
	envServerTLSKey      = "CLUED_SERVER_TLS_KEY"
	envServerBehindProxy = "CLUED_SERVER_BEHIND_PROXY"
	defaultServerAddr    = "127.0.0.1:8443"
)

// serverRunOptions is everything `server run` resolved from its flags, the
// environment and the defaults; runCluedServer takes it so tests start the
// server in-process.
type serverRunOptions struct {
	// Addr is the listen address (--addr, CLUED_SERVER_ADDR, 127.0.0.1:8443).
	Addr string
	// CertFile and KeyFile terminate TLS when both are set.
	CertFile string
	KeyFile  string
	// BehindProxy allows plain HTTP off loopback.
	BehindProxy bool
	// Tokens is the token file (--tokens, CLUED_SERVER_TOKENS, the default).
	Tokens string
	// ConfigPath is the plan and story config (--config; config.Load also
	// honours MIND_PALACE_CONFIG when it is empty).
	ConfigPath string
	// SessionConfig is the session config.json (--session-config);
	// CLUED_MONGO_URL and CLUED_DB_NAME still apply through session.LoadConfig.
	SessionConfig string
	// Store is "" for the configured stores or "memory" for in-memory
	// session and document stores (the test hook).
	Store string
	// Version is reported by /health and whoami.
	Version string
	// Ready, when non-nil, receives the bound address once listening.
	Ready chan<- string
}

// serverRunHook, when set by a test, sees (and may adjust) the resolved
// options before the server starts.
var serverRunHook func(*serverRunOptions)

// newMemorySessionStore builds the session store for --store memory; tests
// replace it to observe or gate the store.
var newMemorySessionStore = func() session.Store { return memsession.New() }

// serverRunCmd is `server run`: the clued server, serving sessions, plans
// and stories over HTTPS behind bearer tokens.
func (a *App) serverRunCmd() *cobra.Command {
	var o serverRunOptions
	cmd := &cobra.Command{
		Use:   "run [--addr host:port] [--tls-cert file --tls-key file | --behind-proxy]",
		Short: "Run the clued server: sessions, plans and stories over HTTPS behind bearer tokens",
		Long: `Serves /health, /api/v1/whoami, the session transport under /api/v1/session,
the per-project store transport under /api/v1/projects, and, for the project
named by the X-Clued-Project header (or ?project=), the plan and story API
under /api/v1, the WebSocket at /ws and the MCP endpoint at /mcp; every route
but /health is behind the bearer tokens of the token file. TLS is terminated from
--tls-cert and --tls-key; plain HTTP is accepted only on a loopback address
or with --behind-proxy. Each flag falls back to its CLUED_SERVER_* variable.

Plans and stories use the storage of the mind-palace config (--config,
MIND_PALACE_CONFIG; a file store is not required), namespaced per project;
sessions use the MongoDB of the clued config (--session-config,
CLUED_MONGO_URL, CLUED_DB_NAME). Enrichment runs on the server.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o.Addr = firstNonEmpty(o.Addr, os.Getenv(envServerAddr), defaultServerAddr)
			o.CertFile = firstNonEmpty(o.CertFile, os.Getenv(envServerTLSCert))
			o.KeyFile = firstNonEmpty(o.KeyFile, os.Getenv(envServerTLSKey))
			if !cmd.Flags().Changed("behind-proxy") {
				o.BehindProxy = envBool(envServerBehindProxy)
			}
			o.Tokens = serverTokensPath(cmd)
			o.ConfigPath = a.configPath
			if o.Store != "" && o.Store != "memory" {
				return a.fail(&service.Error{Kind: service.KindBadRequest, Message: fmt.Sprintf("--store %q: only \"memory\" is accepted (leave it unset for the configured stores)", o.Store)})
			}
			// The listening line is printed from a goroutine; closing ready on
			// return ends it when the server never came up.
			ready := make(chan string, 1)
			defer close(ready)
			o.Ready = ready
			if serverRunHook != nil {
				serverRunHook(&o)
			}
			go func() {
				if bound, ok := <-ready; ok {
					scheme := "http"
					if o.CertFile != "" && o.KeyFile != "" {
						scheme = "https"
					}
					fmt.Fprintf(a.Stderr, "clued server listening on %s://%s\n", scheme, bound)
				}
			}()
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return a.runCluedServer(ctx, o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.Addr, "addr", "", "listen address (default: $"+envServerAddr+", then "+defaultServerAddr+")")
	f.StringVar(&o.CertFile, "tls-cert", "", "TLS certificate file, PEM (default: $"+envServerTLSCert+")")
	f.StringVar(&o.KeyFile, "tls-key", "", "TLS private key file, PEM (default: $"+envServerTLSKey+")")
	f.BoolVar(&o.BehindProxy, "behind-proxy", false, "serve plain HTTP off loopback because a reverse proxy terminates TLS (default: $"+envServerBehindProxy+")")
	f.StringVar(&o.SessionConfig, "session-config", session.DefaultConfigPath(), "clued session config file (mongoUrl, dbName, enrichers)")
	f.StringVar(&o.Store, "store", "", "\"memory\" keeps sessions, plans and stories in memory (for tests)")
	f.StringVar(&o.Version, "version", remote.Version, "version reported by /health and /api/v1/whoami")
	return cmd
}

// firstNonEmpty returns the first non-empty value: flag, then env, then default.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// envBool reads a boolean variable: 1, t, true, y, yes and on (any case)
// are true; everything else, including unset, is false.
func envBool(name string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	if b, err := strconv.ParseBool(v); err == nil {
		return b
	}
	return v == "y" || v == "yes" || v == "on"
}

// runCluedServer opens the stores o names, composes the clued server and
// serves until ctx is done. The plain-HTTP rule is checked before anything
// is opened so a misconfiguration fails fast; errors are reported in the
// CLI envelope on stderr and returned.
func (a *App) runCluedServer(ctx context.Context, o serverRunOptions) error {
	listen := server.Listen{Addr: o.Addr, CertFile: o.CertFile, KeyFile: o.KeyFile, BehindProxy: o.BehindProxy}
	if err := listen.Check(); err != nil {
		return a.fail(&service.Error{Kind: service.KindBadRequest, Message: err.Error()})
	}
	logger := slog.New(slog.NewTextHandler(a.Stderr, nil))

	sessionCfg := session.LoadConfig(o.SessionConfig)
	// The server stores sessions itself. The session config is often this
	// machine's client config, which names a server (perhaps this one) in
	// remote mode; serving from it would relay every write to that server.
	sessionCfg.ServerURL, sessionCfg.ServerToken, sessionCfg.ServerCAFile = "", "", ""
	cfg, err := a.serverDocumentConfig(o)
	if err != nil {
		return a.fail(&service.Error{Kind: service.KindBadRequest, Message: err.Error()})
	}
	opener := projects.NewOpener(cfg.EnabledStorage(), cfg.Dirs())

	var sessions session.Store
	if o.Store == "memory" {
		sessions = newMemorySessionStore()
	} else {
		sessions, err = openSessionStore(ctx, sessionCfg)
		if err != nil {
			_ = opener.Close(ctx)
			return a.fail(&service.Error{Kind: kindConnect, Message: err.Error()})
		}
	}
	// What clients send is redacted and excluded as the server's own config
	// says too, for clients that do not redact themselves.
	sessions = capturing(sessions, sessionCfg)
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := sessions.Close(closeCtx); err != nil {
			logger.Warn("closing the session store", "error", err)
		}
		if err := opener.Close(closeCtx); err != nil {
			logger.Warn("closing the project stores", "error", err)
		}
	}()

	tokens := auth.OpenTokenFile(o.Tokens)
	srv := server.NewClued(server.CluedOptions{
		Version:      o.Version,
		Tokens:       tokens,
		Sessions:     sessions,
		Open:         opener.Open,
		Config:       cfg,
		Enrichers:    session.Active(sessionCfg.DisabledEnrichers, sessionCfg.EnabledEnrichers),
		Logger:       logger,
		Device:       device.New(device.Options{Tokens: tokens}),
		Exchange:     tokens,
		ClientConfig: clientConfigFrom(sessionCfg, o.SessionConfig, time.Now()),
	})
	if err := srv.Serve(ctx, listen, o.Ready); err != nil {
		return a.fail(&service.Error{Kind: service.KindStorage, Message: err.Error(), Cause: err})
	}
	return nil
}

// clientConfigFrom serves the enricher settings of the server's session
// config as the client settings of GET /api/v1/me/config, dated by the
// config file's modification time (the start time without a file).
func clientConfigFrom(cfg session.Config, path string, started time.Time) func() (map[string]any, time.Time) {
	return func() (map[string]any, time.Time) {
		at := started
		if fi, err := os.Stat(path); err == nil {
			at = fi.ModTime()
		}
		return map[string]any{
			"enabledEnrichers":  cfg.EnabledEnrichers,
			"disabledEnrichers": cfg.DisabledEnrichers,
		}, at
	}
}

// serverDocumentConfig is the plan and story configuration the server
// namespaces per project: the memory store under --store memory, else the
// configured one.
func (a *App) serverDocumentConfig(o serverRunOptions) (*config.Config, error) {
	if o.Store == "memory" {
		cfg := config.Default()
		cfg.Storage = []config.StorageDef{{Name: "memory", Kind: "memory"}}
		return cfg, nil
	}
	return loadServerConfig(o.ConfigPath)
}

// loadServerConfig loads the mind-palace config for the server, where a
// file store is not required (plans and stories may live in a database
// alone). config.Load validates with the client rule, so its validation
// failure is re-checked with config.AllowNoFileStore; read and parse
// failures (a nil config) stand.
func loadServerConfig(path string) (*config.Config, error) {
	cfg, err := config.Load(path)
	if err != nil {
		if cfg == nil {
			return nil, err
		}
		if err := config.Validate(cfg, config.AllowNoFileStore); err != nil {
			return nil, err
		}
	}
	if len(cfg.EnabledStorage()) == 0 {
		return nil, errors.New("no storage plugin is enabled in " + firstNonEmpty(cfg.Source, "the default configuration"))
	}
	return cfg, nil
}
