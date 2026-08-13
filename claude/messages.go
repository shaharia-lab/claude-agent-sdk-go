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
	// TypeUser is a turn on the user side (SDKUserMessage) — most often the
	// CLI delivering a tool's result, not something a human typed.
	TypeUser MessageType = "user"
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

// Content block types. These are the values of ContentBlock.Type observed on the
// wire or found in the CLI binary; anything else still decodes with Type + Raw.
const (
	BlockText           = "text"
	BlockThinking       = "thinking"
	BlockToolUse        = "tool_use"
	BlockToolResult     = "tool_result"
	BlockServerToolUse  = "server_tool_use"
	BlockAdvisorToolRes = "advisor_tool_result"

	// Server-side tool results are per-tool block types rather than one generic
	// shape. All of them carry tool_use_id + content, so ContentBlock decodes
	// them uniformly.
	BlockWebSearchToolResult     = "web_search_tool_result"
	BlockCodeExecutionToolResult = "code_execution_tool_result"
)

// ContentBlock is one element of a message's content array.
//
// The wire carries a discriminated union: `text`, `thinking`, `tool_use`,
// `tool_result`, the server-side tool blocks above, and whatever the API adds
// next. This is modelled as one struct with a Type discriminator and the
// superset of fields rather than a Go interface, so a []ContentBlock decodes in
// a single pass and an unknown block degrades to Type + Raw instead of being
// dropped.
//
// Read only the fields that belong to Type; each is documented with the types
// that populate it. Raw is always the authoritative payload.
type ContentBlock struct {
	Type string `json:"type"`

	// Text is set when Type == BlockText.
	Text string `json:"text,omitempty"`

	// Thinking and Signature are set when Type == BlockThinking. Signature
	// arrives whole here, and incrementally as a signature_delta while streaming.
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`

	// ID, Name and Input are set when Type is BlockToolUse or BlockServerToolUse.
	// Input is the tool's arguments object, kept raw because its schema is the
	// tool's own.
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// Caller describes how the tool was invoked, e.g. {"type":"direct"}. Sent
	// alongside every tool_use block by CLI 2.1.224.
	Caller json.RawMessage `json:"caller,omitempty"`

	// ToolUseID, Content and IsError are set on the result blocks
	// (BlockToolResult and the server-side variants). ToolUseID pairs the result
	// back to the ID of the tool_use block that requested it.
	//
	// Content is raw because the CLI sends either a plain string or an array of
	// blocks depending on the tool — both shapes were observed in one session.
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   *bool           `json:"is_error,omitempty"`

	// Raw is the complete block as received, including fields this struct does
	// not model. Always populated.
	Raw json.RawMessage `json:"-"`
}

// UnmarshalJSON records the verbatim block in Raw before decoding the known
// fields, so an unknown or partially-decodable block still carries its payload.
func (b *ContentBlock) UnmarshalJSON(data []byte) error {
	b.Raw = append(b.Raw[:0], data...)

	// A distinct type avoids recursing into this method.
	type blockFields ContentBlock
	return json.Unmarshal(data, (*blockFields)(b))
}

// ContentText returns the block's Content as a plain string when the CLI sent
// one. Result blocks carry either a string or an array of blocks; ok is false
// for the array form, where Content should be decoded by the caller.
func (b *ContentBlock) ContentText() (string, bool) {
	var s string
	if err := json.Unmarshal(b.Content, &s); err != nil {
		return "", false
	}
	return s, true
}

// Failed reports whether this is a result block the tool reported as an error.
func (b *ContentBlock) Failed() bool { return b.IsError != nil && *b.IsError }

// ContentBlocks is a message's content array.
//
// The CLI sends content either as an array of blocks (every message it
// originates) or as a bare string (the shape this SDK itself sends for a user
// turn, and what stored transcripts replay). A string decodes to a single text
// block so callers have one shape to handle.
type ContentBlocks []ContentBlock

// UnmarshalJSON accepts both the array and the bare-string form.
func (c *ContentBlocks) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*c = ContentBlocks{{Type: BlockText, Text: s, Raw: append(json.RawMessage(nil), data...)}}
		return nil
	}

	type blocks ContentBlocks
	return json.Unmarshal(data, (*blocks)(c))
}

// ─── Assistant message ─────────────────────────────────────────────────────────

// AssistantMessageError classifies why an assistant turn failed.
type AssistantMessageError string

// Assistant error classifications.
const (
	ErrAuthenticationFailed AssistantMessageError = "authentication_failed"
	ErrBillingError         AssistantMessageError = "billing_error"
	ErrRateLimit            AssistantMessageError = "rate_limit"
	ErrInvalidRequest       AssistantMessageError = "invalid_request"
	ErrServerError          AssistantMessageError = "server_error"
	ErrUnknown              AssistantMessageError = "unknown"
)

// MessagePayload is the inner `message` object of an assistant or user message.
type MessagePayload struct {
	Role    string        `json:"role"`
	Content ContentBlocks `json:"content"`

	// ID is the API message id, e.g. "msg_011Cdz…". Assistant messages only.
	ID string `json:"id,omitempty"`
	// Type is the API object type; the CLI sends "message".
	Type string `json:"type,omitempty"`
	// Model is the model that produced this turn, e.g. "claude-opus-5".
	Model string `json:"model,omitempty"`

	// StopReason is nil until the turn ends; "tool_use" and "end_turn" are the
	// common terminal values. StopSequence is set only when a stop sequence
	// triggered the stop.
	StopReason   *string         `json:"stop_reason,omitempty"`
	StopSequence *string         `json:"stop_sequence,omitempty"`
	StopDetails  json.RawMessage `json:"stop_details,omitempty"`

	// Usage is this turn's token counts, kept raw because the CLI nests
	// provider-specific detail under it (cache_creation, inference_geo, …) that
	// changes independently of this SDK.
	//
	// This is NOT the cost-accounting field: it reports a single turn of the main
	// loop only. Cumulative per-model usage and cost live in Result.ModelUsages
	// (wire key modelUsage).
	Usage json.RawMessage `json:"usage,omitempty"`
}

// AssistantMessage is emitted when Claude produces a complete response turn.
// Mirrors SDKAssistantMessage in the TypeScript SDK.
type AssistantMessage struct {
	Type            MessageType    `json:"type"`
	Message         MessagePayload `json:"message"`
	ParentToolUseID *string        `json:"parent_tool_use_id"`
	SessionID       string         `json:"session_id"`
	UUID            string         `json:"uuid"`

	// Timestamp is the CLI's send time, RFC 3339 with milliseconds.
	Timestamp string `json:"timestamp,omitempty"`
	// RequestID is the upstream API request id, e.g. "req_011Cdz…".
	RequestID string `json:"request_id,omitempty"`
	// Error classifies a failed turn; empty on success.
	Error AssistantMessageError `json:"error,omitempty"`
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
		if b.Type == BlockThinking {
			out += b.Thinking
		}
	}
	return out
}

// ToolUses returns the tool_use blocks of this turn — the tools the agent is
// asking to run. Each pairs by ID to a tool_result block on the following
// UserMessage.
func (m *AssistantMessage) ToolUses() []ContentBlock {
	var out []ContentBlock
	for _, b := range m.Message.Content {
		if b.Type == BlockToolUse || b.Type == BlockServerToolUse {
			out = append(out, b)
		}
	}
	return out
}

// ─── User message ──────────────────────────────────────────────────────────────

// UserMessage is a turn on the user side of the conversation.
//
// Most of these are not typed by a human: the CLI emits one after every tool
// call, carrying the tool's output as tool_result blocks. They also carry
// replayed input under WithReplayUserMessages and every user turn of a stored
// transcript. Mirrors SDKUserMessage in the TypeScript SDK.
type UserMessage struct {
	Type            MessageType    `json:"type"`
	Message         MessagePayload `json:"message"`
	ParentToolUseID *string        `json:"parent_tool_use_id"`
	SessionID       string         `json:"session_id"`
	UUID            string         `json:"uuid"`

	// Timestamp is the CLI's send time, RFC 3339 with milliseconds.
	Timestamp string `json:"timestamp,omitempty"`

	// ToolUseResult is the tool's structured result, alongside the rendered form
	// in the tool_result block. Raw because its shape is the tool's own: a Read
	// sends an object with file metadata, a failure sends a plain string.
	ToolUseResult json.RawMessage `json:"tool_use_result,omitempty"`

	// IsSynthetic marks a message the CLI generated rather than one the user or a
	// tool produced.
	IsSynthetic bool `json:"isSynthetic,omitempty"`
}

// Text returns the concatenated text from all text content blocks. For a user
// turn sent as a bare string, that is the whole message.
func (m *UserMessage) Text() string {
	var out string
	for _, b := range m.Message.Content {
		if b.Type == BlockText {
			out += b.Text
		}
	}
	return out
}

// ToolResults returns the tool_result blocks of this turn, including the
// server-side result variants. Pair them to the preceding assistant turn's
// tool_use blocks by ToolUseID.
func (m *UserMessage) ToolResults() []ContentBlock {
	var out []ContentBlock
	for _, b := range m.Message.Content {
		switch b.Type {
		case BlockToolResult, BlockAdvisorToolRes,
			BlockWebSearchToolResult, BlockCodeExecutionToolResult:
			out = append(out, b)
		}
	}
	return out
}

// ─── Stream event message ──────────────────────────────────────────────────────

// Stream event types, the values of StreamEvent.Type.
const (
	StreamMessageStart      = "message_start"
	StreamMessageDelta      = "message_delta"
	StreamMessageStop       = "message_stop"
	StreamContentBlockStart = "content_block_start"
	StreamContentBlockDelta = "content_block_delta"
	StreamContentBlockStop  = "content_block_stop"
)

// Delta types, the values of StreamEventDelta.Type.
const (
	DeltaText      = "text_delta"
	DeltaThinking  = "thinking_delta"
	DeltaInputJSON = "input_json_delta"
	DeltaSignature = "signature_delta"
)

// StreamEventDelta is the incremental content of a stream_event delta.
// Which field carries the increment depends on Type.
type StreamEventDelta struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Thinking string `json:"thinking,omitempty"`

	// PartialJSON is a fragment of a tool's input, sent when Type is
	// DeltaInputJSON. Fragments split at arbitrary points — including mid-token
	// and mid-string — so they are only valid JSON once concatenated across the
	// whole block.
	PartialJSON string `json:"partial_json,omitempty"`

	// Signature is a fragment of a thinking block's signature (DeltaSignature).
	Signature string `json:"signature,omitempty"`

	// StopReason and StopSequence ride the message_delta at the end of a turn,
	// where this struct is the event's `delta` rather than a block's.
	StopReason   *string `json:"stop_reason,omitempty"`
	StopSequence *string `json:"stop_sequence,omitempty"`
}

// StreamEvent is the inner `event` object of a StreamEventMessage.
//
// Its shape varies by Type, so the fields specific to each are kept raw and
// reached through the accessors below. Raw holds the whole event verbatim.
type StreamEvent struct {
	Type  string            `json:"type"`
	Delta *StreamEventDelta `json:"delta,omitempty"`
	Index int               `json:"index,omitempty"`

	// Message is the opening message envelope on StreamMessageStart.
	Message json.RawMessage `json:"message,omitempty"`

	// ContentBlock is the block being opened on StreamContentBlockStart. For a
	// tool_use block it already carries id and name, with input arriving as
	// input_json_delta fragments.
	ContentBlock json.RawMessage `json:"content_block,omitempty"`

	// Usage rides StreamMessageDelta as a sibling of delta, not inside it.
	Usage json.RawMessage `json:"usage,omitempty"`

	// ContextManagement reports edits the CLI applied to the context window.
	ContextManagement json.RawMessage `json:"context_management,omitempty"`

	// Raw is the complete event as received. Always populated.
	Raw json.RawMessage `json:"-"`
}

// UnmarshalJSON keeps the event verbatim in Raw before decoding known fields,
// so nothing the CLI sends is lost to this struct's shape.
func (e *StreamEvent) UnmarshalJSON(data []byte) error {
	e.Raw = append(e.Raw[:0], data...)

	type eventFields StreamEvent
	return json.Unmarshal(data, (*eventFields)(e))
}

// PartialJSON returns the tool-input fragment carried by this event, if it is an
// input_json_delta. Concatenate the fragments of one block Index in arrival
// order to rebuild the tool's input.
func (e *StreamEvent) PartialJSON() (string, bool) {
	if e.Delta == nil || e.Delta.Type != DeltaInputJSON {
		return "", false
	}
	return e.Delta.PartialJSON, true
}

// TextDelta returns the text increment carried by this event, if any.
func (e *StreamEvent) TextDelta() (string, bool) {
	if e.Delta == nil || e.Delta.Type != DeltaText {
		return "", false
	}
	return e.Delta.Text, true
}

// ThinkingDelta returns the thinking increment carried by this event, if any.
func (e *StreamEvent) ThinkingDelta() (string, bool) {
	if e.Delta == nil || e.Delta.Type != DeltaThinking {
		return "", false
	}
	return e.Delta.Thinking, true
}

// SignatureDelta returns the thinking-signature fragment carried by this event,
// if any.
func (e *StreamEvent) SignatureDelta() (string, bool) {
	if e.Delta == nil || e.Delta.Type != DeltaSignature {
		return "", false
	}
	return e.Delta.Signature, true
}

// ContentBlockStart returns the block being opened, when this event is a
// content_block_start. This is where a streamed tool call announces its id and
// name, before any of its input has arrived.
func (e *StreamEvent) ContentBlockStart() (*ContentBlock, bool) {
	if e.Type != StreamContentBlockStart || len(e.ContentBlock) == 0 {
		return nil, false
	}
	var b ContentBlock
	if err := json.Unmarshal(e.ContentBlock, &b); err != nil {
		return nil, false
	}
	return &b, true
}

// MessageDelta returns the tail of a turn: why it stopped and its final usage.
// stopReason is empty if the CLI sent none.
func (e *StreamEvent) MessageDelta() (stopReason string, usage json.RawMessage, ok bool) {
	if e.Type != StreamMessageDelta {
		return "", nil, false
	}
	if e.Delta != nil && e.Delta.StopReason != nil {
		stopReason = *e.Delta.StopReason
	}
	return stopReason, e.Usage, true
}

// StreamEventMessage carries incremental deltas during a streaming response.
// Mirrors SDKPartialAssistantMessage in the TypeScript SDK.
type StreamEventMessage struct {
	Type            MessageType `json:"type"`
	Event           StreamEvent `json:"event"`
	ParentToolUseID *string     `json:"parent_tool_use_id"`
	SessionID       string      `json:"session_id"`
	UUID            string      `json:"uuid"`

	// TTFTMs is the time to first token in milliseconds, sent on message_start.
	TTFTMs int `json:"ttft_ms,omitempty"`
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
//   - TypeUser          → User
//   - TypeStreamEvent   → StreamEvent
//   - TypeResult        → Result
//   - TypeSystem        → System
//
// For unknown types (e.g. TypeRateLimitEvent), only Raw is set so callers can
// handle forward-compatibility themselves.
type Event struct {
	Type         MessageType
	Assistant    *AssistantMessage
	User         *UserMessage
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
