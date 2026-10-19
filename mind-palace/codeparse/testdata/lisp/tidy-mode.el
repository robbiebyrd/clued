;;; tidy-mode.el --- Keep buffers tidy  -*- lexical-binding: t -*-

;;; Commentary:
;; A minor mode that trims whitespace and reports what it changed.

;;; Code:

(require 'cl-lib)
(require 'subr-x)
(eval-when-compile (require 'rx))

(autoload 'magit-status "magit")

(defgroup tidy nil
  "Keep buffers tidy."
  :group 'editing)

(defcustom tidy-ignored-modes '(markdown-mode)
  "Major modes `tidy-mode' leaves alone."
  :type '(repeat symbol))

(defvar tidy--changes 0
  "How many lines the last run changed.")

(defvar-local tidy--last-run nil "When this buffer was last tidied.")

(defconst tidy-version "1.2.0")

(defface tidy-highlight '((t :inherit warning))
  "Face of the lines tidy changed.")

(define-error 'tidy-error "Tidy failed" 'user-error)

(cl-defstruct (tidy-report (:include tidy-base) (:constructor tidy-report-create))
  "What one run changed."
  lines
  (seconds 0.0))

(defclass tidy-rule (eieio-named tidy-base)
  ((pattern :initarg :pattern :type string))
  "One clean-up rule.")

(cl-defgeneric tidy-apply (rule)
  "Apply RULE to the current buffer.")

(cl-defmethod tidy-apply ((rule tidy-rule))
  (save-excursion
    (goto-char (point-min))
    (while (re-search-forward (oref rule pattern) nil t)
      (replace-match ""))))

;; Trims trailing white space; returns the number of lines changed.
(defun tidy-trim-buffer ()
  (interactive)
  (let ((count 0))
    (save-excursion
      (goto-char (point-min))
      (while (not (eobp))
        (when (looking-at-p ".*[ \t]+$")
          (cl-incf count)
          (tidy--trim-line))
        (forward-line 1)))
    (setq tidy--changes count)))

(defsubst tidy--trim-line ()
  "Delete the white space at the end of the line."
  (delete-trailing-whitespace (line-beginning-position) (line-end-position)))

(defmacro tidy-with-report (&rest body)
  "Run BODY and message what changed."
  `(let ((start (float-time)))
     ,@body
     (tidy--report (- (float-time) start))))

(cl-defun tidy--report (seconds &key (verbose t))
  "Report SECONDS spent."
  (when verbose
    (message "tidy: %d lines in %.2fs" tidy--changes seconds)))

(defun tidy--maybe-run ()
  (cond ((memq major-mode tidy-ignored-modes) nil)
        ((buffer-modified-p) (tidy-with-report (tidy-trim-buffer)))
        (t (tidy--log "unchanged"))))

(define-minor-mode tidy-mode
  "Tidy the buffer before saving."
  :lighter " Tidy"
  (if tidy-mode
      (add-hook 'before-save-hook #'tidy--maybe-run nil t)
    (remove-hook 'before-save-hook #'tidy--maybe-run t)))

(define-derived-mode tidy-log-mode special-mode "Tidy-Log"
  "Mode of the tidy log buffer.")

(defalias 'tidy #'tidy-mode)

(provide 'tidy-mode)
;;; tidy-mode.el ends here
