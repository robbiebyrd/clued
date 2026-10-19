;; Guile and Chicken style modules, in a .ss file.
(define-module (app main)
  #:use-module (ice-9 match)
  #:use-module (srfi srfi-1)
  #:export (main))

(use-modules (ice-9 format))
(import chicken.string (only srfi-13 string-pad))
(include "config.scm")

(define-syntax unless*
  (syntax-rules ()
    ((_ test body ...) (if test #f (begin body ...)))))

(define ((adder n) x)
  (+ n x))

(define (main args)
  (match args
    ((_ name) (greet name))
    (_ (usage)))
  (for-each (lambda (a) (display (string-pad a 10))) (cdr args))
  (format #t "~a~%" (fold + 0 (map string-length args))))
