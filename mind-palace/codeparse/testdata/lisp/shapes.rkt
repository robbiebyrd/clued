#lang racket/base
;; Shapes with classes, structs and contracts.

(require racket/class
         racket/contract
         racket/match
         (only-in racket/list first rest empty?)
         (prefix-in str: racket/string)
         (for-syntax racket/base syntax/parse)
         "private/util.rkt")

(provide (contract-out [area (-> shape? real?)])
         shape%
         (struct-out point))

;; A point in the plane.
(struct point (x y) #:transparent)

(struct point3 point (z [label #:mutable]))

(define origin (point 0 0))

(define shape<%> (interface () area perimeter))

(define shape%
  (class* object% (shape<%>)
    (init-field [name "shape"])
    (super-new)
    (define/public (area) 0)
    (define/public (perimeter) 0)
    (define/private (log-area)
      (printf "~a: ~a\n" name (area)))))

(define circle%
  (class shape%
    (init-field radius)
    (super-new [name "circle"])
    (define/override (area)
      (* pi (sqr radius)))))

(define (area s)
  (match s
    [(point x y) (distance origin (point x y))]
    [(? (λ (v) (is-a? v shape%))) (send s area)]
    [_ (raise-argument-error 'area "shape?" s)]))

(define/contract (distance a b)
  (-> point? point? real?)
  (sqrt (+ (sqr (- (point-x a) (point-x b)))
           (sqr (- (point-y a) (point-y b))))))

(define-syntax-rule (with-shape (name expr) body ...)
  (let ([name expr])
    (log-shape name)
    body ...))

(define (total-area shapes)
  (for/sum ([s (in-list shapes)])
    (area s)))

(define (labels shapes)
  (for/list ([s shapes]
             #:when (point? s))
    (str:string-join (list (format "~a" (point-x s))) ",")))

(module+ test
  (require rackunit)
  (check-equal? (total-area (list (point 3 4))) 5))
