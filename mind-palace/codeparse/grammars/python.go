package grammars

import "github.com/odvcencio/gotreesitter/grammars/python"

func init() { Register("python", python.Language) }
