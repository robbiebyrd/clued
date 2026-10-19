package grammars

import "github.com/odvcencio/gotreesitter/grammars/sql"

func init() { Register("sql", sql.Language) }
