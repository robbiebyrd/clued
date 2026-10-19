package grammars

import lua "github.com/odvcencio/gotreesitter/grammars/lua"

func init() { Register("lua", lua.Language) }
