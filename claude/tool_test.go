package claude

import (
	"context"
	"testing"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestNewTool_Compiles verifies that NewTool compiles with the MCP SDK's
// generic AddTool signature and produces a valid ToolDef.
func TestNewTool_Compiles(t *testing.T) {
	type Input struct {
		X int `json:"x"`
	}

	td := NewTool[Input, any]("test-tool", "A test tool",
		func(ctx context.Context, req *mcp.CallToolRequest, input Input) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{}, nil, nil
		},
	)

	if td.addFunc == nil {
		t.Fatal("expected addFunc to be non-nil")
	}
}

// TestToolServer creates a tool server and verifies it returns a valid config.
func TestToolServer(t *testing.T) {
	type Input struct {
		A int `json:"a"`
	}

	tool := NewTool[Input, any]("add", "Add tool",
		func(ctx context.Context, req *mcp.CallToolRequest, input Input) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{}, nil, nil
		},
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg, err := ToolServer(ctx, "test-server", tool)
	if err != nil {
		t.Fatalf("ToolServer: %v", err)
	}
	if cfg.Type != "http" {
		t.Fatalf("expected type 'http', got %q", cfg.Type)
	}
	if cfg.URL == "" {
		t.Fatal("expected non-empty URL")
	}
}

// TestWithTools returns an option and error.
func TestWithTools(t *testing.T) {
	type Input struct {
		A int `json:"a"`
	}

	tool := NewTool[Input, any]("add", "Add tool",
		func(ctx context.Context, req *mcp.CallToolRequest, input Input) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{}, nil, nil
		},
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	opt, err := WithTools(ctx, "my-tools", tool)
	if err != nil {
		t.Fatalf("WithTools: %v", err)
	}
	if opt == nil {
		t.Fatal("expected non-nil option")
	}

	// Apply the option and verify McpServers is populated.
	opts := defaultOptions()
	opt(opts)
	if opts.McpServers == nil {
		t.Fatal("expected McpServers to be non-nil")
	}
	if _, ok := opts.McpServers["my-tools"]; !ok {
		t.Fatal("expected 'my-tools' key in McpServers")
	}
}
