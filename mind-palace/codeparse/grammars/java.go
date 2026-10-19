package grammars

import "github.com/odvcencio/gotreesitter/grammars/java"

func init() { Register("java", java.Language) }
