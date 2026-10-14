package enrichers

import (
	"context"
	"fmt"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// Usage records the token usage of an assistant line (its message.usage) with
// the model, the message id and the day, for the usage totals. Claude Code
// repeats a message's usage on every line of the message, so the totals count
// each message id once. A line without a message id stands for itself
// (message id "seq:<seq>"). Synthetic lines Claude Code writes itself (model
// "<synthetic>") are skipped.
var Usage = session.Enricher{
	Name:       "usage",
	Collection: "transcript_lines",
	Enabled:    true,
	Matches: func(doc session.Doc) bool {
		model, _ := doc.Message().String("model")
		return doc.Line()["type"] == "assistant" && doc.Message().Map("usage") != nil && model != "<synthetic>"
	},
	Enrich: enrichUsage,
}

func init() { session.Register(Usage) }

// UsageResult is what the usage enricher stores.
type UsageResult struct {
	Model                    string `json:"model" bson:"model"`
	MessageID                string `json:"message_id" bson:"message_id"`
	Day                      string `json:"day" bson:"day"`
	InputTokens              int64  `json:"input_tokens" bson:"input_tokens"`
	CacheCreationInputTokens int64  `json:"cache_creation_input_tokens" bson:"cache_creation_input_tokens"`
	CacheCreation1hTokens    int64  `json:"cache_creation_1h_tokens" bson:"cache_creation_1h_tokens"`
	CacheReadInputTokens     int64  `json:"cache_read_input_tokens" bson:"cache_read_input_tokens"`
	OutputTokens             int64  `json:"output_tokens" bson:"output_tokens"`
	ContextTokens            int64  `json:"context_tokens" bson:"context_tokens"`
}

func enrichUsage(_ context.Context, doc session.Doc, _ session.Lookup) (any, error) {
	msg := doc.Message()
	usage := msg.Map("usage")
	r := UsageResult{
		InputTokens:              count(usage["input_tokens"]),
		CacheCreationInputTokens: count(usage["cache_creation_input_tokens"]),
		CacheCreation1hTokens:    count(usage.Map("cache_creation")["ephemeral_1h_input_tokens"]),
		CacheReadInputTokens:     count(usage["cache_read_input_tokens"]),
		OutputTokens:             count(usage["output_tokens"]),
	}
	r.ContextTokens = r.InputTokens + r.CacheCreationInputTokens + r.CacheReadInputTokens
	r.Model, _ = msg.String("model")
	if r.MessageID, _ = msg.String("id"); r.MessageID == "" {
		r.MessageID = fmt.Sprintf("seq:%v", doc["seq"])
	}
	r.Day = usageDay(doc)
	return r, nil
}

// usageDay is the UTC day of the line's timestamp, else of when it was
// stored.
func usageDay(doc session.Doc) string {
	if s, ok := doc.Line().String("timestamp"); ok {
		if at, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return at.UTC().Format(time.DateOnly)
		}
	}
	if at, ok := doc["created_at"].(time.Time); ok {
		return at.UTC().Format(time.DateOnly)
	}
	return ""
}

// count reads a token counter of any numeric type the stores decode; anything
// else, and a negative number, is 0.
func count(v any) int64 {
	var n int64
	switch x := v.(type) {
	case int:
		n = int64(x)
	case int32:
		n = int64(x)
	case int64:
		n = x
	case float64:
		n = int64(x)
	}
	return max(n, 0)
}
