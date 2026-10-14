package enrichers

import (
	"context"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// searchTextLimit caps the text indexed for one transcript line.
const searchTextLimit = 16 << 10

// SearchText stores the readable text of each transcript line (its message
// text, tool calls and tool results, without thinking) for the transcript
// search: the Mongo store keeps a text index on it. Credentials the
// privacy-redact patterns recognise (API keys, AWS keys, JWTs) are masked
// whether or not that enricher runs, so the index is never the easiest
// place to find a secret; e-mail addresses stay searchable.
var SearchText = session.Enricher{
	Name:       "search-text",
	Collection: "transcript_lines",
	Enabled:    true,
	Matches:    func(session.Doc) bool { return true },
	Enrich: func(_ context.Context, doc session.Doc, _ session.Lookup) (any, error) {
		text := redactSecrets(session.LineText(doc))
		if len(text) > searchTextLimit {
			text = text[:searchTextLimit]
			for len(text) > 0 && text[len(text)-1]&0xC0 == 0x80 {
				text = text[:len(text)-1]
			}
			if len(text) > 0 && text[len(text)-1] >= 0xC0 {
				text = text[:len(text)-1]
			}
		}
		return text, nil
	},
}

func init() { session.Register(SearchText) }

// redactSecrets applies the credential patterns of redactionPatterns (not
// the e-mail one).
func redactSecrets(text string) string {
	for _, p := range redactionPatterns {
		if p.label == "email" {
			continue
		}
		text = p.re.ReplaceAllLiteralString(text, "[REDACTED:"+p.label+"]")
	}
	return text
}
