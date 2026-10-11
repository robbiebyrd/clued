// Package enrich runs session enrichers over stored documents.
package enrich

import (
	"context"
	"fmt"
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
// Enrich errors are recorded on the document with SetEnrichmentFailure,
// non-matching documents are marked skipped, a failed result write is left for
// the next pass to retry; query failures are logged and the pass moves on to the next enricher. Pass returns
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
		if err := enrichDoc(ctx, store, e, doc, logger); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// enrichDoc handles one queued doc. A non-matching doc is marked skipped so it
// leaves the queue; a failing mark is logged and retried on a later pass. An
// Enrich error or panic (or a Matches panic) is recorded as the doc's failure,
// and only a failure to record that is returned. A failed result write is
// logged and the doc left untouched so the next pass retries it.
func enrichDoc(ctx context.Context, store session.Store, e session.Enricher, doc session.Doc, logger *slog.Logger) error {
	id := doc["_id"]
	matched, err := safely(func() (bool, error) { return e.Matches(doc), nil })
	if err == nil && !matched {
		if err := store.SetEnrichmentSkipped(ctx, e.Collection, id, e.Name); err != nil {
			logger.Error("enricher skip mark failed", "enricher", e.Name, "id", id, "error", err)
		}
		return nil
	}
	if err == nil {
		var result any
		result, err = safely(func() (any, error) { return e.Enrich(ctx, doc, store) })
		if err == nil {
			if err := store.SetEnrichment(ctx, e.Collection, id, e.Name, result); err != nil {
				logger.Warn("enrichment write failed; will retry", "enricher", e.Name, "id", id, "error", err)
			}
			return nil
		}
	}
	logger.Error("enricher failed", "enricher", e.Name, "id", id, "error", err)
	return store.SetEnrichmentFailure(ctx, e.Collection, id, e.Name, err.Error(), time.Now())
}

// safely runs f, turning a panic into an error so one faulty enricher cannot
// take down the process.
func safely[T any](f func() (T, error)) (result T, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return f()
}
