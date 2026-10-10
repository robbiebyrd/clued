package enrichers

import (
	"context"
	"regexp"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

type promptFeaturesResult struct {
	WordCount          int     `json:"word_count" bson:"word_count"`
	IsSlashCommand     bool    `json:"is_slash_command" bson:"is_slash_command"`
	CommandName        *string `json:"command_name" bson:"command_name"`
	Intent             *string `json:"intent" bson:"intent"`
	LooksLikeTaskStart bool    `json:"looks_like_task_start" bson:"looks_like_task_start"`
}

// PromptFeatures extracts word count, slash-command name and intent from a user prompt.
var PromptFeatures = session.Enricher{
	Name:       "prompt-features",
	Collection: "hook_events",
	Enabled:    true,
	Matches: func(doc session.Doc) bool {
		event, _ := doc.String("hook_event_name")
		return event == "UserPromptSubmit"
	},
	Enrich: func(_ context.Context, doc session.Doc, _ session.Lookup) (any, error) {
		raw, _ := doc.String("prompt")
		prompt := strings.TrimFunc(raw, hookEventIsSpace)
		words := strings.FieldsFunc(prompt, hookEventIsSpace)
		result := promptFeaturesResult{WordCount: len(words), IsSlashCommand: strings.HasPrefix(prompt, "/")}
		if result.IsSlashCommand {
			result.CommandName = nonEmpty(words[0][1:])
			return result, nil
		}
		result.Intent = classifyIntent(prompt)
		result.LooksLikeTaskStart = result.Intent != nil && *result.Intent == "instruction" &&
			result.WordCount >= 6 && !continuationPrefix.MatchString(prompt)
		return result, nil
	},
}

func init() { session.Register(PromptFeatures) }

// asciiFolded builds a regexp group matching any of the words with ASCII-only
// case folding. Go's (?i) also folds U+212A to "k" and U+017F to "s", which a
// JavaScript /i regexp does not.
func asciiFolded(words ...string) string {
	alternatives := make([]string, len(words))
	for i, word := range words {
		var b strings.Builder
		for _, r := range word {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				b.WriteString("[" + strings.ToLower(string(r)) + strings.ToUpper(string(r)) + "]")
			} else {
				b.WriteString(regexp.QuoteMeta(string(r)))
			}
		}
		alternatives[i] = b.String()
	}
	return "(?:" + strings.Join(alternatives, "|") + ")"
}

var (
	questionOpener = regexp.MustCompile(`^\b` + asciiFolded("what", "how", "why", "when", "where", "is", "are", "can", "does", "should", "could", "would") + `\b`)
	instruction    = regexp.MustCompile(`\b` + asciiFolded("fix", "add", "remove", "change", "update", "make", "create", "write", "implement", "refactor", "delete", "rename") + `\b`)
	correction     = regexp.MustCompile(`^` + asciiFolded("no", "wrong", "incorrect", "that's not", "don't do", "shouldn't") + `\b`)
	// Applied to the lower-cased prompt, as the JavaScript does.
	approval = regexp.MustCompile(`\b(?:looks good|lgtm|go for it|proceed|approved|sounds good)\b`)

	continuationPrefix = regexp.MustCompile(`^` + asciiFolded("also", "and also", "now also", "one more") + `\b`)
)

func classifyIntent(prompt string) *string {
	switch lower := hookEventLower(prompt); {
	case strings.HasSuffix(prompt, "?") || questionOpener.MatchString(prompt):
		return nonEmpty("question")
	case instruction.MatchString(prompt):
		return nonEmpty("instruction")
	case approval.MatchString(lower) || lower == "yes" || lower == "ok" || lower == "sure":
		return nonEmpty("approval")
	case correction.MatchString(prompt):
		return nonEmpty("correction")
	}
	return nil
}
