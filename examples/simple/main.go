// simple demonstrates the one-call Run() API — ask a question, get an answer.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/shaharia-lab/claude-agent-sdk-go/claude"
)

func main() {
	prompt := "What is 2+2? Answer in one sentence."
	if len(os.Args) > 1 {
		prompt = os.Args[1]
	}

	result, err := claude.Run(
		context.Background(),
		prompt,
		claude.WithModel("claude-haiku-4-5-20251001"),
		claude.WithThinking(claude.ThinkingDisabled),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(result.Result)
	fmt.Fprintf(os.Stderr, "\nsession: %s\n", result.SessionID)
	fmt.Fprintf(os.Stderr, "cost:    $%.6f\n", result.TotalCostUSD)
	fmt.Fprintf(os.Stderr, "tokens:  in=%d out=%d\n", result.Usage.InputTokens, result.Usage.OutputTokens)
}
