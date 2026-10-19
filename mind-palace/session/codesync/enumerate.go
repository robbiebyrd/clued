package codesync

import (
	"context"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/session/gitinfo"
)

// gitFacts are the git facts of one run, each read at most once and only
// when the run needs it: the status (every run), HEAD's tree (when HEAD
// moved or a path is new to the state), the paths that differ from the
// upstream commit (when HEAD or the upstream moved, or a path is new) and
// the file list (when the run enumerates the worktree). A git failure is
// returned, never read as an empty repository.
type gitFacts struct {
	root     string
	upstream string // "" when the branch has no upstream

	// changed holds every path git status lists that exists in the
	// worktree (modified, added, renamed to, untracked): the dirty ones.
	changed map[string]bool
	// statusPaths are every path git status names, the sources of renames
	// and deleted paths included, in git's order.
	statusPaths []string

	tree     map[string]gitinfo.TreeEntry
	unpushed map[string]bool
	listed   map[string]bool
	list     []string
}

// loadStatus runs git status once.
func (g *gitFacts) loadStatus(ctx context.Context) error {
	entries, err := gitinfo.StatusPaths(ctx, g.root)
	if err != nil {
		return err
	}
	g.changed = make(map[string]bool, len(entries))
	g.statusPaths = make([]string, 0, len(entries))
	for _, e := range entries {
		g.statusPaths = append(g.statusPaths, e.Path)
		if e.From != "" {
			g.statusPaths = append(g.statusPaths, e.From)
		}
		if !e.Deleted() {
			g.changed[e.Path] = true
		}
	}
	return nil
}

// dirty reports whether rel differs from HEAD: git status lists it as
// modified, added or untracked.
func (g *gitFacts) dirty(rel string) bool { return g.changed[rel] }

// treeOIDs reads HEAD's tree once.
func (g *gitFacts) treeOIDs(ctx context.Context) (map[string]gitinfo.TreeEntry, error) {
	if g.tree != nil {
		return g.tree, nil
	}
	tree, err := gitinfo.TreeOIDs(ctx, g.root)
	if err != nil {
		return nil, err
	}
	g.tree = tree
	return tree, nil
}

// gitOID is rel's blob id in HEAD, "" when HEAD does not track it.
func (g *gitFacts) gitOID(ctx context.Context, rel string) (string, error) {
	tree, err := g.treeOIDs(ctx)
	if err != nil {
		return "", err
	}
	return tree[rel].OID, nil
}

// isUnpushed reports whether rel differs between the upstream commit and
// HEAD; every path is unpushed on a branch with no upstream.
func (g *gitFacts) isUnpushed(ctx context.Context, rel string) (bool, error) {
	if g.upstream == "" {
		return true, nil
	}
	if g.unpushed == nil {
		paths, err := gitinfo.UnpushedPaths(ctx, g.root, g.upstream)
		if err != nil {
			return false, err
		}
		g.unpushed = make(map[string]bool, len(paths))
		for _, p := range paths {
			g.unpushed[p] = true
		}
	}
	return g.unpushed[rel], nil
}

// listFiles reads the worktree's file list once: tracked plus untracked
// but not ignored (`git ls-files -co --exclude-standard`), so .gitignore
// is respected.
func (g *gitFacts) listFiles(ctx context.Context) ([]string, error) {
	if g.listed != nil {
		return g.list, nil
	}
	list, err := gitinfo.ListFiles(ctx, g.root)
	if err != nil {
		return nil, err
	}
	g.list = list
	g.listed = make(map[string]bool, len(list))
	for _, p := range list {
		g.listed[p] = true
	}
	return list, nil
}

// tracked reports whether git would list rel: it is in the file list when
// the run enumerated it; otherwise it is changed in git status, already in
// the state, or in HEAD's tree. A path none of these knows is ignored (or
// outside the repository) and is not synced.
func (g *gitFacts) tracked(ctx context.Context, rel string, st *State) (bool, error) {
	if g.listed != nil {
		return g.listed[rel], nil
	}
	if g.changed[rel] {
		return true, nil
	}
	if _, ok := st.Files[rel]; ok {
		return true, nil
	}
	tree, err := g.treeOIDs(ctx)
	if err != nil {
		return false, err
	}
	_, ok := tree[rel]
	return ok, nil
}

// candidates are the paths a run looks at, each once, sorted.
type candidates map[string]struct{}

func (c candidates) add(paths ...string) {
	for _, p := range paths {
		if rel, ok := cleanRel(p); ok {
			c[rel] = struct{}{}
		}
	}
}

func (c candidates) sorted() []string {
	out := make([]string, 0, len(c))
	for p := range c {
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}

// cleanRel accepts a root-relative slash path git reported, or one the
// state holds, and returns it cleaned; anything that could leave the root
// is refused.
func cleanRel(rel string) (string, bool) {
	if rel == "" || strings.ContainsRune(rel, 0) || strings.Contains(rel, `\`) || path.IsAbs(rel) {
		return "", false
	}
	clean := path.Clean(rel)
	if clean == "." || !filepath.IsLocal(filepath.FromSlash(clean)) {
		return "", false
	}
	return clean, true
}

// eventRel turns a path from a hook event (absolute, or relative to cwd)
// into a slash path relative to root, or false when it does not sit inside
// the worktree. The check runs on the cleaned path at the point of use:
// filepath.Rel, then filepath.IsLocal on the result.
func eventRel(root, cwd, p string) (string, bool) {
	if p == "" || strings.ContainsRune(p, 0) {
		return "", false
	}
	if !filepath.IsAbs(p) {
		if cwd == "" {
			return "", false
		}
		p = filepath.Join(cwd, p)
	}
	p = filepath.Clean(p)
	if rel, ok := localRel(root, p); ok {
		return rel, true
	}
	// The event may name the file through a symbolic link of a parent
	// directory (macOS's /var and /private/var); git reports the real root.
	if dir, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		if realRoot, err := filepath.EvalSymlinks(root); err == nil {
			return localRel(realRoot, filepath.Join(dir, filepath.Base(p)))
		}
	}
	return "", false
}

func localRel(root, abs string) (string, bool) {
	rel, err := filepath.Rel(root, abs)
	if err != nil || !filepath.IsLocal(rel) {
		return "", false
	}
	return cleanRel(filepath.ToSlash(rel))
}

// onDisk is what lstat says about one candidate.
type onDisk struct {
	info fs.FileInfo
	// mode is git's file mode: "120000" for a symbolic link, "100755" for
	// an executable file, "100644" otherwise.
	mode string
}

// statFile lstats rel under root. ok is false when the path is gone or is
// not a regular file or a symbolic link (a directory, a submodule, a
// socket); symbolic links are never followed.
func statFile(root, rel string) (onDisk, bool) {
	info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return onDisk{}, false
	}
	switch m := info.Mode(); {
	case m&fs.ModeSymlink != 0:
		return onDisk{info: info, mode: "120000"}, true
	case m.IsRegular():
		if m.Perm()&0o111 != 0 {
			return onDisk{info: info, mode: "100755"}, true
		}
		return onDisk{info: info, mode: "100644"}, true
	}
	return onDisk{}, false
}
