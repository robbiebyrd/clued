package service

import (
	"errors"
	"fmt"
	"strings"
)

// Error kinds, as named in the service specification.
const (
	KindNotFound          = "NotFound"
	KindValidation        = "ValidationError"
	KindInvalidTransition = "InvalidTransition"
	KindImmutableField    = "ImmutableField"
	KindUnknownSection    = "UnknownSection"
	KindLinkedPlan        = "LinkedPlan"
	KindBadRequest        = "BadRequest"
	KindConflict          = "Conflict"
	KindStorage           = "StorageError"
)

// Error is the typed error every entrypoint maps to its own representation.
type Error struct {
	Kind     string   `json:"kind"`
	Message  string   `json:"message"`
	Problems []string `json:"problems,omitempty"`
	// Partial is set when the primary store accepted the write but one or
	// more secondary copies failed: the returned plan is valid, the copies
	// need `plan sync`.
	Partial bool  `json:"partial,omitempty"`
	Cause   error `json:"-"`
}

func (e *Error) Error() string {
	s := e.Kind + ": " + e.Message
	if len(e.Problems) > 0 {
		s += "\n  - " + strings.Join(e.Problems, "\n  - ")
	}
	return s
}

func (e *Error) Unwrap() error { return e.Cause }

// HTTPStatus maps the error kind to an HTTP status code.
func (e *Error) HTTPStatus() int {
	switch e.Kind {
	case KindNotFound:
		return 404
	case KindValidation, KindBadRequest, KindUnknownSection:
		return 400
	case KindInvalidTransition, KindImmutableField, KindLinkedPlan, KindConflict:
		return 409
	case KindStorage:
		return 500
	}
	return 500
}

func newErr(kind, format string, args ...any) *Error {
	return &Error{Kind: kind, Message: fmt.Sprintf(format, args...)}
}

// AsError converts any error into *Error (unknown errors become StorageError).
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Kind: KindStorage, Message: err.Error(), Cause: err}
}

// IsKind reports whether err is a service error of the given kind.
func IsKind(err error, kind string) bool {
	var e *Error
	return errors.As(err, &e) && e.Kind == kind
}
