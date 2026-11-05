// Package operation provides shared operation errors and hooks.
package operation

import (
	"context"
	"errors"
	"strings"
)

// SafeError returns an error message safe to expose.
func SafeError(err error) string {
	if err == nil {
		return ""
	}

	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}

	s := err.Error()
	if len(s) > 300 {
		s = s[:300]
	}

	return strings.ReplaceAll(s, "\n", " ")
}
