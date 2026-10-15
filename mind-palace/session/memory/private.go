package memory

import "github.com/robbiebyrd/clued/mind-palace/session/redact"

// StripPrivate removes every <private>…</private> span, tags included.
// Spans nest, and an unclosed tag drops the rest of the text. Memory runs
// it before anything reaches an engine, the store or an export.
func StripPrivate(text string) string { return redact.StripPrivate(text) }
