package apperrors

import (
	"errors"
	"fmt"
)

type Kind string

const (
	KindValidation   Kind = "validation"
	KindUnauthorized Kind = "unauthorized"
	KindNotFound     Kind = "not_found"
	KindConflict     Kind = "conflict"
	KindRateLimited  Kind = "rate_limited"
	KindUnavailable  Kind = "unavailable"
	KindInternal     Kind = "internal"
)

type Error struct {
	Kind    Kind
	Message string
	Cause   error
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return string(e.Kind)
}

func (e *Error) Unwrap() error { return e.Cause }

func Validation(message string) error { return &Error{Kind: KindValidation, Message: message} }
func Unauthorized(message string) error {
	return &Error{Kind: KindUnauthorized, Message: message}
}
func NotFound(message string) error { return &Error{Kind: KindNotFound, Message: message} }
func Conflict(message string) error { return &Error{Kind: KindConflict, Message: message} }
func RateLimited(message string) error {
	return &Error{Kind: KindRateLimited, Message: message}
}
func Unavailable(message string, cause error) error {
	return &Error{Kind: KindUnavailable, Message: message, Cause: cause}
}
func Internal(cause error) error {
	if cause == nil {
		cause = errors.New("unknown internal error")
	}
	return &Error{Kind: KindInternal, Message: "internal server error", Cause: cause}
}
func Internalf(format string, args ...any) error { return Internal(fmt.Errorf(format, args...)) }

func KindOf(err error) Kind {
	var appErr *Error
	if errors.As(err, &appErr) {
		return appErr.Kind
	}
	return KindInternal
}

func PublicMessage(err error) string {
	var appErr *Error
	if errors.As(err, &appErr) && appErr.Message != "" {
		return appErr.Message
	}
	return "internal server error"
}
