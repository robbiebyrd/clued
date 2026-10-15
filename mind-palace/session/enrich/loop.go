// Package enrich runs session enrichers over stored documents.
package enrich

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

const defaultBatchLimit = 100

// Policy decides per document whether an enricher runs for the account the
// document is stamped with ("" for a document without one). A document the
// policy refuses is marked skipped for that enricher, so it leaves the
// queue: switching the enricher on for the account later applies to new
// documents only, unless the caller also puts the account's skipped
// documents back with session.Store.RequeueSkipped. A nil Policy runs
// every enricher for everyone.
type Policy func(ctx context.Context, accountID string, e session.Enricher) bool

// Loop runs a Pass immediately and then once per interval until ctx is done.
// Pass errors are logged. A nil logger means slog.Default().
func Loop(ctx context.Context, store session.Store, enrichers []session.Enricher, interval time.Duration, logger *slog.Logger) {
	LoopWithPolicy(ctx, store, enrichers, nil, interval, logger)
}

// LoopWithPolicy is Loop with a per-account Policy.
func LoopWithPolicy(ctx context.Context, store session.Store, enrichers []session.Enricher, policy Policy, interval time.Duration, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := PassWithPolicy(ctx, store, enrichers, policy, logger); err != nil {
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
	return PassWithPolicy(ctx, store, enrichers, nil, logger)
}

// PassWithPolicy is Pass with a per-account Policy.
func PassWithPolicy(ctx context.Context, store session.Store, enrichers []session.Enricher, policy Policy, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	var first error
	for _, e := range enrichers {
		if err := runEnricher(ctx, store, e, policy, logger); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func runEnricher(ctx context.Context, store session.Store, e session.Enricher, policy Policy, logger *slog.Logger) error {
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
	var batch []session.Doc
	for _, doc := range docs {
		if policy != nil {
			accountID, _ := doc.String("account_id")
			if !policy(ctx, accountID, e) {
				markSkipped(ctx, store, e, doc["_id"], logger)
				continue
			}
		}
		if e.EnrichBatch != nil {
			matched, err := safely(func() (bool, error) { return e.Matches(doc), nil })
			switch {
			case err != nil:
				if err := recordFailure(ctx, store, e, doc["_id"], err, logger); err != nil && first == nil {
					first = err
				}
			case !matched:
				markSkipped(ctx, store, e, doc["_id"], logger)
			default:
				batch = append(batch, doc)
			}
			continue
		}
		if err := enrichDoc(ctx, store, e, doc, logger); err != nil && first == nil {
			first = err
		}
	}
	if len(batch) > 0 {
		if err := enrichBatch(ctx, store, e, batch, logger); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// enrichBatch calls EnrichBatch once over the matching docs and stores each
// result by position. A panic, or a result list of the wrong length, is
// recorded as every doc's failure.
func enrichBatch(ctx context.Context, store session.Store, e session.Enricher, docs []session.Doc, logger *slog.Logger) error {
	type batchResult struct {
		results []any
		errs    []error
	}
	r, err := safely(func() (batchResult, error) {
		results, errs := e.EnrichBatch(ctx, docs, store)
		return batchResult{results, errs}, nil
	})
	if err == nil && (len(r.results) != len(docs) || (r.errs != nil && len(r.errs) != len(docs))) {
		err = fmt.Errorf("enricher returned %d results and %d errors for %d documents", len(r.results), len(r.errs), len(docs))
	}
	var first error
	for i, doc := range docs {
		docErr := err
		if docErr == nil && r.errs != nil {
			docErr = r.errs[i]
		}
		if docErr == nil {
			if werr := store.SetEnrichment(ctx, e.Collection, doc["_id"], e.Name, r.results[i]); werr != nil {
				logger.Warn("enrichment write failed; will retry", "enricher", e.Name, "id", doc["_id"], "error", werr)
			}
			continue
		}
		if ferr := settle(ctx, store, e, doc["_id"], docErr, logger); ferr != nil && first == nil {
			first = ferr
		}
	}
	return first
}

// enrichDoc handles one queued doc. A non-matching doc is marked skipped so it
// leaves the queue; a failing mark is logged and retried on a later pass. An
// Enrich error or panic (or a Matches panic) is recorded as the doc's failure,
// and only a failure to record that is returned; an ErrSkip marks the doc
// skipped. A failed result write is logged and the doc left untouched so
// the next pass retries it.
func enrichDoc(ctx context.Context, store session.Store, e session.Enricher, doc session.Doc, logger *slog.Logger) error {
	id := doc["_id"]
	matched, err := safely(func() (bool, error) { return e.Matches(doc), nil })
	if err == nil && !matched {
		markSkipped(ctx, store, e, id, logger)
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
	return settle(ctx, store, e, id, err, logger)
}

// settle records an enrichment error: ErrSkip marks the doc skipped, any
// other error is its failure.
func settle(ctx context.Context, store session.Store, e session.Enricher, id any, err error, logger *slog.Logger) error {
	if errors.Is(err, session.ErrSkip) {
		markSkipped(ctx, store, e, id, logger)
		return nil
	}
	return recordFailure(ctx, store, e, id, err, logger)
}

func recordFailure(ctx context.Context, store session.Store, e session.Enricher, id any, err error, logger *slog.Logger) error {
	logger.Error("enricher failed", "enricher", e.Name, "id", id, "error", err)
	return store.SetEnrichmentFailure(ctx, e.Collection, id, e.Name, err.Error(), time.Now())
}

// markSkipped marks the doc skipped; a failing mark is logged and the doc
// comes back on a later pass.
func markSkipped(ctx context.Context, store session.Store, e session.Enricher, id any, logger *slog.Logger) {
	if err := store.SetEnrichmentSkipped(ctx, e.Collection, id, e.Name); err != nil {
		logger.Error("enricher skip mark failed", "enricher", e.Name, "id", id, "error", err)
	}
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
