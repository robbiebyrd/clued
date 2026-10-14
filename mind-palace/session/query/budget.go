package query

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// The default token budgets of the session tools: search and list tools,
// and the tools that read a session's content.
const (
	DefaultSearchTokens = 4000
	DefaultReadTokens   = 8000
	// MinTokens is the smallest budget honoured; a smaller max_tokens is
	// raised to it so a call always returns something useful.
	MinTokens = 200
)

// Tokens estimates the tokens of v's JSON as its length over four, the
// usual rule of thumb for English and code; it needs no tokenizer and only
// has to be roughly right.
func Tokens(v any) int {
	b, err := json.Marshal(session.JSONTimes(v))
	if err != nil {
		return 0
	}
	return (len(b) + 3) / 4
}

// Generic is v as the plain JSON values (maps, slices, strings, numbers)
// it encodes to, times in the session format.
func Generic(v any) any {
	b, err := json.Marshal(session.JSONTimes(v))
	if err != nil {
		return v
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return v
	}
	return out
}

// Fit keeps the longest prefix of items whose JSON fits maxTokens, in their
// generic JSON form. A first item that alone is over the budget is kept
// with its longest strings clipped, so a call always makes progress.
// truncated reports whether items were left out or clipped.
func Fit(items []any, maxTokens int) (kept []any, truncated bool) {
	kept = []any{}
	used := 2 // the brackets
	for i, item := range items {
		cost := Tokens(item) + 1
		if used+cost > maxTokens {
			if i == 0 {
				clipped, _ := Clip(item, maxTokens-used)
				return append(kept, clipped), true
			}
			return kept, true
		}
		kept = append(kept, item)
		used += cost
	}
	return kept, false
}

// Clip shortens the longest strings of a generic JSON value until it fits
// maxTokens, marking each cut with how much was left out; clipped reports
// whether anything was cut.
func Clip(v any, maxTokens int) (out any, clipped bool) {
	out = Generic(v)
	for range 64 {
		over := Tokens(out) - maxTokens
		if over <= 0 {
			return out, clipped
		}
		path, length := longestString(out, nil)
		if path == nil || length < 64 {
			return out, clipped
		}
		keep := max(length-over*4-40, 32)
		out = setString(out, path, func(s string) string {
			r := []rune(s)
			if keep >= len(r) {
				keep = len(r) / 2
			}
			return string(r[:keep]) + fmt.Sprintf("…[%d characters clipped]", len(r)-keep)
		})
		clipped = true
	}
	return out, clipped
}

// longestString finds the path to the longest string in v.
func longestString(v any, path []any) (best []any, length int) {
	switch x := v.(type) {
	case string:
		return append([]any(nil), path...), len([]rune(x))
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if p, n := longestString(x[k], append(path, k)); n > length {
				best, length = p, n
			}
		}
	case []any:
		for i, e := range x {
			if p, n := longestString(e, append(path, i)); n > length {
				best, length = p, n
			}
		}
	}
	return best, length
}

func setString(v any, path []any, f func(string) string) any {
	if len(path) == 0 {
		if s, ok := v.(string); ok {
			return f(s)
		}
		return v
	}
	switch x := v.(type) {
	case map[string]any:
		k := path[0].(string)
		x[k] = setString(x[k], path[1:], f)
	case []any:
		i := path[0].(int)
		x[i] = setString(x[i], path[1:], f)
	}
	return v
}

// Page is a list result cut to the budget: the items that fit, and the
// cursor that continues after them.
type Page struct {
	Items     []any  `json:"items"`
	Truncated bool   `json:"truncated"`
	Next      string `json:"next,omitempty"`
}

// Cursor says where a truncated result continues: the tool that issued it,
// the position in its list, and for get_full_session the position in each
// section.
type Cursor struct {
	Tool     string         `json:"t"`
	Offset   int            `json:"o,omitempty"`
	Sections map[string]int `json:"s,omitempty"`
}

// ErrBadCursor is returned for a cursor that is not one the tool issued.
var ErrBadCursor = errors.New("the cursor is not one this tool returned; call it again without cursor")

// Encode is the opaque form of the cursor.
func (c Cursor) Encode() string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

// DecodeCursor reads a cursor issued by tool; an empty one is the start.
func DecodeCursor(raw, tool string) (Cursor, error) {
	if raw == "" {
		return Cursor{Tool: tool}, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	var c Cursor
	if err != nil || json.Unmarshal(b, &c) != nil || c.Tool != tool || c.Offset < 0 {
		return Cursor{}, ErrBadCursor
	}
	for _, o := range c.Sections {
		if o < 0 {
			return Cursor{}, ErrBadCursor
		}
	}
	return c, nil
}

// Budget is the max_tokens of a call, or def when it is not positive, and
// never below MinTokens.
func Budget(maxTokens, def int) int {
	if maxTokens <= 0 {
		return def
	}
	return max(maxTokens, MinTokens)
}
