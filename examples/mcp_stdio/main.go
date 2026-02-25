// mcp_stdio demonstrates the self-invoking binary pattern for MCP stdio servers.
//
// When run normally, this binary resolves itself via os.Executable() and
// registers itself as a McpStdioServer — claude spawns a copy of this binary
// with --mcp-server to handle MCP tool calls over stdio.
//
// When run with --mcp-server, the binary acts as the MCP stdio server itself.
//
// Run: go run examples/mcp_stdio/main.go
// (The binary re-invokes itself as the MCP server automatically.)
package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"time"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shaharia-lab/claude-agent-sdk-go/claude"
)

// CurrentTimeParams is the input schema for the current_time tool.
type CurrentTimeParams struct {
	Timezone string `json:"timezone"`
}

// getCurrentTime returns the current time in the requested timezone.
func getCurrentTime(_ context.Context, _ *mcp.CallToolRequest, params *CurrentTimeParams) (*mcp.CallToolResult, any, error) {
	tz := params.Timezone
	if tz == "" {
		tz = "UTC"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, nil, fmt.Errorf("unknown timezone %q: %w", tz, err)
	}
	now := time.Now().In(loc).Format(time.RFC1123)
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Current time in %s: %s", tz, now)},
		},
	}, nil, nil
}

// buildServer constructs the shared MCP server definition.
func buildServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "time-server-stdio",
		Version: "1.0.0",
	}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "current_time",
		Description: "Returns the current date and time for a given IANA timezone.",
	}, getCurrentTime)
	return server
}

func main() {
	ctx := context.Background()

	// ── MCP server mode ────────────────────────────────────────────────────────
	// When the binary is invoked with --mcp-server, act as the MCP stdio server.
	if slices.Contains(os.Args[1:], "--mcp-server") {
		server := buildServer()
		if err := claude.ServeStdioMCP(ctx, server); err != nil {
			fmt.Fprintf(os.Stderr, "mcp server error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// ── Claude client mode ─────────────────────────────────────────────────────
	// Resolve the current binary so claude can spawn it as an MCP stdio server.
	stdioSrv, err := claude.SelfAsStdioMCPServer("--mcp-server")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error resolving self: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Registering self (%s) as MCP stdio server\n", stdioSrv.Command)

	result, err := claude.Run(ctx,
		"Use the current_time tool to tell me the current time in Tokyo (Asia/Tokyo).",
		claude.WithModel("claude-haiku-4-5-20251001"),
		claude.WithThinking(claude.ThinkingDisabled),
		claude.WithMcpServers(map[string]any{
			"time-server-stdio": stdioSrv,
		}),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(result.Result)
	fmt.Fprintf(os.Stderr, "\nsession: %s\ncost: $%.6f | tokens in=%d out=%d\n",
		result.SessionID, result.TotalCostUSD, result.Usage.InputTokens, result.Usage.OutputTokens)
}
