package claude

import (
	"context"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolDef wraps a tool definition and its handler so that multiple tools can be
// registered on a server without requiring callers to interact with the MCP SDK
// directly. Create ToolDefs with NewTool and pass them to ToolServer or WithTools.
type ToolDef struct {
	// addFunc registers this tool on the given server.
	addFunc func(s *mcp.Server)
}

// NewTool creates a ToolDef from a name, description, and strongly-typed handler.
// The generic parameter T is the input type; the handler signature matches the
// mcp.ToolHandlerFor pattern used by mcp.AddTool.
//
// Example:
//
//	type AddInput struct {
//	    A int `json:"a"`
//	    B int `json:"b"`
//	}
//	tool := claude.NewTool[AddInput, any]("add", "Add two numbers",
//	    func(ctx context.Context, req *mcp.CallToolRequest, input AddInput) (*mcp.CallToolResult, any, error) {
//	        sum := input.A + input.B
//	        return &mcp.CallToolResult{
//	            Content: []mcp.Content{{Type: "text", Text: ptr(fmt.Sprintf("%d", sum))}},
//	        }, nil, nil
//	    },
//	)
func NewTool[In, Out any](name, description string, handler mcp.ToolHandlerFor[In, Out]) ToolDef {
	return ToolDef{
		addFunc: func(s *mcp.Server) {
			mcp.AddTool(s, &mcp.Tool{
				Name:        name,
				Description: description,
			}, handler)
		},
	}
}

// ToolServer creates an in-process MCP server from a set of ToolDefs and starts
// it on a random local port. Returns an McpHTTPServer config ready to pass to
// WithMcpServers. The server is stopped when ctx is cancelled.
func ToolServer(ctx context.Context, name string, tools ...ToolDef) (McpHTTPServer, error) {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    name,
		Version: SDKVersion,
	}, nil)
	for _, t := range tools {
		t.addFunc(server)
	}
	return StartInProcessMCPServer(ctx, name, server)
}

// WithTools is a convenience Option that creates an in-process MCP server from
// the given ToolDefs, starts it, and merges it into the McpServers map under
// the given name. The server is stopped when the Query/Session context ends.
//
// This is the simplest way to expose Go functions as tools to Claude:
//
//	stream, err := claude.Query(ctx, "Add 2+3",
//	    claude.WithTools(ctx, "my-tools", addTool, multiplyTool),
//	)
func WithTools(ctx context.Context, name string, tools ...ToolDef) Option {
	return func(o *Options) {
		cfg, err := ToolServer(ctx, name, tools...)
		if err != nil {
			// If we fail to start the server, skip silently. The caller
			// will notice because the tools won't be available.
			return
		}
		if o.McpServers == nil {
			o.McpServers = make(map[string]any)
		}
		o.McpServers[name] = cfg
	}
}
