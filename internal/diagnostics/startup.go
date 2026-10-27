package diagnostics

import (
	"fmt"
	"strings"
)

// StartupError keeps the underlying error available to errors.Is/errors.As.
// Recovery steps are selected by the caller's actual stage, not guessed from
// arbitrary provider error text, and are never executed automatically.
type StartupError struct {
	Stage string
	Cause error
	Next  []string
}

func (e *StartupError) Error() string {
	var out strings.Builder
	fmt.Fprintf(&out, "Startup failed [%s]: %v", e.Stage, e.Cause)
	for _, next := range e.Next {
		fmt.Fprintf(&out, "\n  Next: %s", next)
	}
	return out.String()
}

func (e *StartupError) Unwrap() error { return e.Cause }
