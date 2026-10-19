;;; queue.scm -- a functional queue as an R7RS library.

(define-library (data queue)
  (export make-queue queue-empty? enqueue dequeue queue->list)
  (import (scheme base)
          (scheme write)
          (only (srfi 1) fold append-reverse)
          (prefix (data util) util:))
  (begin
    ;; A queue is a front list and a reversed back list.
    (define-record-type queue
      (%make-queue front back)
      queue?
      (front queue-front)
      (back queue-back set-queue-back!))

    (define empty-front '())

    (define (make-queue) (%make-queue empty-front '()))

    (define (queue-empty? q)
      (and (null? (queue-front q)) (null? (queue-back q))))

    ;; Adds x at the back.
    (define (enqueue q x)
      (%make-queue (queue-front q) (cons x (queue-back q))))

    (define (dequeue q)
      (cond ((queue-empty? q) (error "dequeue: empty queue" q))
            ((null? (queue-front q))
             (dequeue (%make-queue (reverse (queue-back q)) '())))
            (else
             (values (car (queue-front q))
                     (%make-queue (cdr (queue-front q)) (queue-back q))))))

    (define (queue->list q)
      (let loop ((q q) (acc '()))
        (if (queue-empty? q)
            (reverse acc)
            (let-values (((x rest) (dequeue q)))
              (loop rest (cons x acc))))))

    (define-syntax with-queue
      (syntax-rules ()
        ((_ (name init) body ...)
         (let ((name (util:ensure-queue init)))
           body ...))))

    (define (sum q)
      (fold + 0 (queue->list q)))))
