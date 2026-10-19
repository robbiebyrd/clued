package grammars

import "github.com/odvcencio/gotreesitter/grammars/c"

func init() { Register("c", c.Language) }
