package service

import (
	"errors"
	"fmt"
	"strings"
)

// Error kinds, as named in the service specifications.
const (
	KindNotFound           = "NotFound"
	KindValidation         = "ValidationError"
	KindInvalidTransition  = "InvalidTransition"
	KindIncompleteCriteria = "IncompleteCriteria"
	KindImmutableField     = "ImmutableField"
	KindUnknownSection     = "UnknownSection"
	KindUnknownStep        = "UnknownStep"
	KindUnknownCriterion   = "UnknownCriterion"
	KindLinkedPlan         = "LinkedPlan"
	KindLinkedStory        = "LinkedStory"
	KindBadRequest         = "BadRequest"
	KindConflict           = "Conflict"
	KindStorage            = "StorageError"
)

// Kinds lists every error kind in a stable order.
var Kinds = []string{
	KindBadRequest, KindValidation, KindNotFound, KindInvalidTransition, KindImmutableField,
	KindUnknownSection, KindLinkedPlan, KindConflict, KindStorage,
	KindIncompleteCriteria, KindUnknownStep, KindUnknownCriterion, KindLinkedStory,
}

// Error is the typed error every entrypoint maps to its own representation.
type Error struct {
	Kind     string   `json:"kind"`
	Message  string   `json:"message"`
	Problems []string `json:"problems,omitempty"`
	// Partial is set when the primary store accepted the write but one or
	// more secondary copies failed: the returned document is valid, the
	// copies need `mind-palace sync`.
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
	case KindValidation, KindBadRequest, KindUnknownSection, KindUnknownStep, KindUnknownCriterion:
		return 400
	case KindInvalidTransition, KindIncompleteCriteria, KindImmutableField, KindLinkedPlan, KindLinkedStory, KindConflict:
		return 409
	case KindStorage:
		return 500
	}
	return 500
}

func newErr(kind, format string, args ...any) *Error {
	return &Error{Kind: kind, Message: fmt.Sprintf(format, args...)}
}

func storageErr(err error) *Error {
	return &Error{Kind: KindStorage, Message: err.Error(), Cause: err}
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
