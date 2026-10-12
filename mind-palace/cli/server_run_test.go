package cli

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/server"
	"github.com/robbiebyrd/clued/mind-palace/server/auth"
	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
)

// clearServerEnv blanks every CLUED_SERVER_* variable `server run` reads so
// the developer's shell cannot leak into the tests.
func clearServerEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"CLUED_SERVER_ADDR", "CLUED_SERVER_TLS_CERT", "CLUED_SERVER_TLS_KEY", "CLUED_SERVER_BEHIND_PROXY", "CLUED_SERVER_TOKENS", "MIND_PALACE_CONFIG", "CLUED_MONGO_URL", "CLUED_DB_NAME"} {
		t.Setenv(name, "")
	}
}

// tokenFile writes a token file with one token and returns the path, the
// raw token and its account id.
func tokenFile(t *testing.T, name string, projects ...string) (path, raw, account string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "tokens.json")
	tf := auth.OpenTokenFile(path)
	raw, err := tf.Create(name, "", projects)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := tf.Lookup(raw)
	if !ok {
		t.Fatal("token not found after create")
	}
	return path, raw, p.AccountID
}

// selfSigned writes a self-signed certificate for 127.0.0.1 and returns the
// file paths and a pool that trusts it.
func selfSigned(t *testing.T) (certFile, keyFile string, pool *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "clued-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool = x509.NewCertPool()
	pool.AddCert(cert)
	return certFile, keyFile, pool
}

// running is a server started in-process for one test.
type running struct {
	addr   string
	scheme string
	client *http.Client
	stop   context.CancelFunc
	done   chan error
	stderr *bytes.Buffer
	// exited is closed once done has been drained, so the cleanup does not
	// wait for a result that was already collected.
	exited chan struct{}
}

func (r *running) url(path string) string { return r.scheme + "://" + r.addr + path }

// wait stops the server and returns what runCluedServer returned.
func (r *running) wait(t *testing.T) error {
	t.Helper()
	r.stop()
	select {
	case err := <-r.done:
		close(r.exited)
		return err
	case <-time.After(15 * time.Second):
		t.Fatal("server did not stop")
		return nil
	}
}

// startServer runs a.runCluedServer with o in the background and waits for
// the bound address. The in-memory stores need no config files.
func startServer(t *testing.T, o serverRunOptions, pool *x509.CertPool) *running {
	t.Helper()
	app := New()
	var stderr bytes.Buffer
	app.Stdout, app.Stderr, app.Stdin = io.Discard, &stderr, strings.NewReader("")
	if o.Store == "" {
		o.Store = "memory"
	}
	ready := make(chan string, 1)
	o.Ready = ready
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.runCluedServer(ctx, o) }()
	r := &running{scheme: "http", stop: cancel, done: done, stderr: &stderr, exited: make(chan struct{}), client: &http.Client{Timeout: 10 * time.Second}}
	if pool != nil {
		r.scheme = "https"
		r.client.Transport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
	}
	select {
	case r.addr = <-ready:
	case err := <-done:
		cancel()
		t.Fatalf("server exited before listening: %v\nstderr: %s", err, stderr.String())
	case <-time.After(10 * time.Second):
		cancel()
		t.Fatal("server did not start")
	}
	t.Cleanup(func() {
		cancel()
		select {
		case <-r.exited:
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("server did not stop")
		}
	})
	return r
}

// call sends one request with an optional bearer token and JSON body.
func (r *running) call(t *testing.T, method, path, token string, body any) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, r.url(path), rd)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("%s %s: not JSON: %q", method, path, raw)
		}
	}
	return resp.StatusCode, out
}

func TestServerRunRefusesPlainHTTPOffLoopback(t *testing.T) {
	clearServerEnv(t)
	tokens, _, _ := tokenFile(t, "t")
	app := New()
	var stderr bytes.Buffer
	app.Stdout, app.Stderr, app.Stdin = io.Discard, &stderr, strings.NewReader("")
	err := app.runCluedServer(context.Background(), serverRunOptions{Addr: "0.0.0.0:0", Tokens: tokens, Store: "memory"})
	if err == nil {
		t.Fatal("plain HTTP on 0.0.0.0 started")
	}
	msg := err.Error() + stderr.String()
	for _, want := range []string{"--tls-cert", "--tls-key", "--behind-proxy"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not name %s", msg, want)
		}
	}

	// Through the command line the same refusal exits non-zero and names the options.
	r := runServer(t, "run", "--addr", "0.0.0.0:0", "--tokens", tokens, "--store", "memory")
	if r.code == 0 {
		t.Fatalf("exit code 0: %+v", r)
	}
	for _, want := range []string{"--tls-cert", "--tls-key", "--behind-proxy"} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("stderr %q does not name %s", r.stderr, want)
		}
	}
}

func TestServerRunPlainHTTPOnLoopbackStarts(t *testing.T) {
	clearServerEnv(t)
	tokens, _, _ := tokenFile(t, "t")
	r := startServer(t, serverRunOptions{Addr: "127.0.0.1:0", Tokens: tokens}, nil)
	status, body := r.call(t, "GET", "/health", "", nil)
	if status != http.StatusOK || body["ok"] != true || body["service"] != "clued-server" || body["auth"] != "bearer" {
		t.Fatalf("health = %d %v", status, body)
	}
	if _, ok := body["version"].(string); !ok {
		t.Fatalf("health has no version: %v", body)
	}
	if err := r.wait(t); err != nil {
		t.Fatal(err)
	}
}

func TestServerRunBehindProxyOffLoopbackStarts(t *testing.T) {
	clearServerEnv(t)
	tokens, _, _ := tokenFile(t, "t")
	r := startServer(t, serverRunOptions{Addr: "0.0.0.0:0", BehindProxy: true, Tokens: tokens}, nil)
	// The listener is on every interface; reach it on loopback.
	_, port, err := net.SplitHostPort(r.addr)
	if err != nil {
		t.Fatal(err)
	}
	r.addr = net.JoinHostPort("127.0.0.1", port)
	if status, _ := r.call(t, "GET", "/health", "", nil); status != http.StatusOK {
		t.Fatalf("health = %d", status)
	}
	if err := r.wait(t); err != nil {
		t.Fatal(err)
	}
}

func TestServerRunTLS(t *testing.T) {
	clearServerEnv(t)
	tokens, raw, account := tokenFile(t, "laptop", "alpha")
	certFile, keyFile, pool := selfSigned(t)
	r := startServer(t, serverRunOptions{Addr: "127.0.0.1:0", CertFile: certFile, KeyFile: keyFile, Tokens: tokens, Version: "9.9.9-test"}, pool)

	status, body := r.call(t, "GET", "/health", "", nil)
	if status != http.StatusOK || body["version"] != "9.9.9-test" || body["auth"] != "bearer" {
		t.Fatalf("health = %d %v", status, body)
	}

	status, body = r.call(t, "GET", "/api/v1/whoami", "", nil)
	if status != http.StatusUnauthorized || body["ok"] != false {
		t.Fatalf("whoami without token = %d %v", status, body)
	}

	status, body = r.call(t, "GET", "/api/v1/whoami", raw, nil)
	if status != http.StatusOK {
		t.Fatalf("whoami = %d %v", status, body)
	}
	res, _ := body["result"].(map[string]any)
	if res["name"] != "laptop" || res["account_id"] != account || res["version"] != "9.9.9-test" {
		t.Fatalf("whoami result = %v", res)
	}

	// A plain-HTTP client is refused by the TLS listener.
	plain := &http.Client{Timeout: 5 * time.Second}
	resp, err := plain.Get("http://" + r.addr + "/health")
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Fatal("plain HTTP answered on the TLS listener")
		}
	}

	if err := r.wait(t); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.stderr.String(), "principal=laptop") || !strings.Contains(r.stderr.String(), "principal=-") {
		t.Errorf("request log lacks principals:\n%s", r.stderr.String())
	}
}

func TestServerRunSessionRoutesStampAccount(t *testing.T) {
	clearServerEnv(t)
	tokens, raw, account := tokenFile(t, "laptop", "alpha")
	store := memsession.New()
	newMemorySessionStore = func() session.Store { return store }
	defer func() { newMemorySessionStore = func() session.Store { return memsession.New() } }()
	r := startServer(t, serverRunOptions{Addr: "127.0.0.1:0", Tokens: tokens}, nil)

	status, body := r.call(t, "PUT", "/api/v1/session/sessions", raw, session.Session{SessionID: "s1", AccountID: "forged", Cwd: "/w", LastSeen: time.Now()})
	if status != http.StatusOK {
		t.Fatalf("upsert = %d %v", status, body)
	}
	doc, err := store.GetSession(context.Background(), account, "s1")
	if err != nil {
		t.Fatalf("session not stored under the token's account: %v", err)
	}
	if doc["account_id"] != account {
		t.Fatalf("account_id = %v, want %q", doc["account_id"], account)
	}
	if status, _ := r.call(t, "PUT", "/api/v1/session/sessions", "", session.Session{SessionID: "s2"}); status != http.StatusUnauthorized {
		t.Fatalf("upsert without token = %d", status)
	}
	if err := r.wait(t); err != nil {
		t.Fatal(err)
	}
}

func TestServerRunStoreRoutesHonourProjectList(t *testing.T) {
	clearServerEnv(t)
	tokens, raw, _ := tokenFile(t, "laptop", "alpha")
	r := startServer(t, serverRunOptions{Addr: "127.0.0.1:0", Tokens: tokens}, nil)

	status, body := r.call(t, "GET", "/api/v1/projects/beta/store/plan", raw, nil)
	if status != http.StatusForbidden {
		t.Fatalf("foreign project = %d %v", status, body)
	}
	errv, _ := body["error"].(map[string]any)
	if errv["kind"] != "forbidden" {
		t.Fatalf("error = %v", body)
	}
	status, body = r.call(t, "GET", "/api/v1/projects/alpha/store/plan", raw, nil)
	if status != http.StatusOK || body["ok"] != true {
		t.Fatalf("own project = %d %v", status, body)
	}
	if status, _ := r.call(t, "GET", "/api/v1/projects/alpha/store/plan", "", nil); status != http.StatusUnauthorized {
		t.Fatalf("without token = %d", status)
	}
	if err := r.wait(t); err != nil {
		t.Fatal(err)
	}
}

func TestServerRunMountsTheProjectScopedRoutesAndNoRouteIs404(t *testing.T) {
	clearServerEnv(t)
	tokens, raw, _ := tokenFile(t, "laptop", "alpha")
	r := startServer(t, serverRunOptions{Addr: "127.0.0.1:0", Tokens: tokens}, nil)
	// The service API, the WebSocket and the MCP endpoint are mounted and
	// need a project: none is 400 "invalid".
	for _, path := range []string{"/ws", "/mcp", "/api/v1/plans"} {
		if status, body := r.call(t, "GET", path, raw, nil); status != http.StatusBadRequest {
			t.Errorf("%s = %d %v, want 400 (missing project)", path, status, body)
		}
	}
	// ?project= selects the project (the X-Clued-Project header is the
	// documented way); one outside the token's list is 403.
	if status, body := r.call(t, "GET", "/api/v1/plans?project=alpha", raw, nil); status != http.StatusOK {
		t.Errorf("/api/v1/plans?project=alpha = %d %v", status, body)
	}
	if status, _ := r.call(t, "GET", "/api/v1/plans?project=beta", raw, nil); status != http.StatusForbidden {
		t.Errorf("/api/v1/plans?project=beta = %d, want 403", status)
	}
	if status, _ := r.call(t, "GET", "/nope", raw, nil); status != http.StatusNotFound {
		t.Errorf("/nope = %d, want 404", status)
	}
	if status, _ := r.call(t, "GET", "/ws", "", nil); status != http.StatusUnauthorized {
		t.Error("routes are not behind authentication")
	}
	if err := r.wait(t); err != nil {
		t.Fatal(err)
	}
}

// resolveServerRun runs `server run` through the command line with the
// test hook capturing the resolved options; the hook then points the server
// at an in-memory, loopback, plain-HTTP configuration so it starts, and the
// command is cancelled as soon as it listens.
func resolveServerRun(t *testing.T, args ...string) serverRunOptions {
	t.Helper()
	app := New()
	var out, errb bytes.Buffer
	app.Stdout, app.Stderr, app.Stdin = &out, &errb, strings.NewReader("")
	ready := make(chan string, 1)
	var seen serverRunOptions
	serverRunHook = func(o *serverRunOptions) {
		seen = *o
		o.Addr, o.CertFile, o.KeyFile, o.BehindProxy, o.Store, o.Ready = "127.0.0.1:0", "", "", false, "memory", ready
	}
	defer func() { serverRunHook = nil }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := app.Root()
	root.SetArgs(append([]string{"server", "run"}, args...))
	done := make(chan error, 1)
	go func() { done <- root.ExecuteContext(ctx) }()
	select {
	case <-ready:
		cancel()
	case err := <-done:
		t.Fatalf("command exited before listening: %v\n%s", err, errb.String())
	case <-time.After(10 * time.Second):
		t.Fatal("command did not start listening")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("exit: %v\n%s", err, errb.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatal("command did not stop")
	}
	return seen
}

func TestServerRunFlagsOverrideEnv(t *testing.T) {
	clearServerEnv(t)
	tokensEnv, _, _ := tokenFile(t, "env")
	tokensFlag, _, _ := tokenFile(t, "flag")
	t.Setenv("CLUED_SERVER_ADDR", "0.0.0.0:1")
	t.Setenv("CLUED_SERVER_TLS_CERT", "/env/cert.pem")
	t.Setenv("CLUED_SERVER_TLS_KEY", "/env/key.pem")
	t.Setenv("CLUED_SERVER_BEHIND_PROXY", "true")
	t.Setenv("CLUED_SERVER_TOKENS", tokensEnv)

	// Env alone.
	seen := resolveServerRun(t)
	if seen.Addr != "0.0.0.0:1" || seen.CertFile != "/env/cert.pem" || seen.KeyFile != "/env/key.pem" || !seen.BehindProxy || seen.Tokens != tokensEnv {
		t.Fatalf("env not applied: %+v", seen)
	}
	if seen.Store != "" {
		t.Fatalf("store = %q", seen.Store)
	}

	// Flags win.
	seen = resolveServerRun(t, "--store", "memory", "--addr", "127.0.0.1:0", "--tls-cert", "/flag/cert.pem", "--tls-key", "/flag/key.pem", "--behind-proxy=false", "--tokens", tokensFlag, "--version", "v-test", "--session-config", "/flag/config.json")
	if seen.Addr != "127.0.0.1:0" || seen.CertFile != "/flag/cert.pem" || seen.KeyFile != "/flag/key.pem" || seen.BehindProxy || seen.Tokens != tokensFlag || seen.Version != "v-test" || seen.SessionConfig != "/flag/config.json" || seen.Store != "memory" {
		t.Fatalf("flags did not override env: %+v", seen)
	}

	// Defaults when neither is set.
	clearServerEnv(t)
	seen = resolveServerRun(t)
	if seen.Addr != "127.0.0.1:8443" || seen.CertFile != "" || seen.KeyFile != "" || seen.BehindProxy || seen.Version == "" || seen.SessionConfig != session.DefaultConfigPath() {
		t.Fatalf("defaults: %+v", seen)
	}
	if seen.Tokens != defaultTokensPath() {
		t.Fatalf("tokens default = %q", seen.Tokens)
	}
}

func TestServerRunBehindProxyEnvValues(t *testing.T) {
	clearServerEnv(t)
	for _, tc := range []struct {
		value string
		want  bool
	}{{"1", true}, {"true", true}, {"yes", true}, {"0", false}, {"false", false}, {"", false}} {
		t.Setenv("CLUED_SERVER_BEHIND_PROXY", tc.value)
		if seen := resolveServerRun(t); seen.BehindProxy != tc.want {
			t.Errorf("CLUED_SERVER_BEHIND_PROXY=%q: behind proxy = %v", tc.value, seen.BehindProxy)
		}
	}
}

func TestServerRunCommandServesUntilCancelled(t *testing.T) {
	clearServerEnv(t)
	tokens, _, _ := tokenFile(t, "t")
	app := New()
	var out, errb bytes.Buffer
	app.Stdout, app.Stderr, app.Stdin = &out, &errb, strings.NewReader("")
	ready := make(chan string, 1)
	serverRunHook = func(o *serverRunOptions) { o.Ready = ready }
	defer func() { serverRunHook = nil }()
	ctx, cancel := context.WithCancel(context.Background())
	root := app.Root()
	root.SetArgs([]string{"server", "run", "--store", "memory", "--addr", "127.0.0.1:0", "--tokens", tokens})
	done := make(chan error, 1)
	go func() { done <- root.ExecuteContext(ctx) }()
	var addr string
	select {
	case addr = <-ready:
	case err := <-done:
		t.Fatalf("command exited: %v\n%s", err, errb.String())
	case <-time.After(10 * time.Second):
		t.Fatal("command did not start listening")
	}
	resp, err := http.Get("http://" + addr + "/health")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("exit: %v\n%s", err, errb.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatal("command did not stop")
	}
}

// gatedStore blocks GetSession until released, so a test can hold a
// request in flight across a shutdown.
type gatedStore struct {
	session.Store
	entered chan struct{}
	release chan struct{}
}

func (g *gatedStore) GetSession(ctx context.Context, accountID, sessionID string) (session.Doc, error) {
	g.entered <- struct{}{}
	<-g.release
	return g.Store.GetSession(ctx, accountID, sessionID)
}

func TestServerRunShutdownDrainsInFlightRequests(t *testing.T) {
	clearServerEnv(t)
	tokens, raw, account := tokenFile(t, "laptop")
	gated := &gatedStore{Store: memsession.New(), entered: make(chan struct{}, 1), release: make(chan struct{})}
	if err := gated.Store.UpsertSession(context.Background(), session.Session{SessionID: "s1", AccountID: account, LastSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}
	newMemorySessionStore = func() session.Store { return gated }
	defer func() { newMemorySessionStore = func() session.Store { return memsession.New() } }()
	r := startServer(t, serverRunOptions{Addr: "127.0.0.1:0", Tokens: tokens}, nil)

	type reply struct {
		status int
		body   map[string]any
	}
	replies := make(chan reply, 1)
	go func() {
		status, body := r.call(t, "GET", "/api/v1/session/sessions/s1", raw, nil)
		replies <- reply{status, body}
	}()
	select {
	case <-gated.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("request did not reach the store")
	}

	// SIGTERM equivalent: cancel the context while the request is in flight.
	r.stop()
	select {
	case err := <-r.done:
		t.Fatalf("server stopped with a request in flight: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	close(gated.release)
	select {
	case rep := <-replies:
		if rep.status != http.StatusOK {
			t.Fatalf("in-flight request = %d %v", rep.status, rep.body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("in-flight request never answered")
	}
	select {
	case err := <-r.done:
		close(r.exited)
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("server did not stop after the request finished")
	}
	// New connections are refused once stopped.
	if _, err := r.client.Get(r.url("/health")); err == nil {
		t.Error("server still answers after shutdown")
	}
}

func TestServerRunMemoryNeedsNoConfigFiles(t *testing.T) {
	clearServerEnv(t)
	dir := t.TempDir()
	tokens, raw, _ := tokenFile(t, "laptop", "*")
	// Point every config at files that do not exist.
	t.Setenv("MIND_PALACE_CONFIG", filepath.Join(dir, "missing-mind-palace.yaml"))
	r := startServer(t, serverRunOptions{Addr: "127.0.0.1:0", Tokens: tokens, ConfigPath: "", SessionConfig: filepath.Join(dir, "missing-config.json")}, nil)
	if status, body := r.call(t, "GET", "/api/v1/projects/any/store/story", raw, nil); status != http.StatusOK {
		t.Fatalf("memory store = %d %v", status, body)
	}
	if err := r.wait(t); err != nil {
		t.Fatal(err)
	}
}

func TestServerRunConfigErrorsAreReported(t *testing.T) {
	clearServerEnv(t)
	tokens, _, _ := tokenFile(t, "t")
	bad := filepath.Join(t.TempDir(), "mind-palace.config.yaml")
	if err := os.WriteFile(bad, []byte("storage: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := New()
	var stderr bytes.Buffer
	app.Stdout, app.Stderr, app.Stdin = io.Discard, &stderr, strings.NewReader("")
	err := app.runCluedServer(context.Background(), serverRunOptions{Addr: "127.0.0.1:0", Tokens: tokens, ConfigPath: bad, Store: ""})
	if err == nil {
		t.Fatal("unparsable config started the server")
	}

	// A database-only storage list is accepted on the server (no file store needed).
	okCfg := filepath.Join(t.TempDir(), "mind-palace.config.yaml")
	if err := os.WriteFile(okCfg, []byte("storage:\n  - name: mem\n    kind: memory\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadServerConfig(okCfg)
	if err != nil {
		t.Fatalf("database-only config rejected: %v", err)
	}
	if len(cfg.EnabledStorage()) != 1 || cfg.EnabledStorage()[0].Kind != "memory" {
		t.Fatalf("storage = %+v", cfg.Storage)
	}
}

func TestIsLoopbackAddr(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:8443": true, "localhost:8443": true, "[::1]:8443": true, "127.0.0.1:0": true,
		"0.0.0.0:8443": false, ":8443": false, "[::]:8443": false, "10.0.0.1:8443": false, "example.com:443": false,
	} {
		if got := server.IsLoopbackAddr(addr); got != want {
			t.Errorf("IsLoopbackAddr(%q) = %v, want %v", addr, got, want)
		}
	}
	if (server.Listen{Addr: "0.0.0.0:1", CertFile: "c"}).Check() == nil {
		t.Error("a certificate without its key passed")
	}
	if err := (server.Listen{Addr: "0.0.0.0:1", CertFile: "c", KeyFile: "k"}).Check(); err != nil {
		t.Errorf("TLS off loopback refused: %v", err)
	}
}
