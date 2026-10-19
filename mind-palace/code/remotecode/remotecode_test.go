package remotecode_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/code/codetest"
	"github.com/robbiebyrd/clued/mind-palace/code/memcode"
	"github.com/robbiebyrd/clued/mind-palace/code/remotecode"
	"github.com/robbiebyrd/clued/mind-palace/remote"
	"github.com/robbiebyrd/clued/mind-palace/server/auth"
	"github.com/robbiebyrd/clued/mind-palace/server/codeapi"
	"github.com/robbiebyrd/clued/mind-palace/session"
)

const (
	project    = "github.com__org__repo"
	tokenSuite = "tok-suite"
	tokenOther = "tok-other"
	tokenOnlyA = "tok-only-a"

	wt1  = "wt_0123456789abcdef"
	oid1 = "1111111111111111111111111111111111111111"
	oid2 = "2222222222222222222222222222222222222222"
)

type tokens map[string]auth.Principal

func (s tokens) Lookup(raw string) (auth.Principal, bool) {
	p, ok := s[raw]
	return p, ok
}

var testTokens = tokens{
	tokenSuite: {Name: "suite", AccountID: codetest.Owner, Projects: []string{"*"}},
	tokenOther: {Name: "other", AccountID: "acct-other", Projects: []string{"*"}},
	tokenOnlyA: {Name: "only-a", AccountID: codetest.Owner, Projects: []string{"a"}},
}

// stores hands out one memcode store per namespaced key.
type stores struct {
	mu    sync.Mutex
	byKey map[string]*memcode.Store
}

func newStores() *stores { return &stores{byKey: map[string]*memcode.Store{}} }

func (s *stores) Open(_ context.Context, key string) (code.Store, error) { return s.get(key), nil }

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

// newServer serves codeapi behind auth.Middleware, as the clued server
// does; opts.Open defaults to st.Open.
func newServer(t *testing.T, st *stores, opts codeapi.Options) *httptest.Server {
	t.Helper()
	if opts.Open == nil {
		opts.Open = st.Open
	}
	ts := httptest.NewServer(auth.Middleware(testTokens)(codeapi.Handler(opts)))
	t.Cleanup(ts.Close)
	return ts
}

func newClient(t *testing.T, base, token, proj string) *remotecode.Store {
	t.Helper()
	c, err := remote.New(session.Config{ServerURL: base, ServerToken: token})
	if err != nil {
		t.Fatal(err)
	}
	s, err := remotecode.New(c, proj)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// writer is the write side the suite drives: the HTTP client for every
// write with a route, the server-side store for the writes without one
// (RemoveRef, AppendEvents), which the GitHub sync makes in process.
type writer struct {
	*remotecode.Store
	server code.Store
}

func (w writer) RemoveRef(ctx context.Context, worktree, ref string) error {
	return w.server.RemoveRef(ctx, worktree, ref)
}

func (w writer) AppendEvents(ctx context.Context, events []code.Event) error {
	return w.server.AppendEvents(ctx, events)
}

// TestConformance runs code/codetest with writes over HTTP and reads from
// the server-side store.
func TestConformance(t *testing.T) {
	codetest.Run(t, func(t *testing.T) code.Store {
		st := newStores()
		ts := newServer(t, st, codeapi.Options{})
		server := st.get(project)
		return codetest.Split{Writes: writer{Store: newClient(t, ts.URL, tokenSuite, project), server: server}, Reads: server}
	})
}

func sha(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func blob(content string) code.Blob {
	return code.Blob{SHA256: sha(content), Size: int64(len(content)), Content: []byte(content)}
}

func worktree() code.Worktree {
	return code.Worktree{ID: wt1, Root: "/home/dev/repo", Host: "devbox", Origin: "git@github.com:org/repo.git", DefaultBranch: "main", Refs: []string{"main"}}
}

func change(entries ...code.Entry) code.Change {
	return code.Change{Worktree: wt1, Ref: "main", Head: oid1, Upstream: oid2, SessionID: "sess-1", Upserts: entries}
}

func entry(path, content string) code.Entry {
	return code.Entry{Path: path, SHA256: sha(content), Size: int64(len(content)), Mode: "100644", Lang: "go", GitOID: oid1}
}

func TestCapabilities(t *testing.T) {
	ts := newServer(t, newStores(), codeapi.Options{MaxFileBytes: 4096, MaxBatch: 7})
	got, err := newClient(t, ts.URL, tokenSuite, project).Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Code.MaxFileBytes != 4096 || got.Code.MaxBatch != 7 {
		t.Fatalf("capabilities = %+v", got)
	}
}

// TestUnsupported covers servers without the code transport: the clued
// server's envelope catch-all and a server that knows nothing of the prefix.
func TestUnsupported(t *testing.T) {
	envelope := http.NewServeMux()
	envelope.HandleFunc(codeapi.Pattern, codeapi.NotFound)
	bare := http.NewServeMux()
	bare.HandleFunc("/api/v1/projects/{project}/store/", func(w http.ResponseWriter, _ *http.Request) {})
	for name, h := range map[string]http.Handler{"envelope": envelope, "bare": bare} {
		t.Run(name, func(t *testing.T) {
			ts := httptest.NewServer(auth.Middleware(testTokens)(h))
			t.Cleanup(ts.Close)
			s := newClient(t, ts.URL, tokenSuite, project)
			ctx := context.Background()
			if _, err := s.Capabilities(ctx); !errors.Is(err, remotecode.ErrUnsupported) {
				t.Fatalf("capabilities: %v", err)
			}
			if _, err := s.MissingBlobs(ctx, []string{sha("a")}); !errors.Is(err, remotecode.ErrUnsupported) {
				t.Fatalf("manifest: %v", err)
			}
			if err := s.PutBlob(ctx, blob("a")); !errors.Is(err, remotecode.ErrUnsupported) {
				t.Fatalf("blob: %v", err)
			}
			if err := s.PutWorktree(ctx, worktree()); !errors.Is(err, remotecode.ErrUnsupported) || errors.Is(err, code.ErrNotFound) {
				t.Fatalf("worktree: %v", err)
			}
		})
	}
}

// TestNotFound keeps a known route's 404 apart from a missing transport.
func TestNotFound(t *testing.T) {
	ts := newServer(t, newStores(), codeapi.Options{})
	s := newClient(t, ts.URL, tokenSuite, project)
	_, err := s.Commit(context.Background(), change(entry("a.go", "a")))
	if !errors.Is(err, code.ErrNotFound) || errors.Is(err, remotecode.ErrUnsupported) {
		t.Fatalf("commit to an unknown worktree: %v", err)
	}
	var re *remote.Error
	if !errors.As(err, &re) || re.Status != http.StatusNotFound || re.Kind != codeapi.KindNotFound {
		t.Fatalf("detail: %#v", re)
	}
	// Another account's worktree reads as unknown.
	if err := s.PutWorktree(context.Background(), worktree()); err != nil {
		t.Fatal(err)
	}
	other := newClient(t, ts.URL, tokenOther, project)
	if err := other.TouchSession(context.Background(), wt1, "sess-2", false); !errors.Is(err, code.ErrNotFound) {
		t.Fatalf("touch another's worktree: %v", err)
	}
}

func TestForbidden(t *testing.T) {
	ctx := context.Background()
	t.Run("project not allowed for the token", func(t *testing.T) {
		ts := newServer(t, newStores(), codeapi.Options{Allow: func(r *http.Request, p string) bool {
			pr, ok := auth.FromContext(r.Context())
			return ok && auth.AllowProject(pr, p)
		}})
		_, err := newClient(t, ts.URL, tokenOnlyA, project).MissingBlobs(ctx, []string{sha("a")})
		if !errors.Is(err, remotecode.ErrForbidden) || !errors.Is(err, remote.ErrForbidden) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("refused by BeforeWrite", func(t *testing.T) {
		ts := newServer(t, newStores(), codeapi.Options{BeforeWrite: func(context.Context, string, int64) error {
			return fmt.Errorf("%w: code sync is not in your plan", codeapi.ErrRefused)
		}})
		err := newClient(t, ts.URL, tokenSuite, project).PutBlob(ctx, blob("a"))
		if !errors.Is(err, remotecode.ErrForbidden) || !strings.Contains(err.Error(), "not in your plan") {
			t.Fatalf("got %v", err)
		}
	})
}

func TestUnauthorized(t *testing.T) {
	ts := newServer(t, newStores(), codeapi.Options{})
	_, err := newClient(t, ts.URL, "tok-unknown", project).MissingBlobs(context.Background(), nil)
	if !errors.Is(err, remote.ErrUnauthorized) {
		t.Fatalf("got %v", err)
	}
}

func TestConflict(t *testing.T) {
	ts := newServer(t, newStores(), codeapi.Options{})
	s := newClient(t, ts.URL, tokenSuite, project)
	ctx := context.Background()
	if err := s.PutWorktree(ctx, worktree()); err != nil {
		t.Fatal(err)
	}
	_, err := s.Commit(ctx, change(entry("a.go", "never uploaded")))
	if !errors.Is(err, remotecode.ErrConflict) || !errors.Is(err, code.ErrMissingBlob) {
		t.Fatalf("got %v", err)
	}
	var e *remotecode.Error
	if !errors.As(err, &e) || e.Status() != http.StatusConflict || e.Kind() != codeapi.KindConflict {
		t.Fatalf("detail: %#v", e)
	}
	// Upload and retry, as the collector does.
	if err := s.PutBlob(ctx, blob("never uploaded")); err != nil {
		t.Fatal(err)
	}
	if seq, err := s.Commit(ctx, change(entry("a.go", "never uploaded"))); err != nil || seq != 1 {
		t.Fatalf("retry: seq %d, %v", seq, err)
	}
}

func TestTooLargeAndQuota(t *testing.T) {
	ctx := context.Background()
	ts := newServer(t, newStores(), codeapi.Options{MaxFileBytes: 4, MaxBatch: 2})
	s := newClient(t, ts.URL, tokenSuite, project)
	err := s.PutBlob(ctx, blob("12345"))
	if !errors.Is(err, remotecode.ErrTooLarge) || errors.Is(err, remotecode.ErrQuota) {
		t.Fatalf("blob over the cap: %v", err)
	}
	if _, err := s.MissingBlobs(ctx, []string{sha("a"), sha("b"), sha("c")}); !errors.Is(err, remotecode.ErrTooLarge) {
		t.Fatalf("manifest over the batch: %v", err)
	}

	quota := newServer(t, newStores(), codeapi.Options{BeforeWrite: func(_ context.Context, _ string, n int64) error {
		if n > 0 {
			return fmt.Errorf("%w: 10 MiB used of 10 MiB", codeapi.ErrQuota)
		}
		return nil
	}})
	q := newClient(t, quota.URL, tokenSuite, project)
	err = q.PutBlob(ctx, blob("abc"))
	if !errors.Is(err, remotecode.ErrQuota) || errors.Is(err, remotecode.ErrTooLarge) {
		t.Fatalf("quota: %v", err)
	}
	var e *remotecode.Error
	if !errors.As(err, &e) || e.Status() != http.StatusRequestEntityTooLarge || e.Kind() != codeapi.KindQuota {
		t.Fatalf("detail: %#v", e)
	}
}

// TestLocalChecks covers what the routes cannot carry: a blob's size and
// invalid UTF-8 paths, plus ids that would change the route.
func TestLocalChecks(t *testing.T) {
	var hits int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits++ }))
	t.Cleanup(ts.Close)
	s := newClient(t, ts.URL, tokenSuite, project)
	ctx := context.Background()
	b := blob("abc")
	b.Size = 4
	checks := map[string]error{
		"size":         s.PutBlob(ctx, b),
		"sha":          s.PutBlob(ctx, code.Blob{SHA256: "../x", Content: []byte("x"), Size: 1}),
		"worktree id":  s.PutWorktree(ctx, code.Worktree{ID: ""}),
		"remove id":    s.RemoveWorktree(ctx, "wt_/../x"),
		"session id":   s.TouchSession(ctx, wt1, "", false),
		"commit id":    func() error { _, err := s.Commit(ctx, code.Change{Worktree: "x", Ref: "main"}); return err }(),
		"upsert utf-8": func() error { _, err := s.Commit(ctx, change(entry("a\xff.go", "a"))); return err }(),
		"delete utf-8": func() error {
			c := change()
			c.Deletes = []string{"b\xfe.go"}
			_, err := s.Commit(ctx, c)
			return err
		}(),
	}
	for name, err := range checks {
		if !errors.Is(err, code.ErrInvalid) {
			t.Errorf("%s: got %v, want code.ErrInvalid", name, err)
		}
	}
	if hits != 0 {
		t.Fatalf("%d requests reached the server", hits)
	}
}

// TestNoRoute covers the port methods the transport does not serve.
func TestNoRoute(t *testing.T) {
	s := newClient(t, "http://127.0.0.1:1", tokenSuite, project)
	ctx := context.Background()
	_, _, usageErr := s.Usage(ctx)
	errs := map[string]error{
		"RemoveRef":    s.RemoveRef(ctx, wt1, "main"),
		"AppendEvents": s.AppendEvents(ctx, nil),
		"SHA256ForGitOID": func() error {
			_, err := s.SHA256ForGitOID(ctx, nil)
			return err
		}(),
		"Worktrees": func() error { _, err := s.Worktrees(ctx); return err }(),
		"Tree":      func() error { _, err := s.Tree(ctx, wt1, "main"); return err }(),
		"Changes":   func() error { _, err := s.Changes(ctx, 0, 1); return err }(),
		"Blob":      func() error { _, err := s.Blob(ctx, sha("a")); return err }(),
		"Usage":     usageErr,
		"Events":    func() error { _, err := s.Events(ctx, 0, 1); return err }(),
	}
	for name, err := range errs {
		if !errors.Is(err, remotecode.ErrUnsupported) || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

// TestWrites checks what reaches the server-side store: raw blob bytes,
// every ref of a worktree, the stamped owner and session.
func TestWrites(t *testing.T) {
	st := newStores()
	ts := newServer(t, st, codeapi.Options{})
	s := newClient(t, ts.URL, tokenSuite, project)
	server := st.get(project)
	ctx := context.Background()

	content := "package a\n\x00\xff binary-ish"
	if err := s.PutBlob(ctx, blob(content)); err != nil {
		t.Fatal(err)
	}
	got, err := server.Blob(ctx, sha(content))
	if err != nil || !bytes.Equal(got.Content, []byte(content)) {
		t.Fatalf("blob %q, %v", got.Content, err)
	}
	if err := s.PutBlob(ctx, code.Blob{SHA256: sha(""), Size: 0}); err != nil {
		t.Fatalf("empty blob: %v", err)
	}

	w := worktree()
	w.Owner = "someone-else"
	w.Refs = []string{"main", "feature/x"}
	if err := s.PutWorktree(ctx, w); err != nil {
		t.Fatal(err)
	}
	ws, err := server.Worktrees(ctx)
	if err != nil || len(ws) != 1 {
		t.Fatalf("worktrees %v, %v", ws, err)
	}
	if ws[0].Owner != codetest.Owner || strings.Join(ws[0].Refs, ",") != "feature/x,main" && strings.Join(ws[0].Refs, ",") != "main,feature/x" {
		t.Fatalf("worktree %+v", ws[0])
	}

	e := entry("a.go", content)
	e.Session = "forged"
	c := change(e)
	c.Owner = "someone-else"
	seq, err := s.Commit(ctx, c)
	if err != nil || seq != 1 {
		t.Fatalf("commit: %d, %v", seq, err)
	}
	tree, err := server.Tree(ctx, wt1, "main")
	if err != nil || len(tree) != 1 || tree[0].Session != "sess-1" {
		t.Fatalf("tree %+v, %v", tree, err)
	}
	changes, err := server.Changes(ctx, 0, 10)
	if err != nil || len(changes) != 1 || changes[0].Owner != codetest.Owner {
		t.Fatalf("changes %+v, %v", changes, err)
	}

	if err := s.TouchSession(ctx, wt1, "sess-1", true); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveWorktree(ctx, wt1); err != nil {
		t.Fatal(err)
	}
	ws, _ = server.Worktrees(ctx)
	if len(ws) != 1 || !ws[0].Removed || len(ws[0].Sessions) != 1 || !ws[0].Sessions[0].Ended {
		t.Fatalf("worktree after touch and remove %+v", ws)
	}
}

func TestNew(t *testing.T) {
	c, err := remote.New(session.Config{ServerURL: "http://127.0.0.1:1", ServerToken: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := remotecode.New(c, "not a slug/.."); err == nil {
		t.Fatal("invalid slug accepted")
	}
	if _, err := remotecode.New(nil, project); err == nil {
		t.Fatal("nil client accepted")
	}
	s, err := remotecode.New(c, project)
	if err != nil || s.Project() != project {
		t.Fatalf("%v, %v", s, err)
	}
}
