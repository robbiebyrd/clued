package grammars

import "github.com/odvcencio/gotreesitter/grammars/javascript"

func init() { Register("javascript", javascript.Language) }
