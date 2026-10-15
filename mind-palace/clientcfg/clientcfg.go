// Package clientcfg opens the plan and story services the way the client
// does: from the repository's mind-palace configuration and, in remote mode
// (a clued server configured in the plugin's config.json), with the server
// appended as a secondary store so the file store stays primary and every
// write fans out to the server through store.MultiStore. A project the
// server lists as a workspace's (a shared project on a clued cloud) puts
// the server first instead: reads, If-Match writes and claim leases then
// go to the server and the local stores are copies.
//
// It is the single code path behind the CLI (plan and story commands, mcp,
// serve, sync, stores, config, review), the hook status review and the
// prime_context document source; whatever opens a Palace on the client
// goes through Open so no write misses the server.
package clientcfg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/events"
	remoteclient "github.com/robbiebyrd/clued/mind-palace/remote"
	"github.com/robbiebyrd/clued/mind-palace/server/projects"
	"github.com/robbiebyrd/clued/mind-palace/service"
	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/gitinfo"
	"github.com/robbiebyrd/clued/mind-palace/store"

	// The remote definition Open appends names this driver; registering it
	// here means every caller of Open can open it.
	"github.com/robbiebyrd/clued/mind-palace/store/remotestore"
)

// RemoteStoreName is the name of the store definition Open appends in
// remote mode; `mind-palace sync --from file --to clued-server` reconciles
// the server after an outage.
const RemoteStoreName = "clued-server"

// Where a project slug came from.
const (
	// ProjectFromConfig: the remote.project key of mind-palace.config.yaml.
	ProjectFromConfig = "config"
	// ProjectFromOrigin: the repository's git origin, normalized.
	ProjectFromOrigin = "origin"
	// ProjectFromDirectory: the directory name, for a repository with no
	// origin (two unrelated folders with the same name share a namespace).
	ProjectFromDirectory = "directory"
)

// Project is the slug the server files a repository's documents under and
// how it was chosen.
type Project struct {
	Slug   string
	Source string
	// Origin is the git origin the slug was derived from (ProjectFromOrigin).
	Origin string
}

// Note is a remark worth showing the user: non-empty only when the slug was
// derived from the directory name, which is a weaker identity than an
// origin and may collide with another repository of the same name.
func (p Project) Note() string {
	if p.Source != ProjectFromDirectory {
		return ""
	}
	return fmt.Sprintf("clued-server project %q: project derived from directory name because the repository has no git origin; set remote.project in mind-palace.config.yaml to choose one", p.Slug)
}

// ResolveProject chooses the project slug for the repository at dir: the
// override (remote.project) when set, else the git origin of dir, else the
// base name of dir; each is normalized with projects.Slug.
func ResolveProject(ctx context.Context, dir, override string) Project {
	if slug := projects.Slug(override); slug != "" {
		return Project{Slug: slug, Source: ProjectFromConfig}
	}
	if origin := gitinfo.Origin(ctx, dir); origin != "" {
		if slug := projects.Slug(origin); slug != "" {
			return Project{Slug: slug, Source: ProjectFromOrigin, Origin: origin}
		}
	}
	abs := dir
	if a, err := filepath.Abs(dir); err == nil {
		abs = a
	}
	slug := projects.Slug(filepath.Base(abs))
	if slug == "" {
		slug = "default"
	}
	return Project{Slug: slug, Source: ProjectFromDirectory}
}

// RemoteStorageDef builds the clued-server store definition for a project
// from the plugin's session config. It reports false when sc is not in
// remote mode. The definition carries the token: use Redact before showing
// or storing it.
func RemoteStorageDef(sc session.Config, project string) (config.StorageDef, bool) {
	if !sc.Remote() {
		return config.StorageDef{}, false
	}
	enabled := true
	opts := map[string]string{
		remotestore.OptionURL:     sc.ServerURL,
		remotestore.OptionToken:   sc.ServerToken,
		remotestore.OptionProject: project,
	}
	if sc.ServerToken == "" && sc.ServerKey != "" {
		delete(opts, remotestore.OptionToken)
		opts[remotestore.OptionKey] = sc.ServerKey
		if sc.TokenCache != "" {
			opts[remotestore.OptionTokenCache] = sc.TokenCache
		}
	}
	if sc.ServerCAFile != "" {
		opts[remotestore.OptionCAFile] = sc.ServerCAFile
	}
	if sc.ServerInsecure {
		opts[remotestore.OptionInsecure] = strconv.FormatBool(true)
	}
	return config.StorageDef{Name: RemoteStoreName, Kind: remotestore.Driver, Enabled: &enabled, Options: opts}, true
}

// Redact returns a copy of def without the token option, fit for the
// effective configuration that getConfig and `mind-palace config` print.
func Redact(def config.StorageDef) config.StorageDef {
	out := def
	out.Options = make(map[string]string, len(def.Options))
	for k, v := range def.Options {
		if k != remotestore.OptionToken && k != remotestore.OptionKey {
			out.Options[k] = v
		}
	}
	return out
}

// Remote describes the server store Open appended.
type Remote struct {
	Project Project
	// Def is the definition as the configuration lists it (no token).
	Def config.StorageDef
	// ProbeErr is set when Options.Probe was on and the server did not
	// answer at open time. The store is still in the fan-out: writes
	// report it through store.FanoutError and reads never touch it.
	ProbeErr error
	// Shared is set when the server lists the project as one of a
	// workspace's (GET MeWorkspacesPath, asked when Options.Probe is on):
	// the server is then the primary store, so reads see the other
	// members' writes, a write over a copy someone changed since it was
	// read is refused (If-Match), and claims serialise on the server's
	// lease; the local stores follow as fan-out copies. Workspace names
	// the workspace (its name, else its id).
	Shared    bool
	Workspace string
	// SharedErr is set when the workspaces lookup failed for another
	// reason than a server without the route; the project then stays
	// local-primary.
	SharedErr error
}

// MeWorkspacesPath lists the workspaces of the token's user on a clued
// cloud; a plain clued server answers 404, which means no shared projects.
const MeWorkspacesPath = "/api/v1/me/workspaces"

// Workspace is one entry of GET MeWorkspacesPath: its id, name, the
// user's role and the project slugs bound to it, writable by every
// member. Projects may be given as slugs or as objects with a "slug".
type Workspace struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Role     string   `json:"role"`
	Projects []string `json:"projects"`
}

// UnmarshalJSON accepts projects as strings or as {"slug": …} objects.
func (w *Workspace) UnmarshalJSON(b []byte) error {
	var raw struct {
		ID       string            `json:"id"`
		Name     string            `json:"name"`
		Role     string            `json:"role"`
		Projects []json.RawMessage `json:"projects"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*w = Workspace{ID: raw.ID, Name: raw.Name, Role: raw.Role}
	for _, p := range raw.Projects {
		var slug string
		if json.Unmarshal(p, &slug) != nil {
			var obj struct {
				Slug string `json:"slug"`
			}
			if err := json.Unmarshal(p, &obj); err != nil {
				return err
			}
			slug = obj.Slug
		}
		if slug != "" {
			w.Projects = append(w.Projects, slug)
		}
	}
	return nil
}

// Workspaces asks the server for the token's workspaces (MeWorkspacesPath).
// The result is {"workspaces": [...]} or the bare list. A server without
// the route answers no workspaces and no error.
func Workspaces(ctx context.Context, c *remoteclient.Client) ([]Workspace, error) {
	var raw json.RawMessage
	err := c.Do(ctx, http.MethodGet, MeWorkspacesPath, nil, &raw)
	var re *remoteclient.Error
	switch {
	case remoteclient.IsNotFound(err), errors.As(err, &re) && re.Status == http.StatusNotFound:
		return nil, nil
	case err != nil:
		return nil, err
	}
	var wrapped struct {
		Workspaces []Workspace `json:"workspaces"`
	}
	if len(raw) > 0 && raw[0] == '[' {
		err = json.Unmarshal(raw, &wrapped.Workspaces)
	} else if len(raw) > 0 {
		err = json.Unmarshal(raw, &wrapped)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", MeWorkspacesPath, err)
	}
	return wrapped.Workspaces, nil
}

// SharedWorkspace returns the workspace among ws that binds slug.
func SharedWorkspace(ws []Workspace, slug string) (Workspace, bool) {
	for _, w := range ws {
		for _, p := range w.Projects {
			if p == slug {
				return w, true
			}
		}
	}
	return Workspace{}, false
}

// Options tunes Open.
type Options struct {
	// Probe makes one cheap request to the server at open time so an
	// unreachable server, a rejected token or a project outside the
	// token's list is reported (Remote.ProbeErr) before the first write.
	// A read-only caller leaves it off. With Probe on Open also asks
	// whether the project is shared (see Remote.Shared).
	Probe bool
}

// Opened is what Open returns: the palace, the store behind it and the
// remote part when remote mode is on.
type Opened struct {
	Palace *service.Palace
	Store  *store.MultiStore
	Remote *Remote
}

// Close closes the stores.
func (o *Opened) Close() error {
	if o == nil || o.Store == nil {
		return nil
	}
	return o.Store.Close()
}

// Open opens the repository's enabled stores from cfg, then, when sc is in
// remote mode, appends the clued server for the project of dir (see
// ResolveProject, with cfg.Remote.Project as the override) after them, and
// builds the palace. cfg.Storage gains the redacted remote definition so
// the effective configuration lists it. A remote definition that cannot be
// opened (an invalid URL, an unreadable CA file) is an error; a server that
// does not answer is not (see Remote.ProbeErr).
func Open(ctx context.Context, dir string, cfg *config.Config, sc session.Config, opts Options) (*Opened, error) {
	if cfg == nil {
		cfg = config.Default()
	}
	st, err := store.OpenAll(ctx, cfg)
	if err != nil {
		return nil, err
	}
	var remote *Remote
	if sc.Remote() {
		project := ResolveProject(ctx, dir, cfg.Remote.Project)
		def, _ := RemoteStorageDef(sc, project.Slug)
		rs, err := store.Open(def, cfg.Dirs())
		if err != nil {
			_ = st.Close()
			return nil, fmt.Errorf("storage %q: %w", RemoteStoreName, err)
		}
		remote = &Remote{Project: project, Def: Redact(def)}
		if opts.Probe {
			remote.ProbeErr = rs.Init(ctx)
			if c, ok := rs.(interface{ Client() *remoteclient.Client }); ok && remote.ProbeErr == nil {
				ws, err := Workspaces(ctx, c.Client())
				remote.SharedErr = err
				if w, ok := SharedWorkspace(ws, project.Slug); ok {
					remote.Shared, remote.Workspace = true, w.Name
					if remote.Workspace == "" {
						remote.Workspace = w.ID
					}
				}
			}
		}
		if remote.Shared {
			cfg.Storage = append([]config.StorageDef{remote.Def}, cfg.Storage...)
			st = store.NewMulti(append([]store.Store{rs}, st.Stores()...)...)
		} else {
			cfg.Storage = append(cfg.Storage, remote.Def)
			st = store.NewMulti(append(st.Stores(), rs)...)
		}
	}
	palace, err := service.NewPalace(cfg, st, events.New())
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	return &Opened{Palace: palace, Store: st, Remote: remote}, nil
}
