package cmd

import "errors"

// ExitError wraps an error with a stable process exit code.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *ExitError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// ExitCode extracts the exit code from an error, defaulting to 1 for non-nil errors.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *ExitError
	if errors.As(err, &ee) && ee != nil {
		if ee.Code < 0 {
			return 1
		}
		return ee.Code
	}
	return 1
}

// Stable exit codes for automation.
const (
	ExitOK          = 0
	ExitGeneric     = 1
	ExitUsage       = 2
	ExitAuth        = 4
	ExitNotFound    = 5
	ExitRetryable   = 8
	ExitConfig      = 10
	ExitCancelled   = 130
)
