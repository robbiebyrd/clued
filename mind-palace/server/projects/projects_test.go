package projects

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/model"
	"github.com/robbiebyrd/clued/mind-palace/store"
	_ "github.com/robbiebyrd/clued/mind-palace/store/filestore"
	_ "github.com/robbiebyrd/clued/mind-palace/store/memstore"
	"github.com/robbiebyrd/clued/mind-palace/store/sqlstore"
)

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"git@github.com:owner/repo.git":                         "github.com__owner__repo",
		"https://github.com/owner/repo.git":                     "github.com__owner__repo",
		"ssh://git@github.com/owner/repo":                       "github.com__owner__repo",
		"ssh://git@GitHub.com:22/Owner/Repo/":                   "github.com__owner__repo",
		"https://user:pw@gitlab.example.org/group/sub/repo.git": "gitlab.example.org__group__sub__repo",
		"git@github.com:owner/repo":                             "github.com__owner__repo",
		"/home/me/Projects/scratch":                             "home__me__projects__scratch",
		"scratch":                                               "scratch",
		"file:///srv/git/repo.git":                              "srv__git__repo",
		"git@host:owner/re po~x.git":                            "host__owner__re-po-x",
		"":                                                      "",
		"   ":                                                   "",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
		if want != "" && !ValidSlug(want) {
			t.Errorf("Slug(%q) = %q is not a valid slug", in, want)
		}
	}
}

func TestValidSlug(t *testing.T) {
	for _, ok := range []string{"a", "github.com__owner__repo", "0abc", "a.b-c_d", strings.Repeat("a", 200)} {
		if !ValidSlug(ok) {
			t.Errorf("ValidSlug(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "..", ".", "a..b", "a/b", "/a", `a\b`, "A", "Repo", "-a", ".a", "_a", "a b", "a$", "../etc", strings.Repeat("a", 201)} {
		if ValidSlug(bad) {
			t.Errorf("ValidSlug(%q) = true", bad)
		}
	}
}

func TestStamp(t *testing.T) {
	dirs := map[string]string{kind.Plan: "/srv/docs/plans", kind.Story: "/srv/docs/stories"}
	const project = "github.com__owner__my-repo"

	t.Run("file", func(t *testing.T) {
		d, err := Stamp(config.StorageDef{Name: "file", Kind: "file"}, dirs, project)
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]string{
			"dir":        filepath.Join("/srv/docs", project),
			"plansDir":   filepath.Join("/srv/docs", project, "plans"),
			"storiesDir": filepath.Join("/srv/docs", project, "stories"),
		}
		for k, v := range want {
			if d.Options[k] != v {
				t.Errorf("file %s = %q, want %q", k, d.Options[k], v)
			}
		}
		// An explicit dir option is the root.
		d, err = Stamp(config.StorageDef{Name: "file", Kind: "file", Options: map[string]string{"dir": "/data"}}, dirs, project)
		if err != nil {
			t.Fatal(err)
		}
		if d.Options["dir"] != filepath.Join("/data", project) || d.Options["plansDir"] != filepath.Join("/data", project, "plans") {
			t.Errorf("file with dir: %v", d.Options)
		}
	})
	t.Run("sql", func(t *testing.T) {
		for _, k := range []string{"sqlite", "postgres", "mysql", "clickhouse", "postgresql", "mariadb"} {
			d, err := Stamp(config.StorageDef{Name: "db", Kind: k, Options: map[string]string{"dsn": "x"}}, dirs, project)
			if err != nil {
				t.Fatal(err)
			}
			if d.Options["tablePrefix"] != "github_com__owner__my_repo_" {
				t.Errorf("%s tablePrefix = %q", k, d.Options["tablePrefix"])
			}
			if d.Options["dsn"] != "x" {
				t.Errorf("%s dsn lost: %v", k, d.Options)
			}
		}
		d, _ := Stamp(config.StorageDef{Name: "db", Kind: "sqlite", Options: map[string]string{"tablePrefix": "srv_"}}, dirs, project)
		if d.Options["tablePrefix"] != "srv_github_com__owner__my_repo_" {
			t.Errorf("configured prefix kept: %q", d.Options["tablePrefix"])
		}
		d, _ = Stamp(config.StorageDef{Name: "db", Kind: "sqlite"}, dirs, "1st.repo")
		if d.Options["tablePrefix"] != "_1st_repo_" {
			t.Errorf("leading digit: %q", d.Options["tablePrefix"])
		}
	})
	t.Run("mongo and firestore", func(t *testing.T) {
		for _, k := range []string{"mongodb", "mongo", "firestore"} {
			d, err := Stamp(config.StorageDef{Name: "db", Kind: k, Options: map[string]string{"database": "mp"}}, dirs, project)
			if err != nil {
				t.Fatal(err)
			}
			if d.Options["collection"] != project+"_" || d.Options["database"] != "mp" {
				t.Errorf("%s options = %v", k, d.Options)
			}
		}
		d, _ := Stamp(config.StorageDef{Name: "db", Kind: "mongodb", Options: map[string]string{"collection": "srv_"}}, dirs, project)
		if d.Options["collection"] != "srv_"+project+"_" {
			t.Errorf("configured collection kept: %q", d.Options["collection"])
		}
	})
	t.Run("memory and unknown", func(t *testing.T) {
		d, err := Stamp(config.StorageDef{Name: "mem", Kind: "memory"}, dirs, project)
		if err != nil || len(d.Options) != 0 {
			t.Errorf("memory: %v %v", d.Options, err)
		}
		if _, err := Stamp(config.StorageDef{Name: "x", Kind: "clued"}, dirs, project); err == nil {
			t.Error("unknown kind accepted")
		}
		if _, err := Stamp(config.StorageDef{Name: "x", Kind: "file"}, dirs, "../etc"); !errors.Is(err, ErrInvalidProject) {
			t.Errorf("bad slug: %v", err)
		}
	})
	t.Run("does not alias the input", func(t *testing.T) {
		in := config.StorageDef{Name: "db", Kind: "mongodb", Options: map[string]string{"collection": "a_"}}
		if _, err := Stamp(in, dirs, project); err != nil {
			t.Fatal(err)
		}
		if in.Options["collection"] != "a_" {
			t.Error("Stamp mutated the input definition")
		}
	})
}

func doc(k, id string) *model.Document {
	return &model.Document{Kind: k, Path: model.FileName(id, "feat", "x"), FrontMatter: model.FrontMatter{
		ID: id, Title: "T " + id, Type: "feat", Status: "pending", Priority: "2",
		Created: "2026-01-01T00:00:00.000Z", Updated: "2026-01-01T00:00:00.000Z",
	}, Content: "# T " + id + "\n"}
}

func TestOpenerInvalidAndCache(t *testing.T) {
	ctx := context.Background()
	o := NewOpener([]config.StorageDef{{Name: "mem", Kind: "memory"}}, nil)
	for _, bad := range []string{"", "..", "a/b", "A", "../x"} {
		if _, err := o.Open(ctx, bad); !errors.Is(err, ErrInvalidProject) {
			t.Errorf("Open(%q) err = %v, want ErrInvalidProject", bad, err)
		}
	}
	a1, err := o.Open(ctx, "github.com__o__a")
	if err != nil {
		t.Fatal(err)
	}
	a2, err := o.Open(ctx, "github.com__o__a")
	if err != nil {
		t.Fatal(err)
	}
	if a1 != a2 {
		t.Error("cache returned a different store for the same project")
	}
	if _, ok := a1.(*store.MultiStore); !ok {
		t.Errorf("store is %T, want *store.MultiStore", a1)
	}
	b, err := o.Open(ctx, "github.com__o__b")
	if err != nil {
		t.Fatal(err)
	}
	if b == a1 {
		t.Error("two projects share one store")
	}
	if got := o.Projects(); len(got) != 2 || got[0] != "github.com__o__a" || got[1] != "github.com__o__b" {
		t.Errorf("Projects = %v", got)
	}
	if err := o.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Open(ctx, "github.com__o__a"); !errors.Is(err, ErrClosed) {
		t.Errorf("Open after Close: %v", err)
	}
	// A disabled definition is skipped; nothing enabled is an error.
	off := false
	o = NewOpener([]config.StorageDef{{Name: "mem", Kind: "memory", Enabled: &off}}, nil)
	if _, err := o.Open(ctx, "p"); err == nil {
		t.Error("expected an error with no enabled store")
	}
}

func TestOpenerProjectsAreIndependent(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dbPath := filepath.Join(root, "mind-palace.sqlite")
	defs := []config.StorageDef{
		{Name: "file", Kind: "file", Options: map[string]string{"dir": filepath.Join(root, "docs")}},
		{Name: "db", Kind: "sqlite", Options: map[string]string{"dsn": dbPath}},
		{Name: "mem", Kind: "memory"},
	}
	o := NewOpener(defs, map[string]string{kind.Plan: filepath.Join(root, "docs", "plans"), kind.Story: filepath.Join(root, "docs", "stories")})
	defer o.Close(ctx)

	a, err := o.Open(ctx, "github.com__o__a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := o.Open(ctx, "github.com__o__b")
	if err != nil {
		t.Fatal(err)
	}
	da, db := doc(kind.Plan, "0001-abc"), doc(kind.Plan, "0001-abc")
	da.FrontMatter.Title, db.FrontMatter.Title = "project a", "project b"
	if err := a.Put(ctx, da); err != nil {
		t.Fatal(err)
	}
	if err := b.Put(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := a.PutTemplate(ctx, &model.Template{Kind: kind.Story, ID: "default", Content: "A"}); err != nil {
		t.Fatal(err)
	}

	// Every copy of each project sees only its own document.
	for _, tc := range []struct {
		name  string
		st    store.Store
		title string
	}{{"a", a, "project a"}, {"b", b, "project b"}} {
		for _, s := range tc.st.(*store.MultiStore).Stores() {
			got, err := s.Get(ctx, kind.Plan, "0001-abc")
			if err != nil {
				t.Fatalf("%s/%s: %v", tc.name, s.Name(), err)
			}
			if got.FrontMatter.Title != tc.title {
				t.Errorf("%s/%s: title = %q, want %q", tc.name, s.Name(), got.FrontMatter.Title, tc.title)
			}
			list, err := s.List(ctx, kind.Plan)
			if err != nil || len(list) != 1 {
				t.Errorf("%s/%s: list = %d docs, %v", tc.name, s.Name(), len(list), err)
			}
		}
	}
	if _, err := b.GetTemplate(ctx, kind.Story, "default"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("template leaked into project b: %v", err)
	}
	if err := a.Delete(ctx, kind.Plan, "0001-abc"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Get(ctx, kind.Plan, "0001-abc"); err != nil {
		t.Errorf("delete in a removed b's document: %v", err)
	}

	// The file copies live under <dir>/<project>/<plural>.
	for _, p := range []string{"github.com__o__b"} {
		if _, err := os.Stat(filepath.Join(root, "docs", p, "plans", "0001-abc-feat-x.md")); err != nil {
			t.Errorf("file copy: %v", err)
		}
		if _, err := os.Stat(filepath.Join(root, "docs", p, "stories", "archive")); err != nil {
			t.Errorf("stories root not initialised: %v", err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "github.com__o__a", "plans", "0001-abc-feat-x.md")); !os.IsNotExist(err) {
		t.Errorf("deleted file copy still present: %v", err)
	}

	// The SQL copies share one database with distinct table prefixes.
	sq, ok := a.(*store.MultiStore).Stores()[1].(*sqlstore.Store)
	if !ok {
		t.Fatalf("second store is %T", a.(*store.MultiStore).Stores()[1])
	}
	rows, err := sq.DB().Query(`SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, n)
	}
	for _, want := range []string{"github_com__o__a_plans", "github_com__o__a_stories", "github_com__o__a_templates", "github_com__o__b_plans", "github_com__o__b_stories", "github_com__o__b_templates"} {
		found := false
		for _, tb := range tables {
			if tb == want {
				found = true
			}
		}
		if !found {
			t.Errorf("table %s missing from %v", want, tables)
		}
	}
}
