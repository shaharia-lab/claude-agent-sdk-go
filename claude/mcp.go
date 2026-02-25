package claude

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// StartInProcessMCPServer starts an HTTP MCP server for the given mcp.Server and
// returns the McpHTTPServer config to pass to WithMcpServers.
//
// The HTTP listener is bound to a random local port on 127.0.0.1 and is stopped
// when ctx is cancelled. This is the clean Go equivalent of the TypeScript SDK's
// McpSdkServerConfig{type:'sdk'} — HTTP is the bridge between in-process Go code
// and the claude subprocess.
//
// Example:
//
//	mcpCfg, err := claude.StartInProcessMCPServer(ctx, "my-server", server)
//	if err != nil { ... }
//	result, err := claude.Run(ctx, prompt,
//	    claude.WithMcpServers(map[string]any{"my-server": mcpCfg}),
//	)
func StartInProcessMCPServer(ctx context.Context, name string, server *mcp.Server) (McpHTTPServer, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return McpHTTPServer{}, fmt.Errorf("claude: mcp %q: listen: %w", name, err)
	}

	handler := mcp.NewStreamableHTTPHandler(func(_ *http.Request) *mcp.Server {
		return server
	}, nil)

	httpServer := &http.Server{Handler: handler}
	go func() {
		if err := httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			// Errors after context cancellation are expected; ignore.
			_ = err
		}
	}()
	go func() {
		<-ctx.Done()
		_ = httpServer.Shutdown(context.Background())
	}()

	serverURL := "http://" + listener.Addr().String()
	return McpHTTPServer{Type: "http", URL: serverURL}, nil
}

// ServeStdioMCP runs server as an MCP stdio server, reading from os.Stdin and
// writing to os.Stdout. Intended for use in a standalone binary registered via
// McpStdioServer. Blocks until ctx is cancelled.
//
// The typical pattern is a self-invoking binary:
//
//	if slices.Contains(os.Args, "--mcp-server") {
//	    if err := claude.ServeStdioMCP(ctx, server); err != nil { ... }
//	    return
//	}
//	// Otherwise run as a normal claude client.
func ServeStdioMCP(ctx context.Context, server *mcp.Server) error {
	return server.Run(ctx, &mcp.StdioTransport{})
}

// SelfAsStdioMCPServer returns a McpStdioServer that runs the current binary
// with the given extra arguments. Useful for the self-invoking MCP stdio pattern.
//
// Example:
//
//	// In the server mode: os.Args contains "--mcp-server"
//	// In the client mode: pass SelfAsStdioMCPServer("--mcp-server") to WithMcpServers.
//	srv, err := claude.SelfAsStdioMCPServer("--mcp-server")
func SelfAsStdioMCPServer(extraArgs ...string) (McpStdioServer, error) {
	self, err := os.Executable()
	if err != nil {
		return McpStdioServer{}, fmt.Errorf("claude: resolve executable: %w", err)
	}
	return McpStdioServer{
		Type:    "stdio",
		Command: self,
		Args:    extraArgs,
	}, nil
}
