// system_prompt demonstrates WithSystemPrompt to give Claude a custom persona,
// and WithAllowedTools / WithDisallowedTools to restrict which tools it may use.
// Run: go run examples/system_prompt/main.go
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/shaharia-lab/claude-agent-sdk-go/claude"
)

func main() {
	ctx := context.Background()

	// ── Custom system prompt ───────────────────────────────────────────────────
	fmt.Println("=== Custom system prompt ===")
	r1, err := claude.Run(ctx, "Introduce yourself in one sentence.",
		claude.WithModel("claude-haiku-4-5-20251001"),
		claude.WithThinking(claude.ThinkingDisabled),
		claude.WithSystemPrompt("You are a helpful assistant who always responds in the style of a 1920s telegram. Use STOP instead of periods. Keep it brief."),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(r1.Result)

	// ── Restricted tools ───────────────────────────────────────────────────────
	fmt.Println("\n=== Restricted tools (Read + Glob only) ===")
	r2, err := claude.Run(ctx, "List the Go files in the current directory.",
		claude.WithModel("claude-haiku-4-5-20251001"),
		claude.WithThinking(claude.ThinkingDisabled),
		claude.WithAllowedTools("Read", "Glob"),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(r2.Result)
	fmt.Fprintf(os.Stderr, "\ncost: $%.6f | tokens in=%d out=%d\n",
		r2.TotalCostUSD, r2.Usage.InputTokens, r2.Usage.OutputTokens)
}
