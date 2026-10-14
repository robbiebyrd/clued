package enrichers

import (
	"context"
	"regexp"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// ErrorFlagResult is the stored result of the error-flag enricher. ErrorType is
// hook_error, api_error (a system record of that subtype), tool_failure,
// error_text, or nil when the line is not an error.
// Rejected marks a tool result the harness wrote to refuse the call, which
// then never ran, with Rejection saying why (permission_denied,
// blocked_by_hook, input_validation, user_declined, symlink, other) and
// ToolUseID naming the call.
type ErrorFlagResult struct {
	IsError   bool    `json:"is_error" bson:"is_error"`
	ErrorType *string `json:"error_type" bson:"error_type"`
	Rejected  bool    `json:"rejected,omitempty" bson:"rejected,omitempty"`
	Rejection string  `json:"rejection,omitempty" bson:"rejection,omitempty"`
	ToolUseID string  `json:"tool_use_id,omitempty" bson:"tool_use_id,omitempty"`
}

var errorTextRe = regexp.MustCompile(`\b(Error:|ENOENT|EACCES|exception|stack trace|exit code [^0])`)

func errorOfType(errorType string) ErrorFlagResult {
	return ErrorFlagResult{IsError: true, ErrorType: &errorType}
}

// ErrorFlag flags transcript lines that report an error. Precedence: a failed
// hook, then a failed tool result, then error-looking message text.
var ErrorFlag = session.Enricher{
	Name:       "error-flag",
	Collection: "transcript_lines",
	Enabled:    true,
	Matches:    func(session.Doc) bool { return true },
	Enrich: func(_ context.Context, doc session.Doc, _ session.Lookup) (any, error) {
		if doc.Line() == nil {
			return ErrorFlagResult{}, nil
		}
		if attType, _ := doc.Attachment().String("type"); attType == "hook_error" {
			return errorOfType("hook_error"), nil
		}
		if t, _ := doc.Line().String("type"); t == "system" {
			if sub, _ := doc.Line().String("subtype"); sub == "api_error" {
				return errorOfType("api_error"), nil
			}
		}

		content := doc.Content()
		if role, _ := doc.Message().String("role"); role == "user" {
			for _, block := range content {
				if blockType, _ := block.String("type"); blockType != "tool_result" {
					continue
				}
				if reason, rejected := Rejection(resultText(block["content"])); rejected {
					r := errorOfType("tool_failure")
					r.Rejected, r.Rejection = true, reason
					r.ToolUseID, _ = block.String("tool_use_id")
					return r, nil
				}
				if block["is_error"] == true {
					return errorOfType("tool_failure"), nil
				}
			}
		}

		for _, block := range content {
			if blockType, _ := block.String("type"); blockType != "text" {
				continue
			}
			if text, ok := block.String("text"); ok && errorTextRe.MatchString(text) {
				return errorOfType("error_text"), nil
			}
		}
		return ErrorFlagResult{}, nil
	},
}

func init() { session.Register(ErrorFlag) }
