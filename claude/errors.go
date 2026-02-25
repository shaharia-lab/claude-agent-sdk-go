package claude

import "fmt"

// CLINotFoundError is returned when the claude binary cannot be found or executed.
type CLINotFoundError struct {
	ExecutablePath string
}

func (e *CLINotFoundError) Error() string {
	return fmt.Sprintf("claude: binary not found: %q", e.ExecutablePath)
}

// ProcessError is returned when the claude subprocess exits with a non-zero status.
type ProcessError struct {
	ExitCode int
	Stderr   string
	Message  string
}

func (e *ProcessError) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("claude: process error (exit %d): %s", e.ExitCode, e.Stderr)
	}
	return fmt.Sprintf("claude: process error (exit %d): %s", e.ExitCode, e.Message)
}

// CLIJSONDecodeError is returned when a JSON line from the claude process cannot be decoded.
type CLIJSONDecodeError struct {
	Line []byte
	Err  error
}

func (e *CLIJSONDecodeError) Error() string {
	return fmt.Sprintf("claude: JSON decode error: %v (line: %s)", e.Err, e.Line)
}

func (e *CLIJSONDecodeError) Unwrap() error { return e.Err }
