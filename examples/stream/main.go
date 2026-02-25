// stream demonstrates the Query() API with real-time event handling.
// Thinking deltas go to stderr; text tokens stream to stdout as they arrive.
// Run: go run examples/stream/main.go ["your question"]
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/shaharia-lab/claude-agent-sdk-go/claude"
)

func main() {
	prompt := "Explain how a binary search tree works. Be concise."
	if len(os.Args) > 1 {
		prompt = os.Args[1]
	}

	stream, err := claude.Query(
		context.Background(),
		prompt,
		claude.WithModel("claude-sonnet-4-6"),
		claude.WithThinking(claude.ThinkingAdaptive),
		claude.WithIncludePartialMessages(),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	for event := range stream.Events() {
		switch event.Type {

		case claude.TypeSystem:
			s := event.System
			if s == nil {
				continue
			}
			switch s.Subtype {
			case claude.SubtypeInit:
				fmt.Fprintf(os.Stderr, "[init] model=%s session=%s\n", s.Model, s.SessionID)
			case "error":
				fmt.Fprintf(os.Stderr, "[error] %s\n", s.Message)
				os.Exit(1)
			}

		case claude.TypeStreamEvent:
			e := event.StreamEvent.Event
			if e.Delta == nil {
				continue
			}
			switch e.Delta.Type {
			case "thinking_delta":
				fmt.Fprint(os.Stderr, e.Delta.Thinking)
			case "text_delta":
				fmt.Print(e.Delta.Text)
			}

		case claude.TypeAssistant:
			// When IncludePartialMessages is on, text arrives via stream_event deltas
			// above. This block catches any text not yet printed (e.g. tool responses).
			if thinking := event.Assistant.Thinking(); thinking != "" {
				fmt.Fprintf(os.Stderr, "\n[thinking]\n%s\n", thinking)
			}

		case claude.TypeRateLimitEvent:
			// Informational only — log raw JSON for transparency.
			raw, _ := json.Marshal(event.Raw)
			fmt.Fprintf(os.Stderr, "[rate_limit] %s\n", raw)

		case claude.TypeResult:
			r := event.Result
			if r.IsError {
				fmt.Fprintf(os.Stderr, "\nerror: %s\n", r.Subtype)
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "\n\nsession:  %s\n", r.SessionID)
			fmt.Fprintf(os.Stderr, "cost:     $%.6f\n", r.TotalCostUSD)
			fmt.Fprintf(os.Stderr, "tokens:   in=%d out=%d cache_read=%d\n",
				r.Usage.InputTokens, r.Usage.OutputTokens, r.Usage.CacheReadInputTokens)
			fmt.Fprintf(os.Stderr, "turns:    %d\n", r.NumTurns)
		}
	}
}
