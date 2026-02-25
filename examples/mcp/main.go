// mcp demonstrates passing an in-process MCP server to claude via StartInProcessMCPServer.
//
// The example starts a local HTTP MCP server (using github.com/modelcontextprotocol/go-sdk)
// that exposes a "current_time" tool, then launches claude with the server's URL
// so claude can call the tool to answer time-related questions.
//
// Run: go run examples/mcp/main.go
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shaharia-lab/claude-agent-sdk-go/claude"
)

// CurrentTimeParams is the input schema for the current_time tool.
type CurrentTimeParams struct {
	Timezone string `json:"timezone" jsonschema:"IANA timezone name, e.g. UTC or America/New_York. Defaults to UTC."`
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

func main() {
	ctx := context.Background()

	// ── 1. Build the MCP server ────────────────────────────────────────────────
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "time-server",
		Version: "1.0.0",
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "current_time",
		Description: "Returns the current date and time for a given IANA timezone (e.g. UTC, America/New_York, Asia/Tokyo). Defaults to UTC.",
	}, getCurrentTime)

	// ── 2. Start the MCP server on a random local port ─────────────────────────
	// StartInProcessMCPServer handles the HTTP listener lifecycle tied to ctx.
	mcpCfg, err := claude.StartInProcessMCPServer(ctx, "time-server", server)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start MCP server: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "MCP server listening at %s\n", mcpCfg.URL)

	// ── 3. Ask claude to use the tool ─────────────────────────────────────────
	// MCP servers in bidirectional mode are passed via sdkMcpServers in the
	// initialize message (not via --mcp-config CLI flag).
	result, err := claude.Run(ctx,
		"Use the current_time tool to tell me the current time in Tokyo (Asia/Tokyo) and New York (America/New_York).",
		claude.WithModel("claude-haiku-4-5-20251001"),
		claude.WithThinking(claude.ThinkingDisabled),
		claude.WithMcpServers(map[string]any{
			"time-server": mcpCfg,
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
