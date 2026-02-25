package claude

import (
	"context"
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
	events    chan Event
	write     func(any) error
	ctx       context.Context
	interrupt func() // graceful shutdown trigger (idempotent)

	// pending maps request_id → response channel for blocking control requests.
	pending   map[string]chan controlResponse
	pendingMu sync.Mutex
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
		"permission_mode": string(mode),
	})
}

// SetMaxThinkingTokens asks the claude CLI to update the max thinking token budget.
// Blocks until the CLI acknowledges the change or the context is cancelled.
func (s *Stream) SetMaxThinkingTokens(n int) error {
	return s.sendControlRequest("set_max_thinking_tokens", map[string]any{
		"max_thinking_tokens": n,
	})
}

// Interrupt initiates graceful shutdown of the session: stdin is closed and
// SIGTERM is sent to the claude subprocess. If the process does not exit within
// 5 seconds, SIGKILL is sent. Interrupt is idempotent.
func (s *Stream) Interrupt() error {
	s.interrupt()
	return nil
}

// sendControlRequest writes a control_request with the given subtype and extra
// fields, then blocks until a matching control_response arrives or the ctx
// is cancelled.
func (s *Stream) sendControlRequest(subtype string, extras map[string]any) error {
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
		return fmt.Errorf("claude: %s: %w", subtype, err)
	}

	select {
	case resp := <-respCh:
		if !resp.Success {
			return fmt.Errorf("claude: %s: %s", subtype, resp.Error)
		}
		return nil
	case <-s.ctx.Done():
		s.pendingMu.Lock()
		delete(s.pending, reqID)
		s.pendingMu.Unlock()
		return s.ctx.Err()
	}
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

	for event := range stream.Events() {
		switch event.Type {

		case TypeResult:
			r := event.Result
			if r.IsError {
				msg := r.Subtype
				if len(r.Errors) > 0 {
					msg = strings.Join(r.Errors, "; ")
				}
				return nil, fmt.Errorf("claude: agent error (%s): %s", r.Subtype, msg)
			}
			return r, nil

		case TypeSystem:
			// Surface process-level errors (bad flag, auth failure, crash) that
			// were synthesised by spawnAndStream because no result message arrived.
			if event.System != nil && event.System.Subtype == "error" {
				return nil, fmt.Errorf("claude: %s", event.System.Message)
			}
		}
	}

	return nil, fmt.Errorf("claude: agent finished without a result message")
}
