package prepare

import (
	"errors"
	"fmt"
)

// ErrorKind describes a preparation failure independently of its transport.
type ErrorKind uint8

const (
	KindInternal ErrorKind = iota
	KindInvalidInput
	KindNotFound
	KindConflict
	KindUnavailable
)

type Error struct {
	Kind    ErrorKind `json:"-"`
	Code    string    `json:"code"`
	Message string    `json:"error"`
}

func (e *Error) Error() string { return e.Message }

func problem(kind ErrorKind, code, format string, args ...any) error {
	return &Error{Kind: kind, Code: code, Message: fmt.Sprintf(format, args...)}
}

func failure(err error) *Failure {
	var typed *Error
	if errors.As(err, &typed) {
		retryable := typed.Kind == KindInternal || typed.Kind == KindUnavailable || typed.Code == "download_failed" || typed.Code == "activation_failed"
		return &Failure{Code: typed.Code, Message: typed.Message, Retryable: retryable}
	}
	return &Failure{Code: "preparation_failed", Message: err.Error(), Retryable: true}
}
