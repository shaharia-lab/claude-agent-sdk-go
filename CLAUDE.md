# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

Unofficial, community-maintained Go SDK for the Claude Agent (module `github.com/shaharia-lab/claude-agent-sdk-go`). It mirrors the design and feature set of Anthropic's official Agent SDKs, which are the **source of truth** for this project:

- TypeScript: https://github.com/anthropics/claude-agent-sdk-typescript
- Python: https://github.com/anthropics/claude-agent-sdk-python

The development cycle is: periodically run a gap analysis against the official SDKs (see "Gap analysis" below, results in `GAP_ANALYSIS.md`), implement the missing features, then cut a new Go SDK release. When designing any API, check how the official SDKs (TypeScript first) name and shape it, and follow that.

All library code lives in the single `claude/` package; `examples/` contains runnable programs per feature.

## Commands

```bash
go build ./...                      # build
go test ./...                       # all tests
go test -run TestName ./claude/     # single test
go test -v -race -count=1 ./...     # what CI runs
gofmt -l .                          # CI fails on unformatted files
go vet ./...
go mod tidy                         # CI fails if go.mod/go.sum change
golangci-lint run ./...
go run examples/simple/main.go      # run an example (needs claude CLI + auth)
```

Tests do not spawn the real CLI; examples do (requires `claude` installed and authenticated).

## Architecture

The SDK does not call the Claude API directly — it spawns the `claude` CLI as a subprocess in bidirectional JSON-lines mode (`--input-format stream-json --output-format stream-json --verbose`, no `--print`), the same protocol used by the official SDKs.

- `options.go` — `Options` + all `WithX` functional options. Configuration splits two ways: some options become CLI flags (`buildArgs()`), while system prompt, MCP servers, agents, and hooks are sent in the `initialize` control message over stdin (`initializeMsg`) so they work in bidirectional mode. When adding an option, pick the right channel by checking how the TS SDK sends it.
- `process.go` — `spawnAndStream()`: subprocess lifecycle, stdout JSON-line parsing into `Event`s, stderr capture, and the control-protocol read loop (routes `control_response` to pending requests, dispatches hook/permission/elicitation callbacks). Graceful shutdown: close stdin → SIGTERM → SIGKILL after 5s.
- `client.go` — public entry points: `Run()` (blocking, returns final `*Result`), `Query()` (returns `*Stream` with an `Events()` channel), plus `Stream` control methods (`SetModel`, `Interrupt`, `SupportedModels`, …) implemented as `control_request`/`control_response` pairs correlated by `request_id`.
- `session.go` / `sessions.go` — `NewSession()` for persistent multi-turn conversations (wraps a Stream in session mode); `ListSessions`/`GetSessionMessages` for stored transcripts.
- `messages.go` — event/message types (`TypeAssistant`, `TypeResult`, `TypeSystem`, `TypeStreamEvent`, …) and JSON decoding.
- `hooks.go` — Go hook callbacks (`WithHooks`): hook config is declared in the initialize message with generated callback IDs; the read loop invokes the registered `HookFunc` when the CLI calls back.
- `mcp.go` / `tool.go` — in-process MCP servers via `modelcontextprotocol/go-sdk`: `StartInProcessMCPServer` (HTTP), `SelfAsStdioMCPServer`/`ServeStdioMCP` (stdio), and the `NewTool`/`WithTools` convenience layer for defining tools as plain Go functions.
- `errors.go` — `CLINotFoundError`, `ProcessError`, `CLIJSONDecodeError`. Process failures without a result message are surfaced as synthetic `TypeSystem` error events, which `Run()` converts to Go errors.

## Gap analysis

`GAP_ANALYSIS.md` compares this SDK's feature surface against the official SDKs. To regenerate it (do all of this every time):

1. Ensure the official SDKs are checked out as sibling directories of this repo (`../claude-agent-sdk-typescript`, `../claude-agent-sdk-python`); clone them from `https://github.com/anthropics/claude-agent-sdk-typescript` and `https://github.com/anthropics/claude-agent-sdk-python` if missing.
2. In each, fetch and pull the latest default branch, and identify the latest release tag (`git tag --sort=-v:refname | head -1` or `gh release view`). Analyze against both the branch tip and the latest release, noting features that are on the tip but not yet released.
3. Compare their public API surface (options, message/event types, stream/session/control methods, hooks, MCP/tool helpers, permission handling) against the `claude/` package, in both directions.
4. Rewrite `GAP_ANALYSIS.md` keeping its existing structure: `> Generated:` date line, legend (✅ / 🚧 / ❌), and priority-grouped feature tables (missing here but present in both official SDKs = high priority, in one = medium) with TypeScript / Python / Go columns per feature. Record the SDK versions/tags compared.

## Conventions

- Every PR must be linked to a GitHub issue (see CONTRIBUTING.md); reference it with `Fixes #N`.
- Keep behavior consistent with the official TS/Python SDKs; incompatibilities are treated as bugs.
- Go 1.24+ is the supported minimum (CI runs 1.24).
