package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// Stream represents an active claude subprocess streaming session.
//
// Call Events() to range over the stream of events. The channel is closed when
// the agent finishes, the subprocess exits, or the context is cancelled.
//
// Control methods (SetModel, SetPermissionMode, SetMaxThinkingTokens, Interrupt)
// may be called concurrently from any goroutine while the stream is active.
type Stream struct {
	events   chan Event
	write    func(any) error
	ctx      context.Context
	shutdown func() // graceful shutdown trigger (idempotent); Close only, never Interrupt

	// pending maps request_id → response channel for blocking control requests.
	pending   map[string]chan controlResponse
	pendingMu sync.Mutex

	// initResp caches the initialize control response. It is written once in
	// spawnAndStream before the Stream is handed to the caller, so reads need no
	// synchronisation.
	initResp *initializeResponse

	// capabilities is captured from the system/init event rather than the
	// initialize response, which does not carry it. It therefore arrives with
	// the first turn, so access is mutex-guarded — see Capabilities.
	capabilities   []string
	capabilitiesMu sync.RWMutex
}

// Events returns the receive-only channel of events streamed from the subprocess.
// The channel is closed when the session ends. Callers should always range until
// the channel closes.
func (s *Stream) Events() <-chan Event {
	return s.events
}

// SetModel asks the claude CLI to switch to a different model mid-session.
// Blocks until the CLI acknowledges the change or the context is cancelled.
func (s *Stream) SetModel(model string) error {
	return s.sendControlRequest("set_model", map[string]any{"model": model})
}

// SetPermissionMode asks the claude CLI to change the permission mode mid-session.
// Blocks until the CLI acknowledges the change or the context is cancelled.
func (s *Stream) SetPermissionMode(mode PermissionMode) error {
	return s.sendControlRequest("set_permission_mode", map[string]any{
		"mode": string(mode),
	})
}

// SetMaxThinkingTokens asks the claude CLI to update the max thinking token budget.
// Blocks until the CLI acknowledges the change or the context is cancelled.
func (s *Stream) SetMaxThinkingTokens(n int) error {
	return s.sendControlRequest("set_max_thinking_tokens", map[string]any{
		"max_thinking_tokens": n,
	})
}

// InterruptReceipt is the result of an interrupt operation.
//
// The CLI advertises it through the interrupt_receipt_v1 capability on
// system/init; CLIs without that capability reply to an interrupt with an empty
// success and no receipt at all, which is reported as a nil *InterruptReceipt
// rather than an error.
type InterruptReceipt struct {
	// StillQueued holds the uuids of async user messages that survived the
	// interrupt and remain queued for a subsequent turn. It is empty when
	// nothing survived.
	StillQueued []string `json:"still_queued"`
}

// decodeInterruptReceipt extracts the receipt from an interrupt control_response
// body. Decoding is deliberately lenient: an interrupt that the CLI acknowledged
// has succeeded regardless of what the payload looks like, so anything absent,
// null, unparseable, or of an unexpected type yields a nil receipt instead of an
// error. Only a well-formed still_queued array produces one, matching the
// reference implementation, which returns a receipt solely when still_queued is
// an array and keeps just its string elements.
func decodeInterruptReceipt(body json.RawMessage) *InterruptReceipt {
	if len(body) == 0 {
		return nil
	}
	var payload struct {
		StillQueued []json.RawMessage `json:"still_queued"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.StillQueued == nil {
		return nil
	}
	queued := make([]string, 0, len(payload.StillQueued))
	for _, raw := range payload.StillQueued {
		// Decode into any and type-assert rather than unmarshalling straight
		// into a string: JSON null unmarshals into a string as a no-op, which
		// would smuggle in an empty id. This mirrors the reference's
		// `typeof r === "string"` filter.
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			continue
		}
		if id, ok := v.(string); ok {
			queued = append(queued, id)
		}
	}
	return &InterruptReceipt{StillQueued: queued}
}

// Interrupt aborts the turn currently in progress and leaves the session alive:
// it sends an interrupt control request and blocks until the CLI acknowledges
// it. The subprocess keeps running, Events stays open, and the next
// SendUserMessage (or Session.Send) starts a new turn on the same conversation.
//
// To terminate the session instead, call Close.
//
// The returned receipt lists async user messages that survived the interrupt and
// are still queued. It is nil when the CLI sends no receipt — older CLIs reply
// with an empty success, and only those advertising the interrupt_receipt_v1
// capability populate it. A missing receipt is not an error.
func (s *Stream) Interrupt() (*InterruptReceipt, error) {
	body, err := s.sendControlRequestWithResponse("interrupt", nil)
	if err != nil {
		return nil, err
	}
	return decodeInterruptReceipt(body), nil
}

// Close terminates the session: stdin is closed and SIGTERM is sent to the
// claude subprocess. If the process does not exit within 5 seconds, SIGKILL is
// sent. Close is idempotent and closes the Events channel.
//
// To abort the current turn while keeping the session usable, call Interrupt.
func (s *Stream) Close() error {
	s.shutdown()
	return nil
}

// SendUserMessage injects an additional user message into the running subprocess.
// In single-turn (Query/Run) usage this can be called mid-stream (before TypeResult
// is emitted) to inject extra context — matching TypeScript's streamInput().
// For persistent multi-turn usage prefer Session.Send which wraps this method.
func (s *Stream) SendUserMessage(msg string) error {
	return s.write(userMsg(msg))
}

// RewindFiles asks the CLI to rewind files to the state at the given user message ID.
func (s *Stream) RewindFiles(userMessageID string) error {
	return s.sendControlRequest("rewind_files", map[string]any{
		"user_message_id": userMessageID,
	})
}

// ReconnectMcpServer asks the CLI to reconnect a named MCP server.
func (s *Stream) ReconnectMcpServer(serverName string) error {
	return s.sendControlRequest("reconnect_mcp_server", map[string]any{
		"server_name": serverName,
	})
}

// ToggleMcpServer asks the CLI to enable or disable a named MCP server.
func (s *Stream) ToggleMcpServer(serverName string, enabled bool) error {
	return s.sendControlRequest("toggle_mcp_server", map[string]any{
		"server_name": serverName,
		"enabled":     enabled,
	})
}

// SetMcpServers asks the CLI to replace the current MCP server configuration.
func (s *Stream) SetMcpServers(servers map[string]any) error {
	return s.sendControlRequest("set_mcp_servers", map[string]any{
		"mcp_servers": servers,
	})
}

// SupportedModels returns the models the connected CLI offers.
//
// The value is read from the initialize handshake completed when the session
// started: this performs no I/O, never blocks, and is safe to call concurrently.
func (s *Stream) SupportedModels() []ModelInfo {
	if s.initResp == nil {
		return nil
	}
	return s.initResp.Models
}

// SupportedCommands returns the slash commands available in this session.
//
// Read from the initialize handshake: no I/O, never blocks, concurrency-safe.
func (s *Stream) SupportedCommands() []SlashCommand {
	if s.initResp == nil {
		return nil
	}
	return s.initResp.Commands
}

// SupportedAgents returns the subagent types this session can dispatch to.
//
// Read from the initialize handshake: no I/O, never blocks, concurrency-safe.
func (s *Stream) SupportedAgents() []AgentInfo {
	if s.initResp == nil {
		return nil
	}
	return s.initResp.Agents
}

// AccountInfo returns the account the CLI is authenticated as.
//
// Read from the initialize handshake: no I/O, never blocks, concurrency-safe.
func (s *Stream) AccountInfo() AccountInfo {
	if s.initResp == nil {
		return AccountInfo{}
	}
	return s.initResp.Account
}

// Capabilities returns the protocol capabilities the connected CLI advertises,
// for feature detection:
//
//	if slices.Contains(stream.Capabilities(), "interrupt_receipt_v1") { … }
//
// Unlike the other accessors this is NOT part of the initialize response — the
// CLI advertises it on the system/init event instead, which arrives with the
// first turn. It therefore returns nil until a turn has started. Treat an empty
// list as "not yet known", never as "the CLI supports nothing".
func (s *Stream) Capabilities() []string {
	s.capabilitiesMu.RLock()
	defer s.capabilitiesMu.RUnlock()
	if s.capabilities == nil {
		return nil
	}
	return append([]string(nil), s.capabilities...)
}

// OutputStyle returns the session's output style, and the styles available.
func (s *Stream) OutputStyle() (style string, available []string) {
	if s.initResp == nil {
		return "", nil
	}
	return s.initResp.OutputStyle, s.initResp.AvailableOutputStyles
}

// setCapabilities records the capability list seen on a system/init event.
func (s *Stream) setCapabilities(caps []string) {
	s.capabilitiesMu.Lock()
	defer s.capabilitiesMu.Unlock()
	s.capabilities = caps
}

// StopTask asks the CLI to stop a running background task.
func (s *Stream) StopTask(taskID string) error {
	return s.sendControlRequest("stop_task", map[string]any{
		"task_id": taskID,
	})
}

// sendControlRequestWithResponse is like sendControlRequest but returns the raw
// JSON response body on success.
func (s *Stream) sendControlRequestWithResponse(subtype string, extras map[string]any) (json.RawMessage, error) {
	reqID := newUUID()
	respCh := make(chan controlResponse, 1)

	s.pendingMu.Lock()
	s.pending[reqID] = respCh
	s.pendingMu.Unlock()

	req := map[string]any{"subtype": subtype}
	for k, v := range extras {
		req[k] = v
	}

	err := s.write(map[string]any{
		"type":       "control_request",
		"request_id": reqID,
		"request":    req,
	})
	if err != nil {
		s.pendingMu.Lock()
		delete(s.pending, reqID)
		s.pendingMu.Unlock()
		return nil, fmt.Errorf("claude: %s: %w", subtype, err)
	}

	select {
	case resp := <-respCh:
		if !resp.Success {
			return nil, fmt.Errorf("claude: %s: %s", subtype, resp.Error)
		}
		return resp.Body, nil
	case <-s.ctx.Done():
		s.pendingMu.Lock()
		delete(s.pending, reqID)
		s.pendingMu.Unlock()
		return nil, s.ctx.Err()
	}
}

// sendControlRequest writes a control_request with the given subtype and extra
// fields, then blocks until a matching control_response arrives or the ctx
// is cancelled.
func (s *Stream) sendControlRequest(subtype string, extras map[string]any) error {
	_, err := s.sendControlRequestWithResponse(subtype, extras)
	return err
}

// Query runs the claude agent with the given prompt and returns a *Stream for
// real-time event processing.
//
// The Stream.Events() channel is closed when the agent emits a TypeResult
// message, the subprocess exits, or ctx is cancelled. Callers should always
// range over the channel until it is closed.
//
// Stream control methods (SetModel, SetPermissionMode, SetMaxThinkingTokens,
// Interrupt) may be called at any time while the stream is active.
//
// Example — stream all events:
//
//	stream, err := claude.Query(ctx, "What is 2+2?")
//	if err != nil { ... }
//	for event := range stream.Events() {
//	    switch event.Type {
//	    case claude.TypeAssistant:
//	        fmt.Print(event.Assistant.Text())
//	    case claude.TypeResult:
//	        fmt.Println("session:", event.Result.SessionID)
//	    }
//	}
func Query(ctx context.Context, prompt string, opts ...Option) (*Stream, error) {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}
	return spawnAndStream(ctx, o, prompt)
}

// Run is a convenience wrapper around Query that blocks until the agent
// finishes and returns only the final Result.
//
// Intermediate events (streaming deltas, system messages, rate-limit events)
// are discarded. Use Query directly if you need to process them.
//
// Errors from the subprocess itself (bad flags, auth failures, crashes) are
// surfaced as Go errors so callers always get a meaningful message.
//
// Example:
//
//	result, err := claude.Run(ctx, "What is 2+2?",
//	    claude.WithModel("claude-haiku-4-5-20251001"),
//	    claude.WithThinking(claude.ThinkingDisabled),
//	)
//	if err != nil { ... }
//	fmt.Println(result.Result)
//	fmt.Println("session:", result.SessionID)
func Run(ctx context.Context, prompt string, opts ...Option) (*Result, error) {
	stream, err := Query(ctx, prompt, opts...)
	if err != nil {
		return nil, err
	}
	return resultFromStream(stream)
}

// resultError renders a failed result as a Go error, surfacing the fields a
// caller would otherwise have to reach for Query() to see: why the loop ended,
// and the HTTP status when an upstream call was what failed.
func resultError(r *Result) error {
	var detail strings.Builder
	detail.WriteString(r.Subtype)
	if r.TerminalReason != "" {
		fmt.Fprintf(&detail, ", %s", r.TerminalReason)
	}
	if r.APIErrorStatus != nil {
		fmt.Fprintf(&detail, ", HTTP %d", *r.APIErrorStatus)
	}

	// The CLI does not always send an errors list; repeating the subtype as the
	// message when it doesn't adds nothing the detail above hasn't said.
	if len(r.Errors) == 0 {
		return fmt.Errorf("claude: agent error (%s)", detail.String())
	}
	return fmt.Errorf("claude: agent error (%s): %s", detail.String(), strings.Join(r.Errors, "; "))
}

// resultFromStream drains a stream and returns its final result, converting
// error results and process-level failures into Go errors.
func resultFromStream(stream *Stream) (*Result, error) {
	for event := range stream.Events() {
		switch event.Type {

		case TypeResult:
			r := event.Result
			// parseLine leaves Result nil when the payload does not decode.
			// Report that instead of dereferencing it — a library must not
			// panic because the CLI sent a field shape we do not model yet.
			if r == nil {
				return nil, fmt.Errorf("claude: could not decode the result message: %s",
					truncate(string(event.Raw), 512))
			}
			if r.IsError {
				return nil, resultError(r)
			}
			return r, nil

		case TypeSystem:
			// Surface process-level errors (bad flag, auth failure, crash) that
			// were synthesised by spawnAndStream because no result message arrived.
			if event.System != nil && event.System.Subtype == "error" {
				return nil, fmt.Errorf("claude: %s", event.System.Error)
			}
		}
	}

	return nil, fmt.Errorf("claude: agent finished without a result message")
}

// truncate shortens s for inclusion in an error message. A result payload runs
// to several kilobytes; the head is enough to identify the offending shape.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "… (truncated)"
}
