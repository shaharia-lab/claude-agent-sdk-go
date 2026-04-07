# Gap Analysis: Go SDK vs Official TypeScript & Python SDKs

> Generated: 2026-03-02

This report identifies features present in the official TypeScript and/or Python Claude Agent SDKs that are missing from our Go SDK.

## Legend

| Symbol | Meaning |
|--------|---------|
| :white_check_mark: | Fully implemented |
| :construction: | Partially implemented |
| :x: | Not implemented |

---

## 1. High Priority Gaps

These features exist in **both** official SDKs and are missing in Go.

| Feature | TypeScript | Python | Go | Notes |
|---------|:----------:|:------:|:--:|-------|
| Tool definition helper (`tool()` / `@tool`) | :white_check_mark: | :white_check_mark: | :x: | TS has `tool()` with Zod schema validation. Python has `@tool` decorator with type-based schema. Go requires manually creating an MCP server — no convenience wrapper exists. |
| `rewindFiles()` on Stream/Session | :white_check_mark: | :white_check_mark: | :x: | Go has `WithEnableFileCheckpointing()` flag but no method to actually trigger a file rewind to a specific message checkpoint. |

---

## 2. Medium Priority Gaps

These features exist in **one** official SDK and add meaningful capability.

### 2a. Session Discovery & Introspection

| Feature | TypeScript | Python | Go | Notes |
|---------|:----------:|:------:|:--:|-------|
| `listSessions()` | :white_check_mark: | :x: | :x: | List past sessions with metadata for session management UIs. |
| `getSessionMessages()` | :white_check_mark: | :x: | :x: | Read full session transcripts by session ID. |
| `supportedCommands()` | :white_check_mark: | :x: | :x: | Query available slash commands at runtime. |
| `supportedModels()` | :white_check_mark: | :x: | :x: | Query available models and their capabilities. |
| `supportedAgents()` | :white_check_mark: | :x: | :x: | Query available agent definitions at runtime. |

### 2b. Missing Hook Events

| Hook Event | TypeScript | Python | Go | Notes |
|------------|:----------:|:------:|:--:|-------|
| `SessionEnd` | :white_check_mark: | :x: | :x: | Go has `Start` but no `SessionEnd` counterpart. |
| `TeammateIdle` | :white_check_mark: | :x: | :x: | Track when team member agents become idle. |
| `TaskCompleted` | :white_check_mark: | :x: | :x: | Track when background tasks complete. |
| `Elicitation` | :white_check_mark: | :x: | :x: | MCP server requests user input (form/URL auth). |
| `ElicitationResult` | :white_check_mark: | :x: | :x: | User response to elicitation request. |

### 2c. Missing Event/Message Types

| Event Type | TypeScript | Python | Go | Notes |
|------------|:----------:|:------:|:--:|-------|
| Tool progress events | :white_check_mark: | :x: | :x: | Real-time tool execution elapsed time reporting. |
| Task started/progress/notification | :white_check_mark: | :x: | :x: | Background task lifecycle events. |
| Per-model usage breakdown | :white_check_mark: | :x: | :x: | `modelUsage` field with per-model token/cost breakdown in result. |

### 2d. Callbacks & Architecture

| Feature | TypeScript | Python | Go | Notes |
|---------|:----------:|:------:|:--:|-------|
| `onElicitation` callback | :white_check_mark: | :x: | :x: | Handle MCP elicitation requests (form input, URL auth). |
| Pluggable transport abstraction | :x: | :white_check_mark: | :x: | Python has `Transport` base class for custom transport implementations (e.g., remote execution). |

---

## 3. Low Priority Gaps

These are nice-to-have features, often unstable or niche.

| Feature | TypeScript | Python | Go | Notes |
|---------|:----------:|:------:|:--:|-------|
| V2 Session API (`createSession`/`resumeSession`) | :white_check_mark: | :x: | :x: | Marked `unstable` in TS. Alpha-quality. |
| `accountInfo()` | :white_check_mark: | :x: | :x: | Retrieve authenticated account details. |
| `stopTask(taskId)` | :white_check_mark: | :x: | :x: | Stop a running background task by ID. |
| `reconnectMcpServer()` | :white_check_mark: | :x: | :x: | Reconnect a failed MCP server at runtime. |
| `toggleMcpServer()` | :white_check_mark: | :x: | :x: | Enable/disable MCP server at runtime. |
| `setMcpServers()` | :white_check_mark: | :x: | :x: | Reconfigure all MCP servers at runtime. |
| `claudeai-proxy` MCP server type | :white_check_mark: | :x: | :x: | Proxy-based MCP server for Claude AI integrations. |
| `resumeSessionAt` (resume from specific message) | :white_check_mark: | :x: | :x: | Resume session from a specific message UUID. |
| `promptSuggestions` option | :white_check_mark: | :x: | :x: | Emit suggested next prompts after responses. |
| Hook progress events | :white_check_mark: | :x: | :x: | `HookStarted`/`HookProgress`/`HookResponse` messages. |
| Compact boundary events | :white_check_mark: | :x: | :x: | `CompactBoundaryMessage` when compaction occurs. |
| Files persisted events | :white_check_mark: | :x: | :x: | `FilesPersistedEvent` on write operations. |
| Auth status events | :white_check_mark: | :x: | :x: | `AuthStatusMessage` for authentication state changes. |
| `ConfigChange` hook | :white_check_mark: | :x: | :x: | Fires when settings/CLAUDE.md/skills change. |
| `WorktreeCreate`/`WorktreeRemove` hooks | :white_check_mark: | :x: | :x: | Git worktree lifecycle hooks. |
| `webSearchRequests` in usage | :white_check_mark: | :x: | :x: | Track web search request count in usage stats. |
| Custom process spawner | :white_check_mark: | :x: | :x: | `spawnClaudeCodeProcess` for VM/container execution. |

---

## 4. Go SDK Advantages (Features Ahead of Official SDKs)

| Feature | TypeScript | Python | Go | Notes |
|---------|:----------:|:------:|:--:|-------|
| `PreBash` / `PostBash` hooks | :x: | :x: | :white_check_mark: | Fine-grained hooks specifically for Bash command execution. |
| `PreEdit` / `PostEdit` hooks | :x: | :x: | :white_check_mark: | Fine-grained hooks specifically for file edit operations. |
| `ServeStdioMCP` helper | :x: | :x: | :white_check_mark: | Serve an MCP server over stdio with one function call. |
| `SelfAsStdioMCPServer` pattern | :x: | :x: | :white_check_mark: | Self-invoking binary pattern for MCP stdio servers. |

---

## 5. Features at Parity (No Gaps)

The Go SDK is fully on par with both official SDKs for these areas:

| Area | Details |
|------|---------|
| **Core API** | `Query()`, `Run()`, `Session` — all three entry points covered |
| **Streaming** | Partial messages, text/thinking deltas, event channel |
| **Model config** | Model selection, fallback, mid-stream switching |
| **Thinking** | Adaptive/enabled/disabled modes, effort levels, budget control |
| **Permissions** | All 5 modes, custom handler callback, rule mutations, multi-level storage |
| **MCP servers** | HTTP, stdio, SSE — all transport types supported |
| **Hooks** | 15+ hook events with matcher patterns and timeouts |
| **Sessions** | Resume, continue, fork — all session management features |
| **Agents** | Sub-agent definitions with prompt/tools/model/turns |
| **Plugins** | Local plugin registration |
| **Sandbox** | Full sandbox config including network and filesystem |
| **Structured output** | JSON Schema output format with parsed result |
| **Cost tracking** | USD cost, token usage, cache metrics, duration |
| **Settings** | Multi-source settings, presets, isolation mode |
| **Error handling** | Typed errors (CLINotFound, ProcessError, JSONDecode) |
| **File checkpointing** | Enable flag present (rewind method missing) |
| **Betas** | Feature flag support |

---

## 6. Recommended Action Items

### Immediate (High Impact)

1. **Add `tool()` helper function** — Create a Go-idiomatic tool definition API that wraps MCP server creation. Something like:
   ```go
   claude.NewTool("my-tool", "description", inputSchema, handler)
   ```

2. **Add `RewindFiles()` method** — Add to both `Stream` and `Session` types to complete file checkpointing support.

### Short Term (Medium Impact)

3. **Add session introspection methods** — `ListSessions()`, `GetSessionMessages()`, `SupportedModels()`.
4. **Add missing hook events** — `SessionEnd`, `TeammateIdle`, `TaskCompleted`, `Elicitation`, `ElicitationResult`.
5. **Add per-model usage breakdown** — Extend `Result` with `ModelUsage` map.
6. **Add tool/task progress event types** — New event types for real-time progress tracking.
7. **Add transport abstraction** — Interface for pluggable transport implementations.

### Long Term (Low Impact)

8. Add runtime MCP management methods (`reconnectMcpServer`, `toggleMcpServer`).
9. Add `onElicitation` callback support.
10. Add remaining TS-only event types as they stabilize.
