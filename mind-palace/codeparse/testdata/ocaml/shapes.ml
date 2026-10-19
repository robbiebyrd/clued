(** Geometric shapes and their areas. *)

open Printf

(** A point in the plane. *)
type point = { x : float; mutable y : float }

(** The shapes this module knows. *)
type shape =
  | Circle of point * float
  | Rect of point * point
  | Polygon of point list

type 'a labelled = string * 'a

exception Degenerate of string

let pi = 4.0 *. atan 1.0

(** [area s] is the area of [s]. *)
let area = function
  | Circle (_, r) -> pi *. r *. r
  | Rect (a, b) -> abs_float ((b.x -. a.x) *. (b.y -. a.y))
  | Polygon [] -> raise (Degenerate "empty polygon")
  | Polygon _ -> 0.0

let rec perimeter_of_points acc = function
  | a :: (b :: _ as rest) -> perimeter_of_points (acc +. distance a b) rest
  | _ -> acc

and distance a b = sqrt (((b.x -. a.x) ** 2.0) +. ((b.y -. a.y) ** 2.0))

let translate dx dy p = { p with x = p.x +. dx; y = p.y +. dy }

let ( +| ) a b = { x = a.x +. b.x; y = a.y +. b.y }

let describe s =
  sprintf "shape with area %.2f" (area s)

external hypot : float -> float -> float = "caml_hypot_float" "caml_hypot"
  [@@unboxed] [@@noalloc]

let () =
  [ Circle ({ x = 0.; y = 0. }, 1.); Rect ({ x = 0.; y = 0. }, { x = 2.; y = 3. }) ]
  |> List.map describe
  |> List.iter print_endline
