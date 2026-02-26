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
)

// System message subtype constants.
const (
	SubtypeInit   = "init"
	SubtypeStatus = "status"
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
	// Populated when IsError is true.
	Errors []string `json:"errors,omitempty"`
	// StructuredOutput holds parsed structured output when an OutputFormat
	// with type "json" or "json_schema" was requested.
	StructuredOutput any `json:"structured_output,omitempty"`
	// PermissionDenials lists any tool calls that were denied during the run.
	PermissionDenials []string `json:"permission_denials,omitempty"`
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
	Agents        []string `json:"agents,omitempty"`
	Betas         []string `json:"betas,omitempty"`
	Skills        []string `json:"skills,omitempty"`
	Plugins       []string `json:"plugins,omitempty"`
	SlashCommands []string `json:"slash_commands,omitempty"`
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
	Type        MessageType
	Assistant   *AssistantMessage
	StreamEvent *StreamEventMessage
	Result      *Result
	System      *SystemMessage
	Raw         json.RawMessage
}
