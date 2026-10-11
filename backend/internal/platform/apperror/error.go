// Package apperror defines errors at the application boundary without HTTP or SQL dependencies.
package apperror

import "errors"

type Kind string

const (
	Validation      Kind = "validation"
	NotFound        Kind = "not_found"
	Conflict        Kind = "conflict"
	Unauthenticated Kind = "unauthenticated"
	Unavailable     Kind = "unavailable"
	Internal        Kind = "internal"
)

type Error struct {
	Kind    Kind
	Message string
	Cause   error
}

func New(kind Kind, message string) *Error { return &Error{Kind: kind, Message: message} }
func Wrap(kind Kind, message string, cause error) *Error {
	return &Error{Kind: kind, Message: message, Cause: cause}
}
func (e *Error) Error() string   { return e.Message }
func (e *Error) Unwrap() error   { return e.Cause }
func (e *Error) ErrorKind() Kind { return e.Kind }

func KindOf(err error) Kind {
	var typed interface{ ErrorKind() Kind }
	if errors.As(err, &typed) {
		return typed.ErrorKind()
	}
	return Internal
}

// PublicMessage returns an explicitly classified client error. Internal causes never become API messages.
func PublicMessage(err error, fallback string) string {
	switch KindOf(err) {
	case Validation, NotFound, Conflict, Unauthenticated:
		var typed *Error
		if errors.As(err, &typed) {
			return typed.Message
		}
	}
	return fallback
}
