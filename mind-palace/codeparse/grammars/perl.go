package grammars

import "github.com/odvcencio/gotreesitter/grammars/perl"

func init() { Register("perl", perl.Language) }
