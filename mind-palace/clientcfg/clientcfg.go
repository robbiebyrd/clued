// Package clientcfg opens the plan and story services the way the client
// does: from the repository's mind-palace configuration and, in remote mode
// (a clued server configured in the plugin's config.json), with the server
// appended as a secondary store so the file store stays primary and every
// write fans out to the server through store.MultiStore.
//
// It is the single code path behind the CLI (plan and story commands, mcp,
// serve, sync, stores, config, review), the hook status review and the
// prime_context document source; whatever opens a Palace on the client
// goes through Open so no write misses the server.
package clientcfg

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/events"
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
}

// Options tunes Open.
type Options struct {
	// Probe makes one cheap request to the server at open time so an
	// unreachable server, a rejected token or a project outside the
	// token's list is reported (Remote.ProbeErr) before the first write.
	// A read-only caller leaves it off.
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
		}
		cfg.Storage = append(cfg.Storage, remote.Def)
		st = store.NewMulti(append(st.Stores(), rs)...)
	}
	palace, err := service.NewPalace(cfg, st, events.New())
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	return &Opened{Palace: palace, Store: st, Remote: remote}, nil
}
