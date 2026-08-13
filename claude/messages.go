// Package claude provides a Go SDK for the Claude Code agent.
// It communicates with the `claude` CLI subprocess using the JSON-lines
// streaming protocol (--output-format stream-json), mirroring the behaviour
// of @anthropic-ai/claude-agent-sdk.
package claude

import "encoding/json"

// MessageType is the discriminant field present on every message.
type MessageType string

const (
	// TypeAssistant is a complete assistant turn (SDKAssistantMessage).
	TypeAssistant MessageType = "assistant"
	// TypeStreamEvent carries incremental streaming deltas (SDKPartialAssistantMessage).
	TypeStreamEvent MessageType = "stream_event"
	// TypeResult is the final message emitted when the agent finishes (SDKResultMessage).
	TypeResult MessageType = "result"
	// TypeSystem carries status/info messages from the CLI (SDKStatusMessage).
	// Subtypes include "init" (session start) and "status".
	TypeSystem MessageType = "system"
	// TypeRateLimitEvent is emitted when rate-limit information is available.
	TypeRateLimitEvent MessageType = "rate_limit_event"
	// TypeToolProgress carries incremental tool execution progress updates.
	TypeToolProgress MessageType = "tool_progress"
	// TypeToolUseSummary carries a summary of a tool use after completion.
	TypeToolUseSummary MessageType = "tool_use_summary"
	// TypeAuthStatus carries authentication status updates.
	TypeAuthStatus MessageType = "auth_status"
	// TypePromptSuggestion carries prompt suggestions from the agent.
	TypePromptSuggestion MessageType = "prompt_suggestion"
)

// PluginInfo describes one plugin loaded into the session, as reported on the
// system/init event. Verified against captured output (#23) — note the wire
// carries a source alongside name/path/version.
type PluginInfo struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Source is the marketplace reference, e.g. "lab-workflow@shaharia-lab".
	Source  string `json:"source,omitempty"`
	Version string `json:"version,omitempty"`
}

// System message subtype constants.
// These are the subtypes of a TypeSystem message. They are NOT top-level
// message types: nothing on the wire ever carries type:"task_started". They
// lived in the MessageType block until #23, which made eight parse branches
// permanently dead — including the only one that could populate Event.Task.
const (
	SubtypeInit   = "init"
	SubtypeStatus = "status"

	// Task lifecycle.
	SubtypeTaskStarted      = "task_started"
	SubtypeTaskProgress     = "task_progress"
	SubtypeTaskNotification = "task_notification"

	// Hook lifecycle.
	SubtypeHookStarted  = "hook_started"
	SubtypeHookProgress = "hook_progress"
	SubtypeHookResponse = "hook_response"

	// Session lifecycle.
	SubtypeCompactBoundary = "compact_boundary"
	SubtypeFilesPersisted  = "files_persisted"

	// Observed on the wire while capturing the #23 corpus, and carried here so
	// callers can switch on them without string literals. Their payloads are not
	// typed yet — read Event.Raw.
	SubtypeThinkingTokens        = "thinking_tokens"
	SubtypeBackgroundTasksChange = "background_tasks_changed"
)

// ─── Content blocks ────────────────────────────────────────────────────────────

// ContentBlock is one element of an assistant message's content array.
// Type is always set; Text and Thinking are populated based on Type.
type ContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Thinking string `json:"thinking,omitempty"`
}

// ─── Assistant message ─────────────────────────────────────────────────────────

// MessagePayload is the inner `message` object inside AssistantMessage.
type MessagePayload struct {
	Role    string         `json:"role"`
	Content []ContentBlock `json:"content"`
}

// AssistantMessage is emitted when Claude produces a complete response turn.
// Mirrors SDKAssistantMessage in the TypeScript SDK.
type AssistantMessage struct {
	Type            MessageType    `json:"type"`
	Message         MessagePayload `json:"message"`
	ParentToolUseID *string        `json:"parent_tool_use_id"`
	SessionID       string         `json:"session_id"`
	UUID            string         `json:"uuid"`
}

// Text returns the concatenated text from all text content blocks.
func (m *AssistantMessage) Text() string {
	var out string
	for _, b := range m.Message.Content {
		if b.Type == "text" {
			out += b.Text
		}
	}
	return out
}

// Thinking returns the concatenated thinking text from all thinking content blocks.
func (m *AssistantMessage) Thinking() string {
	var out string
	for _, b := range m.Message.Content {
		if b.Type == "thinking" {
			out += b.Thinking
		}
	}
	return out
}

// ─── Stream event message ──────────────────────────────────────────────────────

// StreamEventDelta is the incremental content of a stream_event delta.
type StreamEventDelta struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Thinking string `json:"thinking,omitempty"`
}

// StreamEvent is the inner `event` object of a StreamEventMessage.
type StreamEvent struct {
	Type  string            `json:"type"`
	Delta *StreamEventDelta `json:"delta,omitempty"`
	Index int               `json:"index,omitempty"`
}

// StreamEventMessage carries incremental deltas during a streaming response.
// Mirrors SDKPartialAssistantMessage in the TypeScript SDK.
type StreamEventMessage struct {
	Type            MessageType `json:"type"`
	Event           StreamEvent `json:"event"`
	ParentToolUseID *string     `json:"parent_tool_use_id"`
	SessionID       string      `json:"session_id"`
	UUID            string      `json:"uuid"`
}

// ─── Usage ────────────────────────────────────────────────────────────────────

// Usage holds token and cache usage from a completed agent run.
// Mirrors NonNullableUsage in the TypeScript SDK.
type Usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`

	// ServerToolUse counts server-side tool invocations. On the wire these are
	// nested under usage.server_tool_use, not top-level — verified against a
	// captured result line (#23).
	ServerToolUse ServerToolUse `json:"server_tool_use,omitempty"`

	// ServiceTier is e.g. "standard".
	ServiceTier string `json:"service_tier,omitempty"`
}

// ServerToolUse counts tool invocations the server performed on the model's
// behalf, reported under a result's usage.server_tool_use.
type ServerToolUse struct {
	WebSearchRequests int `json:"web_search_requests,omitempty"`
	WebFetchRequests  int `json:"web_fetch_requests,omitempty"`
}

// ModelUsage holds per-model token and cost breakdown.
// ModelUsage is the per-model usage breakdown carried by a result's modelUsage
// map. Its fields are camelCase on the wire — the CLI passes the value through
// verbatim from its TypeScript shape — unlike the snake_case used everywhere
// else in the protocol. Verified against a captured result line (#23).
type ModelUsage struct {
	InputTokens              int     `json:"inputTokens"`
	OutputTokens             int     `json:"outputTokens"`
	CacheReadInputTokens     int     `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int     `json:"cacheCreationInputTokens"`
	WebSearchRequests        int     `json:"webSearchRequests,omitempty"`
	CostUSD                  float64 `json:"costUSD"`
	ContextWindow            int     `json:"contextWindow,omitempty"`
	MaxOutputTokens          int     `json:"maxOutputTokens,omitempty"`
	// CanonicalModel is the model id without any suffix, e.g. "claude-haiku-4-5".
	CanonicalModel string `json:"canonicalModel,omitempty"`
	// Provider is e.g. "firstParty", "bedrock", "vertex".
	Provider string `json:"provider,omitempty"`
}

// ─── Result message ────────────────────────────────────────────────────────────

// Result is the final message emitted by the agent.
// It covers both SDKResultSuccess and SDKResultError from the TypeScript SDK.
// Check IsError (or Subtype) to determine which case you have.
type Result struct {
	Type          MessageType `json:"type"`
	Subtype       string      `json:"subtype"`
	DurationMS    int64       `json:"duration_ms"`
	DurationAPIMS int64       `json:"duration_api_ms"`
	IsError       bool        `json:"is_error"`
	NumTurns      int         `json:"num_turns"`
	Result        string      `json:"result"`
	StopReason    *string     `json:"stop_reason"`
	TotalCostUSD  float64     `json:"total_cost_usd"`
	Usage         Usage       `json:"usage"`
	SessionID     string      `json:"session_id"`
	UUID          string      `json:"uuid"`
	// ModelUsages holds per-model token and cost breakdowns keyed by model ID.
	ModelUsages map[string]ModelUsage `json:"modelUsage,omitempty"`
	// Populated when IsError is true.
	Errors []string `json:"errors,omitempty"`
	// StructuredOutput holds parsed structured output when an OutputFormat
	// with type "json" or "json_schema" was requested.
	StructuredOutput any `json:"structured_output,omitempty"`
	// PermissionDenials lists any tool calls that were denied during the run.
	PermissionDenials []PermissionDenial `json:"permission_denials,omitempty"`
}

// PermissionDenial describes one tool call that was refused during a run,
// as reported in the final result message.
type PermissionDenial struct {
	// ToolName is the tool that was denied (e.g. "Write").
	ToolName string `json:"tool_name"`
	// ToolUseID identifies the specific denied call.
	ToolUseID string `json:"tool_use_id"`
	// ToolInput is the raw input the tool would have been invoked with.
	ToolInput json.RawMessage `json:"tool_input,omitempty"`
}

// ─── System message ────────────────────────────────────────────────────────────

// SystemMessage covers all "system" typed messages from the CLI.
//
// When Subtype == SubtypeInit ("init"), it is emitted at session start and the
// session/model/tools/version fields are populated.
//
// When Subtype == SubtypeStatus ("status"), the Status and Message fields are
// populated with a human-readable status update.
type SystemMessage struct {
	Type    MessageType `json:"type"`
	Subtype string      `json:"subtype"`

	// Status subtype fields.
	Status  string `json:"status,omitempty"`
	Message string `json:"message,omitempty"`

	// Init subtype fields — populated when Subtype == SubtypeInit.
	SessionID         string   `json:"session_id,omitempty"`
	CWD               string   `json:"cwd,omitempty"`
	Model             string   `json:"model,omitempty"`
	Tools             []string `json:"tools,omitempty"`
	PermissionMode    string   `json:"permissionMode,omitempty"`
	ClaudeCodeVersion string   `json:"claude_code_version,omitempty"`
	APIKeySource      string   `json:"apiKeySource,omitempty"`

	// Additional init fields populated by newer CLI versions.
	Agents        []string     `json:"agents,omitempty"`
	Betas         []string     `json:"betas,omitempty"`
	Skills        []string     `json:"skills,omitempty"`
	Plugins       []PluginInfo `json:"plugins,omitempty"`
	SlashCommands []string     `json:"slash_commands,omitempty"`
}

// ─── Tool progress message ────────────────────────────────────────────────────

// ToolProgressMessage carries incremental progress updates from a running tool.
type ToolProgressMessage struct {
	Type      MessageType `json:"type"`
	ToolUseID string      `json:"tool_use_id"`
	Progress  float64     `json:"progress,omitempty"`
	Message   string      `json:"message,omitempty"`
}

// ─── Task message ─────────────────────────────────────────────────────────────

// TaskMessage carries task lifecycle events (started, progress, notification).
type TaskMessage struct {
	Type    MessageType `json:"type"`
	TaskID  string      `json:"task_id,omitempty"`
	Status  string      `json:"status,omitempty"`
	Message string      `json:"message,omitempty"`
}

// ─── Top-level Event ──────────────────────────────────────────────────────────

// Event is the top-level value yielded from Query().
//
// Type is always set. The corresponding typed field is non-nil for known types:
//   - TypeAssistant     → Assistant
//   - TypeStreamEvent   → StreamEvent
//   - TypeResult        → Result
//   - TypeSystem        → System
//
// For unknown types (e.g. TypeRateLimitEvent), only Raw is set so callers can
// handle forward-compatibility themselves.
type Event struct {
	Type         MessageType
	Assistant    *AssistantMessage
	StreamEvent  *StreamEventMessage
	Result       *Result
	System       *SystemMessage
	ToolProgress *ToolProgressMessage
	Task         *TaskMessage
	Raw          json.RawMessage

	// DecodeErr records why a typed field decoded only partially, if it did.
	// Typed fields are best-effort: on a type mismatch the fields that did
	// decode are kept and this is set, rather than the whole message being
	// discarded. Raw is always the authoritative payload.
	DecodeErr error `json:"-"`
}
