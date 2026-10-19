(* A key-value store over a functorised map, with a cache. *)

open Stdlib
open! Base
include Store_intf

module SMap = Map.Make (String)
module L = List
module Json = Yojson.Safe

module type BACKEND = sig
  type conn
  val connect : string -> conn
  val get : conn -> string -> string option
end

(** Build a store over any backend. *)
module Make (B : BACKEND) : S with type key = string = struct
  type key = string
  type t = { conn : B.conn; mutable cache : string SMap.t }

  let create url = { conn = B.connect url; cache = SMap.empty }

  let find t k =
    match SMap.find_opt k t.cache with
    | Some v -> Some v
    | None ->
      let v = B.get t.conn k in
      Option.iter (fun v -> t.cache <- SMap.add k v t.cache) v;
      v
end

module Memory : BACKEND = struct
  type conn = (string, string) Hashtbl.t
  let connect _ = Hashtbl.create 16
  let get conn k = Hashtbl.find_opt conn k
end

module Mem_store = Make (Memory)

let to_json (pairs : (string * string) list) : Json.t =
  `Assoc (L.map (fun (k, v) -> (k, `String v)) pairs)

let keys m = SMap.bindings m |> L.map fst

let count_keys m =
  let open L in
  length (keys m)

let dump m = String.(concat ", " (keys m))

let lookup store k =
  try Mem_store.find store k with Not_found -> raise (Store_error.Missing k)

let first_class = (module Memory : BACKEND)

let host r = r.Config.host
