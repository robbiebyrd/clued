;;;; geometry.lisp --- shapes, areas and a little drawing protocol.

(defpackage #:geometry
  (:use #:cl)
  (:import-from #:alexandria #:when-let #:ensure-list)
  (:local-nicknames (#:a #:alexandria))
  (:export #:shape #:area #:make-circle #:*default-scale*)
  (:documentation "Shapes and their areas."))

(in-package #:geometry)

(require :sb-posix)

(defvar *default-scale* 1.0
  "The scale every shape is drawn at.")

(defparameter *registry* (make-hash-table :test 'equal))

(defconstant +epsilon+ 1d-9 "Tolerance of comparisons.")

(deftype coordinate () '(real -1000 1000))

;; The root of every shape.
(defclass shape ()
  ((name :initarg :name :reader shape-name)
   (origin :initarg :origin :initform (make-point 0 0) :accessor origin))
  (:documentation "Something with an area."))

(defclass circle (shape a:mixin)
  ((radius :initarg :radius :reader radius)))

(defstruct (point (:include base-point) (:constructor make-point (x y)))
  "A point in the plane."
  x
  (y 0 :type coordinate))

(define-condition invalid-shape (error)
  ((shape :initarg :shape :reader invalid-shape-shape))
  (:report (lambda (c stream)
             (format stream "invalid shape ~a" (invalid-shape-shape c)))))

(defgeneric area (shape)
  (:documentation "The area of SHAPE."))

(defmethod area ((c circle))
  (* pi (expt (radius c) 2)))

(defmethod area :around ((s shape))
  (let ((result (call-next-method)))
    (when (minusp result)
      (error 'invalid-shape :shape s))
    result))

(defun make-circle (radius &key (name "circle"))
  "Makes a circle of RADIUS, registered under NAME."
  (let ((c (make-instance 'circle :radius radius :name name)))
    (setf (gethash name *registry*) c)
    (register-shape c)
    c))

;;; Scales a shape; returns the new one.
(defun scale (shape factor)
  (cond ((typep shape 'circle)
         (make-circle (* (radius shape) factor)))
        (t (error 'invalid-shape :shape shape))))

(defun total-area (shapes)
  (reduce #'+ (mapcar #'area (a:ensure-list shapes))))

(defun safe-area (shape)
  (handler-case (area shape)
    (invalid-shape (e)
      (log-problem e)
      0)))

(defmacro with-scale ((factor) &body body)
  "Runs BODY with *default-scale* bound to FACTOR."
  `(let ((*default-scale* ,factor))
     (notify-scale ,factor)
     ,@body))

(defun draw-all (shapes stream)
  (dolist (s shapes)
    (when-let (name (shape-name s))
      (format stream "~a: ~,2f~%" name (area s))))
  (uiop:println "done" stream))
