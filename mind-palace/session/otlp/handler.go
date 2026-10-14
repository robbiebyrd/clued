package otlp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// The OTLP/HTTP paths an exporter appends to OTEL_EXPORTER_OTLP_ENDPOINT.
const (
	PathLogs    = "/v1/logs"
	PathMetrics = "/v1/metrics"
)

// DefaultMaxBody bounds a request body, decompressed.
const DefaultMaxBody = 8 << 20

// storeTimeout bounds the store calls of one export.
const storeTimeout = 30 * time.Second

// Ingest stores the events whose session the account holds and drops the
// others: telemetry never creates a session, so a session excluded at
// capture (excludePaths) or one this server never received stays absent,
// and a made-up session id stores nothing. It reports how many events were
// new and how many were dropped for an unknown session.
func Ingest(ctx context.Context, store session.Store, accountID string, events []session.OtelEvent) (inserted, unknown int, err error) {
	known := map[string]bool{}
	kept := make([]session.OtelEvent, 0, len(events))
	for _, e := range events {
		has, seen := known[e.SessionID]
		if !seen {
			_, err := store.GetSession(ctx, accountID, e.SessionID)
			switch {
			case err == nil:
				has = true
			case errors.Is(err, session.ErrNotFound):
			default:
				return 0, 0, err
			}
			known[e.SessionID] = has
		}
		if !has {
			unknown++
			continue
		}
		kept = append(kept, e)
	}
	if len(kept) == 0 {
		return 0, unknown, nil
	}
	inserted, err = store.InsertOtelEvents(ctx, kept)
	return inserted, unknown, err
}

// Options configure Handler.
type Options struct {
	// Store receives the events. Required.
	Store session.Store
	// Account resolves the request's account; false answers 401. Nil
	// stores every event without an account (a single-user daemon).
	Account func(r *http.Request) (string, bool)
	// MaxBody bounds the decompressed body (DefaultMaxBody when zero).
	MaxBody int64
	// Logger receives one debug line per export (slog.Default when nil).
	Logger *slog.Logger
	// Now stamps records that carry no time (time.Now when nil).
	Now func() time.Time
}

// Handler serves POST PathLogs and POST PathMetrics as OTLP/HTTP: JSON or
// protobuf bodies, gzip or not, answered in the request's encoding with an
// empty Export…ServiceResponse. A body that does not decode is 400, an
// unknown content type 415, a body above MaxBody 413 and a store failure
// 503 (an exporter retries it), each with a google.rpc.Status.
func Handler(opts Options) http.Handler {
	if opts.Store == nil {
		panic("otlp: Options.Store is required")
	}
	if opts.MaxBody <= 0 {
		opts.MaxBody = DefaultMaxBody
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != PathLogs && r.URL.Path != PathMetrics {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		format, err := FormatOf(r.Header.Get("Content-Type"))
		if err != nil {
			writeStatus(w, FormatJSON, http.StatusUnsupportedMediaType, err.Error())
			return
		}
		account := ""
		if opts.Account != nil {
			var ok bool
			if account, ok = opts.Account(r); !ok {
				w.Header().Set("WWW-Authenticate", "Bearer")
				writeStatus(w, format, http.StatusUnauthorized, "authentication required")
				return
			}
		}
		raw, err := Body(r.Body, r.Header.Get("Content-Encoding"), opts.MaxBody)
		switch {
		case errors.Is(err, ErrTooLarge):
			writeStatus(w, format, http.StatusRequestEntityTooLarge, fmt.Sprintf("body exceeds %d bytes", opts.MaxBody))
			return
		case err != nil:
			writeStatus(w, format, http.StatusBadRequest, err.Error())
			return
		}
		if r.URL.Path == PathMetrics {
			points, err := DecodeMetrics(format, raw)
			if err != nil {
				writeStatus(w, format, http.StatusBadRequest, err.Error())
				return
			}
			opts.Logger.Debug("clued: otlp metrics accepted, not stored", "points", points)
			writeMessage(w, format, http.StatusOK, &colmetrics.ExportMetricsServiceResponse{})
			return
		}
		req, err := DecodeLogs(format, raw)
		if err != nil {
			writeStatus(w, format, http.StatusBadRequest, err.Error())
			return
		}
		events, skipped := Events(req, account, opts.Now())
		// The write outlives the request, as every capture write does, but
		// not a store that hangs.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), storeTimeout)
		defer cancel()
		inserted, unknown, err := Ingest(ctx, opts.Store, account, events)
		if err != nil {
			opts.Logger.Warn("clued: otlp logs not stored", "error", err)
			writeStatus(w, format, http.StatusServiceUnavailable, "store: "+err.Error())
			return
		}
		opts.Logger.Debug("clued: otlp logs", "records", Records(req), "kept", len(events), "stored", inserted, "other", skipped, "unknown_session", unknown)
		writeMessage(w, format, http.StatusOK, &collogs.ExportLogsServiceResponse{})
	})
}

func writeStatus(w http.ResponseWriter, f Format, code int, message string) {
	writeMessage(w, f, code, &status.Status{Code: int32(grpcCode(code)), Message: message})
}

// grpcCode is the google.rpc.Code an HTTP status stands for.
func grpcCode(httpStatus int) int {
	switch httpStatus {
	case http.StatusBadRequest, http.StatusUnsupportedMediaType:
		return 3 // INVALID_ARGUMENT
	case http.StatusUnauthorized:
		return 16 // UNAUTHENTICATED
	case http.StatusRequestEntityTooLarge:
		return 8 // RESOURCE_EXHAUSTED
	case http.StatusServiceUnavailable:
		return 14 // UNAVAILABLE
	}
	return 2 // UNKNOWN
}

func writeMessage(w http.ResponseWriter, f Format, code int, m proto.Message) {
	var (
		body []byte
		err  error
	)
	if f == FormatProtobuf {
		w.Header().Set("Content-Type", ContentProtobuf)
		body, err = proto.Marshal(m)
	} else {
		w.Header().Set("Content-Type", ContentJSON)
		body, err = protojson.Marshal(m)
	}
	if err != nil {
		code, body = http.StatusInternalServerError, nil
	}
	w.WriteHeader(code)
	_, _ = w.Write(body)
}
