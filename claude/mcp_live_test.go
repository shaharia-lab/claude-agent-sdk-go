//go:build live

// Live smoke tests run against a real `claude` process and are excluded from
// the default build. Run them with:
//
//	go test -tags live -run TestLive ./claude/
//
// They require the `claude` CLI to be installed, on PATH, and authenticated.
package claude

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type echoMagicParams struct {
	Token string `json:"token" jsonschema:"the token to echo back"`
}

// An in-process MCP server and a hook must both work in the SAME session.
//
// Before #38 they could not: initializeMsg sent sdkMcpServers as an object, the
// CLI rejected the entire initialize ("must be arrays of strings"), and hooks —
// which travel in that same message — were silently dead. The MCP tool still
// worked, because it reaches the CLI over --mcp-config rather than initialize,
// so the breakage was invisible unless you asserted on both at once. That is
// exactly what this test does.
func TestLiveMcpServerAndHookCoexist(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude CLI not on PATH")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	var (
		mu         sync.Mutex
		toolTokens []string
		hookEvents []HookEvent
	)

	server := mcp.NewServer(&mcp.Implementation{Name: "probe-server", Version: "1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "echo_magic",
		Description: "Echoes a token back. Always use this tool when asked to echo a token.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, p *echoMagicParams) (*mcp.CallToolResult, any, error) {
		mu.Lock()
		toolTokens = append(toolTokens, p.Token)
		mu.Unlock()
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "MAGIC-OK-" + p.Token}},
		}, nil, nil
	})

	cfg, err := StartInProcessMCPServer(ctx, "probe-server", server)
	if err != nil {
		t.Fatalf("start in-process MCP server: %v", err)
	}

	hooks := map[HookEvent][]HookMatcher{
		HookEventPreToolUse: {{
			Hooks: []HookFunc{
				func(event HookEvent, _ json.RawMessage, _ string) (*HookOutput, error) {
					mu.Lock()
					hookEvents = append(hookEvents, event)
					mu.Unlock()
					return nil, nil
				},
			},
		}},
	}

	result, err := Run(ctx,
		"Call the echo_magic tool with token=ZQ7. Then reply with exactly what it returned.",
		WithModel("claude-haiku-4-5-20251001"),
		WithThinking(ThinkingDisabled),
		WithMcpServers(map[string]any{"probe-server": cfg}),
		WithHooks(hooks),
	)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	// The MCP tool must actually have been invoked in-process.
	if len(toolTokens) == 0 {
		t.Errorf("in-process MCP tool was never invoked; result was %q", result.Result)
	} else if toolTokens[0] != "ZQ7" {
		t.Errorf("tool got token %q, want ZQ7", toolTokens[0])
	}

	// The tool's return value must have reached the model.
	if !strings.Contains(result.Result, "MAGIC-OK-ZQ7") {
		t.Errorf("result does not carry the tool output: %q", result.Result)
	}

	// And the hook must have fired in the same session — the half that #38 broke.
	if len(hookEvents) == 0 {
		t.Error("no hook fired; initialize was likely rejected (the #38 regression)")
	}
	for _, ev := range hookEvents {
		if ev != HookEventPreToolUse {
			t.Errorf("hook got event %q, want %q", ev, HookEventPreToolUse)
		}
	}
}
