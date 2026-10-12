package storeapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/model"
	"github.com/robbiebyrd/clued/mind-palace/store"
	"github.com/robbiebyrd/clued/mind-palace/store/memstore"
)

// opener hands out one memstore per project and counts opens.
type opener struct {
	mu     sync.Mutex
	stores map[string]*memstore.Store
	opens  int
}

func newOpener() *opener { return &opener{stores: map[string]*memstore.Store{}} }

func (o *opener) Open(_ context.Context, project string) (store.Store, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.opens++
	s, ok := o.stores[project]
	if !ok {
		s = memstore.New(project)
		o.stores[project] = s
	}
	return s, nil
}

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Kind     string `json:"kind"`
		Message  string `json:"message"`
		Problems []any  `json:"problems"`
	} `json:"error"`
	Warning json.RawMessage `json:"warning"`
}

type client struct {
	t *testing.T
	h http.Handler
}

func (c *client) do(method, path string, body any) (int, envelope) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		switch b := body.(type) {
		case string:
			rd = strings.NewReader(b)
		default:
			buf, err := json.Marshal(b)
			if err != nil {
				c.t.Fatal(err)
			}
			rd = bytes.NewReader(buf)
		}
	}
	r := httptest.NewRequest(method, path, rd)
	w := httptest.NewRecorder()
	c.h.ServeHTTP(w, r)
	var env envelope
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		c.t.Fatalf("%s %s: content type %q, body %s", method, path, ct, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		c.t.Fatalf("%s %s: bad envelope %q: %v", method, path, w.Body.String(), err)
	}
	if (w.Code < 300) != env.OK {
		c.t.Errorf("%s %s: status %d but ok=%v", method, path, w.Code, env.OK)
	}
	return w.Code, env
}

func (c *client) expectError(method, path string, body any, status int, kind string) {
	c.t.Helper()
	code, env := c.do(method, path, body)
	if code != status || env.Error == nil || env.Error.Kind != kind {
		c.t.Errorf("%s %s: got %d %+v, want %d %s", method, path, code, env.Error, status, kind)
	}
}

func doc(k, id, title string) *model.Document {
	return &model.Document{Kind: k, Path: model.FileName(id, "feat", "x"), FrontMatter: model.FrontMatter{
		ID: id, Title: title, Type: "feat", Status: "pending", Priority: "2",
		Created: "2026-01-01T00:00:00.000Z", Updated: "2026-01-01T00:00:00.000Z",
	}, Content: "# " + title + "\n"}
}

const (
	projA = "github.com__owner__a"
	projB = "github.com__owner__b"
)

func base(project, k string) string { return Prefix + "/" + project + "/store/" + k }

func TestDocumentsRoutes(t *testing.T) {
	o := newOpener()
	c := &client{t: t, h: Handler(Options{Open: o.Open})}
	plans := base(projA, kind.Plan)

	// Empty list is an empty array, not null.
	code, env := c.do("GET", plans, nil)
	if code != 200 || string(env.Result) != "[]" {
		t.Fatalf("empty list: %d %s", code, env.Result)
	}
	// Missing document.
	c.expectError("GET", plans+"/0001-abc", nil, 404, ErrNotFound)
	c.expectError("DELETE", plans+"/0001-abc", nil, 404, ErrNotFound)

	// Put, then read it back.
	d := doc(kind.Plan, "0001-abc", "First")
	code, env = c.do("PUT", plans+"/0001-abc", d)
	if code != 200 {
		t.Fatalf("put: %d %+v", code, env.Error)
	}
	code, env = c.do("GET", plans+"/0001-abc", nil)
	if code != 200 {
		t.Fatalf("get: %d", code)
	}
	var got model.Document
	if err := json.Unmarshal(env.Result, &got); err != nil {
		t.Fatal(err)
	}
	if got.FrontMatter.Title != "First" || got.Kind != kind.Plan || got.Path != d.Path || got.Content != d.Content {
		t.Errorf("round trip: %+v", got)
	}
	// The kind may be omitted in the body: it comes from the path.
	d2 := doc("", "0002-def", "Second")
	if code, env := c.do("PUT", plans+"/0002-def", d2); code != 200 {
		t.Fatalf("put without kind: %d %+v", code, env.Error)
	}
	code, env = c.do("GET", plans, nil)
	var list []*model.Document
	if err := json.Unmarshal(env.Result, &list); err != nil {
		t.Fatal(err)
	}
	if code != 200 || len(list) != 2 || list[0].ID() != "0001-abc" || list[1].ID() != "0002-def" || list[1].Kind != kind.Plan {
		t.Errorf("list: %d %d docs", code, len(list))
	}
	// Stories are a separate namespace.
	code, env = c.do("GET", base(projA, kind.Story), nil)
	if code != 200 || string(env.Result) != "[]" {
		t.Errorf("stories: %d %s", code, env.Result)
	}
	// Replace, then delete.
	d.FrontMatter.Title = "Renamed"
	c.do("PUT", plans+"/0001-abc", d)
	_, env = c.do("GET", plans+"/0001-abc", nil)
	json.Unmarshal(env.Result, &got)
	if got.FrontMatter.Title != "Renamed" {
		t.Errorf("replace: %q", got.FrontMatter.Title)
	}
	code, env = c.do("DELETE", plans+"/0001-abc", nil)
	if code != 200 || !strings.Contains(string(env.Result), `"0001-abc"`) {
		t.Errorf("delete: %d %s", code, env.Result)
	}
	c.expectError("GET", plans+"/0001-abc", nil, 404, ErrNotFound)
}

func TestTemplateRoutes(t *testing.T) {
	o := newOpener()
	c := &client{t: t, h: Handler(Options{Open: o.Open})}
	stories := base(projA, kind.Story)

	code, env := c.do("GET", stories+"/templates", nil)
	if code != 200 || string(env.Result) != "[]" {
		t.Fatalf("empty templates: %d %s", code, env.Result)
	}
	c.expectError("GET", stories+"/templates/default", nil, 404, ErrNotFound)
	c.expectError("DELETE", stories+"/templates/default", nil, 404, ErrNotFound)

	// Kind and id may be omitted in the body.
	if code, env := c.do("PUT", stories+"/templates/default", map[string]string{"content": "# {{title}}\n"}); code != 200 {
		t.Fatalf("put template: %d %+v", code, env.Error)
	}
	if code, env := c.do("PUT", stories+"/templates/bug", &model.Template{Kind: kind.Story, ID: "bug", Content: "# Bug\n"}); code != 200 {
		t.Fatalf("put template: %d %+v", code, env.Error)
	}
	code, env = c.do("GET", stories+"/templates/default", nil)
	var tm model.Template
	if err := json.Unmarshal(env.Result, &tm); err != nil {
		t.Fatal(err)
	}
	if code != 200 || tm.Kind != kind.Story || tm.ID != "default" || tm.Content != "# {{title}}\n" {
		t.Errorf("template: %d %+v", code, tm)
	}
	code, env = c.do("GET", stories+"/templates", nil)
	var list []*model.Template
	json.Unmarshal(env.Result, &list)
	if code != 200 || len(list) != 2 || list[0].ID != "bug" || list[1].ID != "default" {
		t.Errorf("list templates: %d %+v", code, list)
	}
	// Templates do not show up as documents, and a document named
	// "templates" is shadowed by the literal route rather than confused
	// with the template list.
	code, env = c.do("GET", stories, nil)
	if code != 200 || string(env.Result) != "[]" {
		t.Errorf("documents after templates: %d %s", code, env.Result)
	}
	// Plans have their own templates.
	code, env = c.do("GET", base(projA, kind.Plan)+"/templates", nil)
	if code != 200 || string(env.Result) != "[]" {
		t.Errorf("plan templates: %d %s", code, env.Result)
	}
	if code, _ := c.do("DELETE", stories+"/templates/default", nil); code != 200 {
		t.Errorf("delete template: %d", code)
	}
	c.expectError("GET", stories+"/templates/default", nil, 404, ErrNotFound)
	// Mismatched ids and kinds are rejected.
	c.expectError("PUT", stories+"/templates/x", &model.Template{ID: "y", Content: "c"}, 400, ErrInvalid)
	c.expectError("PUT", stories+"/templates/x", &model.Template{Kind: kind.Plan, ID: "x", Content: "c"}, 400, ErrInvalid)
}

func TestProjectsAreIndependent(t *testing.T) {
	o := newOpener()
	c := &client{t: t, h: Handler(Options{Open: o.Open})}
	if code, env := c.do("PUT", base(projA, kind.Plan)+"/0001-abc", doc(kind.Plan, "0001-abc", "in a")); code != 200 {
		t.Fatalf("put a: %d %+v", code, env.Error)
	}
	if code, env := c.do("PUT", base(projB, kind.Plan)+"/0001-abc", doc(kind.Plan, "0001-abc", "in b")); code != 200 {
		t.Fatalf("put b: %d %+v", code, env.Error)
	}
	for p, want := range map[string]string{projA: "in a", projB: "in b"} {
		_, env := c.do("GET", base(p, kind.Plan)+"/0001-abc", nil)
		var d model.Document
		json.Unmarshal(env.Result, &d)
		if d.FrontMatter.Title != want {
			t.Errorf("%s: title %q, want %q", p, d.FrontMatter.Title, want)
		}
		_, env = c.do("GET", base(p, kind.Plan), nil)
		var list []*model.Document
		json.Unmarshal(env.Result, &list)
		if len(list) != 1 {
			t.Errorf("%s: %d documents", p, len(list))
		}
	}
	c.do("DELETE", base(projA, kind.Plan)+"/0001-abc", nil)
	if code, _ := c.do("GET", base(projB, kind.Plan)+"/0001-abc", nil); code != 200 {
		t.Errorf("delete in a touched b: %d", code)
	}
}

func TestBadRequests(t *testing.T) {
	o := newOpener()
	c := &client{t: t, h: Handler(Options{Open: o.Open, MaxBody: 512})}

	// Bad slugs: 400 before any store is opened. ".." and "/" inside the
	// slug are normalised away by the mux or ruled out by ValidSlug.
	for _, bad := range []string{"Upper", "a$b", "-lead", ".dot", "a..b", strings.Repeat("a", 201)} {
		c.expectError("GET", base(bad, kind.Plan), nil, 400, ErrInvalid)
	}
	if o.opens != 0 {
		t.Errorf("a store was opened for an invalid slug (%d opens)", o.opens)
	}
	// An empty slug, a "/" and a ".." segment never reach a handler: the
	// mux has no such route (404 with no envelope) or cleans the path.
	for _, path := range []string{Prefix + "//store/plan", Prefix + "/a/b/store/plan"} {
		w := httptest.NewRecorder()
		c.h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code == 200 {
			t.Errorf("%s answered 200", path)
		}
	}
	w := httptest.NewRecorder()
	c.h.ServeHTTP(w, httptest.NewRequest("GET", Prefix+"/../store/plan", nil))
	if w.Code == 200 {
		t.Errorf("parent reference answered 200")
	}
	if o.opens != 0 {
		t.Errorf("a store was opened for a malformed path (%d opens)", o.opens)
	}

	// Unknown kinds (and plurals) are 400.
	for _, k := range []string{"plans", "stories", "note", "Plan"} {
		c.expectError("GET", base(projA, k), nil, 400, ErrInvalid)
		c.expectError("GET", base(projA, k)+"/templates", nil, 400, ErrInvalid)
	}
	if o.opens != 0 {
		t.Errorf("a store was opened for an unknown kind (%d opens)", o.opens)
	}

	plans := base(projA, kind.Plan)
	// Id mismatch, kind mismatch, bad JSON, empty body, oversized body.
	c.expectError("PUT", plans+"/0001-abc", doc(kind.Plan, "0002-xyz", "x"), 400, ErrInvalid)
	c.expectError("PUT", plans+"/0001-abc", doc(kind.Story, "0001-abc", "x"), 400, ErrInvalid)
	c.expectError("PUT", plans+"/0001-abc", "{not json", 400, ErrInvalid)
	c.expectError("PUT", plans+"/0001-abc", "", 400, ErrInvalid)
	big := doc(kind.Plan, "0001-abc", "x")
	big.Content = strings.Repeat("x", 1024)
	c.expectError("PUT", plans+"/0001-abc", big, 413, ErrTooLarge)
	if code, _ := c.do("GET", plans, nil); code != 200 {
		t.Errorf("list after bad puts: %d", code)
	}
	if _, env := c.do("GET", plans, nil); string(env.Result) != "[]" {
		t.Errorf("a rejected put was stored: %s", env.Result)
	}
}

func TestAllow(t *testing.T) {
	o := newOpener()
	allowed := map[string]bool{projA: true}
	var seen []string
	h := Handler(Options{Open: o.Open, Allow: func(r *http.Request, project string) bool {
		seen = append(seen, project)
		return allowed[project] && r.Header.Get("Authorization") == "Bearer ok"
	}})
	req := func(method, path string, hdr string) int {
		r := httptest.NewRequest(method, path, strings.NewReader(`{"id":"x"}`))
		if hdr != "" {
			r.Header.Set("Authorization", hdr)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var env envelope
		json.Unmarshal(w.Body.Bytes(), &env)
		if w.Code == 403 && (env.Error == nil || env.Error.Kind != ErrForbidden) {
			t.Errorf("%s %s: 403 without forbidden envelope: %s", method, path, w.Body.String())
		}
		return w.Code
	}
	for _, path := range []string{
		base(projB, kind.Plan), base(projB, kind.Plan) + "/0001-abc", base(projB, kind.Story) + "/templates", base(projB, kind.Story) + "/templates/default",
	} {
		for _, method := range []string{"GET", "PUT", "DELETE"} {
			if method != "GET" && strings.HasSuffix(path, kind.Plan) {
				continue
			}
			if code := req(method, path, "Bearer ok"); code != 403 {
				t.Errorf("%s %s: %d, want 403", method, path, code)
			}
		}
	}
	if code := req("GET", base(projA, kind.Plan), ""); code != 403 {
		t.Errorf("wrong token: %d", code)
	}
	if o.opens != 0 {
		t.Error("a store was opened for a forbidden request")
	}
	// Allow runs after slug validation and before the kind check.
	if code := req("GET", base("Bad", kind.Plan), "Bearer ok"); code != 400 {
		t.Errorf("bad slug with allow: %d", code)
	}
	if code := req("GET", base(projB, "nope"), "Bearer ok"); code != 403 {
		t.Errorf("forbidden project with bad kind: %d, want 403", code)
	}
	if code := req("GET", base(projA, kind.Plan), "Bearer ok"); code != 200 {
		t.Errorf("allowed: %d", code)
	}
	for _, s := range seen {
		if s == "Bad" {
			t.Error("Allow was called with an invalid slug")
		}
	}
}

// failing wraps a store so writes fail on a secondary copy or everywhere.
type failing struct {
	store.Store
	err error
}

func (f *failing) Put(context.Context, *model.Document) error   { return f.err }
func (f *failing) Delete(context.Context, string, string) error { return f.err }

func TestStoreErrors(t *testing.T) {
	// Open fails: 502 store.
	boom := errors.New("mongo is down")
	c := &client{t: t, h: Handler(Options{Open: func(context.Context, string) (store.Store, error) { return nil, boom }})}
	code, env := c.do("GET", base(projA, kind.Plan), nil)
	if code != 502 || env.Error == nil || env.Error.Kind != ErrStore || !strings.Contains(env.Error.Message, "mongo is down") {
		t.Errorf("open failure: %d %+v", code, env.Error)
	}

	// A write that fails on a secondary copy is 200 with a warning.
	primary := memstore.New("primary")
	multi := store.NewMulti(primary, &failing{Store: memstore.New("copy"), err: boom})
	c = &client{t: t, h: Handler(Options{Open: func(context.Context, string) (store.Store, error) { return multi, nil }})}
	code, env = c.do("PUT", base(projA, kind.Plan)+"/0001-abc", doc(kind.Plan, "0001-abc", "x"))
	if code != 200 || env.Warning == nil || !strings.Contains(string(env.Warning), "copy") {
		t.Errorf("partial write: %d warning=%s err=%+v", code, env.Warning, env.Error)
	}
	if _, err := primary.Get(context.Background(), kind.Plan, "0001-abc"); err != nil {
		t.Errorf("primary did not keep the document: %v", err)
	}

	// A write that fails on the primary is 502.
	bad := store.NewMulti(&failing{Store: memstore.New("primary"), err: boom})
	c = &client{t: t, h: Handler(Options{Open: func(context.Context, string) (store.Store, error) { return bad, nil }})}
	code, env = c.do("PUT", base(projA, kind.Plan)+"/0001-abc", doc(kind.Plan, "0001-abc", "x"))
	if code != 502 || env.Error == nil || env.Error.Kind != ErrStore {
		t.Errorf("primary failure: %d %+v", code, env.Error)
	}
}

func TestHandlerRequiresOpen(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("Handler without Open did not panic")
		}
	}()
	Handler(Options{})
}
