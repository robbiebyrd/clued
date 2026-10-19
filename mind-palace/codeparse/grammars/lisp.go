package grammars

import (
	"github.com/odvcencio/gotreesitter/grammars/clojure"
	"github.com/odvcencio/gotreesitter/grammars/commonlisp"
	"github.com/odvcencio/gotreesitter/grammars/elisp"
	"github.com/odvcencio/gotreesitter/grammars/racket"
	"github.com/odvcencio/gotreesitter/grammars/scheme"
)

// Lisp has one grammar per dialect (the Languages table's Dialects):
// Common Lisp, Emacs Lisp, Clojure, Scheme and Racket.
func init() {
	Register("commonlisp", commonlisp.Language)
	Register("elisp", elisp.Language)
	Register("clojure", clojure.Language)
	Register("scheme", scheme.Language)
	Register("racket", racket.Language)
}
