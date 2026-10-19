package grammars

import "github.com/odvcencio/gotreesitter/grammars/ruby"

func init() { Register("ruby", ruby.Language) }
