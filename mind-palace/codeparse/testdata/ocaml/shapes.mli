(** Geometric shapes and their areas. *)

open Printf

(** A point in the plane. *)
type point = { x : float; mutable y : float }

type shape =
  | Circle of point * float
  | Rect of point * point
  | Polygon of point list

(** An abstract handle. *)
type t

type 'a labelled = private string * 'a

exception Degenerate of string

val pi : float

(** [area s] is the area of [s]. *)
val area : shape -> float

val translate : float -> float -> point -> point

val ( +| ) : point -> point -> point

val describe : shape -> string

val origins : point list

external hypot : float -> float -> float = "caml_hypot_float" "caml_hypot"

(** Ordered collections of shapes. *)
module type ORDERED = sig
  type t
  val compare : t -> t -> int
end

module Sorted (O : ORDERED) : sig
  type elt = O.t
  val sort : elt list -> elt list
end

module Units : sig
  val cm : float -> float
end

class canvas : int -> int -> object
  method draw : shape -> unit
  method size : int * int
end

class type drawable = object
  method draw : canvas -> unit
end
