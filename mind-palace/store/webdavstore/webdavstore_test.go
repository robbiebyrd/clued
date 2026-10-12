package webdavstore

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/webdav"

	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/model"
	"github.com/robbiebyrd/clued/mind-palace/store"
	"github.com/robbiebyrd/clued/mind-palace/store/storetest"
)

// newServer serves a real WebDAV tree from a temporary directory under /dav/.
// With user set, every request must carry matching basic auth.
func newServer(t *testing.T, user, pass string) *httptest.Server {
	t.Helper()
	h := &webdav.Handler{Prefix: "/dav", FileSystem: webdav.Dir(t.TempDir()), LockSystem: webdav.NewMemLS()}
	var handler http.Handler = h
	if user != "" {
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u, p, ok := r.BasicAuth()
			if !ok || u != user || p != pass {
				w.Header().Set("WWW-Authenticate", `Basic realm="dav"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			h.ServeHTTP(w, r)
		})
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func openStore(t *testing.T, srv *httptest.Server, o Options) *Store {
	t.Helper()
	s, err := New("webdav", o)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Init(context.Background()); err != nil {
		t.Fatalf("init against %s: %v", srv.URL, err)
	}
	return s
}

func TestConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Store {
		srv := newServer(t, "", "")
		return openStore(t, srv, Options{URL: srv.URL + "/dav/palace"})
	})
}

func TestConformanceWithBasicAuthAndCustomPaths(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Store {
		srv := newServer(t, "robbie", "s3cret")
		return openStore(t, srv, Options{
			URL:      srv.URL + "/dav/notes/mind palace/",
			Paths:    map[string]string{kind.Plan: "my plans", kind.Story: "issues/open"},
			Username: "robbie", Password: "s3cret",
		})
	})
}

// Optional: MIND_PALACE_TEST_WEBDAV_URL (plus MIND_PALACE_WEBDAV_USERNAME /
// MIND_PALACE_WEBDAV_PASSWORD or MIND_PALACE_WEBDAV_TOKEN) runs the suite
// against a real server, in a fresh collection named after the test.
func TestConformanceAgainstRealServer(t *testing.T) {
	base := os.Getenv("MIND_PALACE_TEST_WEBDAV_URL")
	if base == "" {
		t.Skip("set MIND_PALACE_TEST_WEBDAV_URL to run the WebDAV conformance suite against a server")
	}
	storetest.Run(t, func(t *testing.T) store.Store {
		s, err := NewFromOptions("webdav", map[string]string{"url": strings.TrimSuffix(base, "/") + "/mind-palace-test-" + time.Now().UTC().Format("20060102T150405.000000000")})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Init(context.Background()); err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestWrongCredentialsFailWithoutLeakingThem(t *testing.T) {
	srv := newServer(t, "robbie", "s3cret")
	s, err := New("webdav", Options{URL: srv.URL + "/dav/x", Username: "robbie", Password: "wrong"})
	if err != nil {
		t.Fatal(err)
	}
	err = s.Init(context.Background())
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("want a 401 error, got %v", err)
	}
	if strings.Contains(err.Error(), "wrong") || strings.Contains(err.Error(), "robbie") {
		t.Errorf("credentials leaked into the error: %v", err)
	}
}

func TestBearerTokenWinsOverBasicAuth(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	s, err := New("webdav", Options{URL: srv.URL + "/d", Username: "u", Password: "p", Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.mkcol(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	if got != "Bearer tok" {
		t.Errorf("Authorization = %q", got)
	}
}

func TestURLUserinfoBecomesBasicAuthAndIsStripped(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	u := strings.Replace(srv.URL, "http://", "http://alice:pw@", 1) + "/d"
	s, err := New("webdav", Options{URL: u})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s.Base(), "alice") || strings.Contains(s.Base(), "pw") {
		t.Errorf("base keeps credentials: %s", s.Base())
	}
	if err := s.mkcol(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	r, _ := http.NewRequest("GET", "/", nil)
	r.SetBasicAuth("alice", "pw")
	if got != r.Header.Get("Authorization") {
		t.Errorf("Authorization = %q", got)
	}
}

func TestNewFromOptionsDefaultsAndEnvironment(t *testing.T) {
	if _, err := NewFromOptions("x", map[string]string{}); err == nil || !strings.Contains(err.Error(), "url") {
		t.Errorf("missing url: %v", err)
	}
	if _, err := NewFromOptions("x", map[string]string{"url": "ftp://h/p"}); err == nil {
		t.Error("non-http url accepted")
	}
	if _, err := NewFromOptions("x", map[string]string{"url": "http://h/p", "timeout": "soon"}); err == nil {
		t.Error("bad timeout accepted")
	}
	if _, err := NewFromOptions("x", map[string]string{"url": "http://h/p", "plansPath": "../x"}); err == nil {
		t.Error("path escaping the base accepted")
	}
	t.Setenv("MIND_PALACE_WEBDAV_USERNAME", "envuser")
	t.Setenv("MIND_PALACE_WEBDAV_PASSWORD", "envpass")
	t.Setenv("MIND_PALACE_WEBDAV_TOKEN", "")
	s, err := NewFromOptions("", map[string]string{"url": "https://cloud.example/remote.php/dav/files/me/palace", "storiesPath": "/issues/"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Name() != "webdav" || s.Driver() != "webdav" || s.user != "envuser" || s.pass != "envpass" {
		t.Errorf("store = %+v", s)
	}
	if s.roots[kind.Plan] != "plans" || s.roots[kind.Story] != "issues" {
		t.Errorf("roots = %v", s.roots)
	}
	if s.Base() != "https://cloud.example/remote.php/dav/files/me/palace/" {
		t.Errorf("base = %s", s.Base())
	}
	if s.href("plans/0001-abc-impl-a b.md") != "https://cloud.example/remote.php/dav/files/me/palace/plans/0001-abc-impl-a%20b.md" {
		t.Errorf("href = %s", s.href("plans/0001-abc-impl-a b.md"))
	}
	if s.http.Timeout != DefaultTimeout {
		t.Errorf("timeout = %v", s.http.Timeout)
	}
}

func TestParseMultistatusNextcloudStyle(t *testing.T) {
	body := `<?xml version="1.0"?>
<d:multistatus xmlns:d="DAV:" xmlns:s="http://sabredav.org/ns" xmlns:oc="http://owncloud.org/ns">
 <d:response>
  <d:href>/remote.php/dav/files/me/palace/plans/</d:href>
  <d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype><d:getlastmodified>Mon, 12 Oct 2026 10:00:00 GMT</d:getlastmodified></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat>
 </d:response>
 <d:response>
  <d:href>/remote.php/dav/files/me/palace/plans/archive/</d:href>
  <d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat>
 </d:response>
 <d:response>
  <d:href>/remote.php/dav/files/me/palace/plans/0001-abc-impl-plan%20service.md</d:href>
  <d:propstat><d:prop><d:resourcetype/><d:getlastmodified>Mon, 12 Oct 2026 11:30:00 GMT</d:getlastmodified></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat>
  <d:propstat><d:prop><oc:share-types/></d:prop><d:status>HTTP/1.1 404 Not Found</d:status></d:propstat>
 </d:response>
</d:multistatus>`
	entries, err := parseMultistatus([]byte(body), "/remote.php/dav/files/me/palace/plans/")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %+v", entries)
	}
	if entries[0].Name != "0001-abc-impl-plan service.md" || entries[0].Dir || entries[0].Modified.UTC().Hour() != 11 {
		t.Errorf("file entry = %+v", entries[0])
	}
	if entries[1].Name != "archive" || !entries[1].Dir {
		t.Errorf("dir entry = %+v", entries[1])
	}
	if _, err := parseMultistatus([]byte("<html>not dav</html>"), "/x/"); err == nil {
		t.Error("non-multistatus body accepted")
	}
}

func TestMissingCollectionsReadAsEmptyAndMissingFilesAsNotFound(t *testing.T) {
	srv := newServer(t, "", "")
	s := openStore(t, srv, Options{URL: srv.URL + "/dav/p"})
	ctx := context.Background()
	// Templates collections are created lazily, so listing them is empty, not an error.
	if list, err := s.ListTemplates(ctx, kind.Plan); err != nil || len(list) != 0 {
		t.Errorf("templates before any put: %v %v", list, err)
	}
	if _, err := s.get(ctx, "plans/nope.md"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing file: %v", err)
	}
	if err := s.Put(ctx, &model.Document{Kind: kind.Plan, Path: "../escape.md", FrontMatter: model.FrontMatter{ID: "0001-aaa"}}); err == nil {
		t.Error("path escaping the collection accepted")
	}
	if _, err := s.GetTemplate(ctx, kind.Plan, "../x"); err == nil {
		t.Error("template id with a path accepted")
	}
}
