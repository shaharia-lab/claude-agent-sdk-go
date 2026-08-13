// streaming_tools demonstrates watching a tool call assemble itself, token by
// token, and pairing the result back to the call that asked for it.
//
// A streamed tool call arrives in three parts: a content_block_start announcing
// the tool's name and id, a run of input_json_delta fragments carrying its
// arguments, and — after the tool runs — a user turn holding the tool_result.
// The fragments split at arbitrary points, so the arguments are only valid JSON
// once the whole block has been concatenated.
//
// Run: go run examples/streaming_tools/main.go ["your question"]
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/shaharia-lab/claude-agent-sdk-go/claude"
)

// pendingTool is a tool call still assembling from the stream.
type pendingTool struct {
	name  string
	id    string
	input strings.Builder
}

func main() {
	prompt := "List the files in the current directory, then say how many there are."
	if len(os.Args) > 1 {
		prompt = os.Args[1]
	}

	stream, err := claude.Query(
		context.Background(),
		prompt,
		claude.WithIncludePartialMessages(),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Blocks are identified by their index within the current turn.
	pending := map[int]*pendingTool{}

	for event := range stream.Events() {
		switch event.Type {

		case claude.TypeStreamEvent:
			e := event.StreamEvent.Event

			// A tool call announces itself before any of its input arrives.
			if block, ok := e.ContentBlockStart(); ok && block.Type == claude.BlockToolUse {
				pending[e.Index] = &pendingTool{name: block.Name, id: block.ID}
				fmt.Printf("\n→ %s (%s) assembling", block.Name, block.ID)
			}

			// Each fragment is a slice of the tool's arguments — never valid JSON
			// on its own.
			if fragment, ok := e.PartialJSON(); ok {
				if tool, live := pending[e.Index]; live {
					tool.input.WriteString(fragment)
					fmt.Print(".")
				}
			}

			// The block is complete: the accumulated fragments now parse.
			if e.Type == claude.StreamContentBlockStop {
				if tool, live := pending[e.Index]; live {
					fmt.Printf("\n  input: %s\n", compact(tool.input.String()))
				}
			}

			if text, ok := e.TextDelta(); ok {
				fmt.Print(text)
			}

			// The tail of the turn reports why it stopped.
			if stopReason, _, ok := e.MessageDelta(); ok && stopReason != "" {
				fmt.Fprintf(os.Stderr, "\n[stop_reason] %s\n", stopReason)
			}

		case claude.TypeAssistant:
			// The complete turn carries the same tool calls, fully assembled —
			// useful as a cross-check against what the stream reported.
			for _, use := range event.Assistant.ToolUses() {
				fmt.Fprintf(os.Stderr, "[complete] %s %s %s\n", use.Name, use.ID, use.Input)
			}

		case claude.TypeUser:
			// Tool output comes back as a user turn. Before #27 this was invisible
			// to typed consumers — there was no case for it at all.
			for _, result := range event.User.ToolResults() {
				status := "ok"
				if result.Failed() {
					status = "error"
				}
				text, _ := result.ContentText()
				fmt.Fprintf(os.Stderr, "[result %s] %s → %s\n",
					status, result.ToolUseID, truncate(text, 120))
			}

		case claude.TypeResult:
			fmt.Fprintf(os.Stderr, "\ncost: $%.6f · turns: %d\n",
				event.Result.TotalCostUSD, event.Result.NumTurns)
		}
	}
}

// compact re-encodes the assembled fragments, proving they parse as JSON.
func compact(s string) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(s)); err != nil {
		return fmt.Sprintf("%q (incomplete: %v)", s, err)
	}
	return buf.String()
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
