package claude

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
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
	Body    json.RawMessage
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
	if err := opts.validate(); err != nil {
		return nil, err
	}
	warnPermissionHandlerShadowed(opts)

	args := opts.buildArgs()

	cmd := exec.Command(opts.ClaudeExecutable, args...)
	cmd.Env = buildEnv(opts)
	if opts.CWD != "" {
		cmd.Dir = opts.CWD
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("claude: stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("claude: stdout pipe: %w", err)
	}

	// Capture stderr. When opts.Stderr is set, each line is forwarded to the
	// callback in addition to being buffered for error reporting.
	var stderrBuf bytes.Buffer
	if opts.Stderr != nil {
		cmd.Stderr = io.MultiWriter(&stderrBuf, &stderrLineWriter{fn: opts.Stderr})
	} else {
		cmd.Stderr = &stderrBuf
	}

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

	// Send the user message (the prompt), unless we're in session mode
	// (the caller will send the first message via Session.Send).
	if !opts.sessionMode && prompt != "" {
		if err := write(userMsg(prompt)); err != nil {
			_ = cmd.Process.Kill()
			return nil, fmt.Errorf("claude: user message: %w", err)
		}
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
				if opts.sessionMode {
					// Emit TypeResult to signal "turn done" but keep stdin open
					// and the scanner running so the subprocess stays alive for the next Send().
					// Do NOT closeStdin() — the session lives on.
				} else {
					gotResult = true
					closeStdin()
					break
				}
			}
		}

		if err := scanner.Err(); err != nil {
			sendEvent(ctx, stream.events, errorEvent(fmt.Sprintf("stdout read error: %v", err)))
		}

		// Surface stderr on unexpected exit (bad flag, auth error, crash, etc.).
		if err := cmd.Wait(); err != nil && !gotResult {
			// In session mode suppress the error when Close()/Interrupt() was called
			// (expected shutdown) or the context was cancelled.
			interrupted := false
			select {
			case <-interruptCh:
				interrupted = true
			default:
			}
			if !interrupted && ctx.Err() == nil {
				stderr := strings.TrimSpace(stderrBuf.String())
				msg := err.Error()
				if stderr != "" {
					msg = stderr
				}
				sendEvent(ctx, stream.events, errorEvent(msg))
			}
		}
	}()

	return stream, nil
}

// writeControlError answers a control_request with an error control_response.
// Every inbound request must be answered — a missing reply hangs the CLI.
func writeControlError(write func(any) error, requestID, msg string) {
	_ = write(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "error",
			"request_id": requestID,
			"error":      msg,
		},
	})
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
			ToolName       string             `json:"tool_name"`
			ToolUseID      string             `json:"tool_use_id"`
			Input          json.RawMessage    `json:"input"`
			Suggestions    []PermissionUpdate `json:"permission_suggestions,omitempty"`
			BlockedPath    string             `json:"blocked_path,omitempty"`
			DecisionReason string             `json:"decision_reason,omitempty"`
			AgentID        string             `json:"agent_id,omitempty"`
			Title          string             `json:"title,omitempty"`
			DisplayName    string             `json:"display_name,omitempty"`
			Description    string             `json:"description,omitempty"`

			// hook_callback fields
			CallbackID string `json:"callback_id,omitempty"`

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
		// Fail closed. Answering a permission question nobody was asked would
		// grant the tool call, so an absent handler is an error, never an allow.
		if opts.PermissionHandler == nil {
			writeControlError(write, envelope.RequestID, "canUseTool callback is not provided")
			return
		}

		permCtx := PermissionContext{
			Suggestions:    envelope.Request.Suggestions,
			BlockedPath:    envelope.Request.BlockedPath,
			DecisionReason: envelope.Request.DecisionReason,
			ToolUseID:      envelope.Request.ToolUseID,
			AgentID:        envelope.Request.AgentID,
			Title:          envelope.Request.Title,
			DisplayName:    envelope.Request.DisplayName,
			Description:    envelope.Request.Description,
		}
		result := opts.PermissionHandler(envelope.Request.ToolName, envelope.Request.Input, permCtx)

		var resp map[string]any
		switch result.Behavior {
		case "allow":
			resp = map[string]any{"behavior": "allow"}
			// The CLI expects the input it should actually run; when the handler
			// does not rewrite it, echo the original back verbatim.
			if result.UpdatedInput != nil {
				resp["updatedInput"] = result.UpdatedInput
			} else {
				resp["updatedInput"] = envelope.Request.Input
			}
			if len(result.UpdatedPermissions) > 0 {
				resp["updatedPermissions"] = result.UpdatedPermissions
			}
		case "deny":
			resp = map[string]any{
				"behavior": "deny",
				"message":  result.Message,
			}
			if result.Interrupt {
				resp["interrupt"] = true
			}
		default:
			// Includes the zero value: an unset Behavior is a usage error, and
			// treating it as "allow" is exactly the fail-open bug being fixed.
			writeControlError(write, envelope.RequestID, fmt.Sprintf(
				"PermissionHandler returned invalid Behavior %q: must be \"allow\" or \"deny\"",
				result.Behavior))
			return
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
		fn, ok := hookReg[envelope.Request.CallbackID]
		if !ok {
			writeControlError(write, envelope.RequestID,
				"no hook callback found for ID: "+envelope.Request.CallbackID)
			return
		}
		// The event name travels inside the hook input payload, not on the
		// control_request envelope. An absent or unparseable name yields ""
		// rather than dropping the callback — a missing reply hangs the CLI.
		var hookInput struct {
			HookEventName HookEvent `json:"hook_event_name"`
		}
		_ = json.Unmarshal(envelope.Request.Input, &hookInput)

		output, err := fn(hookInput.HookEventName, envelope.Request.Input, envelope.Request.ToolUseID)
		if err != nil {
			writeControlError(write, envelope.RequestID, err.Error())
			return
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

	case "elicitation":
		resp := map[string]any{"cancel": true}
		if opts.ElicitationHandler != nil {
			resp = opts.ElicitationHandler(envelope.Request.Input)
			if resp == nil {
				resp = map[string]any{"cancel": true}
			}
		}
		_ = write(map[string]any{
			"type": "control_response",
			"response": map[string]any{
				"subtype":    "success",
				"request_id": envelope.RequestID,
				"response":   resp,
			},
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
//
// The wire shape is three levels deep — the correlation id lives inside the
// response object, NOT at the top level of the envelope:
//
//	{"type":"control_response","response":{"subtype":…,"request_id":…,"error":…,"response":{…}}}
//
// Matching the reference SDKs (Python _internal/query.py:283-295; TypeScript
// sdk.mjs reads e.response.request_id), routing is by response.request_id and
// the value handed to the caller is the *innermost* response payload — not the
// wrapper that carries subtype/request_id. The CLI omits that payload entirely
// on replies that carry no data (a real set_model success), in which case Body
// is nil and the request still succeeded.
//
// Routing is strictly nested-only: a line whose response is not an object, or
// which carries no request_id, cannot be correlated to any caller and is
// dropped. There is no top-level fallback — the CLI has never emitted one, and
// inventing a shape here is what broke this in the first place (see #36).
func routeControlResponse(line []byte, s *Stream) {
	var envelope struct {
		Response struct {
			Subtype   string          `json:"subtype"`
			RequestID string          `json:"request_id"`
			Error     string          `json:"error,omitempty"`
			Response  json.RawMessage `json:"response,omitempty"`
		} `json:"response"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		return
	}

	reqID := envelope.Response.RequestID
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
			Body:    envelope.Response.Response,
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
	agents := any(map[string]any{})
	if len(opts.Agents) > 0 {
		m := make(map[string]any, len(opts.Agents))
		for k, v := range opts.Agents {
			m[k] = v
		}
		agents = m
	}

	// systemPrompt: use preset object when set, otherwise plain string.
	var systemPromptVal any = opts.SystemPrompt
	if opts.SystemPromptPreset != nil {
		systemPromptVal = opts.SystemPromptPreset
	}

	req := map[string]any{
		"subtype":            "initialize",
		"systemPrompt":       systemPromptVal,
		"appendSystemPrompt": opts.AppendSystemPrompt,
		"hooks":              hooksConfig,
		"agents":             agents,
		"promptSuggestions":  opts.PromptSuggestions,
	}

	// Only send sdkMcpServers when there is something to send: the CLI rejects
	// the whole initialize ("must be arrays of strings") on an empty object,
	// which would take hooks and agents down with it.
	if len(opts.McpServers) > 0 {
		req["sdkMcpServers"] = opts.McpServers
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

// ─── Stderr line writer ───────────────────────────────────────────────────────

// stderrLineWriter is an io.Writer that buffers writes and invokes fn for each
// complete newline-terminated line. Incomplete trailing data is flushed on the
// next write or discarded; the zero value is safe to use.
type stderrLineWriter struct {
	fn  func(string)
	buf bytes.Buffer
}

func (w *stderrLineWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	for {
		idx := bytes.IndexByte(w.buf.Bytes(), '\n')
		if idx < 0 {
			break
		}
		line := string(w.buf.Next(idx + 1))
		w.fn(strings.TrimRight(line, "\r\n"))
	}
	return len(p), nil
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
			strings.HasPrefix(e, "CLAUDE_AGENT_SDK_VERSION="),
			strings.HasPrefix(e, "MAX_THINKING_TOKENS="),
			opts.CWD != "" && strings.HasPrefix(e, "PWD="):
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
	out = append(out, "CLAUDE_AGENT_SDK_VERSION="+SDKVersion)
	if opts.Thinking == ThinkingDisabled {
		out = append(out, "MAX_THINKING_TOKENS=0")
	} else if opts.MaxThinkingTokens > 0 {
		out = append(out, fmt.Sprintf("MAX_THINKING_TOKENS=%d", opts.MaxThinkingTokens))
	}
	// Set PWD when CWD is configured (matches Python SDK behaviour).
	if opts.CWD != "" {
		out = append(out, "PWD="+opts.CWD)
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
	case TypeToolProgress:
		var m ToolProgressMessage
		if err := json.Unmarshal(line, &m); err == nil {
			event.ToolProgress = &m
		}
	case TypeTaskStarted, TypeTaskProgress, TypeTaskNotification:
		var m TaskMessage
		if err := json.Unmarshal(line, &m); err == nil {
			event.Task = &m
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

// spawnSession starts a persistent Claude subprocess in session mode.
// Unlike spawnAndStream, it does NOT send an initial user message — the caller
// sends each turn via Stream.SendUserMessage (or Session.Send).
// The subprocess stays alive across multiple TypeResult events; it exits only
// when the Stream (or Session) is closed.
func spawnSession(ctx context.Context, opts *Options) (*Stream, error) {
	opts.sessionMode = true
	return spawnAndStream(ctx, opts, "")
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
