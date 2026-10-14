package export

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/query"
	"github.com/robbiebyrd/clued/mind-palace/session/redact"
	"github.com/robbiebyrd/clued/mind-palace/session/render"
)

// The formats Run writes.
const (
	Markdown = "md"
	HTML     = "html"
	JSONL    = "jsonl"
	Zip      = "zip"
)

// ValidFormat reports whether f is a format Run writes.
func ValidFormat(f string) bool { return f == Markdown || f == HTML || f == JSONL || f == Zip }

// Options select what Run exports and where. Sessions are SessionIDs, else
// those whose project path matches ProjectPathRe and that were last seen
// at or after Since (at most 500, newest first). md, html and jsonl take
// one session; zip any number. Out is the file to write (default
// <session id>.<format>, or clued-sessions-<day>.zip, in the working
// directory); an existing file is refused unless Force. Raw skips the
// redaction an export otherwise gets (the built-in rules plus
// RedactPatterns).
type Options struct {
	SessionIDs     []string
	ProjectPathRe  string
	Since          time.Time
	Format         string
	Out            string
	Force          bool
	Raw            bool
	RedactPatterns []string
	Prices         map[string]session.ModelPrice
}

// Result names the file written and the sessions in it.
type Result struct {
	Path     string   `json:"path"`
	Format   string   `json:"format"`
	Sessions []string `json:"sessions"`
	Redacted bool     `json:"redacted"`
}

// ErrNoSessions is returned when the options select nothing.
var ErrNoSessions = errors.New("no session matches")

// Run exports the account's sessions as the options say.
func Run(ctx context.Context, store session.Store, accountID string, o Options) (Result, error) {
	if !ValidFormat(o.Format) {
		return Result{}, fmt.Errorf("format %q is not md, html, jsonl or zip", o.Format)
	}
	ids, err := selectSessions(ctx, store, accountID, o)
	if err != nil {
		return Result{}, err
	}
	if len(ids) == 0 {
		return Result{}, ErrNoSessions
	}
	if o.Format != Zip && len(ids) > 1 {
		return Result{}, fmt.Errorf("%d sessions match; %s takes one, zip takes many", len(ids), o.Format)
	}
	var r *redact.Redactor
	if !o.Raw {
		r, _ = redact.New(o.RedactPatterns)
	}
	svc := &query.Service{Store: store, AccountID: accountID, Prices: o.Prices}
	var bundles []Bundle
	models := map[string]render.Model{}
	for _, id := range ids {
		b, err := Load(ctx, store, accountID, id)
		if err != nil {
			return Result{}, err
		}
		if r != nil {
			b = b.Redact(r)
		}
		bundles = append(bundles, b)
		models[id] = b.Model(usageOf(ctx, svc, id))
	}
	var body bytes.Buffer
	switch o.Format {
	case Markdown:
		body.Write(render.Markdown(models[ids[0]]))
	case HTML:
		body.Write(render.HTML(models[ids[0]]))
	case JSONL:
		err = bundles[0].WriteJSONL(&body)
	case Zip:
		err = WriteZip(&body, bundles, models)
	}
	if err != nil {
		return Result{}, err
	}
	out := o.Out
	if out == "" {
		out = ids[0] + "." + o.Format
		if o.Format == Zip {
			out = "clued-sessions-" + time.Now().UTC().Format(time.DateOnly) + ".zip"
		}
	}
	if err := writeFile(out, body.Bytes(), o.Force); err != nil {
		return Result{}, err
	}
	abs, _ := filepath.Abs(out)
	return Result{Path: abs, Format: o.Format, Sessions: ids, Redacted: !o.Raw}, nil
}

func selectSessions(ctx context.Context, store session.Store, accountID string, o Options) ([]string, error) {
	if len(o.SessionIDs) > 0 {
		for _, id := range o.SessionIDs {
			if _, err := store.GetSession(ctx, accountID, id); err != nil {
				return nil, fmt.Errorf("session %s: %w", id, err)
			}
		}
		return o.SessionIDs, nil
	}
	found, err := store.FindSessions(ctx, session.SessionQuery{AccountID: accountID, ProjectPathRe: o.ProjectPathRe, Limit: 500})
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, d := range found {
		if seen, _ := d["last_seen"].(time.Time); !o.Since.IsZero() && seen.Before(o.Since) {
			continue
		}
		if id, _ := d.String("session_id"); id != "" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// usageOf is the session's usage for the header, nil when there is none
// or it cannot be read (the export does not depend on it).
func usageOf(ctx context.Context, svc *query.Service, id string) *render.Usage {
	report, err := svc.Usage(ctx, query.UsageArgs{SessionID: id})
	if err != nil || report.Total.Messages == 0 {
		return nil
	}
	t := report.Total
	return &render.Usage{Messages: t.Messages, InputTokens: t.InputTokens, OutputTokens: t.OutputTokens,
		CacheTokens: t.CacheCreationInputTokens + t.CacheReadInputTokens, CostUSD: t.CostUSD}
}

// writeFile writes data to path with mode 0600 (an export holds a whole
// conversation), refusing to replace a file unless force.
func writeFile(path string, data []byte, force bool) error {
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if force {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0o600)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%s exists; choose another --out or pass --force", path)
	}
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
