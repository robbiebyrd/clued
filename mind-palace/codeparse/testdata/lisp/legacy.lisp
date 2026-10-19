;;; legacy.lisp --- upper-case code, nested and odd definitions.

(IN-PACKAGE :LEGACY)

;; Pushes ITEM onto the queue.
(DEFUN ENQUEUE (ITEM QUEUE)
  (PUSH ITEM (CDR QUEUE)))

(DEFVAR *QUEUE* (LIST NIL))

(defun (setf queue-head) (value queue)
  (setf (car queue) value))

(defun walk-tree (tree fn)
  (labels ((walk (node depth)
             (funcall fn node depth)
             (dolist (child (node-children node))
               (walk child (1+ depth)))))
    (walk tree 0)))

(defun parse-config (lines)
  (flet ((trim (s) (string-trim " " s)))
    (multiple-value-bind (key value) (split-line (trim (first lines)))
      (destructuring-bind (&optional section &rest rest) (parse-key key)
        (list section rest value)))))

;; An unknown definer is a call: defroute is a library macro.
(defroute home "/" ()
  (render-page "home"))

(eval-when (:compile-toplevel :load-toplevel :execute)
  (defun helper-at-compile-time (x)
    (legacy::internal-check x)))
