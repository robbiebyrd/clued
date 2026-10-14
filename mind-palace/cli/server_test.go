package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/server/auth"
)

// runServer executes `mind-palace server ...` through the root command.
func runServer(t *testing.T, args ...string) run {
	t.Helper()
	app := New()
	var out, errb bytes.Buffer
	app.Stdout, app.Stderr, app.Stdin = &out, &errb, strings.NewReader("")
	root := app.Root()
	root.SetArgs(append([]string{"server"}, args...))
	err := root.Execute()
	app.close()
	code := 0
	if err != nil {
		code = ExitCode(err)
	}
	return run{code: code, stdout: out.String(), stderr: errb.String()}
}

func serverResult(t *testing.T, r run) map[string]any {
	t.Helper()
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("unexpected failure: %+v", r)
	}
	return result(t, r)
}

func serverList(t *testing.T, r run) []map[string]any {
	t.Helper()
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("unexpected failure: %+v", r)
	}
	var env struct {
		OK     bool             `json:"ok"`
		Result []map[string]any `json:"result"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &env); err != nil || !env.OK {
		t.Fatalf("not an ok list envelope: %q (%v)", r.stdout, err)
	}
	return env.Result
}

func TestServerTokenCreateListGrantRevoke(t *testing.T) {
	t.Setenv("CLUED_SERVER_TOKENS", "")
	path := filepath.Join(t.TempDir(), "srv", "tokens.json")

	r := runServer(t, "token", "create", "--tokens", path, "--name", "laptop", "--project", "alpha", "--project", "beta")
	res := serverResult(t, r)
	raw, _ := res["token"].(string)
	if raw == "" {
		t.Fatalf("no token in %v", res)
	}
	if strings.Count(r.stdout, raw) != 1 {
		t.Fatalf("raw token printed %d times:\n%s", strings.Count(r.stdout, raw), r.stdout)
	}
	if res["name"] != "laptop" || !reflect.DeepEqual(res["projects"], []any{"alpha", "beta"}) {
		t.Fatalf("result = %v", res)
	}
	account, _ := res["account_id"].(string)
	if account == "" {
		t.Fatal("no generated account id")
	}
	if _, ok := res["hash"]; ok {
		t.Fatal("create output exposes the hash; the token is enough")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), raw) {
		t.Fatal("file holds the raw token")
	}
	if !strings.Contains(string(data), auth.HashToken(raw)) {
		t.Fatal("file does not hold the token's SHA-256 hex")
	}

	// An explicit account is kept.
	r = runServer(t, "token", "create", "--tokens", path, "--name", "desktop", "--account", "acct-1", "--project", "*")
	if res := serverResult(t, r); res["account_id"] != "acct-1" {
		t.Fatalf("result = %v", res)
	}

	list := serverList(t, runServer(t, "token", "list", "--tokens", path))
	if len(list) != 2 || list[0]["name"] != "laptop" || list[1]["name"] != "desktop" {
		t.Fatalf("list = %v", list)
	}
	for _, tok := range list {
		if _, ok := tok["token"]; ok {
			t.Fatalf("list exposes a raw token: %v", tok)
		}
		if tok["hash"] == "" || tok["revoked"] != nil {
			t.Fatalf("list entry = %v", tok)
		}
	}
	if list[0]["hash"] != auth.HashToken(raw) {
		t.Fatal("listed hash is not the SHA-256 of the raw token")
	}

	res = serverResult(t, runServer(t, "token", "grant", "--tokens", path, "laptop", "--project", "gamma", "--project", "alpha"))
	if !reflect.DeepEqual(res["projects"], []any{"alpha", "beta", "gamma"}) {
		t.Fatalf("after grant: %v", res)
	}
	if p, ok := auth.OpenTokenFile(path).Lookup(raw); !ok || !reflect.DeepEqual(p.Projects, []string{"alpha", "beta", "gamma"}) {
		t.Fatalf("lookup after grant: %+v %v", p, ok)
	}

	res = serverResult(t, runServer(t, "token", "revoke", "--tokens", path, "laptop"))
	if res["revoked"] == nil || res["name"] != "laptop" {
		t.Fatalf("after revoke: %v", res)
	}
	if _, ok := auth.OpenTokenFile(path).Lookup(raw); ok {
		t.Fatal("revoked token still resolves")
	}
	if r := runServer(t, "token", "grant", "--tokens", path, "laptop", "--project", "delta"); r.code == 0 {
		t.Fatalf("grant to a revoked token succeeded: %+v", r)
	}
}

func TestServerTokenErrors(t *testing.T) {
	t.Setenv("CLUED_SERVER_TOKENS", "")
	path := filepath.Join(t.TempDir(), "tokens.json")
	if r := runServer(t, "token", "create", "--tokens", path); r.code == 0 {
		t.Fatalf("create without --name succeeded: %+v", r)
	}
	if r := runServer(t, "token", "revoke", "--tokens", path, "ghost"); r.code == 0 || !strings.Contains(r.stderr, `"ok": false`) {
		t.Fatalf("revoke of unknown token: %+v", r)
	}
	if r := runServer(t, "token", "grant", "--tokens", path, "ghost", "--project", "x"); r.code == 0 {
		t.Fatalf("grant to unknown token succeeded: %+v", r)
	}
	if r := runServer(t, "token", "grant", "--tokens", path, "ghost"); r.code == 0 {
		t.Fatalf("grant without --project succeeded: %+v", r)
	}
	if list := serverList(t, runServer(t, "token", "list", "--tokens", path)); len(list) != 0 {
		t.Fatalf("list of a missing file = %v", list)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("list created the token file")
	}
}

func TestServerTokensPathFromEnv(t *testing.T) {
	envPath := filepath.Join(t.TempDir(), "env", "tokens.json")
	t.Setenv("CLUED_SERVER_TOKENS", envPath)
	serverResult(t, runServer(t, "token", "create", "--name", "a", "--project", "p"))
	if _, err := os.Stat(envPath); err != nil {
		t.Fatalf("token file not written at $CLUED_SERVER_TOKENS: %v", err)
	}
	// The flag wins over the environment.
	flagPath := filepath.Join(t.TempDir(), "flag", "tokens.json")
	serverResult(t, runServer(t, "token", "create", "--tokens", flagPath, "--name", "b", "--project", "p"))
	if list := serverList(t, runServer(t, "token", "list", "--tokens", flagPath)); len(list) != 1 || list[0]["name"] != "b" {
		t.Fatalf("flag file = %v", list)
	}
	if list := serverList(t, runServer(t, "token", "list")); len(list) != 1 || list[0]["name"] != "a" {
		t.Fatalf("env file = %v", list)
	}
}

func TestServerTokensDefaultPath(t *testing.T) {
	t.Setenv("CLUED_SERVER_TOKENS", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := defaultTokensPath(); got != filepath.Join(home, ".clued-server", "tokens.json") {
		t.Fatalf("default = %q", got)
	}
}

func TestServerGroupHelp(t *testing.T) {
	r := runServer(t, "--help")
	if r.code != 0 || !strings.Contains(r.stdout, "token") {
		t.Fatalf("server --help: %+v", r)
	}
	r = runServer(t, "token", "--help")
	for _, sub := range []string{"create", "list", "revoke", "grant"} {
		if !strings.Contains(r.stdout, sub) {
			t.Fatalf("token --help lacks %s:\n%s", sub, r.stdout)
		}
	}
}

func TestServerTokenCreateEnvironmentKey(t *testing.T) {
	t.Setenv("CLUED_SERVER_TOKENS", "")
	path := filepath.Join(t.TempDir(), "tokens.json")
	res := serverResult(t, runServer(t, "token", "create", "--tokens", path, "--name", "cloud-env", "--project", "*", "--environment", "--ttl", "8h"))
	if res["kind"] != auth.KindEnvironment || res["token_ttl"] != "8h0m0s" || res["token"] == "" {
		t.Fatalf("result = %v", res)
	}
	key, _ := res["token"].(string)
	if _, ok := auth.OpenTokenFile(path).Lookup(key); ok {
		t.Error("the environment key authenticates as a token")
	}
	list := serverList(t, runServer(t, "token", "list", "--tokens", path))
	if len(list) != 1 || list[0]["kind"] != auth.KindEnvironment || list[0]["token_ttl"] != "8h0m0s" {
		t.Errorf("list = %v", list)
	}
	for _, args := range [][]string{
		{"--name", "plain", "--ttl", "8h"},
		{"--name", "short", "--environment", "--ttl", "10m"},
	} {
		if r := runServer(t, append([]string{"token", "create", "--tokens", path}, args...)...); r.code != 2 {
			t.Errorf("%v: %+v", args, r)
		}
	}
}
