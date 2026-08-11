package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestBuildEnv_PWD(t *testing.T) {
	opts := defaultOptions()
	opts.CWD = "/tmp/test-dir"

	env := buildEnv(opts)
	found := false
	for _, e := range env {
		if e == "PWD=/tmp/test-dir" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected PWD=/tmp/test-dir in environment")
	}
}

func TestBuildEnv_PWD_NotSetWhenCWDEmpty(t *testing.T) {
	opts := defaultOptions()
	// CWD is empty (default)

	env := buildEnv(opts)
	for _, e := range env {
		if strings.HasPrefix(e, "PWD=") {
			// PWD from parent env is fine; we only care that we don't add
			// an explicit PWD= when CWD is empty.
			parentPWD := os.Getenv("PWD")
			if e != "PWD="+parentPWD {
				t.Fatalf("unexpected PWD entry: %s", e)
			}
		}
	}
}

func TestBuildEnv_PWD_StripsInheritedWhenCWDSet(t *testing.T) {
	opts := defaultOptions()
	opts.CWD = "/my/dir"

	env := buildEnv(opts)
	count := 0
	for _, e := range env {
		if strings.HasPrefix(e, "PWD=") {
			count++
			if e != "PWD=/my/dir" {
				t.Fatalf("expected PWD=/my/dir, got %s", e)
			}
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 PWD entry, got %d", count)
	}
}

func TestBuildEnv_SDKVersion(t *testing.T) {
	opts := defaultOptions()
	env := buildEnv(opts)
	found := false
	for _, e := range env {
		if e == "CLAUDE_AGENT_SDK_VERSION="+SDKVersion {
			found = true
		}
	}
	if !found {
		t.Fatal("expected CLAUDE_AGENT_SDK_VERSION in environment")
	}
}

func TestBuildEnv_Entrypoint(t *testing.T) {
	opts := defaultOptions()
	env := buildEnv(opts)
	found := false
	for _, e := range env {
		if e == "CLAUDE_CODE_ENTRYPOINT=sdk-go" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected CLAUDE_CODE_ENTRYPOINT=sdk-go in environment")
	}
}

func TestBuildEnv_ThinkingDisabled(t *testing.T) {
	opts := defaultOptions()
	opts.Thinking = ThinkingDisabled
	env := buildEnv(opts)
	found := false
	for _, e := range env {
		if e == "MAX_THINKING_TOKENS=0" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected MAX_THINKING_TOKENS=0 when thinking disabled")
	}
}

func TestBuildEnv_MaxThinkingTokens(t *testing.T) {
	opts := defaultOptions()
	opts.MaxThinkingTokens = 1000
	env := buildEnv(opts)
	found := false
	for _, e := range env {
		if e == "MAX_THINKING_TOKENS=1000" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected MAX_THINKING_TOKENS=1000 in environment")
	}
}

func TestBuildEnv_UserEnvOverride(t *testing.T) {
	opts := defaultOptions()
	opts.Env = map[string]string{"MY_VAR": "my_value"}
	env := buildEnv(opts)
	found := false
	for _, e := range env {
		if e == "MY_VAR=my_value" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected MY_VAR=my_value in environment")
	}
}

func TestInitializeMsg_PromptSuggestions(t *testing.T) {
	tests := []struct {
		enabled bool
	}{
		{true},
		{false},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("enabled=%v", tt.enabled), func(t *testing.T) {
			opts := defaultOptions()
			opts.PromptSuggestions = tt.enabled

			msg := initializeMsg(opts, map[string]any{})

			// Marshal and re-parse to inspect the structure.
			b, err := json.Marshal(msg)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var m map[string]any
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			req, ok := m["request"].(map[string]any)
			if !ok {
				t.Fatal("expected request field")
			}
			got, ok := req["promptSuggestions"].(bool)
			if !ok {
				t.Fatal("expected promptSuggestions to be bool")
			}
			if got != tt.enabled {
				t.Fatalf("expected promptSuggestions=%v, got %v", tt.enabled, got)
			}
		})
	}
}

func TestRouteControlResponse_MalformedResponse(t *testing.T) {
	s := &Stream{
		events:  make(chan Event, 1),
		pending: make(map[string]chan controlResponse),
	}

	reqID := "test-req-id"
	ch := make(chan controlResponse, 1)
	s.pending[reqID] = ch

	// Send a control_response with invalid JSON in the response field.
	line := []byte(fmt.Sprintf(`{"type":"control_response","request_id":"%s","response":"not-json-object"}`, reqID))
	routeControlResponse(line, s)

	resp := <-ch
	if resp.Success {
		t.Fatal("expected failure for malformed response")
	}
	if !strings.Contains(resp.Error, "malformed control_response") {
		t.Fatalf("expected malformed error message, got %q", resp.Error)
	}
}

func TestRouteControlResponse_Success(t *testing.T) {
	s := &Stream{
		events:  make(chan Event, 1),
		pending: make(map[string]chan controlResponse),
	}

	reqID := "test-req-id-2"
	ch := make(chan controlResponse, 1)
	s.pending[reqID] = ch

	line := []byte(fmt.Sprintf(`{"type":"control_response","request_id":"%s","response":{"subtype":"success","data":"value"}}`, reqID))
	routeControlResponse(line, s)

	resp := <-ch
	if !resp.Success {
		t.Fatalf("expected success, got error: %s", resp.Error)
	}
	if resp.Body == nil {
		t.Fatal("expected Body to be non-nil")
	}
}

func TestRouteControlResponse_Error(t *testing.T) {
	s := &Stream{
		events:  make(chan Event, 1),
		pending: make(map[string]chan controlResponse),
	}

	reqID := "test-req-id-3"
	ch := make(chan controlResponse, 1)
	s.pending[reqID] = ch

	line := []byte(fmt.Sprintf(`{"type":"control_response","request_id":"%s","response":{"subtype":"error","error":"something failed"}}`, reqID))
	routeControlResponse(line, s)

	resp := <-ch
	if resp.Success {
		t.Fatal("expected failure")
	}
	if resp.Error != "something failed" {
		t.Fatalf("expected error %q, got %q", "something failed", resp.Error)
	}
}

func TestRouteControlResponse_UnknownRequestID(t *testing.T) {
	s := &Stream{
		events:  make(chan Event, 1),
		pending: make(map[string]chan controlResponse),
	}

	// No pending request registered for this ID — should not panic.
	line := []byte(`{"type":"control_response","request_id":"unknown","response":{"subtype":"success"}}`)
	routeControlResponse(line, s) // should not panic
}

func TestHandleControlRequest_Elicitation_WithHandler(t *testing.T) {
	var written []any
	write := func(v any) error {
		written = append(written, v)
		return nil
	}

	opts := defaultOptions()
	opts.ElicitationHandler = func(request json.RawMessage) map[string]any {
		return map[string]any{"response": "user said yes"}
	}

	line := []byte(`{"type":"control_request","request_id":"r1","request":{"subtype":"elicitation","input":{"question":"Continue?"}}}`)
	handleControlRequest(line, write, opts, hookRegistry{})

	if len(written) != 1 {
		t.Fatalf("expected 1 write, got %d", len(written))
	}

	b, _ := json.Marshal(written[0])
	var resp map[string]any
	_ = json.Unmarshal(b, &resp)

	respObj, ok := resp["response"].(map[string]any)
	if !ok {
		t.Fatal("expected response field")
	}
	inner, ok := respObj["response"].(map[string]any)
	if !ok {
		t.Fatal("expected inner response field")
	}
	if inner["response"] != "user said yes" {
		t.Fatalf("expected 'user said yes', got %v", inner["response"])
	}
}

func TestHandleControlRequest_Elicitation_NilHandler(t *testing.T) {
	var written []any
	write := func(v any) error {
		written = append(written, v)
		return nil
	}

	opts := defaultOptions()
	// ElicitationHandler is nil — should auto-cancel.

	line := []byte(`{"type":"control_request","request_id":"r2","request":{"subtype":"elicitation","input":{}}}`)
	handleControlRequest(line, write, opts, hookRegistry{})

	if len(written) != 1 {
		t.Fatalf("expected 1 write, got %d", len(written))
	}

	b, _ := json.Marshal(written[0])
	var resp map[string]any
	_ = json.Unmarshal(b, &resp)

	respObj := resp["response"].(map[string]any)
	inner := respObj["response"].(map[string]any)
	if inner["cancel"] != true {
		t.Fatalf("expected cancel=true, got %v", inner["cancel"])
	}
}

// A hook_callback control_request carries the event name inside its input
// payload as hook_event_name; there is no top-level hook_event field on the
// wire. Driven by a payload captured from a real CLI — see testdata/README.md.
func TestHandleControlRequest_HookCallback_EventNameFromInput(t *testing.T) {
	line, err := os.ReadFile("testdata/hook_callback_pretooluse.json")
	if err != nil {
		t.Fatalf("read captured payload: %v", err)
	}

	// Guard the premise: the captured payload must carry the event name in the
	// input and must NOT have a top-level hook_event field.
	var captured struct {
		Request struct {
			HookEvent string                     `json:"hook_event"`
			Input     map[string]json.RawMessage `json:"input"`
		} `json:"request"`
	}
	if err := json.Unmarshal(line, &captured); err != nil {
		t.Fatalf("captured payload is not valid JSON: %v", err)
	}
	if captured.Request.HookEvent != "" {
		t.Fatal("captured payload unexpectedly has a top-level hook_event field")
	}
	if _, ok := captured.Request.Input["hook_event_name"]; !ok {
		t.Fatal("captured payload is missing input.hook_event_name")
	}

	var written []any
	write := func(v any) error {
		written = append(written, v)
		return nil
	}

	var gotEvent HookEvent
	var gotToolUseID string
	var gotInput json.RawMessage
	reg := hookRegistry{
		"cb-1": func(event HookEvent, input json.RawMessage, toolUseID string) (*HookOutput, error) {
			gotEvent, gotInput, gotToolUseID = event, input, toolUseID
			return &HookOutput{Decision: "approve"}, nil
		},
	}

	handleControlRequest(line, write, defaultOptions(), reg)

	if gotEvent != HookEventPreToolUse {
		t.Fatalf("expected event %q from input.hook_event_name, got %q", HookEventPreToolUse, gotEvent)
	}
	if gotToolUseID != "toolu_01Co5gP6ay654HtkuVW6zpK6" {
		t.Fatalf("expected the captured tool_use_id, got %q", gotToolUseID)
	}
	// The raw input is passed through untouched.
	var input map[string]any
	if err := json.Unmarshal(gotInput, &input); err != nil {
		t.Fatalf("input is not valid JSON: %v", err)
	}
	if input["tool_name"] != "Bash" {
		t.Fatalf("expected raw input to be passed through, got %v", input)
	}

	if len(written) != 1 {
		t.Fatalf("expected 1 write, got %d", len(written))
	}
	b, _ := json.Marshal(written[0])
	var resp struct {
		Type     string `json:"type"`
		Response struct {
			Subtype   string     `json:"subtype"`
			RequestID string     `json:"request_id"`
			Response  HookOutput `json:"response"`
		} `json:"response"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Type != "control_response" || resp.Response.Subtype != "success" {
		t.Fatalf("expected success control_response, got %s", b)
	}
	if resp.Response.RequestID != "6251005c-03c1-4c7f-a2fc-c4cc07a64145" {
		t.Fatalf("expected the captured request_id to be echoed, got %q", resp.Response.RequestID)
	}
	if resp.Response.Response.Decision != "approve" {
		t.Fatalf("expected decision 'approve', got %q", resp.Response.Response.Decision)
	}
}

// Regression guard for the original defect: even if a top-level hook_event
// field is present, the event name must come from input.hook_event_name. This
// fails if the envelope field is ever reintroduced and preferred.
func TestHandleControlRequest_HookCallback_IgnoresEnvelopeHookEvent(t *testing.T) {
	write := func(any) error { return nil }

	var gotEvent HookEvent
	reg := hookRegistry{
		"cb-1": func(event HookEvent, _ json.RawMessage, _ string) (*HookOutput, error) {
			gotEvent = event
			return nil, nil
		},
	}

	line := []byte(`{"type":"control_request","request_id":"r5","request":{"subtype":"hook_callback","callback_id":"cb-1","hook_event":"Stop","input":{"hook_event_name":"PreToolUse"}}}`)
	handleControlRequest(line, write, defaultOptions(), reg)

	if gotEvent != HookEventPreToolUse {
		t.Fatalf("expected input.hook_event_name to win over the envelope field, got %q", gotEvent)
	}
}

// A missing hook_event_name must not drop the reply — a missing reply hangs
// the CLI — the callback just receives an empty event.
func TestHandleControlRequest_HookCallback_MissingEventName(t *testing.T) {
	var written []any
	write := func(v any) error {
		written = append(written, v)
		return nil
	}

	called := false
	var gotEvent HookEvent
	reg := hookRegistry{
		"cb-1": func(event HookEvent, _ json.RawMessage, _ string) (*HookOutput, error) {
			called, gotEvent = true, event
			return nil, nil
		},
	}

	line := []byte(`{"type":"control_request","request_id":"r2","request":{"subtype":"hook_callback","callback_id":"cb-1","input":{"tool_name":"Bash"}}}`)
	handleControlRequest(line, write, defaultOptions(), reg)

	if !called {
		t.Fatal("expected the callback to still be invoked")
	}
	if gotEvent != "" {
		t.Fatalf("expected empty event, got %q", gotEvent)
	}
	if len(written) != 1 {
		t.Fatalf("expected 1 write (a reply is mandatory), got %d", len(written))
	}
	b, _ := json.Marshal(written[0])
	if !strings.Contains(string(b), `"subtype":"success"`) {
		t.Fatalf("expected success response, got %s", b)
	}
}

// An unknown callback ID is an error, not a silent success — matching the
// official Python SDK, which raises "No hook callback found for ID".
func TestHandleControlRequest_HookCallback_UnknownCallbackID(t *testing.T) {
	var written []any
	write := func(v any) error {
		written = append(written, v)
		return nil
	}

	line := []byte(`{"type":"control_request","request_id":"r3","request":{"subtype":"hook_callback","callback_id":"nope","input":{"hook_event_name":"PreToolUse"}}}`)
	handleControlRequest(line, write, defaultOptions(), hookRegistry{})

	if len(written) != 1 {
		t.Fatalf("expected 1 write, got %d", len(written))
	}
	b, _ := json.Marshal(written[0])
	if !strings.Contains(string(b), `"subtype":"error"`) {
		t.Fatalf("expected an error control_response, got %s", b)
	}
	if !strings.Contains(string(b), "nope") {
		t.Fatalf("expected the unknown callback ID in the error, got %s", b)
	}
}

// A hook returning an error replies with an error control_response.
func TestHandleControlRequest_HookCallback_HookError(t *testing.T) {
	var written []any
	write := func(v any) error {
		written = append(written, v)
		return nil
	}

	reg := hookRegistry{
		"cb-1": func(HookEvent, json.RawMessage, string) (*HookOutput, error) {
			return nil, errors.New("policy denied")
		},
	}

	line := []byte(`{"type":"control_request","request_id":"r4","request":{"subtype":"hook_callback","callback_id":"cb-1","input":{"hook_event_name":"PreToolUse"}}}`)
	handleControlRequest(line, write, defaultOptions(), reg)

	b, _ := json.Marshal(written[0])
	if !strings.Contains(string(b), `"subtype":"error"`) || !strings.Contains(string(b), "policy denied") {
		t.Fatalf("expected error response carrying the hook error, got %s", b)
	}
}

// The CLI rejects the entire initialize when sdkMcpServers is an empty object
// ("sdkMcpServers and webSearchIsolationExemptMcpServers must be arrays of
// strings"), which would take hooks down with it, so the key is omitted when
// there are no servers.
func TestInitializeMsg_OmitsEmptySdkMcpServers(t *testing.T) {
	msg := initializeMsg(defaultOptions(), map[string]any{})

	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var envelope struct {
		Request map[string]json.RawMessage `json:"request"`
	}
	if err := json.Unmarshal(b, &envelope); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := envelope.Request["sdkMcpServers"]; present {
		t.Fatalf("expected sdkMcpServers to be omitted when empty, got %s", b)
	}
}
