package grammars

import csharp "github.com/odvcencio/gotreesitter/grammars/c_sharp"

func init() { Register("c_sharp", csharp.Language) }
