package enrichers

import (
	"context"
	"regexp"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// IntentResult is the stored result of the intent-classifier enricher. Intent
// is question, instruction, feedback, correction, approval, slash_command, or
// nil when the line has no prompt text.
type IntentResult struct {
	Intent *string `json:"intent" bson:"intent"`
}

var (
	questionOpenerRe = regexp.MustCompile(`(?i)^\b(what|how|why|when|where|is|are|can|does|should|could|would)\b`)
	instructionRe    = regexp.MustCompile(`(?i)\b(fix|add|remove|change|update|make|create|write|implement|refactor|delete|rename)\b`)
	approvalRe       = regexp.MustCompile(`(?i)\b(looks good|lgtm|go for it|proceed|approved|sounds good)\b`)
	correctionRe     = regexp.MustCompile(`(?i)^(no|wrong|incorrect|that's not|don't do|shouldn't)\b`)
)

func intentOf(intent string) IntentResult { return IntentResult{Intent: &intent} }

// IntentClassifier classifies the prompt text of a user prompt line.
var IntentClassifier = session.Enricher{
	Name:       "intent-classifier",
	Collection: "transcript_lines",
	Enabled:    true,
	Matches:    func(session.Doc) bool { return true },
	Enrich: func(_ context.Context, doc session.Doc, _ session.Lookup) (any, error) {
		display, ok := doc.Line().String("display")
		if !ok {
			return IntentResult{}, nil
		}
		d := strings.TrimSpace(display)
		switch lower := strings.ToLower(d); {
		case d == "":
			return IntentResult{}, nil
		case strings.HasPrefix(d, "/"):
			return intentOf("slash_command"), nil
		case strings.HasSuffix(d, "?") || questionOpenerRe.MatchString(d):
			return intentOf("question"), nil
		case instructionRe.MatchString(d):
			return intentOf("instruction"), nil
		case approvalRe.MatchString(lower) || lower == "yes" || lower == "ok" || lower == "sure":
			return intentOf("approval"), nil
		case correctionRe.MatchString(d):
			return intentOf("correction"), nil
		}
		return intentOf("feedback"), nil
	},
}

func init() { session.Register(IntentClassifier) }
