package grammars

import golang "github.com/odvcencio/gotreesitter/grammars/go"

func init() { Register("go", golang.Language) }
