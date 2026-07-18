package domain

import "fmt"

// Code is a stable, machine-readable error code.
type Code string

const (
	ErrInvalidSpec      Code = "INVALID_SPEC"
	ErrProviderDisabled Code = "PROVIDER_DISABLED"
	ErrPoolExhausted    Code = "POOL_EXHAUSTED"
	ErrSandboxNotFound  Code = "SANDBOX_NOT_FOUND"
	ErrExecuteFailed    Code = "EXECUTE_FAILED"
	ErrInternal         Code = "INTERNAL"
)

// SandboxError is the domain error type with an HTTP status mapping.
type SandboxError struct {
	Code       Code
	HTTPStatus int
	Msg        string
}

func (e *SandboxError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Msg) }

// NewError builds a SandboxError.
func NewError(code Code, httpStatus int, msg string) *SandboxError {
	return &SandboxError{Code: code, HTTPStatus: httpStatus, Msg: msg}
}
