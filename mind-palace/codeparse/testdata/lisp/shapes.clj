(ns geometry.shapes
  "Shapes, their areas and a registry."
  (:require [clojure.string :as str]
            [clojure.set :refer [union difference]]
            [geometry.util :as u :refer [clamp]]
            clojure.walk)
  (:import (java.util UUID Date)
           java.io.File))

(set! *warn-on-reflection* true)

(def ^:dynamic *scale*
  "The scale every shape is drawn at."
  1.0)

(defonce registry (atom {}))

(defprotocol Shape
  "Something with an area."
  (area [this] "The area of this shape.")
  (perimeter [this]))

(defrecord Circle [radius]
  Shape
  (area [_] (* Math/PI radius radius))
  (perimeter [_] (* 2 Math/PI radius)))

(deftype Square [side]
  Shape
  (area [_] (* side side))
  (perimeter [_] (* 4 side))
  Object
  (toString [_] (str "Square " side)))

(extend-protocol Shape
  java.awt.Rectangle
  (area [r] (* (.getWidth r) (.getHeight r)))
  (perimeter [r] (u/perimeter-of r)))

(defmulti describe :kind)

(defmethod describe :circle [{:keys [radius]}]
  (format "circle of radius %s" radius))

(defmethod describe :default [shape]
  (str "shape " (pr-str shape)))

;; Registers a shape under a fresh id.
(defn register!
  [shape]
  (let [id (str (UUID/randomUUID))]
    (swap! registry assoc id shape)
    id))

(defn total-area
  "Sums the areas of shapes, scaled."
  ([shapes] (total-area shapes *scale*))
  ([shapes scale]
   (->> shapes
        (map area)
        (map #(* % scale))
        (reduce +))))

(defn- file-name ^String [^File f]
  (str/lower-case (.getName f)))

(defmacro with-scale
  "Runs body with *scale* bound."
  [s & body]
  `(binding [*scale* ~s]
     (log-scale ~s)
     ~@body))

(defn shape-proxy []
  (proxy [java.util.AbstractList clojure.lang.IFn] []
    (size [] 0)))

(defn sized [n]
  (reify Shape
    (area [_] (clamp 0 n (u/square n)))
    (perimeter [_] 0)))

(def stamp (new Date))
