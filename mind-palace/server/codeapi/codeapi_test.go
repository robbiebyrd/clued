package codeapi_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/code/codetest"
	"github.com/robbiebyrd/clued/mind-palace/code/memcode"
	"github.com/robbiebyrd/clued/mind-palace/server"
	"github.com/robbiebyrd/clued/mind-palace/server/auth"
	"github.com/robbiebyrd/clued/mind-palace/server/codeapi"
	"github.com/robbiebyrd/clued/mind-palace/server/httpapi"
	"github.com/robbiebyrd/clued/mind-palace/server/projects"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
	"github.com/robbiebyrd/clued/mind-palace/store"
	"github.com/robbiebyrd/clued/mind-palace/store/memstore"
)

const (
	tokenAlice = "tok-alice"
	tokenBob   = "tok-bob"
	tokenOnlyA = "tok-only-a"
	tokenSuite = "tok-suite"

	project = "github.com__org__repo"
	wt1     = "wt_0123456789abcdef"
	oid1    = "1111111111111111111111111111111111111111"
	oid2    = "2222222222222222222222222222222222222222"
)

// tokens is the auth.Source of the tests.
type tokens map[string]auth.Principal

func (s tokens) Lookup(raw string) (auth.Principal, bool) {
	p, ok := s[raw]
	return p, ok
}

var testTokens = tokens{
	tokenAlice: {Name: "alice", AccountID: "acct-alice", Projects: []string{"*"}},
	tokenBob:   {Name: "bob", AccountID: "acct-bob", Projects: []string{"*"}},
	tokenOnlyA: {Name: "only-a", AccountID: "acct-alice", Projects: []string{"a"}},
	tokenSuite: {Name: "suite", AccountID: codetest.Owner, Projects: []string{"*"}},
}

// stores hands out one memcode store per key and records the keys opened.
type stores struct {
	mu     sync.Mutex
	byKey  map[string]*memcode.Store
	opened []string
}

func newStores() *stores { return &stores{byKey: map[string]*memcode.Store{}} }

func (s *stores) Open(_ context.Context, key string) (code.Store, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opened = append(s.opened, key)
	st, ok := s.byKey[key]
	if !ok {
		st = memcode.New()
		s.byKey[key] = st
	}
	return st, nil
}

func (s *stores) get(key string) *memcode.Store {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.byKey[key]
	if !ok {
		st = memcode.New()
		s.byKey[key] = st
	}
	return st
}

// newServer mounts codeapi.Handler behind auth.Middleware, as a remote
// adapter's test does. opts.Open defaults to st.Open.
func newServer(t *testing.T, st *stores, opts codeapi.Options) *httptest.Server {
	t.Helper()
	if opts.Open == nil {
		opts.Open = st.Open
	}
	ts := httptest.NewServer(auth.Middleware(testTokens)(codeapi.Handler(opts)))
	t.Cleanup(ts.Close)
	return ts
}

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Kind    string `json:"kind"`
		Message string `json:"message"`
	} `json:"error"`
}

func (e envelope) kind() string {
	if e.Error == nil {
		return ""
	}
	return e.Error.Kind
}

// call sends one request with token; body is a string, []byte or a value
// marshalled as JSON.
func call(t *testing.T, base, token, method, path string, body any) (int, envelope) {
	t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	case []byte:
		rd = bytes.NewReader(b)
	default:
		buf, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, base+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var env envelope
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("%s %s: %d, content type %q, body %s", method, path, resp.StatusCode, ct, raw)
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("%s %s: %v in %s", method, path, err, raw)
	}
	var compact bytes.Buffer
	if json.Compact(&compact, env.Result) == nil {
		env.Result = compact.Bytes()
	}
	return resp.StatusCode, env
}

func expect(t *testing.T, what string, status int, env envelope, wantStatus int, wantKind string) {
	t.Helper()
	if status != wantStatus || env.kind() != wantKind {
		msg := ""
		if env.Error != nil {
			msg = env.Error.Message
		}
		t.Fatalf("%s: got %d %q (%s), want %d %q", what, status, env.kind(), msg, wantStatus, wantKind)
	}
}

func codePath(rest string) string { return "/api/v1/projects/" + project + "/code/" + rest }

func sha(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func cleanEntry(path, content string) map[string]any {
	return map[string]any{"path": path, "sha256": sha(content), "size": len(content), "mode": "100644", "lang": "go", "git_oid": oid1}
}

func changeBody(entries ...map[string]any) map[string]any {
	ups := []map[string]any{}
	ups = append(ups, entries...)
	return map[string]any{"ref": "main", "head": oid1, "upstream": oid2, "session_id": "sess-1", "upserts": ups}
}

func worktreeBody() map[string]any {
	return map[string]any{"root": "/home/dev/repo", "host": "devbox", "origin": "git@github.com:org/repo.git", "default_branch": "main", "ref": "main", "head": oid1}
}

// TestRoundTrip covers each write route and its stamping.
func TestRoundTrip(t *testing.T) {
	st := newStores()
	ts := newServer(t, st, codeapi.Options{})
	mem := st.get(project)
	ctx := t.Context()

	status, env := call(t, ts.URL, tokenAlice, "POST", codePath("manifest"), map[string]any{"shas": []string{sha("a"), sha("b")}})
	expect(t, "manifest", status, env, 200, "")
	if got := string(env.Result); !strings.Contains(got, sha("a")) || !strings.Contains(got, sha("b")) {
		t.Fatalf("manifest result %s", got)
	}

	status, env = call(t, ts.URL, tokenAlice, "PUT", codePath("blobs/"+sha("a")), "a")
	expect(t, "put blob", status, env, 200, "")
	if string(env.Result) != "{}" {
		t.Fatalf("put blob result %s", env.Result)
	}
	status, env = call(t, ts.URL, tokenAlice, "POST", codePath("manifest"), map[string]any{"shas": []string{sha("b"), sha("a")}})
	expect(t, "manifest after upload", status, env, 200, "")
	var m codeapi.ManifestResult
	json.Unmarshal(env.Result, &m)
	if len(m.Missing) != 1 || m.Missing[0] != sha("b") {
		t.Fatalf("missing %v", m.Missing)
	}
	status, env = call(t, ts.URL, tokenAlice, "POST", codePath("manifest"), map[string]any{"shas": []string{sha("a")}})
	expect(t, "manifest none missing", status, env, 200, "")
	if string(env.Result) != `{"missing":[]}` {
		t.Fatalf("none missing: %s", env.Result)
	}

	status, env = call(t, ts.URL, tokenAlice, "PUT", codePath("worktrees/"+wt1), worktreeBody())
	expect(t, "put worktree", status, env, 200, "")
	ws, _ := mem.Worktrees(ctx)
	if len(ws) != 1 || ws[0].Owner != "acct-alice" || ws[0].Root != "/home/dev/repo" || ws[0].DefaultBranch != "main" || len(ws[0].Refs) != 1 || ws[0].Refs[0] != "main" {
		t.Fatalf("worktree %#v", ws)
	}

	status, env = call(t, ts.URL, tokenAlice, "POST", codePath("worktrees/"+wt1+"/changes"), changeBody(cleanEntry("a.go", "a")))
	expect(t, "commit", status, env, 200, "")
	if string(env.Result) != `{"seq":1}` {
		t.Fatalf("commit result %s", env.Result)
	}
	tree, _ := mem.Tree(ctx, wt1, "main")
	if len(tree) != 1 || tree[0].Session != "sess-1" || tree[0].SHA256 != sha("a") || tree[0].GitOID != oid1 {
		t.Fatalf("tree %#v", tree)
	}
	cs, _ := mem.Changes(ctx, 0, 10)
	if len(cs) != 1 || cs[0].Owner != "acct-alice" || cs[0].SessionID != "sess-1" || cs[0].Head != oid1 || cs[0].Upstream != oid2 {
		t.Fatalf("changes %#v", cs)
	}

	status, env = call(t, ts.URL, tokenAlice, "PUT", codePath("worktrees/"+wt1+"/sessions/sess-2"), map[string]any{"ended": true})
	expect(t, "touch session", status, env, 200, "")
	ws, _ = mem.Worktrees(ctx)
	ended := false
	for _, s := range ws[0].Sessions {
		if s.ID == "sess-2" {
			ended = s.Ended
		}
	}
	if !ended {
		t.Fatalf("sessions %#v", ws[0].Sessions)
	}

	status, env = call(t, ts.URL, tokenAlice, "DELETE", codePath("worktrees/"+wt1), nil)
	expect(t, "remove", status, env, 200, "")
	ws, _ = mem.Worktrees(ctx)
	if !ws[0].Removed {
		t.Fatalf("not removed: %#v", ws[0])
	}
}

func TestCapabilitiesAreLimitsOnly(t *testing.T) {
	ts := newServer(t, newStores(), codeapi.Options{MaxFileBytes: 1234, MaxBatch: 7})
	status, env := call(t, ts.URL, tokenAlice, "GET", codePath("capabilities"), nil)
	expect(t, "capabilities", status, env, 200, "")
	if got := string(env.Result); got != `{"code":{"maxFileBytes":1234,"maxBatch":7}}` {
		t.Fatalf("capabilities %s", got)
	}
	ts = newServer(t, newStores(), codeapi.Options{})
	_, env = call(t, ts.URL, tokenAlice, "GET", codePath("capabilities"), nil)
	if want := fmt.Sprintf(`{"code":{"maxFileBytes":%d,"maxBatch":%d}}`, codeapi.DefaultMaxFileBytes, codeapi.DefaultMaxBatch); string(env.Result) != want {
		t.Fatalf("default capabilities %s, want %s", env.Result, want)
	}
}

// TestNoReadRoute: no GET (or other) route returns blobs, trees, worktrees
// or changes, even for content the caller wrote.
func TestNoReadRoute(t *testing.T) {
	st := newStores()
	ts := newServer(t, st, codeapi.Options{})
	call(t, ts.URL, tokenAlice, "PUT", codePath("blobs/"+sha("a")), "a")
	call(t, ts.URL, tokenAlice, "PUT", codePath("worktrees/"+wt1), worktreeBody())
	call(t, ts.URL, tokenAlice, "POST", codePath("worktrees/"+wt1+"/changes"), changeBody(cleanEntry("a.go", "a")))
	for _, path := range []string{
		"", "manifest", "blobs", "blobs/" + sha("a"), "worktrees", "worktrees/" + wt1,
		"worktrees/" + wt1 + "/changes", "worktrees/" + wt1 + "/tree", "worktrees/" + wt1 + "/sessions/sess-1",
		"changes", "tree", "usage",
	} {
		for _, method := range []string{"GET", "HEAD"} {
			req, _ := http.NewRequest(method, ts.URL+codePath(path), nil)
			req.Header.Set("Authorization", "Bearer "+tokenAlice)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("%s %s: %d, want 404", method, path, resp.StatusCode)
			}
			if strings.Contains(string(raw), "a.go") || strings.Contains(string(raw), "/home/dev") {
				t.Errorf("%s %s leaked data: %s", method, path, raw)
			}
		}
	}
	status, env := call(t, ts.URL, tokenAlice, "POST", codePath("tree"), "{}")
	expect(t, "unknown POST", status, env, 404, "not_found")
}

func TestAuthenticationAndProject(t *testing.T) {
	st := newStores()
	ts := newServer(t, st, codeapi.Options{
		Allow: func(r *http.Request, project string) bool {
			p, ok := auth.FromContext(r.Context())
			return ok && auth.AllowProject(p, project)
		},
	})
	status, env := call(t, ts.URL, "", "GET", codePath("capabilities"), nil)
	if status != 401 {
		t.Fatalf("no token: %d %s", status, env.kind())
	}

	// Without auth.Middleware there is no principal: 401 in the envelope.
	bare := httptest.NewServer(codeapi.Handler(codeapi.Options{Open: st.Open}))
	t.Cleanup(bare.Close)
	status, env = call(t, bare.URL, "", "POST", codePath("manifest"), `{"shas":[]}`)
	expect(t, "no principal", status, env, 401, "unauthorized")

	status, env = call(t, ts.URL, tokenAlice, "POST", "/api/v1/projects/Bad%20Slug/code/manifest", `{"shas":[]}`)
	expect(t, "bad slug", status, env, 400, "invalid")
	status, env = call(t, ts.URL, tokenOnlyA, "POST", codePath("manifest"), `{"shas":[]}`)
	expect(t, "project not allowed", status, env, 403, "forbidden")
	status, env = call(t, ts.URL, tokenOnlyA, "POST", "/api/v1/projects/a/code/manifest", `{"shas":[]}`)
	expect(t, "allowed project", status, env, 200, "")
}

func TestNamespaceAndOpenErrors(t *testing.T) {
	st := newStores()
	var gotProject string
	ts := newServer(t, st, codeapi.Options{
		Namespace: func(ctx context.Context, project string) (string, error) {
			p, _ := auth.FromContext(ctx)
			gotProject = codeapi.Project(ctx)
			switch project {
			case "status":
				return "", &httpapi.StatusError{Status: http.StatusTeapot, Kind: "teapot", Message: "no"}
			case "forbidden":
				return "", fmt.Errorf("%w: removed from workspace", auth.ErrProjectForbidden)
			case "invalid":
				return "", fmt.Errorf("%w: bad", projects.ErrInvalidProject)
			case "unknown":
				return "", fmt.Errorf("%w: project", code.ErrNotFound)
			case "broken":
				return "", errors.New("database down")
			}
			return "u" + p.AccountID + "__" + project, nil
		},
	})
	for _, c := range []struct {
		project string
		status  int
		kind    string
	}{
		{"status", 418, "teapot"},
		{"forbidden", 403, "forbidden"},
		{"invalid", 400, "invalid"},
		{"unknown", 404, "not_found"},
		{"broken", 502, "store"},
	} {
		status, env := call(t, ts.URL, tokenAlice, "PUT", "/api/v1/projects/"+c.project+"/code/worktrees/"+wt1, worktreeBody())
		expect(t, c.project, status, env, c.status, c.kind)
	}
	if len(st.opened) != 0 {
		t.Fatalf("a failed namespace opened %v", st.opened)
	}

	// Each account's write lands in its own namespace.
	call(t, ts.URL, tokenAlice, "PUT", codePath("worktrees/"+wt1), worktreeBody())
	call(t, ts.URL, tokenBob, "PUT", codePath("worktrees/"+wt1), worktreeBody())
	if gotProject != project {
		t.Fatalf("Project(ctx) = %q", gotProject)
	}
	for _, acct := range []string{"acct-alice", "acct-bob"} {
		ws, _ := st.get("u" + acct + "__" + project).Worktrees(t.Context())
		if len(ws) != 1 || ws[0].Owner != acct {
			t.Fatalf("%s namespace holds %#v", acct, ws)
		}
	}
	if ws, _ := st.get(project).Worktrees(t.Context()); len(ws) != 0 {
		t.Fatalf("the bare slug's store was written: %#v", ws)
	}

	failing := newServer(t, st, codeapi.Options{Open: func(context.Context, string) (code.Store, error) {
		return nil, errors.New("mongo unreachable")
	}})
	status, env := call(t, failing.URL, tokenAlice, "GET", codePath("capabilities"), nil)
	expect(t, "open failure", status, env, 502, "store")
}

func TestOwnership(t *testing.T) {
	st := newStores()
	ts := newServer(t, st, codeapi.Options{})
	mem := st.get(project)
	call(t, ts.URL, tokenAlice, "PUT", codePath("blobs/"+sha("a")), "a")
	status, env := call(t, ts.URL, tokenAlice, "PUT", codePath("worktrees/"+wt1), worktreeBody())
	expect(t, "alice creates", status, env, 200, "")

	other := worktreeBody()
	other["root"] = "/home/bob/repo"
	status, env = call(t, ts.URL, tokenBob, "PUT", codePath("worktrees/"+wt1), other)
	expect(t, "bob overwrites", status, env, 404, "not_found")
	status, env = call(t, ts.URL, tokenBob, "POST", codePath("worktrees/"+wt1+"/changes"), changeBody(cleanEntry("a.go", "a")))
	expect(t, "bob commits", status, env, 404, "not_found")
	status, env = call(t, ts.URL, tokenBob, "PUT", codePath("worktrees/"+wt1+"/sessions/s1"), `{"ended":false}`)
	expect(t, "bob touches", status, env, 404, "not_found")
	status, env = call(t, ts.URL, tokenBob, "DELETE", codePath("worktrees/"+wt1), nil)
	expect(t, "bob removes", status, env, 404, "not_found")

	// Another owner's worktree answers exactly as an unknown one.
	const unknown = "wt_aaaaaaaaaaaaaaaa"
	_, unknownEnv := call(t, ts.URL, tokenBob, "DELETE", codePath("worktrees/"+unknown), nil)
	_, otherEnv := call(t, ts.URL, tokenBob, "DELETE", codePath("worktrees/"+wt1), nil)
	if strings.ReplaceAll(otherEnv.Error.Message, wt1, unknown) != unknownEnv.Error.Message {
		t.Fatalf("messages differ: %q vs %q", otherEnv.Error.Message, unknownEnv.Error.Message)
	}
	status, env = call(t, ts.URL, tokenBob, "POST", codePath("worktrees/"+unknown+"/changes"), changeBody(cleanEntry("a.go", "a")))
	expect(t, "commit to unknown", status, env, 404, "not_found")
	status, env = call(t, ts.URL, tokenBob, "PUT", codePath("worktrees/"+unknown+"/sessions/s1"), `{}`)
	expect(t, "touch unknown", status, env, 404, "not_found")

	ws, _ := mem.Worktrees(t.Context())
	if len(ws) != 1 || ws[0].Owner != "acct-alice" || ws[0].Root != "/home/dev/repo" || ws[0].Removed || len(ws[0].Sessions) != 0 {
		t.Fatalf("alice's worktree changed: %#v", ws)
	}
	if cs, _ := mem.Changes(t.Context(), 0, 10); len(cs) != 0 {
		t.Fatalf("changes %#v", cs)
	}

	// The owner still writes it, and a concurrent creation by two accounts
	// leaves one owner.
	status, env = call(t, ts.URL, tokenAlice, "POST", codePath("worktrees/"+wt1+"/changes"), changeBody(cleanEntry("a.go", "a")))
	expect(t, "alice commits", status, env, 200, "")
	const race = "wt_bbbbbbbbbbbbbbbb"
	var wg sync.WaitGroup
	codes := make([]int, 20)
	for i := range codes {
		tok := tokenAlice
		if i%2 == 1 {
			tok = tokenBob
		}
		wg.Go(func() {
			codes[i], _ = call(t, ts.URL, tok, "PUT", codePath("worktrees/"+race), worktreeBody())
		})
	}
	wg.Wait()
	ws, _ = mem.Worktrees(t.Context())
	var owner string
	for _, w := range ws {
		if w.ID == race {
			owner = w.Owner
		}
	}
	for i, c := range codes {
		mine := (i%2 == 0) == (owner == "acct-alice")
		if (mine && c != 200) || (!mine && c != 404) {
			t.Fatalf("race: request %d (owner %s) answered %d", i, owner, c)
		}
	}
}

func TestStampedFieldsAreRefused(t *testing.T) {
	st := newStores()
	ts := newServer(t, st, codeapi.Options{})
	call(t, ts.URL, tokenAlice, "PUT", codePath("blobs/"+sha("a")), "a")

	for _, owner := range []string{`"acct-alice"`, `"acct-bob"`, `null`, `""`} {
		body := `{"root":"/r","ref":"main","owner":` + owner + `}`
		status, env := call(t, ts.URL, tokenAlice, "PUT", codePath("worktrees/"+wt1), body)
		expect(t, "worktree owner "+owner, status, env, 400, "invalid")
	}
	call(t, ts.URL, tokenAlice, "PUT", codePath("worktrees/"+wt1), worktreeBody())

	withOwner := changeBody(cleanEntry("a.go", "a"))
	withOwner["owner"] = "acct-bob"
	status, env := call(t, ts.URL, tokenAlice, "POST", codePath("worktrees/"+wt1+"/changes"), withOwner)
	expect(t, "change owner", status, env, 400, "invalid")

	e := cleanEntry("a.go", "a")
	e["session"] = "forged"
	status, env = call(t, ts.URL, tokenAlice, "POST", codePath("worktrees/"+wt1+"/changes"), changeBody(e))
	expect(t, "entry session", status, env, 400, "invalid")

	unknownField := worktreeBody()
	unknownField["source"] = "github"
	status, env = call(t, ts.URL, tokenAlice, "PUT", codePath("worktrees/"+wt1), unknownField)
	expect(t, "unknown worktree field", status, env, 400, "invalid")

	if cs, _ := st.get(project).Changes(t.Context(), 0, 10); len(cs) != 0 {
		t.Fatalf("a refused change was committed: %#v", cs)
	}
}

func TestValidation(t *testing.T) {
	st := newStores()
	ts := newServer(t, st, codeapi.Options{MaxBatch: 3})
	call(t, ts.URL, tokenAlice, "PUT", codePath("blobs/"+sha("a")), "a")
	call(t, ts.URL, tokenAlice, "PUT", codePath("worktrees/"+wt1), worktreeBody())
	changes := codePath("worktrees/" + wt1 + "/changes")

	entry := func(edit func(map[string]any)) map[string]any {
		e := cleanEntry("ok.go", "a")
		edit(e)
		return changeBody(e)
	}
	change := func(edit func(map[string]any)) map[string]any {
		c := changeBody(cleanEntry("ok.go", "a"))
		edit(c)
		return c
	}
	for _, c := range []struct {
		name string
		body any
	}{
		{"parent path", entry(func(e map[string]any) { e["path"] = "../escape.go" })},
		{"absolute path", entry(func(e map[string]any) { e["path"] = "/etc/passwd" })},
		{"dot element", entry(func(e map[string]any) { e["path"] = "a/./b.go" })},
		{"backslash", entry(func(e map[string]any) { e["path"] = `a\b.go` })},
		{"NUL", entry(func(e map[string]any) { e["path"] = "a\x00b.go" })},
		{"too long path", entry(func(e map[string]any) { e["path"] = strings.Repeat("a", code.MaxPathBytes+1) })},
		{"uppercase sha256", entry(func(e map[string]any) { e["sha256"] = strings.ToUpper(sha("a")) })},
		{"malformed git oid", entry(func(e map[string]any) { e["git_oid"] = "xyz" })},
		{"clean entry without git oid", entry(func(e map[string]any) { delete(e, "git_oid") })},
		{"negative size", entry(func(e map[string]any) { e["size"] = -1 })},
		{"bad mode", entry(func(e map[string]any) { e["mode"] = "rwx" })},
		{"control character in lang", entry(func(e map[string]any) { e["lang"] = "go\n" })},
		{"pushed entry without upstream", change(func(c map[string]any) { delete(c, "upstream") })},
		{"bad delete", change(func(c map[string]any) { c["deletes"] = []string{"../x"} })},
		{"empty ref", change(func(c map[string]any) { c["ref"] = "" })},
		{"bad ref", change(func(c map[string]any) { c["ref"] = "bad..ref" })},
		{"bad head", change(func(c map[string]any) { c["head"] = "HEAD" })},
		{"bad upstream", change(func(c map[string]any) { c["upstream"] = strings.Repeat("G", 40) })},
		{"negative ahead", change(func(c map[string]any) { c["ahead"] = -1 })},
		{"bad session id", change(func(c map[string]any) { c["session_id"] = "a/b" })},
		{"not JSON", "nope"},
		{"empty body", ""},
		{"trailing data", `{"ref":"main"} {}`},
	} {
		status, env := call(t, ts.URL, tokenAlice, "POST", changes, c.body)
		expect(t, c.name, status, env, 400, "invalid")
	}

	// An untracked file is dirty without a git oid; a never-pushed branch
	// sends unpushed entries without an upstream; content may be absent.
	ok := changeBody(map[string]any{"path": "new.go", "sha256": sha("a"), "size": 1, "dirty": true, "unpushed": true},
		map[string]any{"path": "logo.png", "size": 9000, "mode": "100644", "git_oid": oid1, "unpushed": true})
	delete(ok, "upstream")
	status, env := call(t, ts.URL, tokenAlice, "POST", changes, ok)
	expect(t, "valid unpushed change", status, env, 200, "")

	big := change(func(c map[string]any) { c["deletes"] = []string{"a", "b", "c"} })
	status, env = call(t, ts.URL, tokenAlice, "POST", changes, big)
	expect(t, "over the batch", status, env, 413, "too_large")
	status, env = call(t, ts.URL, tokenAlice, "POST", codePath("manifest"), map[string]any{"shas": []string{sha("1"), sha("2"), sha("3"), sha("4")}})
	expect(t, "manifest over the batch", status, env, 413, "too_large")
	status, env = call(t, ts.URL, tokenAlice, "POST", codePath("manifest"), map[string]any{"shas": []string{"abc"}})
	expect(t, "manifest bad sha", status, env, 400, "invalid")

	for _, id := range []string{"wt_0123", "wt_0123456789ABCDEF", "gh_0123", "x", "..%2Fwt_0123456789abcdef"} {
		status, env = call(t, ts.URL, tokenAlice, "PUT", codePath("worktrees/"+id), worktreeBody())
		expect(t, "worktree id "+id, status, env, 400, "invalid")
	}
	status, env = call(t, ts.URL, tokenAlice, "PUT", codePath("worktrees/gh_123456789"), worktreeBody())
	expect(t, "github worktree id", status, env, 200, "")
	for name, edit := range map[string]func(map[string]any){
		"bad ref":            func(b map[string]any) { b["ref"] = "a b" },
		"bad default branch": func(b map[string]any) { b["default_branch"] = "-x" },
		"bad head":           func(b map[string]any) { b["head"] = "abc" },
		"control in host":    func(b map[string]any) { b["host"] = "a\x01" },
		"long root":          func(b map[string]any) { b["root"] = strings.Repeat("r", 5000) },
	} {
		b := worktreeBody()
		edit(b)
		status, env = call(t, ts.URL, tokenAlice, "PUT", codePath("worktrees/"+wt1), b)
		expect(t, "worktree "+name, status, env, 400, "invalid")
	}
	status, env = call(t, ts.URL, tokenAlice, "PUT", codePath("worktrees/"+wt1+"/sessions/"+strings.Repeat("s", 200)), `{}`)
	expect(t, "long session id", status, env, 400, "invalid")
	status, env = call(t, ts.URL, tokenAlice, "PUT", codePath("worktrees/"+wt1+"/sessions/s1"), `{"ended":"yes"}`)
	expect(t, "session body", status, env, 400, "invalid")
}

func TestBlobs(t *testing.T) {
	st := newStores()
	ts := newServer(t, st, codeapi.Options{MaxFileBytes: 8})
	mem := st.get(project)

	status, env := call(t, ts.URL, tokenAlice, "PUT", codePath("blobs/"+sha("real")), "fake")
	expect(t, "hash mismatch", status, env, 400, "invalid")
	status, env = call(t, ts.URL, tokenAlice, "PUT", codePath("blobs/"+strings.ToUpper(sha("real"))), "real")
	expect(t, "uppercase hash", status, env, 400, "invalid")
	status, env = call(t, ts.URL, tokenAlice, "PUT", codePath("blobs/"+sha("123456789")), "123456789")
	expect(t, "over maxFileBytes", status, env, 413, "too_large")
	if n, b, _ := mem.Usage(t.Context()); n != 0 || b != 0 {
		t.Fatalf("refused blobs stored: %d %d", n, b)
	}

	bin := "\x00\xff\x01"
	status, env = call(t, ts.URL, tokenAlice, "PUT", codePath("blobs/"+sha(bin)), []byte(bin))
	expect(t, "binary blob", status, env, 200, "")
	status, env = call(t, ts.URL, tokenAlice, "PUT", codePath("blobs/"+sha("12345678")), "12345678")
	expect(t, "at maxFileBytes", status, env, 200, "")
	got, err := mem.Blob(t.Context(), sha(bin))
	if err != nil || string(got.Content) != bin || got.Size != 3 {
		t.Fatalf("blob %#v %v", got, err)
	}

	// A change naming a blob the server lacks is 409 conflict.
	call(t, ts.URL, tokenAlice, "PUT", codePath("worktrees/"+wt1), worktreeBody())
	status, env = call(t, ts.URL, tokenAlice, "POST", codePath("worktrees/"+wt1+"/changes"), changeBody(cleanEntry("x.go", "never uploaded")))
	expect(t, "missing blob", status, env, 409, "conflict")
}

func TestBeforeWrite(t *testing.T) {
	st := newStores()
	type seen struct {
		key, project, op, account string
		bytes                     int64
	}
	var (
		mu     sync.Mutex
		calls  []seen
		refuse error
	)
	ts := newServer(t, st, codeapi.Options{
		Namespace: func(_ context.Context, project string) (string, error) { return "ns__" + project, nil },
		BeforeWrite: func(ctx context.Context, key string, n int64) error {
			p, _ := auth.FromContext(ctx)
			mu.Lock()
			defer mu.Unlock()
			calls = append(calls, seen{key, codeapi.Project(ctx), codeapi.Operation(ctx), p.AccountID, n})
			return refuse
		},
	})
	mem := st.get("ns__" + project)
	last := func() seen {
		mu.Lock()
		defer mu.Unlock()
		if len(calls) == 0 {
			return seen{}
		}
		return calls[len(calls)-1]
	}
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(calls)
	}

	call(t, ts.URL, tokenAlice, "GET", codePath("capabilities"), nil)
	if count() != 0 {
		t.Fatal("capabilities called BeforeWrite")
	}
	call(t, ts.URL, tokenAlice, "PUT", codePath("blobs/"+sha("hello")), "hello")
	if got := last(); got != (seen{"ns__" + project, project, codeapi.OpBlob, "acct-alice", 5}) {
		t.Fatalf("new blob: %+v", got)
	}
	call(t, ts.URL, tokenAlice, "PUT", codePath("blobs/"+sha("hello")), "hello")
	if got := last(); got.bytes != 0 || got.op != codeapi.OpBlob {
		t.Fatalf("held blob: %+v", got)
	}
	for _, c := range []struct {
		method, path string
		body         any
		op           string
	}{
		{"POST", "manifest", `{"shas":[]}`, codeapi.OpManifest},
		{"PUT", "worktrees/" + wt1, worktreeBody(), codeapi.OpWorktree},
		{"POST", "worktrees/" + wt1 + "/changes", changeBody(cleanEntry("h.go", "hello")), codeapi.OpChanges},
		{"PUT", "worktrees/" + wt1 + "/sessions/s1", `{}`, codeapi.OpSession},
		{"DELETE", "worktrees/" + wt1, nil, codeapi.OpRemove},
	} {
		status, env := call(t, ts.URL, tokenAlice, c.method, codePath(c.path), c.body)
		expect(t, c.op, status, env, 200, "")
		if got := last(); got.op != c.op || got.bytes != 0 || got.key != "ns__"+project {
			t.Fatalf("%s: %+v", c.op, got)
		}
	}

	// Invalid requests are refused before the hook.
	n := count()
	call(t, ts.URL, tokenAlice, "PUT", codePath("blobs/"+sha("x")), "y")
	if count() != n {
		t.Fatal("BeforeWrite ran for an invalid blob")
	}

	usage := func() int64 { _, b, _ := mem.Usage(t.Context()); return b }
	before := usage()
	for _, c := range []struct {
		err    error
		status int
		kind   string
	}{
		{fmt.Errorf("%w: code sync is not in your plan", codeapi.ErrRefused), 403, "forbidden"},
		{fmt.Errorf("%w: 10 MB of 10 MB used", codeapi.ErrQuota), 413, "quota"},
		{&httpapi.StatusError{Status: 429, Kind: "rate_limited", Message: "slow down"}, 429, "rate_limited"},
		{errors.New("anything else"), 403, "forbidden"},
	} {
		mu.Lock()
		refuse = c.err
		mu.Unlock()
		status, env := call(t, ts.URL, tokenAlice, "PUT", codePath("blobs/"+sha("refused")), "refused")
		expect(t, c.err.Error(), status, env, c.status, c.kind)
		status, env = call(t, ts.URL, tokenAlice, "PUT", codePath("worktrees/wt_cccccccccccccccc"), worktreeBody())
		expect(t, c.err.Error()+" worktree", status, env, c.status, c.kind)
	}
	if usage() != before {
		t.Fatal("a refused blob was stored")
	}
	for _, w := range func() []code.Worktree { ws, _ := mem.Worktrees(t.Context()); return ws }() {
		if w.ID == "wt_cccccccccccccccc" {
			t.Fatal("a refused worktree was stored")
		}
	}
	status, env := call(t, ts.URL, tokenAlice, "GET", codePath("capabilities"), nil)
	expect(t, "capabilities while refusing", status, env, 200, "")
}

// TestCluedMountsCode composes server.NewClued with and without Code.
func TestCluedMountsCode(t *testing.T) {
	newClued := func(t *testing.T, st *stores, before func(context.Context, string, int64) error) *httptest.Server {
		opts := server.CluedOptions{
			Version:  "test",
			Tokens:   testTokens,
			Sessions: memsession.New(),
			Open: func(_ context.Context, key string) (store.Store, error) {
				return memstore.New(key), nil
			},
			Namespace: func(p auth.Principal, project string) string { return "u" + p.AccountID + "__" + project },
			Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		}
		if st != nil {
			opts.Code = st.Open
			opts.BeforeCodeWrite = before
			opts.CodeMaxFileBytes = 64
		}
		c := server.NewClued(opts)
		t.Cleanup(c.Close)
		ts := httptest.NewServer(c.Handler)
		t.Cleanup(ts.Close)
		return ts
	}

	t.Run("without Code every code route is 404", func(t *testing.T) {
		ts := newClued(t, nil, nil)
		for _, r := range []struct{ method, path string }{
			{"GET", "capabilities"}, {"POST", "manifest"}, {"PUT", "blobs/" + sha("a")},
			{"PUT", "worktrees/" + wt1}, {"DELETE", "worktrees/" + wt1},
		} {
			status, env := call(t, ts.URL, tokenAlice, r.method, codePath(r.path), "{}")
			expect(t, r.method+" "+r.path, status, env, 404, "not_found")
		}
		// The store transport next to it still answers.
		status, env := call(t, ts.URL, tokenAlice, "GET", "/api/v1/projects/"+project+"/store/plan", nil)
		expect(t, "storeapi", status, env, 200, "")
	})

	t.Run("with Code the routes are namespaced, authenticated and limited", func(t *testing.T) {
		st := newStores()
		var keys []string
		var mu sync.Mutex
		ts := newClued(t, st, func(_ context.Context, key string, _ int64) error {
			mu.Lock()
			keys = append(keys, key)
			mu.Unlock()
			return nil
		})
		status, env := call(t, ts.URL, tokenAlice, "GET", codePath("capabilities"), nil)
		expect(t, "capabilities", status, env, 200, "")
		if string(env.Result) != fmt.Sprintf(`{"code":{"maxFileBytes":64,"maxBatch":%d}}`, codeapi.DefaultMaxBatch) {
			t.Fatalf("capabilities %s", env.Result)
		}
		status, _ = call(t, ts.URL, "", "GET", codePath("capabilities"), nil)
		if status != 401 {
			t.Fatalf("no token: %d", status)
		}
		status, env = call(t, ts.URL, tokenOnlyA, "PUT", codePath("worktrees/"+wt1), worktreeBody())
		expect(t, "project not allowed", status, env, 403, "forbidden")

		status, env = call(t, ts.URL, tokenAlice, "PUT", codePath("worktrees/"+wt1), worktreeBody())
		expect(t, "alice", status, env, 200, "")
		status, env = call(t, ts.URL, tokenBob, "PUT", codePath("worktrees/"+wt1), worktreeBody())
		expect(t, "bob", status, env, 200, "")
		for _, acct := range []string{"acct-alice", "acct-bob"} {
			ws, _ := st.get("u" + acct + "__" + project).Worktrees(t.Context())
			if len(ws) != 1 || ws[0].Owner != acct {
				t.Fatalf("%s: %#v", acct, ws)
			}
		}
		mu.Lock()
		if len(keys) != 2 || keys[0] != "uacct-alice__"+project || keys[1] != "uacct-bob__"+project {
			t.Fatalf("BeforeCodeWrite keys %v", keys)
		}
		mu.Unlock()

		status, env = call(t, ts.URL, tokenAlice, "PUT", codePath("blobs/"+sha(strings.Repeat("x", 65))), strings.Repeat("x", 65))
		expect(t, "over CodeMaxFileBytes", status, env, 413, "too_large")
		status, env = call(t, ts.URL, tokenAlice, "GET", codePath("worktrees/"+wt1), nil)
		expect(t, "no read route", status, env, 404, "not_found")
	})

	t.Run("a failing NamespaceCtx fails closed", func(t *testing.T) {
		st := newStores()
		c := server.NewClued(server.CluedOptions{
			Tokens:   testTokens,
			Sessions: memsession.New(),
			Open:     func(_ context.Context, key string) (store.Store, error) { return memstore.New(key), nil },
			NamespaceCtx: func(context.Context, auth.Principal, string) (string, error) {
				return "", fmt.Errorf("%w: left the workspace", auth.ErrProjectForbidden)
			},
			Code:   st.Open,
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
		t.Cleanup(c.Close)
		ts := httptest.NewServer(c.Handler)
		t.Cleanup(ts.Close)
		status, env := call(t, ts.URL, tokenAlice, "PUT", codePath("worktrees/"+wt1), worktreeBody())
		expect(t, "namespace refused", status, env, 403, "forbidden")
		if len(st.opened) != 0 {
			t.Fatalf("opened %v", st.opened)
		}
	})
}

// TestConformanceOverHTTP runs code/codetest through the transport with a
// token whose account is codetest.Owner: writes go over HTTP, reads come
// from the server-side store, as code/remotecode's test does.
func TestConformanceOverHTTP(t *testing.T) {
	codetest.Run(t, func(t *testing.T) code.Store {
		st := newStores()
		ts := newServer(t, st, codeapi.Options{})
		return codetest.Split{Writes: &httpWriter{t: t, base: ts.URL + codePath("")}, Reads: st.get(project)}
	})
}

// httpWriter is the write side of code.Store over the transport, just
// enough for the conformance suite (code/remotecode is the real client).
type httpWriter struct {
	code.Store // the read side is never called through it
	t          *testing.T
	base       string
}

func (h *httpWriter) do(ctx context.Context, method, path string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, h.base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tokenSuite)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var env envelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return fmt.Errorf("%s %s: %d: %w", method, path, resp.StatusCode, err)
	}
	if env.OK {
		if out != nil {
			return json.Unmarshal(env.Result, out)
		}
		return nil
	}
	err = fmt.Errorf("%s %s: %d %s: %s", method, path, resp.StatusCode, env.kind(), env.Error.Message)
	switch env.kind() {
	case codeapi.KindInvalid:
		return errors.Join(code.ErrInvalid, err)
	case codeapi.KindNotFound:
		return errors.Join(code.ErrNotFound, err)
	case codeapi.KindConflict:
		return errors.Join(code.ErrMissingBlob, err)
	}
	return err
}

func (h *httpWriter) doJSON(ctx context.Context, method, path string, in, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return h.do(ctx, method, path, b, out)
}

func (h *httpWriter) MissingBlobs(ctx context.Context, shas []string) ([]string, error) {
	var out codeapi.ManifestResult
	err := h.doJSON(ctx, "POST", "manifest", codeapi.Manifest{SHAs: append([]string{}, shas...)}, &out)
	return out.Missing, err
}

func (h *httpWriter) PutBlob(ctx context.Context, b code.Blob) error {
	// The route carries no size: the client checks it, as remotecode does.
	if b.Size != int64(len(b.Content)) || !code.ValidSHA256(b.SHA256) {
		return fmt.Errorf("%w: blob %q", code.ErrInvalid, b.SHA256)
	}
	return h.do(ctx, "PUT", "blobs/"+url.PathEscape(b.SHA256), b.Content, nil)
}

func (h *httpWriter) PutWorktree(ctx context.Context, w code.Worktree) error {
	body := codeapi.WorktreeBody{Root: w.Root, Host: w.Host, Origin: w.Origin, DefaultBranch: w.DefaultBranch}
	if len(w.Refs) > 0 {
		body.Ref = w.Refs[0]
	}
	return h.doJSON(ctx, "PUT", "worktrees/"+url.PathEscape(w.ID), body, nil)
}

func (h *httpWriter) Commit(ctx context.Context, c code.Change) (int64, error) {
	// JSON cannot carry invalid UTF-8 (it would arrive as U+FFFD), so the
	// client refuses such a path itself, as remotecode does.
	for _, e := range c.Upserts {
		if !utf8.ValidString(e.Path) {
			return 0, fmt.Errorf("%w: path %q", code.ErrInvalid, e.Path)
		}
	}
	for _, p := range c.Deletes {
		if !utf8.ValidString(p) {
			return 0, fmt.Errorf("%w: path %q", code.ErrInvalid, p)
		}
	}
	body := codeapi.ChangeBody{Ref: c.Ref, Head: c.Head, Upstream: c.Upstream, Ahead: c.Ahead, SessionID: c.SessionID, Full: c.Full, Deletes: c.Deletes}
	for _, e := range c.Upserts {
		body.Upserts = append(body.Upserts, codeapi.EntryBody{Path: e.Path, SHA256: e.SHA256, Size: e.Size, Mode: e.Mode, Lang: e.Lang, GitOID: e.GitOID, Dirty: e.Dirty, Unpushed: e.Unpushed})
	}
	var out codeapi.ChangeResult
	err := h.doJSON(ctx, "POST", "worktrees/"+url.PathEscape(c.Worktree)+"/changes", body, &out)
	return out.Seq, err
}

func (h *httpWriter) RemoveWorktree(ctx context.Context, id string) error {
	return h.do(ctx, "DELETE", "worktrees/"+url.PathEscape(id), nil, nil)
}

func (h *httpWriter) TouchSession(ctx context.Context, worktree, sessionID string, ended bool) error {
	return h.doJSON(ctx, "PUT", "worktrees/"+url.PathEscape(worktree)+"/sessions/"+url.PathEscape(sessionID), codeapi.SessionBody{Ended: ended}, nil)
}
