package grammars

import "github.com/odvcencio/gotreesitter/grammars/php"

func init() { Register("php", php.Language) }
