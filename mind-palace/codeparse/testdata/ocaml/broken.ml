(* A file with syntax errors: a stray parenthesis in a match and a record
   field without a type. The definitions around them are still found. *)

type t = { name : string; size : int }

let make name = { name; size = 0 }

let oops x = match x with | Some -> ) | None -> 0

type half = { a : int; b : }

let grow t n = { t with size = t.size + n }

module After = struct
  let still_found () = make "x"
end
