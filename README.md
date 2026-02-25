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

## Reporting Bugs

We welcome your feedback. File a [GitHub issue](https://github.com/shaharia-lab/claude-agent-sdk-go/issues)
to:

- Report bugs or unexpected behavior
- Request new features
- Report incompatibilities with the official TypeScript or Python SDKs

Please include a minimal reproducible example when filing bug reports.

## License

This project is licensed under the [MIT License](LICENSE).
