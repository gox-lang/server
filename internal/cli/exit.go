package cli

import "fmt"

// SilentExitError tells main to exit with Code without logging the error message.
type SilentExitError struct {
	Code int
}

func (e SilentExitError) Error() string {
	return fmt.Sprintf("silent exit %d", e.Code)
}

func SilentExit(code int) error {
	if code == 0 {
		code = 1
	}
	return SilentExitError{Code: code}
}
