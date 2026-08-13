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

// InitializeError reports a failed initialize handshake: the CLI rejected the
// initialize control request, or never acknowledged it.
//
// It is returned by Query, Run and NewSession, and means the session never
// started — the subprocess has been shut down. A rejection usually points at
// something in the options the CLI could not accept (an invalid agent
// definition, an unusable MCP server config); Timeout means the CLI was still
// starting up, which MCP servers can make slow.
type InitializeError struct {
	// Message is the CLI's own error text, or a description of the timeout.
	Message string
	// Timeout reports whether the handshake timed out rather than being rejected.
	Timeout bool
}

func (e *InitializeError) Error() string {
	if e.Timeout {
		return "claude: initialize timed out: " + e.Message
	}
	return "claude: initialize rejected by the CLI: " + e.Message
}
