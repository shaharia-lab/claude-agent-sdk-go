# Claude Agent SDK for Go

> **Disclaimer:** This is an **unofficial, community-maintained** Go SDK inspired by the official
> [TypeScript](https://github.com/anthropics/claude-agent-sdk-typescript) and
> [Python](https://github.com/anthropics/claude-agent-sdk-python) Claude Agent SDKs published by Anthropic.
> This project is open source and is **not affiliated with, endorsed by, or associated with Anthropic in any form**.
> Anthropic does not provide support for this SDK, and the maintainers of this project cannot guarantee
> correctness, completeness, or ongoing compatibility with the official SDKs or the Claude API.
> **Use at your own risk.**

A Go SDK for the Claude Agent, following the design and conventions of the official
[TypeScript](https://github.com/anthropics/claude-agent-sdk-typescript) and
[Python](https://github.com/anthropics/claude-agent-sdk-python) SDKs.

## Installation

```bash
go get github.com/shaharia-lab/claude-agent-sdk-go@latest
```

**Prerequisites:**

- Go 1.24+
- Claude Code CLI installed: `curl -fsSL https://claude.ai/install.sh | bash`

## Quick Start

```go
package main

import (
    "context"
    "fmt"

    "github.com/shaharia-lab/claude-agent-sdk-go/claude"
)

func main() {
    result, err := claude.Run(
        context.Background(),
        "What is 2 + 2? Answer in one sentence.",
        claude.WithModel("claude-haiku-4-5-20251001"),
        claude.WithThinking(claude.ThinkingDisabled),
    )
    if err != nil {
        panic(err)
    }
    fmt.Println(result.Result)
}
```

## Usage

### Run() — simple one-shot queries

`Run()` blocks until the agent finishes and returns the final result. This is the
simplest way to query Claude when you don't need to process intermediate events.

```go
result, err := claude.Run(
    context.Background(),
    "Summarise this file in one paragraph.",
    claude.WithModel("claude-haiku-4-5-20251001"),
    claude.WithThinking(claude.ThinkingDisabled),
)
if err != nil {
    log.Fatal(err)
}
fmt.Println(result.Result)
fmt.Printf("cost: $%.6f | tokens in=%d out=%d\n",
    result.TotalCostUSD, result.Usage.InputTokens, result.Usage.OutputTokens)
```

### Query() — real-time streaming

`Query()` returns a `*Stream` whose `Events()` channel delivers events as they
arrive from the Claude process. Use it when you want to stream tokens, handle
tool calls, or process thinking deltas in real time.

```go
stream, err := claude.Query(
    context.Background(),
    "Explain how a binary search tree works.",
    claude.WithModel("claude-sonnet-4-6"),
    claude.WithThinking(claude.ThinkingAdaptive),
    claude.WithIncludePartialMessages(),
)
if err != nil {
    log.Fatal(err)
}

for event := range stream.Events() {
    switch event.Type {
    case claude.TypeStreamEvent:
        if event.StreamEvent.Event.Delta != nil {
            fmt.Print(event.StreamEvent.Event.Delta.Text)
        }
    case claude.TypeResult:
        fmt.Printf("\ncost: $%.6f\n", event.Result.TotalCostUSD)
    }
}
```

### Multi-turn sessions

Resume a previous conversation by passing the session ID returned in the result:

```go
// Turn 1
r1, err := claude.Run(ctx, "My name is Alice.", opts...)

// Turn 2 — resumes the same session
r2, err := claude.Run(ctx, "What is my name?",
    append(opts, claude.WithSessionID(r1.SessionID))...,
)
```

### Message types

Every line the CLI sends decodes into an `Event`. `Event.Type` is always set;
the matching typed field is non-nil for the types below, and anything else
carries `Event.Raw` alone.

| `Event.Type` | Field | What it carries |
| --- | --- | --- |
| `TypeAssistant` | `Assistant` | A complete assistant turn: content blocks, `Message.Model`, `Message.StopReason`, per-turn `Message.Usage` |
| `TypeUser` | `User` | The user side — usually the CLI delivering a tool's output as `tool_result` blocks, not something a human typed |
| `TypeStreamEvent` | `StreamEvent` | One incremental event of a streamed turn (needs `WithIncludePartialMessages()`) |
| `TypeResult` | `Result` | The final message: cost, cumulative `ModelUsages`, permission denials |
| `TypeSystem` | `System` | Session lifecycle. Task and hook events arrive here as **subtypes**; task subtypes also populate `Task` |
| `TypeToolProgress` | `ToolProgress` | Incremental progress from a running tool |
| `TypeRateLimitEvent` | — | `Raw` only |

A message's `Content` is a `[]ContentBlock` — one struct with a `Type`
discriminator covering the whole union, so unknown block types degrade to
`Type` + `Raw` rather than being dropped:

| `Block.Type` | Populated fields |
| --- | --- |
| `BlockText` | `Text` |
| `BlockThinking` | `Thinking`, `Signature` |
| `BlockToolUse`, `BlockServerToolUse` | `ID`, `Name`, `Input`, `Caller` |
| `BlockToolResult` and the server-side result blocks | `ToolUseID`, `Content`, `IsError` |

Pair a turn's `Assistant.ToolUses()` to the next `User.ToolResults()` by
`ToolUseID`:

```go
for ev := range stream.Events() {
    switch ev.Type {
    case claude.TypeAssistant:
        for _, use := range ev.Assistant.ToolUses() {
            fmt.Printf("calling %s(%s)\n", use.Name, use.Input)
        }
    case claude.TypeUser:
        for _, res := range ev.User.ToolResults() {
            text, _ := res.ContentText()   // Content is a string or a block array
            fmt.Printf("%s → %s (failed=%v)\n", res.ToolUseID, text, res.Failed())
        }
    }
}
```

> **`usage` vs `modelUsage`.** `Assistant.Message.Usage` is **per-turn** usage
> for one call of the main loop — it is *not* the cost-accounting field. Totals
> and cost live on the result, in `Result.ModelUsages` (wire key `modelUsage`)
> and `Result.TotalCostUSD`.

### Streaming a tool call

With `WithIncludePartialMessages()`, a tool call arrives in pieces: a
`content_block_start` announcing its name and id, then `input_json_delta`
fragments carrying the arguments. Fragments split at arbitrary points — even
mid-string — so they are valid JSON only once concatenated:

```go
inputs := map[int]*strings.Builder{}

e := ev.StreamEvent.Event
if block, ok := e.ContentBlockStart(); ok && block.Type == claude.BlockToolUse {
    inputs[e.Index] = &strings.Builder{}          // tool announced: block.Name, block.ID
}
if fragment, ok := e.PartialJSON(); ok {
    inputs[e.Index].WriteString(fragment)         // one slice of the arguments
}
if stopReason, usage, ok := e.MessageDelta(); ok {
    // end of the turn — usage rides the event, not the delta
}
```

`e.TextDelta()`, `e.ThinkingDelta()` and `e.SignatureDelta()` cover the other
increments, and `e.Raw` holds the whole event verbatim. The existing
`e.Delta.Text` path is unchanged. See `examples/streaming_tools/`.

### Reading events

Typed fields on an `Event` are **best-effort**; `Event.Raw` is authoritative.

```go
for ev := range stream.Events() {
    switch ev.Type {
    case claude.TypeAssistant:
        fmt.Println(ev.Assistant.Text())
    case claude.TypeSystem:
        // Task and hook lifecycle messages arrive here, as SUBTYPES —
        // there is no top-level "task_started" message on the wire.
        switch ev.System.Subtype {
        case claude.SubtypeInit:
            fmt.Println(ev.System.Plugins)
        case claude.SubtypeTaskStarted:
            fmt.Println(ev.Task.TaskID)
        }
    case claude.TypeResult:
        fmt.Println(ev.Result.Result, ev.Result.ModelUsages)
    }

    if ev.DecodeErr != nil {
        // One field decoded partially; the rest is still populated and Raw is
        // complete. Useful for spotting protocol drift.
        log.Printf("partial decode: %v", ev.DecodeErr)
    }
}
```

A single unexpected field degrades that field only — it never nils the whole
message. Per-model usage lives in `Result.ModelUsages` (wire key `modelUsage`,
camelCase fields), and server-side tool counters are under
`Result.Usage.ServerToolUse`.

### Reacting to how a run ended

`Result.Subtype` is not enough to branch on: an interrupted turn and an ordinary
execution failure both report `error_during_execution`. `TerminalReason` is what
separates them.

| Field | Use it to |
| --- | --- |
| `TerminalReason` | Tell *why* the loop stopped — `TerminalCompleted`, `TerminalMaxTurns`, `TerminalAbortedStreaming`/`TerminalAbortedTools` (cancelled), `TerminalAPIError`, `TerminalBudgetExhausted`, … |
| `TerminalReason.Aborted()` | Shortcut for "the caller cancelled this" |
| `APIErrorStatus` | Classify an upstream failure for retry: `429` back off, `529` retry, `500` retry once. `nil` when the CLI sent none |
| `DeferredToolUse` | Inspect the call a `PreToolUse` hook deferred, and decide whether to resume |
| `ModelUsages[m].CanonicalModel` / `.Provider` | Attribute cost to a model and provider (`ProviderFirstParty`, `ProviderBedrock`, …) |

```go
result, err := claude.Run(ctx, prompt, opts...)
if err != nil {
    // Run()'s error already names the subtype, terminal reason and HTTP status.
    return err
}

switch {
case result.TerminalReason.Aborted():
    log.Print("cancelled by the caller")
case result.APIErrorStatus != nil && *result.APIErrorStatus == 429:
    // back off and retry
case result.TerminalReason == claude.TerminalMaxTurns:
    // raise WithMaxTurns, or accept the partial answer
}
```

`TerminalReason` is a named string, not a closed enum — a reason this SDK does
not know yet decodes through unchanged rather than being dropped.

### What the connected CLI supports

The SDK completes an `initialize` handshake with the CLI before the first turn
starts, and caches the result. These accessors are **instant cache reads** — no
I/O, never block, safe to call concurrently:

```go
sess, err := claude.NewSession(ctx, opts...)   // returns once initialize is acknowledged
defer sess.Close()

for _, m := range sess.SupportedModels() {
    fmt.Printf("%s → %s (%s)\n", m.Value, m.ResolvedModel, m.DisplayName)
}
sess.SupportedCommands()   // []SlashCommand
sess.SupportedAgents()     // []AgentInfo
sess.AccountInfo()         // AccountInfo
sess.OutputStyle()         // style, available
```

**Feature detection** uses the capability list:

```go
if slices.Contains(sess.Capabilities(), "interrupt_receipt_v1") {
    // this CLI populates InterruptReceipt.StillQueued
}
```

⚠️ Capabilities are advertised on the `system`/`init` event, **not** in the
initialize handshake, so they arrive with the first turn. `Capabilities()`
returns `nil` until then — treat empty as *"not yet known"*, never as *"the CLI
supports nothing"*.

Because the handshake gates the first turn, MCP servers and agents are fully
configured before any message is sent. Slow MCP servers make it slower; the wait
is bounded by `WithInitTimeout` (default 60s, also settable via
`CLAUDE_CODE_STREAM_CLOSE_TIMEOUT` in milliseconds). If the CLI rejects or never
answers the handshake, `Query`/`Run`/`NewSession` return a typed
`*claude.InitializeError` and the subprocess is shut down.

### Stopping a turn vs ending a session

`Interrupt()` and `Close()` are different operations:

| | what it does | session after |
| --- | --- | --- |
| `Interrupt()` | aborts the turn in progress via an interrupt control request | **alive** — call `Send` for the next turn |
| `Close()` | closes stdin, SIGTERM, SIGKILL after 5s | terminated, `Events()` closed |

```go
sess, err := claude.NewSession(ctx, opts...)
defer sess.Close()

// ... a turn is running and the user hits "stop" ...
receipt, err := sess.Interrupt()   // aborts the turn, keeps the conversation
if receipt != nil {
    // async user messages that survived and are still queued
    fmt.Println(receipt.StillQueued)
}

// the session is still usable
if err := sess.Send("Never mind — summarise what you had so far."); err != nil {
    log.Fatal(err)
}
```

`receipt` is `nil` when the CLI does not send one (only CLIs advertising the
`interrupt_receipt_v1` capability do); that is not an error.

### Custom system prompt

```go
result, err := claude.Run(ctx, "Introduce yourself.",
    claude.WithSystemPrompt("You are a helpful assistant who always responds in formal English."),
    claude.WithModel("claude-haiku-4-5-20251001"),
    claude.WithThinking(claude.ThinkingDisabled),
)
```

### Restricting tools

```go
result, err := claude.Run(ctx, "List the Go files in the current directory.",
    claude.WithAllowedTools("Read", "Glob"),
    claude.WithModel("claude-haiku-4-5-20251001"),
    claude.WithThinking(claude.ThinkingDisabled),
)
```

### Approving tool calls in your own code

Register a permission handler and the CLI asks your code before running a tool:

```go
result, err := claude.Run(ctx, "Clean up the temporary files in /tmp.",
    claude.WithPermissionHandler(func(tool string, input json.RawMessage, pctx claude.PermissionContext) claude.PermissionResult {
        if tool == "Bash" {
            return claude.PermissionResult{Behavior: "deny", Message: "no shell commands"}
        }
        return claude.PermissionResult{Behavior: "allow"}
    }),
    claude.WithDefaultPermissions(),
)
```

`Behavior` is required and must be `"allow"` or `"deny"`. Three things are worth
knowing:

- **`WithDefaultPermissions()` is usually required.** The SDK currently defaults
  to `bypassPermissions`, which pre-approves everything, so the CLI never asks
  and your handler never runs. The SDK prints a warning when it detects this (or
  an `AllowedTools` list) shadowing a registered handler.
- **It fails closed.** If the CLI asks and no handler is registered, or the
  handler returns an empty `Behavior`, the SDK answers with an error rather than
  allowing the call.
- **It is mutually exclusive with `WithPermissionPromptToolName`.** A handler is
  served over `--permission-prompt-tool stdio`; setting both returns an error.

Denied calls are reported on the final result as `Result.PermissionDenials`.

### In-process MCP server (HTTP)

Register a Go MCP server directly in your process — no separate binary needed:

```go
server := mcp.NewServer(&mcp.Implementation{Name: "my-server", Version: "1.0.0"}, nil)
mcp.AddTool(server, &mcp.Tool{Name: "my_tool", Description: "..."}, myHandler)

mcpCfg, err := claude.StartInProcessMCPServer(ctx, "my-server", server)
if err != nil {
    log.Fatal(err)
}

result, err := claude.Run(ctx, "Use my_tool to ...",
    claude.WithMcpServers(map[string]any{"my-server": mcpCfg}),
)
```

### MCP server over stdio

Spawn an external binary as an MCP server over stdin/stdout:

```go
result, err := claude.Run(ctx, "...",
    claude.WithMcpServers(map[string]any{
        "my-server": claude.McpStdioServer{
            Type:    "stdio",
            Command: "/path/to/mcp-binary",
            Args:    []string{"--flag"},
        },
    }),
)
```

## Examples

The [`examples/`](examples/) directory contains fully working programs for each
feature:

| Example | Description |
|---------|-------------|
| [`examples/simple/`](examples/simple/) | One-shot `Run()` query |
| [`examples/stream/`](examples/stream/) | Real-time streaming with `Query()` |
| [`examples/session/`](examples/session/) | Multi-turn session resumption |
| [`examples/system_prompt/`](examples/system_prompt/) | Custom system prompt and restricted tools |
| [`examples/mcp/`](examples/mcp/) | In-process HTTP MCP server |
| [`examples/mcp_stdio/`](examples/mcp_stdio/) | Self-invoking stdio MCP server |

Run any example from the repository root:

```bash
go run examples/simple/main.go
go run examples/stream/main.go
go run examples/session/main.go
go run examples/system_prompt/main.go
go run examples/mcp/main.go
go run examples/mcp_stdio/main.go
```

## Reporting Bugs

We welcome your feedback. File a [GitHub issue](https://github.com/shaharia-lab/claude-agent-sdk-go/issues)
to:

- Report bugs or unexpected behavior
- Request new features
- Report incompatibilities with the official TypeScript or Python SDKs

Please include a minimal reproducible example when filing bug reports.

## License

This project is licensed under the [MIT License](LICENSE).
