package hookguard

import (
	"encoding/json"
	"regexp"
)

// StopInput is the part of the Stop hook's JSON input the guard reads.
type StopInput struct {
	// StopHookActive is true when the assistant continues because a Stop
	// hook already blocked it once this turn.
	StopHookActive bool `json:"stop_hook_active"`
	// LastAssistantMessage is the message the assistant is stopping on.
	LastAssistantMessage string `json:"last_assistant_message"`
}

// Decision is the JSON a hook prints to block the assistant from stopping.
type Decision struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// Rule pairs a phrase with the correction the assistant is sent when its
// message contains it.
type Rule struct {
	// Pattern is matched case-insensitively anywhere in the message.
	Pattern *regexp.Regexp
	// Correction is the instruction the assistant continues with.
	Correction string
}

// Name returns the pattern's text, for logs and tests.
func (r Rule) Name() string { return r.Pattern.String() }

// StopReasonPrefix starts every reason the Stop guard returns.
const StopReasonPrefix = "STOP HOOK VIOLATION: "

// StopRules are checked in order against the whole message; the first match
// wins. They are grouped by severity: dodging ownership of a failure, calling
// something a known limitation, quitting because the session is long, and
// asking permission to continue work already asked for.
var StopRules = compileRules([][2]string{
	// Ownership dodging (dismissing failures as not-my-fault).
	{`pre-existing`, "NOTHING IS PRE-EXISTING (CLAUDE.md golden rule). All builds and tests are green upstream. If something fails, YOUR work caused it. Investigate and fix it. Never dismiss a failure as pre-existing."},
	{`not from my changes`, "NOTHING IS PRE-EXISTING. You own every change. Investigate the failure."},
	{`not my change`, "NOTHING IS PRE-EXISTING. You own every change. Investigate the failure."},
	{`not caused by my`, "NOTHING IS PRE-EXISTING. You own every change. Investigate the failure."},
	{`not introduced by my`, "NOTHING IS PRE-EXISTING. You own every change. Investigate the failure."},
	{`already existed before`, "NOTHING IS PRE-EXISTING. If you found it broken, fix it or explain exactly what is wrong and what the fix requires."},
	{`before my changes`, "NOTHING IS PRE-EXISTING. There is no 'before your changes' — you own the codebase as it is now."},
	{`prior to my changes`, "NOTHING IS PRE-EXISTING. There is no 'prior to your changes.'"},
	{`unrelated to my changes`, "NOTHING IS PRE-EXISTING. If it is broken, fix it. If you broke it, fix it. Nothing is unrelated."},
	{`an existing issue`, "NOTHING IS PRE-EXISTING. Investigate and fix, or explain exactly what is wrong."},
	{`existing bug`, "NOTHING IS PRE-EXISTING. Investigate and fix."},

	// Known-limitation dodging.
	{`known limitation`, "NO KNOWN LIMITATIONS (CLAUDE.md golden rule). Investigate whether it is fixable. Either fix it or explain the specific technical reason it cannot be fixed right now."},
	{`known issue`, "NO KNOWN LIMITATIONS. Explain the specific technical reason or fix it."},
	{`future work`, "NO KNOWN LIMITATIONS. Fix it now or describe exactly what the fix requires — not as a TODO, as a technical explanation."},
	{`left as an exercise`, "NO KNOWN LIMITATIONS. Do the work."},

	// Session-length quitting.
	{`session length`, "Sessions are unlimited (CLAUDE.md rule). If work remains, do the work. Continue."},
	{`session depth`, "Sessions are unlimited. Continue working."},
	{`given the length of this`, "Sessions are unlimited. Continue working."},
	{`continue in a new session`, "Sessions are unlimited. There is no reason to defer to a new session. Continue."},
	{`good place to stop`, "Is the task done? If not, continue working. Sessions are unlimited."},
	{`good stopping point`, "Is the task done? If not, continue working. Sessions are unlimited."},
	{`good checkpoint given`, "Is the task done? If not, continue working."},
	{`natural stopping`, "Is the task done? If not, continue working."},
	{`logical stopping`, "Is the task done? If not, continue working."},
	{`this session has gotten long`, "Sessions are unlimited. You are a machine. Continue working."},
	{`session has been long`, "Sessions are unlimited. Continue working."},
	{`getting long`, "Sessions are unlimited. Continue working."},
	{`lengthy session`, "Sessions are unlimited. Continue working."},

	// Permission-seeking mid-task: when the only possible answer is "yes,
	// you have not finished yet", the question should not be asked.
	{`want to continue.*or `, "Do not ask. The task is not done. Continue working."},
	{`or save it for`, "Do not ask. The task is not done. Continue working."},
	{`should I continue`, "Do not ask. If the task is not done, continue. The user will interrupt if they want you to stop."},
	{`shall I continue`, "Do not ask. Continue working until the task is complete."},
	{`shall I proceed`, "Do not ask. Proceed."},
	{`would you like me to continue`, "Do not ask. Continue."},
	{`would you like to continue`, "Do not ask. Continue."},
	{`want me to keep going`, "Do not ask. Keep going."},
	{`want me to continue`, "Do not ask. Continue."},
	{`should I keep going`, "Do not ask. Keep going."},
	{`save it for next time`, "There is no 'next time.' Sessions are unlimited. Continue working."},
	{`in the next session`, "There is no 'next session.' This session is unlimited. Continue working."},
	{`next session`, "There is no 'next session.' This session is unlimited. Continue working."},
	{`next conversation`, "There is no 'next conversation.' Continue working."},
	{`pick this up later`, "There is no 'later.' Continue working now."},
	{`come back to this`, "There is no 'coming back.' Continue working now."},
	{`continue in a follow-up`, "There is no 'follow-up.' Continue now."},
	{`pause here`, "Do not pause. The task is not done. Continue."},
	{`stop here for now`, "Do not stop. The task is not done. Continue."},
	{`wrap up for now`, "Do not wrap up. The task is not done. Continue."},
	{`call it here`, "Do not stop. Continue working."},
})

func compileRules(pairs [][2]string) []Rule {
	rules := make([]Rule, 0, len(pairs))
	for _, p := range pairs {
		rules = append(rules, Rule{Pattern: regexp.MustCompile(`(?is)` + p[0]), Correction: p[1]})
	}
	return rules
}

// MatchStop returns the first rule whose pattern occurs in message.
func MatchStop(message string) (Rule, bool) {
	for _, r := range StopRules {
		if r.Pattern.MatchString(message) {
			return r, true
		}
	}
	return Rule{}, false
}

// CheckStop decides whether the assistant may stop. It returns nil to allow
// the stop: when a Stop hook already blocked this turn (so the assistant is
// never trapped in a loop), when there is no message, or when no rule
// matches. Otherwise it returns the blocking decision carrying the
// correction of the first matching rule.
func CheckStop(in StopInput) *Decision {
	if in.StopHookActive || in.LastAssistantMessage == "" {
		return nil
	}
	rule, ok := MatchStop(in.LastAssistantMessage)
	if !ok {
		return nil
	}
	return &Decision{Decision: "block", Reason: StopReasonPrefix + rule.Correction}
}

// CheckStopJSON parses the hook's JSON input and runs CheckStop. Fields the
// guard does not read are ignored.
func CheckStopJSON(input []byte) (*Decision, error) {
	var in StopInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	return CheckStop(in), nil
}
