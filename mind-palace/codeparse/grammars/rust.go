package grammars

import "github.com/odvcencio/gotreesitter/grammars/rust"

func init() { Register("rust", rust.Language) }
