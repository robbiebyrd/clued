package grammars

import "github.com/odvcencio/gotreesitter/grammars/ocaml"

// OCaml implementations (.ml) and interfaces (.mli, the "interface" dialect)
// both parse with the ocaml grammar: gotreesitter does not ship the separate
// ocaml_interface grammar, and the ocaml one accepts signature items.
func init() { Register("ocaml", ocaml.Language) }
