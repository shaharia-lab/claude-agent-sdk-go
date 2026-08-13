// Package claude provides a Go SDK for the Claude Code agent.
// It communicates with the `claude` CLI subprocess using the JSON-lines
// streaming protocol (--output-format stream-json), mirroring the behaviour
// of @anthropic-ai/claude-agent-sdk.
package claude

import (
	"bytes"
	"encoding/json"
)

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
	SubtypeTaskUpdated      = "task_updated"

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
	// Check for null first: Go unmarshals JSON null into a string as a no-op,
	// so the string branch below would silently turn absent content into one
	// empty text block rather than no content at all.
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		*c = nil
		return nil
	}

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

	// Error classifies a failed turn; empty on success.
	//
	// Placement is inferred, not captured: no session available here produced a
	// failed turn, and the official Python SDK flattens the whole `message`
	// object onto its own type, so its top-level `error` most likely sits beside
	// the other fields here. If a future capture proves otherwise this field
	// stays empty — read Event.Raw when it matters.
	Error AssistantMessageError `json:"error,omitempty"`

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

// Result subtypes, the values of Result.Subtype. These are NOT system message
// subtypes — they classify how a run finished, and each pairs with a
// TerminalReason below. Verified against captured output: `--max-turns 1`
// produces error_max_turns, and interrupting a streaming turn produces
// error_during_execution.
const (
	SubtypeSuccess                         = "success"
	SubtypeErrorDuringExecution            = "error_during_execution"
	SubtypeErrorMaxTurns                   = "error_max_turns"
	SubtypeErrorMaxBudgetUSD               = "error_max_budget_usd"
	SubtypeErrorMaxStructuredOutputRetries = "error_max_structured_output_retries"
)

// TerminalReason says why the agent's loop ended.
//
// This is a named string, not a closed enum: the CLI may add reasons at any
// time and an unrecognised one must decode through unchanged rather than being
// dropped. Compare against the constants below, and treat anything else as a
// reason this SDK does not know yet.
type TerminalReason string

// Terminal reasons. All nine were found in the CLI binary; "completed",
// "max_turns" and "aborted_streaming" are additionally confirmed by captured
// fixtures in testdata/messages.
const (
	// TerminalCompleted is a run that finished on its own.
	TerminalCompleted TerminalReason = "completed"
	// TerminalMaxTurns is the turn limit from WithMaxTurns being reached.
	TerminalMaxTurns TerminalReason = "max_turns"

	// TerminalAbortedStreaming and TerminalAbortedTools mean the turn was
	// cancelled — via Stream.Interrupt() or an interrupt control request —
	// while streaming a response or while running tools. These are how a caller
	// tells "the user stopped it" from "the model finished".
	TerminalAbortedStreaming TerminalReason = "aborted_streaming"
	TerminalAbortedTools     TerminalReason = "aborted_tools"

	// TerminalAPIError is an upstream API failure; see Result.APIErrorStatus
	// for the HTTP status.
	TerminalAPIError TerminalReason = "api_error"

	// TerminalBudgetExhausted is the cost limit being hit.
	TerminalBudgetExhausted TerminalReason = "budget_exhausted"
	// TerminalStructuredOutputRetryExhausted means structured output failed
	// schema validation too many times.
	TerminalStructuredOutputRetryExhausted TerminalReason = "structured_output_retry_exhausted"
	// TerminalToolDeferredUnavailable means a deferred tool could not be run.
	TerminalToolDeferredUnavailable TerminalReason = "tool_deferred_unavailable"
	// TerminalTurnSetupFailed means the turn failed before it began.
	TerminalTurnSetupFailed TerminalReason = "turn_setup_failed"
)

// Aborted reports whether the run was cancelled rather than finishing or
// failing on its own.
func (r TerminalReason) Aborted() bool {
	return r == TerminalAbortedStreaming || r == TerminalAbortedTools
}

// DeferredToolUse is a tool call parked by a PreToolUse hook that returned
// permissionDecision "defer". The run stops and reports the call here so the
// caller can inspect it and decide whether to resume.
type DeferredToolUse struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input,omitempty"`
}

// Model providers, the values of ModelUsage.Provider.
const (
	ProviderFirstParty           = "firstParty"
	ProviderBedrock              = "bedrock"
	ProviderVertex               = "vertex"
	ProviderFoundry              = "foundry"
	ProviderAnthropicAWS         = "anthropicAws"
	ProviderAnthropicGoogleCloud = "anthropicGoogleCloud"
	ProviderMantle               = "mantle"
	ProviderGateway              = "gateway"
)

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

	// TerminalReason says why the loop ended. Unlike Subtype, it distinguishes
	// a cancelled turn from a completed one — see TerminalReason.Aborted().
	TerminalReason TerminalReason `json:"terminal_reason,omitempty"`

	// APIErrorStatus is the HTTP status of the failing upstream call (429, 500,
	// 529, …) — nil when the CLI sent none, which is why it is a pointer rather
	// than a zero-means-absent int.
	//
	// It can be set on a "success" subtype whose IsError is true, so branch on
	// this field rather than on Subtype when classifying a failure for retry.
	APIErrorStatus *int `json:"api_error_status,omitempty"`

	// DeferredToolUse is the tool call a PreToolUse hook deferred, if any.
	DeferredToolUse *DeferredToolUse `json:"deferred_tool_use,omitempty"`

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

	// Status subtype field.
	Status string `json:"status,omitempty"`

	// Error carries the reason for a synthetic system/error event — one this
	// SDK generates when the subprocess fails without sending a result. It is
	// not a wire field: the CLI never sends system/error.
	Error string `json:"-"`

	// Init subtype fields — populated when Subtype == SubtypeInit.
	SessionID         string   `json:"session_id,omitempty"`
	UUID              string   `json:"uuid,omitempty"`
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

	// Capabilities is the CLI's feature list, e.g. "interrupt_receipt_v1" —
	// the same list Session.Capabilities() serves. It arrives here rather than
	// in the initialize control response (#19), so it is unknown until the
	// first turn starts.
	Capabilities []string `json:"capabilities,omitempty"`

	// MCPServers reports each configured MCP server and whether it connected.
	MCPServers []MCPServerInit `json:"mcp_servers,omitempty"`

	// OutputStyle is the active output style, e.g. "default".
	OutputStyle string `json:"output_style,omitempty"`

	// FastModeState is "on" or "off"; FastModeDisabledReason says why when the
	// CLI turned it off.
	FastModeState          string `json:"fast_mode_state,omitempty"`
	FastModeDisabledReason string `json:"fast_mode_disabled_reason,omitempty"`
}

// MCPServerInit is one MCP server as reported on system/init.
type MCPServerInit struct {
	Name string `json:"name"`
	// Status is "connected", "pending", "failed", …
	Status string `json:"status"`
}

// ─── Tool progress message ────────────────────────────────────────────────────

// ToolProgressMessage reports that a tool is still running.
//
// The field set is taken from the CLI's own emit code rather than from a
// specification: it has three shapes (a Bash/PowerShell progress tick, a REPL
// call, and a bare heartbeat), which share everything below. The `progress` and
// `message` fields this struct used to declare exist nowhere on the wire.
type ToolProgressMessage struct {
	Type      MessageType `json:"type"`
	ToolUseID string      `json:"tool_use_id"`

	// ToolName is the tool still running, e.g. "Bash" or "REPL".
	ToolName string `json:"tool_name,omitempty"`

	// ElapsedTimeSeconds is how long it has been running.
	ElapsedTimeSeconds float64 `json:"elapsed_time_seconds,omitempty"`

	// Heartbeat marks a keep-alive tick that reports no new progress.
	Heartbeat bool `json:"heartbeat,omitempty"`

	// TaskID is set when the tool runs as a background task.
	TaskID string `json:"task_id,omitempty"`

	ParentToolUseID *string `json:"parent_tool_use_id,omitempty"`
	SessionID       string  `json:"session_id,omitempty"`
	UUID            string  `json:"uuid,omitempty"`

	// Raw is the complete message; REPL ticks carry a repl_call object this
	// struct does not model.
	Raw json.RawMessage `json:"-"`
}

// ─── Task lifecycle messages ──────────────────────────────────────────────────

// TaskStatus is the state of a background task.
//
// Two vocabularies overlap here: task_updated reports the raw lifecycle state
// (including "killed"), while task_notification reports the user-facing outcome
// (where a killed task becomes "stopped"). Use IsTerminal rather than comparing
// against one vocabulary.
type TaskStatus string

// Task statuses, spanning both the task_updated and task_notification
// vocabularies.
const (
	TaskPending   TaskStatus = "pending"
	TaskRunning   TaskStatus = "running"
	TaskPaused    TaskStatus = "paused"
	TaskCompleted TaskStatus = "completed"
	TaskFailed    TaskStatus = "failed"
	// TaskStopped is the notification vocabulary's name for a task the caller
	// stopped; task_updated calls the same event "killed".
	TaskStopped TaskStatus = "stopped"
	TaskKilled  TaskStatus = "killed"
)

// TerminalTaskStatuses is the set of statuses after which a task produces no
// further updates. It spans both vocabularies deliberately.
var TerminalTaskStatuses = map[TaskStatus]bool{
	TaskCompleted: true,
	TaskFailed:    true,
	TaskStopped:   true,
	TaskKilled:    true,
}

// IsTerminal reports whether the task has finished for good.
//
// A consumer tracking active tasks must clear its entry on a terminal status
// from *either* a TaskUpdatedMessage or a TaskNotificationMessage. A stopped
// task reports "killed" via task_updated, and the matching notification is not
// guaranteed — so waiting only for the notification can leak the task forever.
func (s TaskStatus) IsTerminal() bool { return TerminalTaskStatuses[s] }

// TaskUsage is what a task has consumed so far.
type TaskUsage struct {
	TotalTokens int   `json:"total_tokens,omitempty"`
	ToolUses    int   `json:"tool_uses,omitempty"`
	DurationMS  int64 `json:"duration_ms,omitempty"`
}

// TaskStartedMessage announces a background task, as system/task_started.
type TaskStartedMessage struct {
	Subtype   string `json:"subtype"`
	TaskID    string `json:"task_id"`
	ToolUseID string `json:"tool_use_id,omitempty"`
	// Description is the human-readable label, e.g. "Sleep 20 seconds then echo".
	Description string `json:"description,omitempty"`
	// TaskType is the kind of task, e.g. "local_bash".
	TaskType string `json:"task_type,omitempty"`
	// SubagentType and WorkflowName are set for agent and workflow tasks.
	SubagentType string `json:"subagent_type,omitempty"`
	WorkflowName string `json:"workflow_name,omitempty"`

	SessionID string          `json:"session_id,omitempty"`
	UUID      string          `json:"uuid,omitempty"`
	Raw       json.RawMessage `json:"-"`
}

// TaskProgressMessage reports a running task's progress, as
// system/task_progress.
type TaskProgressMessage struct {
	Subtype      string `json:"subtype"`
	TaskID       string `json:"task_id"`
	ToolUseID    string `json:"tool_use_id,omitempty"`
	Description  string `json:"description,omitempty"`
	SubagentType string `json:"subagent_type,omitempty"`

	Usage TaskUsage `json:"usage,omitempty"`
	// LastToolName is the most recent tool the task ran.
	LastToolName string `json:"last_tool_name,omitempty"`
	Summary      string `json:"summary,omitempty"`

	SessionID string          `json:"session_id,omitempty"`
	UUID      string          `json:"uuid,omitempty"`
	Raw       json.RawMessage `json:"-"`
}

// TaskNotificationMessage reports a task's outcome, as
// system/task_notification.
type TaskNotificationMessage struct {
	Subtype   string     `json:"subtype"`
	TaskID    string     `json:"task_id"`
	ToolUseID string     `json:"tool_use_id,omitempty"`
	Status    TaskStatus `json:"status,omitempty"`

	// OutputFile is where the task's full output was written.
	OutputFile string    `json:"output_file,omitempty"`
	Summary    string    `json:"summary,omitempty"`
	Usage      TaskUsage `json:"usage,omitempty"`

	SessionID string          `json:"session_id,omitempty"`
	UUID      string          `json:"uuid,omitempty"`
	Raw       json.RawMessage `json:"-"`
}

// TaskUpdatedMessage carries a change to a task's state, as
// system/task_updated.
//
// This is the message a consumer cannot skip: a task stopped via TaskStop
// reports "killed" here, and the corresponding notification is not guaranteed
// to follow.
type TaskUpdatedMessage struct {
	Subtype string `json:"subtype"`
	TaskID  string `json:"task_id"`

	// Patch is the raw change, e.g. {"status":"completed","end_time":…}. It is
	// kept whole because the CLI puts arbitrary task fields in it.
	Patch json.RawMessage `json:"patch,omitempty"`

	// Status is the task's new state.
	//
	// CLI 2.1.224 sends it inside patch rather than at the top level, so
	// parseLine lifts it out; the tag stays so that a top-level status, which
	// the official SDKs' types declare, still decodes and wins over the patch.
	Status TaskStatus `json:"status,omitempty"`

	SessionID string          `json:"session_id,omitempty"`
	UUID      string          `json:"uuid,omitempty"`
	Raw       json.RawMessage `json:"-"`
}

// ─── Hook lifecycle messages ──────────────────────────────────────────────────

// HookLifecycleMessage reports a hook starting, progressing, or responding, as
// system/hook_started, system/hook_progress or system/hook_response.
//
// The CLI only emits these when hook events are enabled, so a session that
// configures hooks does not necessarily observe them (#31).
type HookLifecycleMessage struct {
	Subtype string `json:"subtype"`
	// HookEvent is the hook that fired, e.g. "PreToolUse".
	HookEvent string `json:"hook_event,omitempty"`

	SessionID string          `json:"session_id,omitempty"`
	UUID      string          `json:"uuid,omitempty"`
	Raw       json.RawMessage `json:"-"`
}

// ─── Rate limit event ─────────────────────────────────────────────────────────

// RateLimitStatus is whether requests are currently being served. It is a named
// string, not a closed enum — an unrecognised status decodes through.
type RateLimitStatus string

// Rate limit statuses.
const (
	RateLimitAllowed        RateLimitStatus = "allowed"
	RateLimitAllowedWarning RateLimitStatus = "allowed_warning"
	RateLimitRejected       RateLimitStatus = "rejected"
)

// RateLimitType is which limit window the report concerns. Open named string,
// for the same reason as RateLimitStatus.
type RateLimitType string

// Rate limit windows.
const (
	RateLimitFiveHour       RateLimitType = "five_hour"
	RateLimitSevenDay       RateLimitType = "seven_day"
	RateLimitSevenDayOpus   RateLimitType = "seven_day_opus"
	RateLimitSevenDaySonnet RateLimitType = "seven_day_sonnet"
	RateLimitOverage        RateLimitType = "overage"
)

// RateLimitInfo is the rate-limit state carried by a rate_limit_event.
//
// Its field names are camelCase on the wire — like modelUsage, and unlike the
// snake_case used by the surrounding message. Verified against a captured
// event.
type RateLimitInfo struct {
	Status RateLimitStatus `json:"status,omitempty"`
	// ResetsAt is a Unix timestamp in seconds.
	ResetsAt      int64         `json:"resetsAt,omitempty"`
	RateLimitType RateLimitType `json:"rateLimitType,omitempty"`
	// Utilization is the fraction of the window consumed, when sent.
	Utilization float64 `json:"utilization,omitempty"`

	// Overage fields describe billing beyond the included allowance.
	OverageStatus         string `json:"overageStatus,omitempty"`
	OverageResetsAt       int64  `json:"overageResetsAt,omitempty"`
	OverageDisabledReason string `json:"overageDisabledReason,omitempty"`
	IsUsingOverage        bool   `json:"isUsingOverage,omitempty"`

	ErrorCode string `json:"errorCode,omitempty"`

	// Raw keeps the whole info object, including fields not modelled here.
	Raw json.RawMessage `json:"-"`
}

// UnmarshalJSON keeps the verbatim payload in Raw before decoding.
func (i *RateLimitInfo) UnmarshalJSON(data []byte) error {
	i.Raw = append(i.Raw[:0], data...)

	type infoFields RateLimitInfo
	return json.Unmarshal(data, (*infoFields)(i))
}

// Limited reports whether requests are currently being refused.
func (i *RateLimitInfo) Limited() bool { return i.Status == RateLimitRejected }

// RateLimitEvent reports a change in rate-limit state. The CLI sends one at the
// start of a turn and whenever the state changes.
type RateLimitEvent struct {
	Type          MessageType   `json:"type"`
	RateLimitInfo RateLimitInfo `json:"rate_limit_info"`
	SessionID     string        `json:"session_id,omitempty"`
	UUID          string        `json:"uuid,omitempty"`
}

// ─── Top-level Event ──────────────────────────────────────────────────────────

// Event is the top-level value yielded from Query().
//
// Type is always set. The corresponding typed field is non-nil for known types:
//   - TypeAssistant       → Assistant
//   - TypeUser            → User
//   - TypeStreamEvent     → StreamEvent
//   - TypeResult          → Result
//   - TypeSystem          → System
//   - TypeRateLimitEvent  → RateLimit
//   - TypeToolProgress    → ToolProgress
//
// System messages additionally populate one of the lifecycle fields according
// to their subtype: task_started → TaskStarted, task_progress → TaskProgress,
// task_notification → TaskNotification, task_updated → TaskUpdated, and the
// hook_* subtypes → HookLifecycle. System is always set alongside them.
//
// For unknown types, only Raw is set so callers can handle
// forward-compatibility themselves.
type Event struct {
	Type         MessageType
	Assistant    *AssistantMessage
	User         *UserMessage
	StreamEvent  *StreamEventMessage
	Result       *Result
	System       *SystemMessage
	ToolProgress *ToolProgressMessage
	RateLimit    *RateLimitEvent

	// Task lifecycle, populated from the matching system subtype.
	TaskStarted      *TaskStartedMessage
	TaskProgress     *TaskProgressMessage
	TaskNotification *TaskNotificationMessage
	TaskUpdated      *TaskUpdatedMessage

	// HookLifecycle is populated from the hook_started/hook_progress/
	// hook_response subtypes.
	HookLifecycle *HookLifecycleMessage

	Raw json.RawMessage

	// DecodeErr records why a typed field decoded only partially, if it did.
	// Typed fields are best-effort: on a type mismatch the fields that did
	// decode are kept and this is set, rather than the whole message being
	// discarded. Raw is always the authoritative payload.
	DecodeErr error `json:"-"`
}
