package grammars

import "github.com/odvcencio/gotreesitter/grammars/kotlin"

func init() { Register("kotlin", kotlin.Language) }
