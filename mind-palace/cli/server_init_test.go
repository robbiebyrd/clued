package cli

import (
	"crypto/x509"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/remote"
	"github.com/robbiebyrd/clued/mind-palace/server/stack"
	"github.com/robbiebyrd/clued/mind-palace/session"
)

func TestServerInitPreparesTheStackAndItsCertificateServesTLS(t *testing.T) {
	clearServerEnv(t)
	t.Setenv(envPluginVersion, "9.9.9")
	dir := filepath.Join(t.TempDir(), "stack")
	res := serverResult(t, runServer(t, "init", "--dir", dir, "--name", "laptop", "--host", "clued.example.test", "--port", "9443"))

	if res["server_url"] != "https://localhost:9443" || res["proxy"] != "nginx" || res["tls"] != stack.TLSGenerated || res["ca_file"] != filepath.Join(dir, stack.CAFile) {
		t.Fatalf("result = %v", res)
	}
	token, _ := res["token"].(map[string]any)
	if token["name"] != "laptop" || token["token"] == "" || token["file"] != filepath.Join(dir, stack.TokensFile) {
		t.Fatalf("token = %v", token)
	}
	compose, err := os.ReadFile(filepath.Join(dir, stack.ComposeFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(compose), "image: ghcr.io/robbiebyrd/clued-server:v9.9.9\n") || !strings.Contains(string(compose), `"9443:443"`) {
		t.Errorf("compose should pin the plugin's release and the port:\n%s", compose)
	}
	if res["runtime"] != stack.RuntimeCompose || res["up"] != "docker compose -f "+filepath.Join(dir, stack.ComposeFile)+" up -d" || !strings.HasPrefix(res["down"].(string), "docker compose -f ") {
		t.Errorf("runtime = %v, up = %v, down = %v", res["runtime"], res["up"], res["down"])
	}

	// The generated certificate is what nginx presents; the server can
	// present it too, and the plugin's client trusts it through ca.pem.
	pool := x509.NewCertPool()
	caPEM, err := os.ReadFile(res["ca_file"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatal("ca.pem holds no certificate")
	}
	r := startServer(t, serverRunOptions{
		Addr: "127.0.0.1:0", CertFile: filepath.Join(dir, stack.CertFile), KeyFile: filepath.Join(dir, stack.KeyFile),
		Tokens: token["file"].(string), Version: "stack-test",
	}, pool)
	status, body := r.call(t, "GET", "/api/v1/whoami", token["token"].(string), nil)
	if status != http.StatusOK {
		t.Fatalf("whoami over the generated certificate = %d %v", status, body)
	}
	if got, _ := body["result"].(map[string]any); got["name"] != "laptop" {
		t.Errorf("whoami = %v", got)
	}

	client, err := remote.New(session.Config{ServerURL: "https://" + r.addr, ServerToken: token["token"].(string), ServerCAFile: res["ca_file"].(string)})
	if err != nil {
		t.Fatal(err)
	}
	var who map[string]any
	if err := client.Do(t.Context(), "GET", "/api/v1/whoami", nil, &who); err != nil {
		t.Fatalf("the remote client does not trust the generated CA: %v", err)
	}
	if who["name"] != "laptop" {
		t.Errorf("remote whoami = %v", who)
	}
	if err := r.wait(t); err != nil {
		t.Fatal(err)
	}
}

func TestServerInitFlagsAndErrors(t *testing.T) {
	clearServerEnv(t)
	t.Setenv(envPluginVersion, "")
	dir := t.TempDir()
	res := serverResult(t, runServer(t, "init", "--dir", dir, "--proxy", "none", "--no-token", "--image", "clued-server:dev", "--port", "9000"))
	if res["server_url"] != "http://127.0.0.1:9000" || res["tls"] != stack.TLSNone || res["token"] != nil {
		t.Errorf("result = %v", res)
	}
	compose, _ := os.ReadFile(filepath.Join(dir, stack.ComposeFile))
	if !strings.Contains(string(compose), "image: clued-server:dev\n") {
		t.Errorf("--image ignored:\n%s", compose)
	}
	res = serverResult(t, runServer(t, "init", "--dir", t.TempDir(), "--no-token", "--proxy", "nginx"))
	compose, _ = os.ReadFile(res["compose_file"].(string))
	if !strings.Contains(string(compose), "image: "+stack.DefaultServerImage+":latest\n") {
		t.Errorf("without a plugin version the image should be :latest:\n%s", compose)
	}

	// --runtime apple writes the scripts instead of a compose file.
	dir = t.TempDir()
	res = serverResult(t, runServer(t, "init", "--dir", dir, "--runtime", "apple", "--no-token", "--image", "clued-server:dev"))
	if res["runtime"] != stack.RuntimeApple || res["compose_file"] != nil || res["up"] != "bash "+filepath.Join(dir, stack.UpScript) || res["down"] != "bash "+filepath.Join(dir, stack.DownScript) || res["logs"] != "container logs clued-server" {
		t.Errorf("apple result = %v", res)
	}
	up, err := os.ReadFile(filepath.Join(dir, stack.UpScript))
	if err != nil || !strings.Contains(string(up), "SERVER_IMAGE=clued-server:dev\n") {
		t.Errorf("up.sh = %s (%v)", up, err)
	}
	if _, err := os.Stat(filepath.Join(dir, stack.ComposeFile)); err == nil {
		t.Error("--runtime apple wrote a compose file")
	}
	r := runServer(t, "init", "--dir", t.TempDir(), "--runtime", "podman")
	if r.code == 0 || !strings.Contains(r.stderr, "podman") {
		t.Errorf("bad runtime accepted: %+v", r)
	}

	r = runServer(t, "init", "--dir", t.TempDir(), "--proxy", "caddy")
	if r.code == 0 || !strings.Contains(r.stderr, `"ok": false`) || !strings.Contains(r.stderr, "caddy") {
		t.Errorf("bad proxy accepted: %+v", r)
	}
	r = runServer(t, "init", "--dir", t.TempDir(), "--port", "70000")
	if r.code == 0 {
		t.Errorf("port 70000 accepted: %+v", r)
	}
}

func TestServerImageTag(t *testing.T) {
	if serverImageTag("") != "latest" || serverImageTag("1.1.0") != "v1.1.0" {
		t.Error("serverImageTag")
	}
}

// Keep the json import for a result shape check without a server.
var _ = json.Unmarshal
