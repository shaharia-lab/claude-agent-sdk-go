package claude

import (
	"context"
	"os"
)

// Transport is the interface for communicating with the claude subprocess.
// This is a preparatory abstraction for future alternative transports (e.g.
// direct API, WebSocket). The default implementation uses a subprocess with
// JSON-lines over stdin/stdout (SubprocessTransport, not yet extracted).
type Transport interface {
	// Start launches the transport and begins the session.
	Start(ctx context.Context) error

	// Write sends a JSON-serialisable message to the subprocess.
	Write(msg any) error

	// Events returns a channel of events from the subprocess. The channel is
	// closed when the transport shuts down.
	Events() <-chan Event

	// Close initiates graceful shutdown of the transport.
	Close() error

	// Signal sends an OS signal to the underlying process (if applicable).
	Signal(sig os.Signal) error
}
