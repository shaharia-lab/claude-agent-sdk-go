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
