package enrichers

import (
	"context"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// SystemRecordName is the name of the system-record enricher.
const SystemRecordName = "system-record"

// SystemRecord is what the system-record enricher stores for a transcript
// line of type "system": its subtype and the payload that subtype carries.
type SystemRecord struct {
	Subtype string `json:"subtype" bson:"subtype"`
	// Text is the record's content, at most 300 characters.
	Text string `json:"text,omitempty" bson:"text,omitempty"`
	// A turn_duration record.
	DurationMs int64 `json:"duration_ms,omitempty" bson:"duration_ms,omitempty"`
	// An api_error record.
	Status  int    `json:"status,omitempty" bson:"status,omitempty"`
	Message string `json:"message,omitempty" bson:"message,omitempty"`
	Retry   int    `json:"retry,omitempty" bson:"retry,omitempty"`
	// A compact_boundary record.
	Trigger    string `json:"trigger,omitempty" bson:"trigger,omitempty"`
	PreTokens  int64  `json:"pre_tokens,omitempty" bson:"pre_tokens,omitempty"`
	PostTokens int64  `json:"post_tokens,omitempty" bson:"post_tokens,omitempty"`
	// A stop_hook_summary record.
	HookCount int  `json:"hook_count,omitempty" bson:"hook_count,omitempty"`
	HookError bool `json:"hook_error,omitempty" bson:"hook_error,omitempty"`
}

// SystemRecordEnricher reads the system records Claude Code writes between
// the messages: turn timings, API errors, compaction boundaries, hook
// summaries and away summaries.
var SystemRecordEnricher = session.Enricher{
	Name:       SystemRecordName,
	Collection: "transcript_lines",
	Enabled:    true,
	Matches: func(doc session.Doc) bool {
		t, _ := doc.Line().String("type")
		return t == "system"
	},
	Enrich: func(_ context.Context, doc session.Doc, _ session.Lookup) (any, error) {
		return readSystemRecord(doc.Line()), nil
	},
}

func init() { session.Register(SystemRecordEnricher) }

// readSystemRecord reads one system line.
func readSystemRecord(line session.Doc) SystemRecord {
	r := SystemRecord{}
	r.Subtype, _ = line.String("subtype")
	if content, ok := line.String("content"); ok {
		r.Text = activityDetail(content)
	}
	switch r.Subtype {
	case "turn_duration":
		r.DurationMs = count(firstOf(line, "durationMs", "duration_ms", "duration"))
	case "api_error":
		errDoc := line.Map("error")
		r.Status, _ = seqOf(firstOf(line, "status", "statusCode", "status_code"))
		if r.Status == 0 {
			r.Status, _ = seqOf(firstOf(errDoc, "status", "statusCode", "status_code"))
		}
		if msg, ok := line.String("message"); ok {
			r.Message = activityDetail(msg)
		} else if msg, ok := errDoc.String("message"); ok {
			r.Message = activityDetail(msg)
		} else if errText, ok := line.String("error"); ok {
			r.Message = activityDetail(errText)
		} else {
			r.Message = r.Text
		}
		r.Retry, _ = seqOf(firstOf(line, "retryAttempt", "retry", "attempt", "retries"))
	case "compact_boundary":
		meta := line.Map("compactMetadata")
		if meta == nil {
			meta = line
		}
		r.Trigger, _ = meta.String("trigger")
		r.PreTokens = count(firstOf(meta, "preTokens", "pre_tokens"))
		r.PostTokens = count(firstOf(meta, "postTokens", "post_tokens"))
	case "stop_hook_summary":
		r.HookCount, _ = seqOf(line["hookCount"])
		if errs, ok := line["hookErrors"].([]any); ok && len(errs) > 0 {
			r.HookError = true
		}
	}
	return r
}

// firstOf is the first of the keys present in d.
func firstOf(d session.Doc, keys ...string) any {
	for _, k := range keys {
		if v, ok := d[k]; ok && v != nil {
			return v
		}
	}
	return nil
}
