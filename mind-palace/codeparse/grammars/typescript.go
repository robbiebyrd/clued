package grammars

import (
	"github.com/odvcencio/gotreesitter/grammars/tsx"
	"github.com/odvcencio/gotreesitter/grammars/typescript"
)

// TypeScript and its tsx dialect (.tsx files) have one grammar each.
func init() {
	Register("typescript", typescript.Language)
	Register("tsx", tsx.Language)
}
