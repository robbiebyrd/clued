// Package stack prepares the directory a container deployment of the clued
// server runs from: the clued server, its MongoDB and, by default, an nginx
// front-end that terminates TLS, as a Docker Compose file (RuntimeCompose)
// or as start and stop scripts with a launchd agent for Apple Containers
// (RuntimeApple, apple.go); the nginx configuration; the server's plan and
// story storage, with the code store turned on in the same MongoDB; a private CA with a certificate for the host; and a first
// bearer token. It is the business logic behind `mind-palace server init`,
// which /clued-setup runs to host the server on the user's own machine.
//
// The clued server itself always serves plain HTTP inside the container
// network (CLUED_SERVER_BEHIND_PROXY): with ProxyNginx the nginx container
// publishes the public port over HTTPS, with ProxyNone the server is
// published on loopback only for another terminator on the host (Caddy,
// Traefik, an nginx of the operator's own, a Kubernetes ingress).
package stack

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/server/auth"
)

// Proxy modes.
const (
	// ProxyNginx: an nginx service terminates TLS with tls/cert.pem and
	// tls/key.pem and forwards to the clued server.
	ProxyNginx = "nginx"
	// ProxyNone: no front-end; the clued server is published on
	// 127.0.0.1:<port> as plain HTTP for a terminator on the host.
	ProxyNone = "none"
)

// Runtimes.
const (
	// RuntimeCompose: a docker-compose.yml started with `docker compose`.
	RuntimeCompose = "compose"
	// RuntimeApple: up.sh and down.sh driving Apple's `container` CLI
	// (macOS 26) and a launchd agent that starts the stack at login.
	RuntimeApple = "apple"
)

// Defaults.
const (
	DefaultPort        = 8443
	DefaultServerImage = "ghcr.io/robbiebyrd/clued-server"
	DefaultNginxImage  = "nginx:stable-alpine"
	DefaultMongoImage  = "mongo:8"
	// CodeStore is the server's code store in every generated stack
	// (CLUED_SERVER_CODE_STORE): collected code lives in the stack's
	// MongoDB beside the sessions, so the stack needs no other container.
	CodeStore = "mongo"
	// DefaultDirName is the stack directory under the home directory; it is
	// also where `mind-palace server token` looks for tokens.json by default.
	DefaultDirName = ".clued-server"
)

// File names inside the stack directory.
const (
	ComposeFile = "docker-compose.yml"
	ConfigFile  = "mind-palace.config.yaml"
	TokensFile  = "tokens.json"
	NginxConf   = "nginx/clued.conf"
	CAFile      = "tls/ca.pem"
	CAKeyFile   = "tls/ca-key.pem"
	CertFile    = "tls/cert.pem"
	KeyFile     = "tls/key.pem"
	// StackFile records how the directory's stack is run (runtime, proxy,
	// port, the up, down and logs commands).
	StackFile = "stack.json"
	// The Apple Containers runtime's files; LaunchdLabel is the agent
	// up.sh installs under ~/Library/LaunchAgents.
	UpScript     = "up.sh"
	DownScript   = "down.sh"
	LaunchdLabel = "com.robbiebyrd.clued-server"
	LaunchdPlist = "launchd/" + LaunchdLabel + ".plist"
)

// Options configures Init. Zero values take the defaults above.
type Options struct {
	// Dir is the stack directory; "" means ~/.clued-server.
	Dir string
	// Runtime is RuntimeCompose (default) or RuntimeApple; DetectRuntime
	// picks one for the machine.
	Runtime string
	// Proxy is ProxyNginx (default) or ProxyNone.
	Proxy string
	// Port is the port published on the host: nginx's HTTPS port, or the
	// server's plain-HTTP loopback port with ProxyNone.
	Port int
	// Hosts are extra names or addresses the certificate must cover, beyond
	// localhost, the loopback addresses and Hostname.
	Hosts []string
	// Hostname is the machine's name, used in the certificate and as the
	// default token name; "" means os.Hostname.
	Hostname string
	// NoTLS skips the CA and certificate: tls/cert.pem and tls/key.pem are
	// supplied by the operator (a certificate from mkcert or a real CA).
	NoTLS bool
	// Force reissues the server certificate even when it exists; the CA is
	// kept while it covers every host and is not about to expire, so a
	// machine that trusts it keeps trusting the server.
	Force bool
	// NewCA replaces the CA too (with Force, or when there is none yet);
	// every machine that trusted the old one must trust the new one.
	NewCA bool
	// NoToken skips minting a token.
	NoToken bool
	// TokenName names the token; "" means Hostname.
	TokenName string
	// Projects is the token's project list; nil means every project ("*").
	Projects []string
	// Account binds the token to an existing account id; "" mints a new one.
	Account string
	// Images override the defaults; ServerImage is a full reference with
	// its tag.
	ServerImage, NginxImage, MongoImage string
	// UID and GID are the user the clued-server container runs as, so it
	// can read the 0600 token file; -1 (or both 0) means the current user
	// when known and the compose default otherwise.
	UID, GID int
	// Now is the clock for the certificates and the compose header (tests).
	Now func() time.Time
}

// Token is the token Init minted; the raw value is printed once and never
// stored.
type Token struct {
	Name      string   `json:"name"`
	AccountID string   `json:"account_id"`
	Projects  []string `json:"projects"`
	Token     string   `json:"token"`
	File      string   `json:"file"`
}

// Result is what Init prepared.
type Result struct {
	Dir     string `json:"dir"`
	Runtime string `json:"runtime"`
	// ComposeFile is the compose file (RuntimeCompose only).
	ComposeFile string `json:"compose_file,omitempty"`
	Proxy       string `json:"proxy"`
	Port        int    `json:"port"`
	// ServerURL is what a client on this machine puts in serverUrl.
	ServerURL string `json:"server_url"`
	// CAFile is the generated CA a client puts in serverCaFile; empty when
	// the certificates were supplied (NoTLS) or there is no TLS (ProxyNone).
	CAFile string `json:"ca_file,omitempty"`
	// Hosts are the names and addresses the certificate covers.
	Hosts []string `json:"hosts,omitempty"`
	// TLS says what happened to the certificates: "generated", "kept",
	// "supplied" or "none".
	TLS string `json:"tls"`
	// CA says what happened to the CA when certificates were generated:
	// "generated" (machines must trust it anew, see `server trust`) or
	// "kept"; CAFingerprint is its SHA-256.
	CA            string `json:"ca,omitempty"`
	CAFingerprint string `json:"ca_fingerprint,omitempty"`
	// Token is the minted token, or nil with NoToken or when TokenName
	// already exists (see Notes).
	Token *Token `json:"token,omitempty"`
	// Up is the command that starts the stack; Down the one that stops it
	// (its data stays); Logs the one that shows the server's log.
	Up   string `json:"up"`
	Down string `json:"down"`
	Logs string `json:"logs"`
	// Health is a command that checks the stack answers.
	Health string `json:"health"`
	// Written and Kept list the files Init wrote and left as they were.
	Written []string `json:"written"`
	Kept    []string `json:"kept,omitempty"`
	// Notes are remarks for the operator (an existing token name, the
	// certificates to supply).
	Notes []string `json:"notes,omitempty"`
}

// TLS outcomes.
const (
	TLSGenerated = "generated"
	TLSKept      = "kept"
	TLSSupplied  = "supplied"
	TLSNone      = "none"
)

// Init prepares the stack directory. It is safe to rerun: the runtime's
// files (the compose file, or the scripts and the launchd agent), the nginx
// configuration and the storage configuration are regenerated, the
// certificates and the token file are kept (Force regenerates the
// certificates), and a token whose name exists is not minted again.
// Switching the runtime removes the other runtime's files.
func Init(opts Options) (Result, error) {
	opts, err := withDefaults(opts)
	if err != nil {
		return Result{}, err
	}
	// The directories are world-readable: the keys and the token file carry
	// their own 0600, and the containers (nginx as root, the server as the
	// user's uid) read the rest through bind mounts.
	if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
		return Result{}, err
	}
	subdirs := []string{"tls", "nginx"}
	if opts.Runtime == RuntimeApple {
		subdirs = append(subdirs, "launchd")
	}
	for _, sub := range subdirs {
		if err := os.MkdirAll(filepath.Join(opts.Dir, sub), 0o755); err != nil {
			return Result{}, err
		}
	}
	res := Result{Dir: opts.Dir, Runtime: opts.Runtime, Proxy: opts.Proxy, Port: opts.Port}
	if opts.Runtime == RuntimeCompose {
		res.ComposeFile = filepath.Join(opts.Dir, ComposeFile)
	}

	hosts := certHosts(opts)
	switch {
	case opts.Proxy == ProxyNone:
		res.TLS = TLSNone
	case opts.NoTLS:
		res.TLS = TLSSupplied
		for _, f := range []string{CertFile, KeyFile} {
			if _, err := os.Stat(filepath.Join(opts.Dir, f)); err != nil {
				res.Notes = append(res.Notes, fmt.Sprintf("supply %s before starting the stack (a certificate for %s from mkcert or a CA your clients trust), or rerun without --no-tls", filepath.Join(opts.Dir, f), strings.Join(hosts, ", ")))
			}
		}
	default:
		res.Hosts = hosts
		res.CAFile = filepath.Join(opts.Dir, CAFile)
		have := true
		for _, f := range []string{CAFile, CertFile, KeyFile} {
			if _, err := os.Stat(filepath.Join(opts.Dir, f)); err != nil {
				have = false
			}
		}
		if have && !opts.Force && !opts.NewCA {
			res.TLS = TLSKept
			res.Kept = append(res.Kept, filepath.Join(opts.Dir, CAFile), filepath.Join(opts.Dir, CertFile), filepath.Join(opts.Dir, KeyFile))
		} else {
			_, hadCA := loadCA(opts.Dir)
			written, outcome, err := writeCertificates(opts.Dir, hosts, opts.Hostname, opts.Now(), opts.NewCA)
			if err != nil {
				return Result{}, err
			}
			res.TLS, res.CA = TLSGenerated, outcome
			res.Written = append(res.Written, written...)
			if outcome == CAKept {
				res.Kept = append(res.Kept, filepath.Join(opts.Dir, CAFile))
			} else if hadCA == nil {
				res.Notes = append(res.Notes, fmt.Sprintf("the CA changed: run `mind-palace server trust` again on this machine and give every other client the new %s", filepath.Join(opts.Dir, CAFile)))
			}
		}
		if cert, err := ReadCA(filepath.Join(opts.Dir, CAFile)); err == nil {
			res.CAFingerprint = Fingerprint(cert)
		}
	}

	if opts.Proxy == ProxyNginx {
		res.ServerURL = fmt.Sprintf("https://localhost:%d", opts.Port)
		if opts.NoTLS {
			res.Health = fmt.Sprintf("curl %s/health", res.ServerURL)
		} else {
			res.Health = fmt.Sprintf("curl --cacert %s %s/health", filepath.Join(opts.Dir, CAFile), res.ServerURL)
		}
	} else {
		res.ServerURL = fmt.Sprintf("http://127.0.0.1:%d", opts.Port)
		res.Health = fmt.Sprintf("curl %s/health", res.ServerURL)
	}
	// Only the runtime's own files are written; the other runtime's are
	// removed so the directory says which one runs the stack.
	files := map[string]string{ConfigFile: configYAML()}
	modes := map[string]os.FileMode{}
	var stale []string
	switch opts.Runtime {
	case RuntimeApple:
		files[UpScript], files[DownScript], files[LaunchdPlist] = upScript(opts), downScript(opts), launchdPlist(opts)
		modes[UpScript], modes[DownScript] = 0o755, 0o755
		res.Up = fmt.Sprintf("bash %s", filepath.Join(opts.Dir, UpScript))
		res.Down = fmt.Sprintf("bash %s", filepath.Join(opts.Dir, DownScript))
		res.Logs = "container logs " + serverContainer
		res.Notes = append(res.Notes, fmt.Sprintf("Apple Containers: up.sh starts `container system` when needed and installs the launchd agent %s so the stack starts at login (down.sh removes it); the server container runs as root because bind-mounted files appear root-owned inside Apple containers", LaunchdLabel))
		stale = []string{ComposeFile}
	default:
		files[ComposeFile] = composeYAML(opts)
		res.Up = fmt.Sprintf("docker compose -f %s up -d", res.ComposeFile)
		res.Down = fmt.Sprintf("docker compose -f %s down", res.ComposeFile)
		res.Logs = fmt.Sprintf("docker compose -f %s logs %s", res.ComposeFile, serverContainer)
		if opts.Proxy == ProxyNginx {
			res.Logs += " nginx"
		}
		stale = []string{UpScript, DownScript, LaunchdPlist}
	}
	if opts.Proxy == ProxyNginx {
		files[NginxConf] = nginxConf()
	} else {
		stale = append(stale, NginxConf)
	}
	for _, name := range stale {
		_ = os.Remove(filepath.Join(opts.Dir, name))
	}
	files[StackFile] = stackJSON(opts, res)
	for name, body := range files {
		path := filepath.Join(opts.Dir, name)
		mode := modes[name]
		if mode == 0 {
			mode = 0o644
		}
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			return Result{}, err
		}
		if err := os.Chmod(path, mode); err != nil {
			return Result{}, err
		}
		res.Written = append(res.Written, path)
	}

	tokens := filepath.Join(opts.Dir, TokensFile)
	if !opts.NoToken {
		tf := auth.OpenTokenFile(tokens)
		raw, err := tf.Create(opts.TokenName, opts.Account, opts.Projects)
		switch {
		case err == nil:
			list, err := tf.List()
			if err != nil {
				return Result{}, err
			}
			for _, t := range list {
				if t.Name == opts.TokenName {
					res.Token = &Token{Name: t.Name, AccountID: t.AccountID, Projects: t.Projects, Token: raw, File: tokens}
				}
			}
			res.Written = append(res.Written, tokens)
		case strings.Contains(err.Error(), "already exists"):
			res.Kept = append(res.Kept, tokens)
			res.Notes = append(res.Notes, fmt.Sprintf("token %q already exists in %s and was not minted again: keep the token this machine has, or mint another with `mind-palace server token create --tokens %s --name <name> --project '*'`", opts.TokenName, tokens, tokens))
		default:
			return Result{}, err
		}
	} else if _, err := os.Stat(tokens); err != nil {
		res.Notes = append(res.Notes, fmt.Sprintf("no token file yet: mint one with `mind-palace server token create --tokens %s --name <name> --project '*'` before starting the stack", tokens))
	}

	return res, nil
}

// DetectRuntime picks the runtime for a machine: RuntimeCompose when
// docker is on the PATH, RuntimeApple when it is not but the machine is a
// Mac with Apple's container CLI, RuntimeCompose otherwise (the setup then
// asks for Docker). goos is runtime.GOOS and lookPath exec.LookPath.
func DetectRuntime(goos string, lookPath func(string) (string, error)) string {
	if _, err := lookPath("docker"); err == nil {
		return RuntimeCompose
	}
	if goos == "darwin" {
		if _, err := lookPath("container"); err == nil {
			return RuntimeApple
		}
	}
	return RuntimeCompose
}

func withDefaults(opts Options) (Options, error) {
	if opts.Dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return opts, fmt.Errorf("stack: no home directory for the default stack directory: %w", err)
		}
		opts.Dir = filepath.Join(home, DefaultDirName)
	}
	if abs, err := filepath.Abs(opts.Dir); err == nil {
		opts.Dir = abs
	}
	switch opts.Runtime {
	case "":
		opts.Runtime = RuntimeCompose
	case RuntimeCompose, RuntimeApple:
	default:
		return opts, fmt.Errorf("stack: runtime %q is not %q or %q", opts.Runtime, RuntimeCompose, RuntimeApple)
	}
	switch opts.Proxy {
	case "":
		opts.Proxy = ProxyNginx
	case ProxyNginx, ProxyNone:
	default:
		return opts, fmt.Errorf("stack: proxy %q is not %q or %q", opts.Proxy, ProxyNginx, ProxyNone)
	}
	if opts.Port == 0 {
		opts.Port = DefaultPort
	}
	if opts.Port < 1 || opts.Port > 65535 {
		return opts, fmt.Errorf("stack: port %d is out of range", opts.Port)
	}
	if opts.Hostname == "" {
		if h, err := os.Hostname(); err == nil {
			opts.Hostname = h
		}
	}
	opts.Hostname = strings.ToLower(strings.TrimSpace(opts.Hostname))
	if opts.TokenName == "" {
		opts.TokenName = opts.Hostname
	}
	if opts.TokenName == "" {
		opts.TokenName = "clued"
	}
	if len(opts.Projects) == 0 {
		opts.Projects = []string{"*"}
	}
	if opts.ServerImage == "" {
		opts.ServerImage = DefaultServerImage + ":latest"
	}
	if opts.NginxImage == "" {
		opts.NginxImage = DefaultNginxImage
	}
	if opts.MongoImage == "" {
		opts.MongoImage = DefaultMongoImage
	}
	if opts.UID <= 0 && opts.GID <= 0 {
		opts.UID, opts.GID = os.Getuid(), os.Getgid()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return opts, nil
}

// certHosts lists the names and addresses the certificate covers: the
// loopback names and addresses, the host name and the extra hosts, lower
// cased and without duplicates; IP addresses are kept as such.
func certHosts(opts Options) []string {
	seen := map[string]bool{}
	var out []string
	add := func(h string) {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" || seen[h] {
			return
		}
		seen[h] = true
		out = append(out, h)
	}
	add("localhost")
	add("127.0.0.1")
	add("::1")
	add(opts.Hostname)
	for _, h := range opts.Hosts {
		add(h)
	}
	return out
}

// splitHosts separates DNS names from IP addresses.
func splitHosts(hosts []string) (names []string, ips []net.IP) {
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			ips = append(ips, ip)
		} else {
			names = append(names, h)
		}
	}
	return names, ips
}
