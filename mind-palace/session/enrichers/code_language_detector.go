package enrichers

import (
	"context"
	"regexp"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// CodeLanguages is the stored result of the code-language-detector enricher.
type CodeLanguages struct {
	Languages []string `json:"languages" bson:"languages"`
}

// jsWhitespace is the character set of JavaScript's \s, which RE2's \s lacks
// (vertical tab and the Unicode spaces).
const jsWhitespace = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

// codeFenceRe is JavaScript's /^```(\w+)\s*$/gm. Line terminators are first
// folded to \n (see jsLineTerminators) so RE2's multi-line ^ and $ agree.
var codeFenceRe = regexp.MustCompile("(?m)^```(\\w+)[" + jsWhitespace + "]*$")

// jsLineTerminators folds the line terminators JavaScript's multi-line ^ and $
// honour, besides \n, into \n.
var jsLineTerminators = strings.NewReplacer("\r", "\n", " ", "\n", " ", "\n")

// CodeLanguageDetector lists the languages of the fenced code blocks in an
// assistant message, lowercased and in order of first appearance.
var CodeLanguageDetector = session.Enricher{
	Name:       "code-language-detector",
	Collection: "transcript_lines",
	Enabled:    true,
	Matches:    func(session.Doc) bool { return true },
	Enrich: func(_ context.Context, doc session.Doc, _ session.Lookup) (any, error) {
		languages := []string{}
		if role, _ := doc.Message().String("role"); role != "assistant" {
			return CodeLanguages{Languages: languages}, nil
		}
		seen := map[string]bool{}
		for _, block := range doc.Content() {
			if blockType, _ := block.String("type"); blockType != "text" {
				continue
			}
			text, ok := block.String("text")
			if !ok {
				continue
			}
			for _, m := range codeFenceRe.FindAllStringSubmatch(jsLineTerminators.Replace(text), -1) {
				lang := strings.ToLower(m[1])
				if !seen[lang] {
					seen[lang] = true
					languages = append(languages, lang)
				}
			}
		}
		return CodeLanguages{Languages: languages}, nil
	},
}

func init() { session.Register(CodeLanguageDetector) }
