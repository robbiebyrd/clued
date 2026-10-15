package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func tokenPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "srv", "tokens.json")
}

func mustCreate(t *testing.T, tf *TokenFile, name, account string, projects ...string) string {
	t.Helper()
	raw, err := tf.Create(name, account, projects)
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	return raw
}

func TestTokenFileRoundTripMode(t *testing.T) {
	path := tokenPath(t)
	tf := OpenTokenFile(path)
	raw := mustCreate(t, tf, "laptop", "", "alpha", "beta")
	if raw == "" {
		t.Fatal("empty raw token")
	}
	if b, err := base64.RawURLEncoding.DecodeString(raw); err != nil || len(b) != 32 {
		t.Fatalf("raw token is not 32 base64url bytes: %q (%v)", raw, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), raw) {
		t.Fatal("token file contains the raw token")
	}
	var file struct {
		Tokens []Token `json:"tokens"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("file is not {\"tokens\":[...]}: %v", err)
	}
	if len(file.Tokens) != 1 {
		t.Fatalf("tokens = %+v", file.Tokens)
	}

	again, err := OpenTokenFile(path).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 1 || again[0].Name != "laptop" || !reflect.DeepEqual(again[0].Projects, []string{"alpha", "beta"}) {
		t.Fatalf("list after reopen = %+v", again)
	}
	if again[0].AccountID == "" {
		t.Fatal("empty account id was not replaced by a generated one")
	}
	if again[0].Created.IsZero() || again[0].Revoked != nil {
		t.Fatalf("created/revoked = %v/%v", again[0].Created, again[0].Revoked)
	}
	// Explicit account ids are kept and names are unique.
	mustCreate(t, tf, "desktop", "acct-9", "*")
	list, _ := tf.List()
	if len(list) != 2 || list[1].AccountID != "acct-9" {
		t.Fatalf("list = %+v", list)
	}
	if _, err := tf.Create("desktop", "", nil); err == nil {
		t.Fatal("duplicate name accepted")
	}
	if _, err := tf.Create("", "", nil); err == nil {
		t.Fatal("empty name accepted")
	}
}

func TestHashIsSHA256Hex(t *testing.T) {
	tf := OpenTokenFile(tokenPath(t))
	raw := mustCreate(t, tf, "a", "acct", "p")
	list, _ := tf.List()
	sum := sha256.Sum256([]byte(raw))
	if want := hex.EncodeToString(sum[:]); list[0].Hash != want {
		t.Fatalf("hash = %q, want sha256 hex %q", list[0].Hash, want)
	}
	if HashToken(raw) != list[0].Hash {
		t.Fatal("HashToken disagrees with the stored hash")
	}
}

func TestLookup(t *testing.T) {
	tf := OpenTokenFile(tokenPath(t))
	raw := mustCreate(t, tf, "a", "acct-1", "alpha")
	other := mustCreate(t, tf, "b", "acct-2", "*")

	p, ok := tf.Lookup(raw)
	if !ok {
		t.Fatal("valid token rejected")
	}
	want := Principal{Name: "a", AccountID: "acct-1", Projects: []string{"alpha"}}
	if !reflect.DeepEqual(p, want) {
		t.Fatalf("principal = %+v, want %+v", p, want)
	}
	if p, ok := tf.Lookup(other); !ok || p.Name != "b" {
		t.Fatalf("second token: %+v %v", p, ok)
	}
	// Near misses: a prefix, a different last byte, the hash itself, empty.
	for _, bad := range []string{raw[:len(raw)-1], raw[:len(raw)-1] + "!", HashToken(raw), "", "nope"} {
		if _, ok := tf.Lookup(bad); ok {
			t.Fatalf("lookup(%q) accepted", bad)
		}
	}
	// A missing file is an empty token set, not a panic.
	if _, ok := OpenTokenFile(filepath.Join(t.TempDir(), "none.json")).Lookup(raw); ok {
		t.Fatal("missing file accepted a token")
	}
}

func TestLookupIsConstantTime(t *testing.T) {
	// The comparison is subtle.ConstantTimeCompare on the hex digest, so the
	// result does not depend on how long a prefix of the hash matches. We
	// check the behaviour: every stored token is accepted whatever its
	// position in the file, a bogus token is rejected, and a digest that
	// shares a prefix with a stored one is not a match.
	tf := OpenTokenFile(tokenPath(t))
	var raws []string
	for i := range 20 {
		raws = append(raws, mustCreate(t, tf, "t"+string(rune('a'+i)), "", "p"))
	}
	for i, raw := range raws {
		if p, ok := tf.Lookup(raw); !ok || p.Name != "t"+string(rune('a'+i)) {
			t.Fatalf("token %d rejected", i)
		}
	}
	if _, ok := tf.Lookup(strings.Repeat("A", 43)); ok {
		t.Fatal("bogus token accepted")
	}
	if !matchHash(HashToken(raws[0]), HashToken(raws[0])) {
		t.Fatal("equal digests do not match")
	}
	h := HashToken(raws[0])
	// The near miss differs in the last hex digit only; it must not be the
	// digit the hash already ends in (one hash in sixteen ends in "0").
	last := "0"
	if h[len(h)-1] == '0' {
		last = "1"
	}
	if matchHash(h, h[:len(h)-1]+last) || matchHash(h, h[:len(h)-1]) || matchHash(h, "") {
		t.Fatal("near-miss digest matched")
	}
}

func TestRevokeAndGrant(t *testing.T) {
	path := tokenPath(t)
	tf := OpenTokenFile(path)
	raw := mustCreate(t, tf, "a", "acct", "alpha")
	if err := tf.Grant("a", "beta", "alpha", "gamma"); err != nil {
		t.Fatal(err)
	}
	p, ok := tf.Lookup(raw)
	if !ok || !reflect.DeepEqual(p.Projects, []string{"alpha", "beta", "gamma"}) {
		t.Fatalf("after grant: %+v %v", p, ok)
	}
	if err := tf.Grant("missing", "x"); err == nil {
		t.Fatal("grant to unknown token succeeded")
	}
	if err := tf.Revoke("missing"); err == nil {
		t.Fatal("revoke of unknown token succeeded")
	}
	if err := tf.Revoke("a"); err != nil {
		t.Fatal(err)
	}
	if _, ok := tf.Lookup(raw); ok {
		t.Fatal("revoked token still accepted")
	}
	list, _ := tf.List()
	if list[0].Revoked == nil {
		t.Fatal("revoked timestamp not stored")
	}
	if err := tf.Grant("a", "delta"); err == nil {
		t.Fatal("grant to a revoked token succeeded")
	}
	// Revocation survives a reopen.
	if _, ok := OpenTokenFile(path).Lookup(raw); ok {
		t.Fatal("revoked token accepted after reopen")
	}
}

func TestReloadOnMtimeChange(t *testing.T) {
	path := tokenPath(t)
	writer := OpenTokenFile(path)
	raw := mustCreate(t, writer, "a", "acct", "alpha")

	server := OpenTokenFile(path)
	if _, ok := server.Lookup(raw); !ok {
		t.Fatal("token rejected before change")
	}
	// Another process revokes the token: the file changes and its mtime moves.
	if err := writer.Revoke("a"); err != nil {
		t.Fatal(err)
	}
	bump := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, bump, bump); err != nil {
		t.Fatal(err)
	}
	if _, ok := server.Lookup(raw); ok {
		t.Fatal("revocation not picked up after mtime change")
	}
	// A new token appears the same way.
	raw2 := mustCreate(t, writer, "b", "acct", "*")
	bump = bump.Add(2 * time.Second)
	if err := os.Chtimes(path, bump, bump); err != nil {
		t.Fatal(err)
	}
	if p, ok := server.Lookup(raw2); !ok || p.Name != "b" {
		t.Fatalf("new token not picked up: %+v %v", p, ok)
	}
}

func TestTokenFileRejectsMalformed(t *testing.T) {
	path := tokenPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	tf := OpenTokenFile(path)
	if _, err := tf.List(); err == nil {
		t.Fatal("malformed file listed without error")
	}
	if _, ok := tf.Lookup("x"); ok {
		t.Fatal("malformed file accepted a token")
	}
	if _, err := tf.Create("a", "", nil); err == nil {
		t.Fatal("create overwrote a malformed file")
	}
}

type stubSource map[string]Principal

func (s stubSource) Lookup(raw string) (Principal, bool) {
	p, ok := s[raw]
	return p, ok
}

func TestMiddleware(t *testing.T) {
	src := stubSource{"good": {Name: "n", AccountID: "acct", Projects: []string{"alpha"}}}
	var seen Principal
	var seenOK bool
	h := Middleware(src)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, seenOK = FromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))

	do := func(header string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		seen, seenOK = Principal{}, false
		h.ServeHTTP(rec, req)
		return rec
	}

	rec := do("Bearer good")
	if rec.Code != http.StatusNoContent || !seenOK || seen.Name != "n" || seen.AccountID != "acct" {
		t.Fatalf("valid token: code %d principal %+v %v", rec.Code, seen, seenOK)
	}
	if rec := do("bearer good"); rec.Code != http.StatusNoContent {
		t.Fatalf("scheme should be case-insensitive: %d", rec.Code)
	}

	for name, header := range map[string]string{
		"missing":   "",
		"basic":     "Basic Zm9vOmJhcg==",
		"no token":  "Bearer",
		"spaces":    "Bearer good extra",
		"unknown":   "Bearer nope",
		"bare":      "good",
		"empty tok": "Bearer ",
	} {
		rec := do(header)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s: code = %d, want 401", name, rec.Code)
		}
		if seenOK {
			t.Fatalf("%s: handler ran", name)
		}
		if got := rec.Header().Get("WWW-Authenticate"); got != "Bearer" {
			t.Fatalf("%s: WWW-Authenticate = %q", name, got)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Fatalf("%s: content-type = %q", name, ct)
		}
		var body struct {
			OK    bool `json:"ok"`
			Error struct {
				Kind    string `json:"kind"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: body %q: %v", name, rec.Body.String(), err)
		}
		if body.OK || body.Error.Kind != "unauthorized" || body.Error.Message == "" {
			t.Fatalf("%s: body = %+v", name, body)
		}
	}
}

func TestMiddlewareRevokedTokenIs401(t *testing.T) {
	tf := OpenTokenFile(tokenPath(t))
	raw := mustCreate(t, tf, "a", "acct", "alpha")
	h := Middleware(tf)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("before revoke: %d", rec.Code)
	}
	if err := tf.Revoke("a"); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("after revoke: %d %q", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}
}

func TestContext(t *testing.T) {
	if _, ok := FromContext(httptest.NewRequest("GET", "/", nil).Context()); ok {
		t.Fatal("principal found in an empty context")
	}
	p := Principal{Name: "n", AccountID: "a", Projects: []string{"*"}}
	got, ok := FromContext(WithPrincipal(httptest.NewRequest("GET", "/", nil).Context(), p))
	if !ok || !reflect.DeepEqual(got, p) {
		t.Fatalf("got %+v %v", got, ok)
	}
}

func TestAllowProject(t *testing.T) {
	listed := Principal{Projects: []string{"alpha", "beta"}}
	if !AllowProject(listed, "alpha") || !AllowProject(listed, "beta") {
		t.Fatal("listed slug rejected")
	}
	if AllowProject(listed, "gamma") || AllowProject(listed, "") || AllowProject(listed, "alph") {
		t.Fatal("unlisted slug accepted")
	}
	star := Principal{Projects: []string{"*"}}
	if !AllowProject(star, "anything") || !AllowProject(star, "alpha") {
		t.Fatal("* did not admit a project")
	}
	if AllowProject(Principal{}, "alpha") || AllowProject(Principal{}, "*") {
		t.Fatal("principal without projects accepted a project")
	}
	// A literal "*" slug is only admitted by a "*" grant.
	if AllowProject(listed, "*") {
		t.Fatal("* slug admitted by a listed grant")
	}
}

func TestForbid(t *testing.T) {
	rec := httptest.NewRecorder()
	Forbid(rec, "project gamma is not in the token's list")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	e, _ := body["error"].(map[string]any)
	if body["ok"] != false || e["kind"] != "forbidden" || e["message"] != "project gamma is not in the token's list" {
		t.Fatalf("body = %v", body)
	}
}

func TestWhoami(t *testing.T) {
	p := Principal{Name: "laptop", AccountID: "acct-1", Projects: []string{"alpha", "beta"}}
	h := Middleware(stubSource{"tok": p})(WhoamiHandler("1.2.3"))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/whoami", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		OK     bool `json:"ok"`
		Result struct {
			Name      string   `json:"name"`
			AccountID string   `json:"account_id"`
			Projects  []string `json:"projects"`
			Version   string   `json:"version"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || body.Result.Name != "laptop" || body.Result.AccountID != "acct-1" || !reflect.DeepEqual(body.Result.Projects, []string{"alpha", "beta"}) || body.Result.Version != "1.2.3" {
		t.Fatalf("body = %+v", body)
	}

	if strings.Contains(rec.Body.String(), "assignee") {
		t.Errorf("assignee without a suggestion: %s", rec.Body.String())
	}
	rec = httptest.NewRecorder()
	WhoamiHandlerWith("1.2.3", func(_ context.Context, p Principal) string { return "github:" + p.Name }).ServeHTTP(rec, req.WithContext(WithPrincipal(req.Context(), p)))
	if !strings.Contains(rec.Body.String(), `"assignee": "github:laptop"`) {
		t.Errorf("whoami with an assignee: %s", rec.Body.String())
	}

	// No projects serialises as [] and the handler alone answers 401.
	rec = httptest.NewRecorder()
	WhoamiHandler("v").ServeHTTP(rec, req.WithContext(WithPrincipal(req.Context(), Principal{Name: "x"})))
	if !strings.Contains(rec.Body.String(), `"projects":[]`) && !strings.Contains(rec.Body.String(), `"projects": []`) {
		t.Fatalf("empty projects: %s", rec.Body.String())
	}
	rec = httptest.NewRecorder()
	WhoamiHandler("v").ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("no principal: %d", rec.Code)
	}
}
