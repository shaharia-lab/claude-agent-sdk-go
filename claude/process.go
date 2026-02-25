package claude

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// controlResponse is used internally to correlate responses to Stream control requests.
type controlResponse struct {
	Success bool
	Error   string
}

// spawnAndStream starts the claude subprocess in bidirectional JSON-lines mode
// (--input-format stream-json --output-format stream-json --verbose) — the same
// protocol used by @anthropic-ai/claude-agent-sdk. No --print flag is used.
//
// On startup, an initialize control_request is written to stdin, followed by the
// user message. claude's responses stream on stdout as JSON lines.
//
// Graceful shutdown (mirrors TS SDK close() behaviour):
//   - On ctx cancellation or Stream.Interrupt(): stdin is closed, SIGTERM is sent.
//   - If the process has not exited after 5 s: SIGKILL is sent.
//
// The Stream.Events() channel is closed when a TypeResult message is received,
// the subprocess exits, or ctx is cancelled. Callers should always range until
// the channel closes.
func spawnAndStream(ctx context.Context, opts *Options, prompt string) (*Stream, error) {
	args := opts.buildArgs()

	cmd := exec.Command(opts.ClaudeExecutable, args...)
	cmd.Env = buildEnv(opts)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("claude: stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("claude: stdout pipe: %w", err)
	}

	// Capture stderr so we can include it in error messages on non-zero exit.
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("claude: start %q: %w", opts.ClaudeExecutable, err)
	}

	// write serialises v as a JSON line and sends it to stdin.
	// It is safe to call from multiple goroutines.
	var stdinMu sync.Mutex
	write := func(v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		b = append(b, '\n')
		stdinMu.Lock()
		defer stdinMu.Unlock()
		_, err = stdin.Write(b)
		return err
	}

	// Build hooks config and registry from options.
	hooksConfig, hookReg := buildHooksForInitialize(opts.Hooks)

	// Send the initialize message. System prompt, MCP servers, agents, and hooks
	// are passed here (not as CLI flags) so they work in bidirectional mode.
	if err := write(initializeMsg(opts, hooksConfig)); err != nil {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("claude: initialize: %w", err)
	}

	// Send the user message (the prompt).
	if err := write(userMsg(prompt)); err != nil {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("claude: user message: %w", err)
	}

	// Create the Stream struct. The goroutines below close over it.
	stream := &Stream{
		events:  make(chan Event, 32),
		write:   write,
		ctx:     ctx,
		pending: make(map[string]chan controlResponse),
	}

	// interruptOnce / interruptCh enable Stream.Interrupt() to trigger graceful shutdown.
	var interruptOnce sync.Once
	interruptCh := make(chan struct{})
	stream.interrupt = func() {
		interruptOnce.Do(func() { close(interruptCh) })
	}

	// closeStdin closes the subprocess stdin (used on graceful shutdown).
	closeStdin := func() {
		stdinMu.Lock()
		defer stdinMu.Unlock()
		stdin.Close()
	}

	// procDone is closed by the reader goroutine after cmd.Wait() returns.
	procDone := make(chan struct{})

	// Graceful shutdown goroutine — mirrors TypeScript SDK close():
	//   this.processStdin.end()
	//   this.process.kill("SIGTERM")
	//   setTimeout(() => this.process.kill("SIGKILL"), 5000)
	go func() {
		select {
		case <-ctx.Done():
			stream.interrupt() // normalise to interruptCh
		case <-interruptCh:
		case <-procDone:
			return
		}
		closeStdin()
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
		case <-procDone:
		}
	}()

	// Reader goroutine: reads stdout line by line, handles control messages from
	// claude, and forwards all other events to stream.events.
	go func() {
		defer close(stream.events)
		defer close(procDone)

		scanner := bufio.NewScanner(stdout)
		// 4 MB buffer — assistant messages with long content can be large.
		scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)

		gotResult := false
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}

			// Peek at the message type for fast routing.
			var typeCheck struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(line, &typeCheck); err != nil {
				continue // skip non-JSON lines
			}

			switch typeCheck.Type {
			case "control_request":
				// control_request messages (can_use_tool, hook_callback, etc.) require
				// a response on stdin and must not be forwarded to the caller.
				handleControlRequest(line, write, opts, hookReg)
				continue

			case "control_response":
				// control_response messages are replies to our set_model /
				// set_permission_mode / etc. requests. Route to the pending map.
				routeControlResponse(line, stream)
				continue
			}

			event, err := parseLine(line)
			if err != nil {
				continue // skip malformed lines
			}

			select {
			case stream.events <- event:
			case <-ctx.Done():
				return
			}

			if event.Type == TypeResult {
				gotResult = true
				closeStdin()
				break
			}
		}

		if err := scanner.Err(); err != nil {
			sendEvent(ctx, stream.events, errorEvent(fmt.Sprintf("stdout read error: %v", err)))
		}

		// Surface stderr on unexpected exit (bad flag, auth error, crash, etc.).
		if err := cmd.Wait(); err != nil && !gotResult {
			stderr := strings.TrimSpace(stderrBuf.String())
			msg := err.Error()
			if stderr != "" {
				msg = stderr
			}
			sendEvent(ctx, stream.events, errorEvent(msg))
		}
	}()

	return stream, nil
}

// handleControlRequest inspects a raw JSON line from claude's stdout to see if
// it is a control_request. If so it writes the appropriate control_response to
// stdin. Returns false and does nothing for non-control_request messages.
func handleControlRequest(line []byte, write func(any) error, opts *Options, hookReg hookRegistry) {
	var envelope struct {
		Type      string `json:"type"`
		RequestID string `json:"request_id"`
		Request   struct {
			Subtype string `json:"subtype"`

			// can_use_tool fields
			ToolName       string            `json:"tool_name"`
			ToolUseID      string            `json:"tool_use_id"`
			Input          json.RawMessage   `json:"input"`
			Suggestions    []PermissionUpdate `json:"permission_suggestions,omitempty"`
			BlockedPath    string            `json:"blocked_path,omitempty"`
			DecisionReason string            `json:"decision_reason,omitempty"`
			AgentID        string            `json:"agent_id,omitempty"`

			// hook_callback fields
			CallbackID string    `json:"callback_id,omitempty"`
			HookEvent  HookEvent `json:"hook_event,omitempty"`

			// set_model / set_permission_mode / set_max_thinking_tokens
			Model             string `json:"model,omitempty"`
			PermissionMode    string `json:"permission_mode,omitempty"`
			MaxThinkingTokens int    `json:"max_thinking_tokens,omitempty"`
		} `json:"request"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		return
	}

	switch envelope.Request.Subtype {
	case "can_use_tool":
		result := PermissionResult{Behavior: "allow"}
		if opts.PermissionHandler != nil {
			permCtx := PermissionContext{
				Suggestions:    envelope.Request.Suggestions,
				BlockedPath:    envelope.Request.BlockedPath,
				DecisionReason: envelope.Request.DecisionReason,
				ToolUseID:      envelope.Request.ToolUseID,
				AgentID:        envelope.Request.AgentID,
			}
			result = opts.PermissionHandler(envelope.Request.ToolName, envelope.Request.Input, permCtx)
		}
		allowed := result.Behavior != "deny"
		resp := map[string]any{
			"allowed":   allowed,
			"toolUseID": envelope.Request.ToolUseID,
		}
		if result.UpdatedInput != nil {
			resp["updatedInput"] = result.UpdatedInput
		}
		if len(result.UpdatedPermissions) > 0 {
			resp["updatedPermissions"] = result.UpdatedPermissions
		}
		if result.Message != "" {
			resp["message"] = result.Message
		}
		if result.Interrupt {
			resp["interrupt"] = true
		}
		_ = write(map[string]any{
			"type": "control_response",
			"response": map[string]any{
				"subtype":    "success",
				"request_id": envelope.RequestID,
				"response":   resp,
			},
		})

	case "hook_callback":
		var output *HookOutput
		if fn, ok := hookReg[envelope.Request.CallbackID]; ok {
			var err error
			output, err = fn(envelope.Request.HookEvent, envelope.Request.Input, envelope.Request.ToolUseID)
			if err != nil {
				_ = write(map[string]any{
					"type": "control_response",
					"response": map[string]any{
						"subtype":    "error",
						"request_id": envelope.RequestID,
						"error":      err.Error(),
					},
				})
				return
			}
		}
		resp := map[string]any{
			"subtype":    "success",
			"request_id": envelope.RequestID,
		}
		if output != nil {
			resp["response"] = output
		}
		_ = write(map[string]any{
			"type":     "control_response",
			"response": resp,
		})

	default:
		// set_model, set_permission_mode, set_max_thinking_tokens, mcp_message:
		// These are read-only notifications from the CLI. Acknowledge silently.
		_ = write(map[string]any{
			"type": "control_response",
			"response": map[string]any{
				"subtype":    "success",
				"request_id": envelope.RequestID,
			},
		})
	}
}

// routeControlResponse routes a control_response message (a reply from claude to
// one of our set_model / set_permission_mode / etc. requests) to the waiting caller.
func routeControlResponse(line []byte, s *Stream) {
	var envelope struct {
		Type      string `json:"type"`
		RequestID string `json:"request_id"`
		Response  struct {
			Subtype string `json:"subtype"`
			Error   string `json:"error,omitempty"`
		} `json:"response"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		return
	}

	// Also check the inner response.request_id pattern used in some CLI versions.
	reqID := envelope.RequestID
	if reqID == "" {
		return
	}

	s.pendingMu.Lock()
	ch, ok := s.pending[reqID]
	if ok {
		delete(s.pending, reqID)
	}
	s.pendingMu.Unlock()

	if ok {
		select {
		case ch <- controlResponse{
			Success: envelope.Response.Subtype != "error",
			Error:   envelope.Response.Error,
		}:
		default:
		}
	}
}

// ─── Stdin message helpers ────────────────────────────────────────────────────

// initializeMsg builds the control_request initialize message sent to stdin at
// session start. This is how system prompt, MCP servers, agents, hooks, and
// output format are passed in bidirectional mode, matching the TS SDK behaviour.
func initializeMsg(opts *Options, hooksConfig map[string]any) any {
	servers := any(map[string]any{})
	if len(opts.McpServers) > 0 {
		servers = opts.McpServers
	}

	agents := any(map[string]any{})
	if len(opts.Agents) > 0 {
		m := make(map[string]any, len(opts.Agents))
		for k, v := range opts.Agents {
			m[k] = v
		}
		agents = m
	}

	req := map[string]any{
		"subtype":            "initialize",
		"systemPrompt":       opts.SystemPrompt,
		"appendSystemPrompt": opts.AppendSystemPrompt,
		"sdkMcpServers":      servers,
		"hooks":              hooksConfig,
		"agents":             agents,
		"promptSuggestions":  false,
	}

	if opts.OutputFormat != nil {
		req["outputFormat"] = opts.OutputFormat.Type
		if opts.OutputFormat.Schema != nil {
			req["jsonSchema"] = opts.OutputFormat.Schema
		}
	}

	if opts.Sandbox != nil {
		req["sandbox"] = opts.Sandbox
	}

	return map[string]any{
		"type":       "control_request",
		"request_id": newUUID(),
		"request":    req,
	}
}

// userMsg builds the user message sent to stdin.
func userMsg(prompt string) any {
	return map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": prompt,
		},
		"parent_tool_use_id": nil,
		"session_id":         "",
	}
}

// ─── Environment ─────────────────────────────────────────────────────────────

// buildEnv returns the environment for the claude subprocess.
//   - Inherits all parent env vars (Claude Code OAuth session is passed through).
//   - Strips CLAUDECODE so the subprocess can launch even inside an existing session
//     (mirrors `delete process.env.CLAUDECODE` in agent.ts).
//   - Strips CLAUDE_CODE_ENTRYPOINT so we can set our own.
//   - Sets CLAUDE_CODE_ENTRYPOINT=sdk-go for Anthropic telemetry.
//   - Sets MAX_THINKING_TOKENS=0 when ThinkingDisabled (documented way to disable thinking).
//   - Merges opts.Env (user-supplied extra vars, applied last so they win).
func buildEnv(opts *Options) []string {
	parent := os.Environ()
	out := make([]string, 0, len(parent)+3+len(opts.Env))
	for _, e := range parent {
		switch {
		case strings.HasPrefix(e, "CLAUDECODE="),
			strings.HasPrefix(e, "CLAUDE_CODE_ENTRYPOINT="),
			strings.HasPrefix(e, "MAX_THINKING_TOKENS="):
			continue
		}
		// Also strip any user-supplied keys so they can override.
		if idx := strings.IndexByte(e, '='); idx > 0 {
			if _, overridden := opts.Env[e[:idx]]; overridden {
				continue
			}
		}
		out = append(out, e)
	}
	out = append(out, "CLAUDE_CODE_ENTRYPOINT=sdk-go")
	if opts.Thinking == ThinkingDisabled {
		out = append(out, "MAX_THINKING_TOKENS=0")
	} else if opts.MaxThinkingTokens > 0 {
		out = append(out, fmt.Sprintf("MAX_THINKING_TOKENS=%d", opts.MaxThinkingTokens))
	}
	// Merge user-supplied env vars (last so they take precedence).
	for k, v := range opts.Env {
		out = append(out, k+"="+v)
	}
	return out
}

// ─── JSON-line parser ─────────────────────────────────────────────────────────

// parseLine decodes one JSON line from stdout into an Event.
// Unknown types are returned with only Type and Raw set.
func parseLine(line []byte) (Event, error) {
	var envelope struct {
		Type MessageType `json:"type"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		return Event{}, fmt.Errorf("not JSON: %w", err)
	}

	raw := make(json.RawMessage, len(line))
	copy(raw, line)
	event := Event{Type: envelope.Type, Raw: raw}

	switch envelope.Type {
	case TypeAssistant:
		var m AssistantMessage
		if err := json.Unmarshal(line, &m); err == nil {
			event.Assistant = &m
		}
	case TypeStreamEvent:
		var m StreamEventMessage
		if err := json.Unmarshal(line, &m); err == nil {
			event.StreamEvent = &m
		}
	case TypeResult:
		var m Result
		if err := json.Unmarshal(line, &m); err == nil {
			event.Result = &m
		}
	case TypeSystem:
		var m SystemMessage
		if err := json.Unmarshal(line, &m); err == nil {
			event.System = &m
		}
	// TypeRateLimitEvent and future types: Raw only.
	}

	return event, nil
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// errorEvent builds a synthetic TypeSystem/error event for process-level failures.
func errorEvent(msg string) Event {
	return Event{
		Type: TypeSystem,
		System: &SystemMessage{
			Type:    TypeSystem,
			Subtype: "error",
			Message: msg,
		},
	}
}

// sendEvent delivers an event to ch, dropping it if ctx is already done.
func sendEvent(ctx context.Context, ch chan<- Event, e Event) {
	select {
	case ch <- e:
	case <-ctx.Done():
	}
}

// newUUID generates a random UUID v4.
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant RFC 4122
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
