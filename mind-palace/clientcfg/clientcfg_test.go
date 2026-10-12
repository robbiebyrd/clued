package clientcfg

import (
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/session"
	_ "github.com/robbiebyrd/clued/mind-palace/store/filestore"
)

var ctx = context.Background()

func gitRepo(t *testing.T, origin string) string {
	t.Helper()
	if _, err := osexec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := t.TempDir()
	args := [][]string{{"init", "-q"}}
	if origin != "" {
		args = append(args, []string{"remote", "add", "origin", origin})
	}
	for _, a := range args {
		if out, err := osexec.Command("git", append([]string{"-C", dir}, a...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
	return dir
}

func TestResolveProjectFromOrigin(t *testing.T) {
	dir := gitRepo(t, "git@github.com:Owner/Repo.git")
	p := ResolveProject(ctx, dir, "")
	if p.Slug != "github.com__owner__repo" || p.Source != ProjectFromOrigin {
		t.Errorf("project = %+v", p)
	}
	if p.Note() != "" {
		t.Errorf("a project from the origin needs no note, got %q", p.Note())
	}
}

func TestResolveProjectOverrideWins(t *testing.T) {
	dir := gitRepo(t, "git@github.com:o/r.git")
	p := ResolveProject(ctx, dir, "Team/Thing")
	if p.Slug != "team__thing" || p.Source != ProjectFromConfig {
		t.Errorf("project = %+v", p)
	}
}

func TestResolveProjectFallsBackToDirectoryName(t *testing.T) {
	for _, dir := range []string{gitRepo(t, ""), t.TempDir()} {
		dir = filepath.Join(dir, "Scratch Pad")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		p := ResolveProject(ctx, dir, "")
		if p.Slug != "scratch-pad" || p.Source != ProjectFromDirectory {
			t.Errorf("project for %s = %+v", dir, p)
		}
		if !strings.Contains(p.Note(), "project derived from directory name") || !strings.Contains(p.Note(), "scratch-pad") {
			t.Errorf("note = %q", p.Note())
		}
	}
}

func TestRemoteStorageDef(t *testing.T) {
	if _, ok := RemoteStorageDef(session.Config{}, "p"); ok {
		t.Error("a definition without a server URL")
	}
	def, ok := RemoteStorageDef(session.Config{ServerURL: "https://clued.example/", ServerToken: "tok", ServerCAFile: "/ca.pem", ServerInsecure: true}, "github.com__o__r")
	if !ok {
		t.Fatal("no definition in remote mode")
	}
	if def.Name != RemoteStoreName || def.Kind != "clued" || !def.IsEnabled() {
		t.Errorf("def = %+v", def)
	}
	want := map[string]string{"url": "https://clued.example/", "token": "tok", "project": "github.com__o__r", "caFile": "/ca.pem", "insecure": "true"}
	for k, v := range want {
		if def.Options[k] != v {
			t.Errorf("option %s = %q, want %q", k, def.Options[k], v)
		}
	}
	def, _ = RemoteStorageDef(session.Config{ServerURL: "https://clued.example", ServerToken: "tok"}, "p")
	for _, k := range []string{"caFile", "insecure"} {
		if _, set := def.Options[k]; set {
			t.Errorf("option %s set although empty", k)
		}
	}
	public := Redact(def)
	if _, set := public.Options["token"]; set {
		t.Error("Redact kept the token")
	}
	if def.Options["token"] != "tok" {
		t.Error("Redact changed the original")
	}
}

func TestOpenWithoutRemoteModeIsUnchanged(t *testing.T) {
	cfg := config.Default()
	root := t.TempDir()
	cfg.Plans.Dir = filepath.Join(root, "docs", "plans")
	cfg.Stories.Dir = filepath.Join(root, "docs", "stories")
	o, err := Open(ctx, root, cfg, session.Config{}, Options{Probe: true})
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	if o.Remote != nil || len(o.Store.Stores()) != 1 || len(cfg.Storage) != 1 {
		t.Errorf("remote = %v, stores = %d, defs = %d", o.Remote, len(o.Store.Stores()), len(cfg.Storage))
	}
}

func TestOpenRejectsAnInvalidServerURL(t *testing.T) {
	cfg := config.Default()
	root := t.TempDir()
	cfg.Plans.Dir = filepath.Join(root, "docs", "plans")
	cfg.Stories.Dir = filepath.Join(root, "docs", "stories")
	_, err := Open(ctx, root, cfg, session.Config{ServerURL: "http://clued.example", ServerToken: "tok"}, Options{})
	if err == nil || !strings.Contains(err.Error(), RemoteStoreName) {
		t.Errorf("err = %v, want one naming %s", err, RemoteStoreName)
	}
	// The config must not keep a half-added definition.
	if len(cfg.Storage) != 1 {
		t.Errorf("storage definitions after a failed open = %+v", cfg.Storage)
	}
}
