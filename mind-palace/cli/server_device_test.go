package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/server"
	"github.com/robbiebyrd/clued/mind-palace/server/auth"
	"github.com/robbiebyrd/clued/mind-palace/server/device"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
	"github.com/robbiebyrd/clued/mind-palace/store"
	"github.com/robbiebyrd/clued/mind-palace/store/memstore"
)

// deviceServer runs a clued server with the device login over a token file
// holding the token "star"; the server's URL and that token are returned.
func deviceServer(t *testing.T) (string, string) {
	t.Helper()
	tf := auth.OpenTokenFile(filepath.Join(t.TempDir(), "tokens.json"))
	star, err := tf.Create("star", "", []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	c := server.NewClued(server.CluedOptions{
		Version:  "test",
		Tokens:   tf,
		Sessions: memsession.New(),
		Open:     func(_ context.Context, project string) (store.Store, error) { return memstore.New(project), nil },
		Config:   config.Default(),
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Device:   device.New(device.Options{Tokens: tf}),
	})
	t.Cleanup(c.Close)
	ts := httptest.NewServer(c.Handler)
	t.Cleanup(ts.Close)
	return ts.URL, star
}

// deviceStartRequest asks the server for a code the way `session login` does.
func deviceStartRequest(t *testing.T, url string) (deviceCode, userCode string) {
	t.Helper()
	resp, err := http.Post(url+server.PathDeviceCode, "application/json", strings.NewReader(`{"client_name": "laptop"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var env struct {
		OK     bool `json:"ok"`
		Result struct {
			DeviceCode string `json:"device_code"`
			UserCode   string `json:"user_code"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil || !env.OK {
		t.Fatalf("device/code: %d %v", resp.StatusCode, err)
	}
	return env.Result.DeviceCode, env.Result.UserCode
}

func TestServerDeviceCommandsApproveAndDenyOverTheAPI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLUED_SERVER_URL", "")
	t.Setenv("CLUED_SERVER_TOKEN", "")
	url, star := deviceServer(t)

	if list := serverList(t, runServer(t, "device", "list", "--server", url, "--token", star)); len(list) != 0 {
		t.Errorf("nothing should be waiting: %v", list)
	}
	deviceCode, userCode := deviceStartRequest(t, url)
	list := serverList(t, runServer(t, "device", "list", "--server", url, "--token", star))
	if len(list) != 1 || list[0]["user_code"] != userCode || list[0]["client_name"] != "laptop" || list[0]["state"] != "pending" {
		t.Errorf("list = %v", list)
	}

	// The approver may narrow the projects to its own; a project it does
	// not hold is refused.
	r := runServer(t, "device", "approve", userCode, "--server", url, "--token", star, "--project", "zzz")
	if r.code == 0 || !strings.Contains(r.stderr, "does not cover") {
		t.Errorf("approving a project the token lacks: %+v", r)
	}
	res := serverResult(t, runServer(t, "device", "approve", strings.ToLower(userCode), "--server", url, "--token", star, "--project", "a", "--name", "desk"))
	if res["name"] != "desk" || res["user_code"] != userCode {
		t.Errorf("approve = %v", res)
	}
	if p, _ := res["projects"].([]any); len(p) != 1 || p[0] != "a" {
		t.Errorf("approve projects = %v", res["projects"])
	}

	// The client's poll receives the token and it authenticates.
	resp, err := http.Post(url+server.PathDeviceToken, "application/json", strings.NewReader(`{"device_code": "`+deviceCode+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var link struct {
		Result struct {
			Token string `json:"token"`
			Name  string `json:"name"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &link); err != nil || resp.StatusCode != 200 || link.Result.Token == "" || link.Result.Name != "desk" {
		t.Fatalf("poll: %d %s", resp.StatusCode, body)
	}
	who := serverResultOf(t, runServer(t, "device", "list", "--server", url, "--token", link.Result.Token))
	if who == nil {
		t.Error("the minted token should list requests")
	}

	// Denying an unknown code is NotFound; denying a real one is reported.
	r = runServer(t, "device", "deny", "BCDF-GHJK", "--server", url, "--token", star)
	if r.code != 4 {
		t.Errorf("deny unknown: %+v", r)
	}
	_, userCode = deviceStartRequest(t, url)
	res = serverResult(t, runServer(t, "device", "deny", userCode, "--server", url, "--token", star))
	if res["denied"] != true || res["user_code"] != userCode {
		t.Errorf("deny = %v", res)
	}

	// Without a server in the config or on the command line there is
	// nothing to talk to.
	r = runServer(t, "device", "list")
	if r.code != 2 || !strings.Contains(r.stderr, "--server is required") {
		t.Errorf("no server: %+v", r)
	}
	r = runServer(t, "device", "list", "--server", url)
	if r.code != 2 || !strings.Contains(r.stderr, "--token is required") {
		t.Errorf("no token: %+v", r)
	}
}

// serverResultOf decodes a successful run's result as JSON, nil on failure.
func serverResultOf(t *testing.T, r run) any {
	t.Helper()
	if r.code != 0 {
		return nil
	}
	var env struct {
		Result any `json:"result"`
	}
	if err := json.NewDecoder(bytes.NewReader([]byte(r.stdout))).Decode(&env); err != nil {
		t.Fatalf("decode: %v\n%s", err, r.stdout)
	}
	return env.Result
}
