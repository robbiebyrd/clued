;;;; geometry.asd

(asdf:defsystem #:geometry
  :description "Shapes and their areas."
  :version "0.3.0"
  :depends-on ("alexandria" #:split-sequence :cl-ppcre)
  :components ((:file "package")
               (:file "geometry" :depends-on ("package"))))

(defsystem "geometry/tests"
  :depends-on ("geometry" "fiveam")
  :perform (test-op (o c) (symbol-call :fiveam '#:run! :geometry)))
