(* Classes, objects and inheritance; ppx extensions and attributes. *)

class virtual base (name : string) = object
  val name = name
  method name = name
  method virtual render : Buffer.t -> unit
end

(** A labelled button. *)
class button name ~on_click = object (self)
  inherit base name as super
  val mutable clicks = 0
  method render buf = Buffer.add_string buf self#name
  method click () = clicks <- clicks + 1; on_click self
  method clicks = clicks
end

class type clickable = object
  method click : unit -> unit
end

let make_button () = new button "ok" ~on_click:ignore

let%test "clicks" =
  let b = make_button () in
  b#click ();
  b#clicks = 1

let[@inline] twice f x = f (f x)

let handler = fun ev -> Event.dispatch ev

type event = [ `Click of int | `Key of char ] [@@deriving show, eq]

module Nested = struct
  module Deeper = struct
    let answer = 42
    let ask () = answer
  end

  let ask_twice () = Deeper.ask () + Deeper.ask ()
end

let local () =
  let helper y = y * 2 in
  helper 3
