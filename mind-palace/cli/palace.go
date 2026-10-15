package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/robbiebyrd/clued/mind-palace/clientcfg"
	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/service"
)

// openPalace opens the palace every command uses through clientcfg.Open:
// the repository's stores from cfg and, when the plugin's config.json (or
// the CLUED_SERVER_* variables) puts the client in remote mode, the clued
// server appended after them for the project of cwd. Notices about the
// remote store go to stderr: a server that does not answer (writes are
// still made locally and reported, `mind-palace sync --from file --to
// clued-server` reconciles) and a project derived from the directory name.
func (a *App) openPalace(ctx context.Context, cwd string, cfg *config.Config) (*clientcfg.Opened, error) {
	opened, err := clientcfg.Open(ctx, cwd, cfg, a.sessionConfig(), clientcfg.Options{Probe: true})
	if err != nil {
		return nil, &service.Error{Kind: service.KindStorage, Message: err.Error()}
	}
	if r := opened.Remote; r != nil {
		if r.ProbeErr != nil {
			fmt.Fprintf(a.Stderr, "mind-palace: %s is not answering (%v); plans and stories are kept locally, run `mind-palace sync --from file --to %s` once it is back\n", clientcfg.RemoteStoreName, r.ProbeErr, clientcfg.RemoteStoreName)
		}
		if r.SharedErr != nil {
			fmt.Fprintf(a.Stderr, "mind-palace: could not ask %s whether project %q is shared (%v); it stays local-primary for this command\n", clientcfg.RemoteStoreName, r.Project.Slug, r.SharedErr)
		}
		if note := r.Project.Note(); note != "" {
			fmt.Fprintf(a.Stderr, "mind-palace: %s\n", note)
		}
	}
	return opened, nil
}

// workingDir is the directory the project is derived from: the working
// directory, which the hooks set to the hook's cwd before opening.
func workingDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

// reviewOptions carries what the status review should note about the
// remote store: a project derived from the directory name.
func (a *App) reviewOptions() []service.ReviewOption {
	if a.remote == nil {
		return nil
	}
	if note := a.remote.Project.Note(); note != "" {
		return []service.ReviewOption{service.WithReviewNote(note)}
	}
	return nil
}
