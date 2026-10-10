package enrichers

import (
	"context"
	"regexp"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

type binariesResult struct {
	Binaries []string `json:"binaries" bson:"binaries"`
}

// BashBinaries lists the binary invoked by each stage of a Bash command.
var BashBinaries = session.Enricher{
	Name:       "bash-binaries",
	Collection: "hook_events",
	Enabled:    true,
	Matches: func(doc session.Doc) bool {
		tool, _ := doc.String("tool_name")
		_, hasCommand := doc.ToolInput().String("command")
		return tool == "Bash" && hasCommand
	},
	Enrich: func(_ context.Context, doc session.Doc, _ session.Lookup) (any, error) {
		command, _ := doc.ToolInput().String("command")
		return binariesResult{Binaries: extractBinaries(command)}, nil
	},
}

func init() { session.Register(BashBinaries) }

var (
	pipelineSeparator = regexp.MustCompile(`&&|\|\||;|\||\n`)
	envAssignment     = regexp.MustCompile(`^\w+=`)
	surroundingQuote  = regexp.MustCompile(`^["']|["']$`)
)

var shellBuiltins = map[string]bool{
	"if": true, "then": true, "else": true, "fi": true, "for": true, "do": true, "done": true,
	"while": true, "case": true, "esac": true, "echo": true, "cd": true, "export": true,
	"source": true, ".": true, "[": true, "[[": true, "]]": true, "]": true,
}

// hookEventIsSpace reports whether r is whitespace in the JavaScript sense
// (the \s class and String.prototype.trim), which is wider than RE2's \s.
func hookEventIsSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// hookEventLower lower-cases like JavaScript's toLowerCase, which expands
// U+0130 to "i" plus a combining dot where Go yields a plain "i".
func hookEventLower(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, "İ", "i̇"))
}

// extractBinaries returns the distinct binary names of a command: the first
// non-assignment token of each stage split on &&, ||, ;, | and newlines, with
// quotes and directories stripped and shell builtins dropped.
func extractBinaries(command string) []string {
	binaries := []string{}
	seen := map[string]bool{}
	for _, stage := range pipelineSeparator.Split(command, -1) {
		stage = strings.TrimFunc(stage, hookEventIsSpace)
		if stage == "" {
			continue
		}
		binary := firstNonAssignmentToken(stage)
		binary = surroundingQuote.ReplaceAllString(binary, "")
		binary = binary[strings.LastIndex(binary, "/")+1:]
		if binary == "" || shellBuiltins[binary] || seen[binary] {
			continue
		}
		seen[binary] = true
		binaries = append(binaries, binary)
	}
	return binaries
}

func firstNonAssignmentToken(stage string) string {
	for _, token := range strings.FieldsFunc(stage, hookEventIsSpace) {
		if !envAssignment.MatchString(token) {
			return token
		}
	}
	return ""
}
