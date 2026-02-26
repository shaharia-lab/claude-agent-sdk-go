// interactive demonstrates bi-directional, multi-turn conversations using
// Session. Each line of stdin becomes a new turn; the agent's reply is printed
// before prompting for the next input.
//
// Run:
//
//	echo -e "My name is Alice\nWhat is my name?" | go run examples/interactive/main.go
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"

	"github.com/shaharia-lab/claude-agent-sdk-go/claude"
)

func main() {
	ctx := context.Background()

	session, err := claude.NewSession(ctx,
		claude.WithModel("claude-haiku-4-5-20251001"),
		claude.WithThinking(claude.ThinkingDisabled),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "session:", err)
		os.Exit(1)
	}
	defer session.Close()

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("You: ")
		if !scanner.Scan() {
			break
		}
		input := scanner.Text()
		if input == "" {
			continue
		}

		if err := session.Send(input); err != nil {
			fmt.Fprintln(os.Stderr, "send:", err)
			break
		}

		fmt.Print("Claude: ")
		for event := range session.Events() {
			switch event.Type {
			case claude.TypeAssistant:
				fmt.Print(event.Assistant.Text())
			case claude.TypeResult:
				fmt.Println()
				goto nextTurn
			case claude.TypeSystem:
				if event.System != nil && event.System.Subtype == "error" {
					fmt.Fprintln(os.Stderr, "error:", event.System.Message)
					os.Exit(1)
				}
			}
		}
		break // events channel closed unexpectedly
	nextTurn:
	}
}
