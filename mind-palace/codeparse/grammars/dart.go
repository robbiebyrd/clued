package grammars

import "github.com/odvcencio/gotreesitter/grammars/dart"

// Dart, and Flutter, which is Dart with no grammar of its own.
func init() { Register("dart", dart.Language) }
