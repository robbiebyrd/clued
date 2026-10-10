// Package enrich runs session enrichers over stored documents.
package enrich

import (
	"context"
	"log/slog"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

const defaultBatchLimit = 100

// Loop runs a Pass immediately and then once per interval until ctx is done.
// Pass errors are logged. A nil logger means slog.Default().
func Loop(ctx context.Context, store session.Store, enrichers []session.Enricher, interval time.Duration, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := Pass(ctx, store, enrichers, logger); err != nil {
			logger.Error("enrichment pass failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Pass runs every enricher once, in order, over its unenriched documents.
// Enrich errors are recorded on the document with SetEnrichmentFailure; query
// failures are logged and the pass moves on to the next enricher. Pass returns
// the first store write error, after finishing the pass.
func Pass(ctx context.Context, store session.Store, enrichers []session.Enricher, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	var first error
	for _, e := range enrichers {
		if err := runEnricher(ctx, store, e, logger); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func runEnricher(ctx context.Context, store session.Store, e session.Enricher, logger *slog.Logger) error {
	limit := e.BatchLimit
	if limit <= 0 {
		limit = defaultBatchLimit
	}
	docs, err := store.Unenriched(ctx, e.Collection, e.Name, limit)
	if err != nil {
		logger.Error("enricher query failed", "enricher", e.Name, "collection", e.Collection, "error", err)
		return nil
	}
	var first error
	for _, doc := range docs {
		if !e.Matches(doc) {
			continue
		}
		if err := enrichDoc(ctx, store, e, doc, logger); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func enrichDoc(ctx context.Context, store session.Store, e session.Enricher, doc session.Doc, logger *slog.Logger) error {
	id := doc["_id"]
	result, err := e.Enrich(ctx, doc, store)
	if err != nil {
		logger.Error("enricher failed", "enricher", e.Name, "id", id, "error", err)
		return store.SetEnrichmentFailure(ctx, e.Collection, id, e.Name, err.Error(), time.Now())
	}
	return store.SetEnrichment(ctx, e.Collection, id, e.Name, result)
}
