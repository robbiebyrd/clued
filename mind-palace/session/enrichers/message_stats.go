package enrichers

import (
	"context"
	"strings"
	"unicode/utf16"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// MessageStats counts the characters, words and content blocks of a line.
var MessageStats = session.Enricher{
	Name:       "message-stats",
	Collection: "transcript_lines",
	Enabled:    true,
	Matches:    func(session.Doc) bool { return true },
	Enrich:     enrichMessageStats,
}

func init() { session.Register(MessageStats) }

type messageStatsResult struct {
	CharCount     int `json:"char_count" bson:"char_count"`
	WordCount     int `json:"word_count" bson:"word_count"`
	BlockCount    int `json:"block_count" bson:"block_count"`
	TokenEstimate int `json:"token_estimate" bson:"token_estimate"`
}

func enrichMessageStats(_ context.Context, doc session.Doc, _ session.Lookup) (any, error) {
	line := doc.Line()
	if line == nil {
		return messageStatsResult{}, nil
	}

	var text strings.Builder
	blockCount := 0
	if display, ok := line.String("display"); ok && line["sessionId"] != nil {
		text.WriteString(display)
	} else {
		blockCount = contentBlockCount(doc)
		for _, block := range doc.Content() {
			switch block["type"] {
			case "text":
				if s, ok := block.String("text"); ok {
					text.WriteString(s)
				}
			case "thinking":
				if s, ok := block.String("thinking"); ok {
					text.WriteString(s)
				}
			}
		}
	}

	chars := utf16Length(text.String())
	return messageStatsResult{
		CharCount:     chars,
		WordCount:     len(strings.FieldsFunc(text.String(), isJSWhitespace)),
		BlockCount:    blockCount,
		TokenEstimate: (chars + 3) / 4,
	}, nil
}

// contentBlockCount is the length of message.content, counting entries that
// are not objects too.
func contentBlockCount(doc session.Doc) int {
	switch list := doc.Message()["content"].(type) {
	case []any:
		return len(list)
	case []session.Doc:
		return len(list)
	}
	return 0
}

// utf16Length is the length JavaScript reports for s (UTF-16 code units), so
// counts match the numbers the Node enrichers stored.
func utf16Length(s string) int {
	n := 0
	for _, r := range s {
		n += len(utf16.Encode([]rune{r}))
	}
	return n
}

// isJSWhitespace reports whether r matches JavaScript's \s.
func isJSWhitespace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}
