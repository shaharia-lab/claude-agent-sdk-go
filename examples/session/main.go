// session demonstrates multi-turn conversations using session resumption.
// Turn 1 establishes context; Turn 2 resumes the same session using the
// session ID returned in the Result message.
// Run: go run examples/session/main.go
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/shaharia-lab/claude-agent-sdk-go/claude"
)

func main() {
	ctx := context.Background()
	opts := []claude.Option{
		claude.WithModel("claude-haiku-4-5-20251001"),
		claude.WithThinking(claude.ThinkingDisabled),
	}

	// ── Turn 1: establish context ──────────────────────────────────────────────
	fmt.Println("=== Turn 1 ===")
	r1, err := claude.Run(ctx, "My name is Shaharia and I am building a Go SDK. Just say 'Got it!'", opts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "turn 1 error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(r1.Result)
	fmt.Fprintf(os.Stderr, "session: %s\n\n", r1.SessionID)

	// ── Turn 2: resume the session, Claude should remember the context ─────────
	fmt.Println("=== Turn 2 (resumed session) ===")
	r2, err := claude.Run(ctx, "What is my name and what am I building?",
		append(opts, claude.WithSessionID(r1.SessionID))...,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "turn 2 error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(r2.Result)
	fmt.Fprintf(os.Stderr, "session: %s\n", r2.SessionID)
}
