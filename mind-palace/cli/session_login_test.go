package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// cloudStub is the slice of a clued cloud the login commands talk to.
type cloudStub struct {
	srv     *httptest.Server
	polls   atomic.Int32
	approve int32 // deliver the token on this poll
	revoked atomic.Bool
	config  map[string]any
	deny    bool
	plain   bool // behave like a plain clued server: no cloud routes
}

func newCloudStub(t *testing.T) *cloudStub {
	t.Helper()
	c := &cloudStub{approve: 2, config: map[string]any{"disabledEnrichers": []string{"privacy-redact"}, "projectsDir": "~/p"}}
	mux := http.NewServeMux()
	env := func(w http.ResponseWriter, status int, body map[string]any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}
	bearer := func(r *http.Request) string { return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") }
	notFound := func(w http.ResponseWriter, r *http.Request) {
		env(w, 404, map[string]any{"ok": false, "error": map[string]string{"kind": "not_found", "message": "no route"}})
	}
	mux.HandleFunc("POST /api/v1/device/code", func(w http.ResponseWriter, r *http.Request) {
		if c.plain {
			notFound(w, r)
			return
		}
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in["client_name"] != "laptop" || in["client_info"].(map[string]any)["os"] == nil {
			t.Errorf("device/code body = %v", in)
		}
		env(w, 200, map[string]any{"ok": true, "result": map[string]any{
			"device_code": "dev-code", "user_code": "BCDF-GHJK", "verification_uri": c.srv.URL + "/link",
			"verification_uri_complete": c.srv.URL + "/link?code=BCDF-GHJK", "expires_in": 900, "interval": 1,
		}})
	})
	mux.HandleFunc("POST /api/v1/device/token", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in["device_code"] != "dev-code" {
			env(w, 400, map[string]any{"ok": false, "error": map[string]string{"kind": "invalid_grant", "message": "unknown"}})
			return
		}
		n := c.polls.Add(1)
		switch {
		case c.deny:
			env(w, 400, map[string]any{"ok": false, "error": map[string]string{"kind": "access_denied", "message": "denied"}})
		case n < c.approve:
			env(w, 400, map[string]any{"ok": false, "error": map[string]string{"kind": "authorization_pending", "message": "pending"}})
		default:
			env(w, 200, map[string]any{"ok": true, "result": map[string]any{"token": "cloud-token", "token_id": "t1", "name": "laptop", "account_id": "0123456789abcdef"}})
		}
	})
	mux.HandleFunc("GET /api/v1/whoami", func(w http.ResponseWriter, r *http.Request) {
		if bearer(r) != "cloud-token" || c.revoked.Load() {
			env(w, 401, map[string]any{"ok": false, "error": map[string]string{"kind": "unauthorized", "message": "unknown token"}})
			return
		}
		env(w, 200, map[string]any{"ok": true, "result": map[string]any{"name": "laptop", "account_id": "0123456789abcdef", "projects": []string{"*"}, "version": "cloud-test"}})
	})
	mux.HandleFunc("GET /api/v1/me/config", func(w http.ResponseWriter, r *http.Request) {
		if c.plain {
			notFound(w, r)
			return
		}
		if bearer(r) != "cloud-token" {
			env(w, 401, map[string]any{"ok": false, "error": map[string]string{"kind": "unauthorized", "message": "unknown token"}})
			return
		}
		env(w, 200, map[string]any{"ok": true, "result": map[string]any{"client_config": c.config, "updated_at": "2026-10-13T12:00:00Z"}})
	})
	mux.HandleFunc("DELETE /api/v1/me/tokens/current", func(w http.ResponseWriter, r *http.Request) {
		if c.plain {
			notFound(w, r)
			return
		}
		if bearer(r) != "cloud-token" || c.revoked.Load() {
			env(w, 401, map[string]any{"ok": false, "error": map[string]string{"kind": "unauthorized", "message": "unknown token"}})
			return
		}
		c.revoked.Store(true)
		env(w, 200, map[string]any{"ok": true, "result": map[string]any{"revoked": "t1", "current": true}})
	})
	mux.HandleFunc("/", notFound)
	c.srv = httptest.NewServer(mux)
	t.Cleanup(c.srv.Close)
	return c
}

func readConfigDoc(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestSessionLoginLinksAndWritesTheConfig(t *testing.T) {
	stub := newCloudStub(t)
	configPath := filepath.Join(t.TempDir(), "data", "config.json")
	_ = os.MkdirAll(filepath.Dir(configPath), 0o700)
	_ = os.WriteFile(configPath, []byte(`{"mongoUrl":"mongodb://x:1","port":9999,"serverCaFile":"/old/ca.pem"}`), 0o600)

	r := execStdin(t, "", "session", "--config", configPath, "login", "--server", stub.srv.URL, "--name", "laptop")
	if r.code != 0 {
		t.Fatalf("login failed: %+v", r)
	}
	if !strings.Contains(r.stderr, stub.srv.URL+"/link?code=BCDF-GHJK") || !strings.Contains(r.stderr, "BCDF-GHJK") {
		t.Errorf("the link and code were not printed: %q", r.stderr)
	}
	res := result(t, r)
	if res["server"] != stub.srv.URL || res["name"] != "laptop" || res["account_id"] != "0123456789abcdef" || res["token_id"] != "t1" || res["config"] != configPath {
		t.Errorf("result = %v", res)
	}
	if stub.polls.Load() < 2 {
		t.Errorf("polls = %d, want the pending answer first", stub.polls.Load())
	}
	doc := readConfigDoc(t, configPath)
	if doc["serverUrl"] != stub.srv.URL || doc["serverToken"] != "cloud-token" || doc["mongoUrl"] != "mongodb://x:1" || doc["port"] != float64(9999) || doc["serverCaFile"] != nil {
		t.Errorf("config = %v", doc)
	}
	if info, _ := os.Stat(configPath); info.Mode().Perm() != 0o600 {
		t.Errorf("config mode = %o", info.Mode().Perm())
	}

	// The linked client's whoami works through the written config.
	if res := sessionResult(t, runWhoami(t, configPath)).(map[string]any); res["name"] != "laptop" {
		t.Errorf("whoami after login = %v", res)
	}

	// config-pull applies the cloud's client settings and keeps the rest.
	r = execStdin(t, "", "session", "--config", configPath, "config-pull")
	if r.code != 0 {
		t.Fatalf("config-pull: %+v", r)
	}
	res = result(t, r)
	if res["supported"] != true || len(res["updated"].([]any)) != 2 {
		t.Errorf("config-pull result = %v", res)
	}
	doc = readConfigDoc(t, configPath)
	if doc["projectsDir"] != "~/p" || len(doc["disabledEnrichers"].([]any)) != 1 || doc["serverToken"] != "cloud-token" || doc["port"] != float64(9999) {
		t.Errorf("config after pull = %v", doc)
	}
	r = execStdin(t, "", "session", "--config", configPath, "config-pull", "--quiet")
	if r.code != 0 || r.stdout != "" {
		t.Errorf("quiet pull: %+v", r)
	}

	// logout revokes and leaves remote mode.
	r = execStdin(t, "", "session", "--config", configPath, "logout")
	if r.code != 0 {
		t.Fatalf("logout: %+v", r)
	}
	if res := result(t, r); res["revoked"] != true {
		t.Errorf("logout result = %v", res)
	}
	doc = readConfigDoc(t, configPath)
	if doc["serverUrl"] != nil || doc["serverToken"] != nil || doc["projectsDir"] != "~/p" || doc["mongoUrl"] != "mongodb://x:1" {
		t.Errorf("config after logout = %v", doc)
	}
	if !stub.revoked.Load() {
		t.Error("the token was not revoked on the cloud")
	}
	if r := execStdin(t, "", "session", "--config", configPath, "logout"); r.code == 0 || !strings.Contains(r.stderr, "no server configured") {
		t.Errorf("logout without a server: %+v", r)
	}
}

func TestSessionLoginDeniedAndNotACloud(t *testing.T) {
	stub := newCloudStub(t)
	stub.deny = true
	configPath := filepath.Join(t.TempDir(), "config.json")
	r := execStdin(t, "", "session", "--config", configPath, "login", "--server", stub.srv.URL, "--name", "laptop")
	if r.code == 0 || !strings.Contains(r.stderr, "denied") {
		t.Errorf("denied login: %+v", r)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Error("a denied login must not write the config")
	}
	if r := execStdin(t, "", "session", "--config", configPath, "login"); r.code == 0 || !strings.Contains(r.stderr, "--server is required") {
		t.Errorf("login without --server: %+v", r)
	}

	stub.plain = true
	r = execStdin(t, "", "session", "--config", configPath, "login", "--server", stub.srv.URL, "--name", "laptop")
	if r.code == 0 || !strings.Contains(r.stderr, "no device login") {
		t.Errorf("login against a plain server: %+v", r)
	}
}

func TestSessionConfigPullAndLogoutOnAPlainServer(t *testing.T) {
	stub := newCloudStub(t)
	stub.plain = true
	configPath := filepath.Join(t.TempDir(), "config.json")
	_ = os.WriteFile(configPath, []byte(`{"serverUrl":"`+stub.srv.URL+`","serverToken":"cloud-token","port":1}`), 0o600)
	r := execStdin(t, "", "session", "--config", configPath, "config-pull")
	if r.code != 0 {
		t.Fatalf("config-pull on a plain server: %+v", r)
	}
	if res := result(t, r); res["remote"] != true || res["supported"] != false {
		t.Errorf("result = %v", res)
	}
	r = execStdin(t, "", "session", "--config", configPath, "logout", "--keep-config")
	if r.code != 0 {
		t.Fatalf("logout on a plain server: %+v", r)
	}
	if res := result(t, r); res["revoked"] != false || res["config_kept"] != true {
		t.Errorf("result = %v", res)
	}
	if doc := readConfigDoc(t, configPath); doc["serverUrl"] == nil {
		t.Error("--keep-config must leave the server keys")
	}

	// Not in remote mode at all: a quiet no-op.
	local := filepath.Join(t.TempDir(), "local.json")
	_ = os.WriteFile(local, []byte(`{"port":1}`), 0o600)
	r = execStdin(t, "", "session", "--config", local, "config-pull")
	if r.code != 0 {
		t.Fatalf("config-pull in local mode: %+v", r)
	}
	if res := result(t, r); res["remote"] != false {
		t.Errorf("result = %v", res)
	}
}

func TestSessionConfigPullWritesRedactionKeysAndEnforcesManaged(t *testing.T) {
	for name, cfg := range map[string]map[string]any{
		// The cloud's effective config: managed keys inside client_config…
		"inside client_config": {
			"redactAtCapture": true, "redactPatterns": []string{`acme-[0-9a-f]{32}`}, "excludePaths": []string{"~/secret/**"},
			"enabledEnrichers": []string{"privacy-redact"}, "projectsDir": "~/p",
			"managed":   []string{"redactAtCapture", "redactPatterns", "excludePaths", "enabledEnrichers"},
			"managedBy": map[string]string{"redactAtCapture": "Acme", "redactPatterns": "Acme", "excludePaths": "Acme", "enabledEnrichers": "Acme"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			stub := newCloudStub(t)
			stub.config = cfg
			configPath := filepath.Join(t.TempDir(), "config.json")
			// The user loosened the policy locally and has a key of their own.
			_ = os.WriteFile(configPath, []byte(`{"serverUrl":"`+stub.srv.URL+`","serverToken":"cloud-token","redactAtCapture":false,"redactPatterns":[],"enabledEnrichers":["privacy-redact"],"port":7}`), 0o600)
			r := execStdin(t, "", "session", "--config", configPath, "config-pull", "--quiet")
			if r.code != 0 || r.stdout != "" {
				t.Fatalf("config-pull --quiet: %+v", r)
			}
			for _, key := range []string{"redactAtCapture", "redactPatterns"} {
				if !strings.Contains(r.stderr, key+" is managed by Acme") {
					t.Errorf("no note for the overwritten %s: %q", key, r.stderr)
				}
			}
			for _, key := range []string{"excludePaths", "enabledEnrichers"} { // absent or already equal locally
				if strings.Contains(r.stderr, key+" is managed") {
					t.Errorf("a note for %s although nothing local was replaced: %q", key, r.stderr)
				}
			}
			doc := readConfigDoc(t, configPath)
			if doc["redactAtCapture"] != true || len(doc["redactPatterns"].([]any)) != 1 || doc["excludePaths"].([]any)[0] != "~/secret/**" || doc["port"] != float64(7) || doc["projectsDir"] != "~/p" {
				t.Errorf("config after pull = %v", doc)
			}
			if _, leaked := doc["managed"]; leaked {
				t.Errorf("the managed list was written into config.json: %v", doc)
			}
			r = execStdin(t, "", "session", "--config", configPath, "config-pull")
			res := result(t, r)
			if len(res["managed"].([]any)) != 4 || len(res["overridden"].([]any)) != 0 {
				t.Errorf("second pull result = %v", res)
			}
		})
	}
}
