package grammars

import "github.com/odvcencio/gotreesitter/grammars/cpp"

func init() { Register("cpp", cpp.Language) }
