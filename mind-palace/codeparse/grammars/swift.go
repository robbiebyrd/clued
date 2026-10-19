package grammars

import "github.com/odvcencio/gotreesitter/grammars/swift"

func init() { Register("swift", swift.Language) }
