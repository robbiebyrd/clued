package enrichers

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// PrivacyRedact masks secrets and email addresses in the raw line. It is
// disabled by default.
var PrivacyRedact = session.Enricher{
	Name:       "privacy-redact",
	Collection: "transcript_lines",
	Enabled:    false,
	Matches:    func(doc session.Doc) bool { return doc["line"] != nil },
	Enrich:     enrichPrivacyRedact,
}

// PrivacyRedactMemory masks the same patterns in the text of the memory
// records ingested from claude-remember (MemorySaved hook events). It
// belongs to the privacy-redact group, so enabling "privacy-redact" switches
// both redactors on.
var PrivacyRedactMemory = session.Enricher{
	Name:       "privacy-redact-memory",
	Group:      PrivacyRedact.Name,
	Collection: "hook_events",
	Enabled:    false,
	Matches: func(doc session.Doc) bool {
		name, _ := doc.String("hook_event_name")
		_, isText := doc.Map("memory")["text"].(string)
		return name == session.MemoryEventName && isText
	},
	Enrich: enrichPrivacyRedactMemory,
}

func init() {
	session.Register(PrivacyRedact)
	session.Register(PrivacyRedactMemory)
}

type privacyRedactResult struct {
	RedactedLine string `json:"redacted_line" bson:"redacted_line"`
}

type privacyRedactMemoryResult struct {
	RedactedText string `json:"redacted_text" bson:"redacted_text"`
}

// The patterns are applied in order. RE2's \b is ASCII-only like JavaScript's
// non-Unicode \b. The email TLD spells out [A-Za-z] instead of using the i
// flag: RE2 folds U+212A (Kelvin sign) and U+017F to K and S under (?i),
// JavaScript's i flag does not.
var redactionPatterns = []struct {
	label string
	re    *regexp.Regexp
}{
	// Hyphens and underscores are part of real keys (sk-ant-api03-…, sk-proj-…).
	{"api-key", regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`)},
	{"email", regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`)},
	{"aws-key", regexp.MustCompile(`\b(AKIA[0-9A-Z]{16})\b`)},
	{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`)},
}

func enrichPrivacyRedact(_ context.Context, doc session.Doc, _ session.Lookup) (any, error) {
	raw, err := lineText(doc["line"])
	if err != nil {
		return nil, err
	}
	return privacyRedactResult{RedactedLine: redact(raw)}, nil
}

func enrichPrivacyRedactMemory(_ context.Context, doc session.Doc, _ session.Lookup) (any, error) {
	text, _ := doc.Map("memory")["text"].(string)
	return privacyRedactMemoryResult{RedactedText: redact(text)}, nil
}

// redact applies the redaction patterns in order.
func redact(text string) string {
	for _, p := range redactionPatterns {
		text = p.re.ReplaceAllLiteralString(text, "[REDACTED:"+p.label+"]")
	}
	return text
}

// lineText is the line itself when it is a string, else its compact JSON
// encoding as JSON.stringify writes it: no HTML escaping, no trailing
// newline, U+2028 and U+2029 left unescaped.
func lineText(line any) (string, error) {
	if s, ok := line.(string); ok {
		return s, nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(line); err != nil {
		return "", err
	}
	text := strings.TrimSuffix(buf.String(), "\n")
	return strings.NewReplacer(` `, " ", ` `, " ").Replace(text), nil
}
