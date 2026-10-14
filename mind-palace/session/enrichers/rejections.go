package enrichers

import "strings"

// The reasons a tool call was rejected and never ran.
const (
	RejectionPermissionDenied = "permission_denied"
	RejectionBlockedByHook    = "blocked_by_hook"
	RejectionInputValidation  = "input_validation"
	RejectionUserDeclined     = "user_declined"
	RejectionSymlink          = "symlink"
	RejectionOther            = "other"
)

// rejectionMarkers are the texts Claude Code answers a tool call with when
// it refuses to run it: the harness, not the tool, wrote the result. Each
// is matched anywhere in the result's text, first match wins. Add a marker
// here when Claude Code gains a refusal.
var rejectionMarkers = []struct {
	Text   string
	Reason string
}{
	{"InputValidationError:", RejectionInputValidation},
	{"Permission for this action was denied", RejectionPermissionDenied},
	{"Refusing to write through symlink", RejectionSymlink},
	{"The user doesn't want to proceed", RejectionUserDeclined},
	{"Tool use was rejected", RejectionUserDeclined},
	{"This operation was blocked by a hook", RejectionBlockedByHook},
	{"Blocked:", RejectionOther},
}

// Rejection reports whether a tool result's text is a refusal of the
// harness and why. Markers are matched anywhere in the text; a bare
// "Blocked:" only at its start, since an ordinary command's output may
// well hold the word.
func Rejection(text string) (reason string, rejected bool) {
	for _, m := range rejectionMarkers {
		if m.Text == "Blocked:" {
			if strings.HasPrefix(strings.TrimSpace(text), m.Text) {
				return m.Reason, true
			}
			continue
		}
		if strings.Contains(text, m.Text) {
			return m.Reason, true
		}
	}
	return "", false
}
