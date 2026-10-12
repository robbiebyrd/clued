// Package projects namespaces plan and story storage on the server by
// project. A project is identified by a slug derived from the repository's
// git origin (github.com__owner__repo); the Opener builds one store.Store
// per slug from the server's storage definitions by stamping the slug into
// each plugin's prefix option, so repositories that share document ids
// (0001-v1i exists everywhere) never collide.
package projects

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/store"
)

// MaxSlugLen bounds a project slug.
const MaxSlugLen = 200

// ErrInvalidProject is returned for a slug that fails ValidSlug.
var ErrInvalidProject = errors.New("invalid project slug")

// ErrClosed is returned by Open after Close.
var ErrClosed = errors.New("project opener is closed")

// slugRe is the shape of a slug: it starts with a letter or digit and
// continues with letters, digits, dots, underscores and dashes.
var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// slugJunkRe matches what Slug replaces in an origin after lower-casing.
var slugJunkRe = regexp.MustCompile(`[^a-z0-9._-]+`)

// ValidSlug reports whether s is an acceptable project slug: lower-case
// letters, digits, dots, underscores and dashes, starting with a letter or
// digit, no "..", at most MaxSlugLen bytes, and a local path (no separator,
// no parent reference) so it is safe to join onto a directory.
func ValidSlug(s string) bool {
	if s == "" || len(s) > MaxSlugLen {
		return false
	}
	if !slugRe.MatchString(s) || strings.Contains(s, "..") {
		return false
	}
	return filepath.IsLocal(s) && !strings.ContainsAny(s, `/\`)
}

// Slug normalizes a git origin to a project slug: host and path,
// lower-cased, with user info, port, a leading "/" and a trailing ".git"
// removed and every "/" encoded as "__". The three common forms yield the
// same slug:
//
//	git@github.com:owner/repo.git        → github.com__owner__repo
//	https://github.com/owner/repo.git    → github.com__owner__repo
//	ssh://git@github.com/owner/repo      → github.com__owner__repo
//
// A bare path (a local origin or a directory name) becomes its segments
// joined with "__". Characters outside the slug alphabet become "-". An
// empty origin yields "".
func Slug(origin string) string {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return ""
	}
	host, path := splitOrigin(origin)
	host = strings.ToLower(host)
	if i := strings.LastIndex(host, ":"); i >= 0 && !strings.Contains(host, "]") {
		host = host[:i] // port
	}
	host = strings.Trim(host, "[]")
	path = strings.ToLower(path)
	path = strings.ReplaceAll(path, `\`, "/")
	path = strings.Trim(path, "/")
	path = strings.TrimSuffix(path, ".git")
	path = strings.Trim(path, "/")
	var parts []string
	if host != "" {
		parts = append(parts, host)
	}
	for _, p := range strings.Split(path, "/") {
		if p != "" && p != "." && p != ".." {
			parts = append(parts, p)
		}
	}
	s := strings.Join(parts, "__")
	s = slugJunkRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "._-")
	for strings.Contains(s, "..") {
		s = strings.ReplaceAll(s, "..", ".")
	}
	if len(s) > MaxSlugLen {
		s = strings.TrimRight(s[:MaxSlugLen], "._-")
	}
	return s
}

// splitOrigin separates an origin into host and path, handling URLs
// (ssh://, https://, git://, file://), scp-like forms (user@host:path) and
// bare paths.
func splitOrigin(origin string) (host, path string) {
	if i := strings.Index(origin, "://"); i > 0 {
		if u, err := url.Parse(origin); err == nil {
			return u.Host, u.Path
		}
		return "", origin[i+3:]
	}
	// scp-like: [user@]host:path, as long as the part before ":" has no "/"
	// (otherwise it is a Windows drive or a local path).
	if i := strings.Index(origin, ":"); i > 0 && !strings.Contains(origin[:i], "/") {
		host = origin[:i]
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
		return host, origin[i+1:]
	}
	return "", origin
}

// sqlIdentRe keeps table prefixes inside the identifier alphabet sqlstore accepts.
var sqlIdentRe = regexp.MustCompile(`[^A-Za-z0-9_]+`)

// sqlKinds are the storage kinds served by sqlstore (dialects and aliases).
var sqlKinds = map[string]bool{
	"sqlite": true, "sqlite3": true, "postgres": true, "postgresql": true, "pgx": true,
	"mysql": true, "mariadb": true, "clickhouse": true,
}

// Stamp returns a copy of def whose plugin prefix option names the project:
//
//	file                 dir: <root>/<project>, plansDir/storiesDir below it
//	sqlite/postgres/...  tablePrefix: <project>_ (dots and dashes become "_")
//	mongodb/mongo        collection: <project>_
//	firestore            collection: <project>_
//	memory               unchanged (store.Open yields a fresh store per call)
//
// A configured prefix is kept in front of the project, so an operator's
// "srv_" becomes "srv_<project>_". For the file plugin the root is the dir
// option, or the parent of the plans directory (dirs, then the default) when
// none is set. Unknown kinds are an error: a plugin whose namespace is not
// known cannot be shared between projects safely.
func Stamp(def config.StorageDef, dirs map[string]string, project string) (config.StorageDef, error) {
	if !ValidSlug(project) {
		return config.StorageDef{}, fmt.Errorf("%w: %q", ErrInvalidProject, project)
	}
	out := def
	out.Options = make(map[string]string, len(def.Options)+2)
	for k, v := range def.Options {
		out.Options[k] = v
	}
	switch k := strings.ToLower(def.Kind); {
	case k == "file":
		root := def.Options["dir"]
		if root == "" {
			plans := def.Options["plansDir"]
			if plans == "" {
				plans = dirs[kind.Plan]
			}
			if plans == "" {
				plans = kind.Must(kind.Plan).DefaultDir
			}
			root = filepath.Dir(filepath.Clean(plans))
		}
		// project passed ValidSlug: a single local path segment.
		base := filepath.Join(root, filepath.Clean(project))
		out.Options["dir"] = base
		for _, kk := range kind.All() {
			out.Options[kk.Plural+"Dir"] = filepath.Join(base, kk.Plural)
		}
	case sqlKinds[k]:
		p := sqlIdentRe.ReplaceAllString(strings.NewReplacer(".", "_", "-", "_").Replace(project), "_")
		prefix := def.Options["tablePrefix"] + p + "_"
		if prefix[0] >= '0' && prefix[0] <= '9' {
			prefix = "_" + prefix
		}
		out.Options["tablePrefix"] = prefix
	case k == "mongodb" || k == "mongo" || k == "firestore":
		out.Options["collection"] = def.Options["collection"] + project + "_"
	case k == "memory":
		// Nothing to stamp: every Open returns an empty store.
	default:
		return config.StorageDef{}, fmt.Errorf("storage %q: kind %q cannot be namespaced per project", def.Name, def.Kind)
	}
	return out, nil
}

// Opener opens and caches one store per project from a set of storage
// definitions (the server's template; disabled entries are skipped).
type Opener struct {
	defs []config.StorageDef
	dirs map[string]string

	mu     sync.Mutex
	stores map[string]*entry
	closed bool
}

type entry struct {
	done  chan struct{}
	store store.Store
	err   error
}

// NewOpener returns an Opener over the enabled definitions in defs. dirs
// maps kind names to the configured directories, as config.Dirs does.
func NewOpener(defs []config.StorageDef, dirs map[string]string) *Opener {
	o := &Opener{dirs: map[string]string{}, stores: map[string]*entry{}}
	for _, d := range defs {
		if d.IsEnabled() {
			o.defs = append(o.defs, d)
		}
	}
	for k, v := range dirs {
		o.dirs[k] = v
	}
	return o
}

// Open returns the project's store, opening it on first use. The result is
// a MultiStore over every enabled definition (the first is primary), each
// stamped with the project; it is cached, so later calls return the same
// store. An invalid slug is ErrInvalidProject.
func (o *Opener) Open(ctx context.Context, project string) (store.Store, error) {
	if !ValidSlug(project) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidProject, project)
	}
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return nil, ErrClosed
	}
	e, ok := o.stores[project]
	if ok {
		o.mu.Unlock()
		select {
		case <-e.done:
			return e.store, e.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	e = &entry{done: make(chan struct{})}
	o.stores[project] = e
	o.mu.Unlock()

	e.store, e.err = o.open(ctx, project)
	if e.err != nil {
		// Leave the slot free so the next call retries (a database that
		// was down, for instance).
		o.mu.Lock()
		if o.stores[project] == e {
			delete(o.stores, project)
		}
		o.mu.Unlock()
	}
	close(e.done)
	return e.store, e.err
}

func (o *Opener) open(ctx context.Context, project string) (store.Store, error) {
	if len(o.defs) == 0 {
		return nil, errors.New("no storage plugin is enabled")
	}
	var stores []store.Store
	fail := func(err error) (store.Store, error) {
		for _, s := range stores {
			_ = s.Close()
		}
		return nil, err
	}
	for _, def := range o.defs {
		stamped, err := Stamp(def, o.dirs, project)
		if err != nil {
			return fail(err)
		}
		s, err := store.Open(stamped, o.dirs)
		if err != nil {
			return fail(fmt.Errorf("project %q: %w", project, err))
		}
		if err := s.Init(ctx); err != nil {
			_ = s.Close()
			return fail(fmt.Errorf("project %q: storage %q: init: %w", project, def.Name, err))
		}
		stores = append(stores, s)
	}
	return store.NewMulti(stores...), nil
}

// Projects lists the cached project slugs in sorted order.
func (o *Opener) Projects() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]string, 0, len(o.stores))
	for p, e := range o.stores {
		select {
		case <-e.done:
			if e.err == nil {
				out = append(out, p)
			}
		default:
		}
	}
	sort.Strings(out)
	return out
}

// Close closes every cached store and refuses further opens.
func (o *Opener) Close(ctx context.Context) error {
	o.mu.Lock()
	o.closed = true
	entries := o.stores
	o.stores = map[string]*entry{}
	o.mu.Unlock()
	var errs []error
	for p, e := range entries {
		select {
		case <-e.done:
		case <-ctx.Done():
			errs = append(errs, fmt.Errorf("project %q: %w", p, ctx.Err()))
			continue
		}
		if e.err == nil && e.store != nil {
			if err := e.store.Close(); err != nil {
				errs = append(errs, fmt.Errorf("project %q: %w", p, err))
			}
		}
	}
	return errors.Join(errs...)
}
