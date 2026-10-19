(ns geometry.util
  (:require #?(:clj [clojure.java.io :as io]
               :cljs [goog.string :as gstring])))

(defn clamp
  "Clamps x between lo and hi."
  [lo hi x]
  (max lo (min hi x)))

(defn square [n] (* n n))

(defn now []
  #?(:clj (System/currentTimeMillis)
     :cljs (.now js/Date)))

(defn perimeter-of [r]
  #?(:clj (* 2 (+ (.getWidth r) (.getHeight r)))
     :cljs (js/console.warn "unsupported")))

(defn read-config [path]
  #?(:clj (slurp (io/resource path))
     :cljs (gstring/format "%s" path)))
