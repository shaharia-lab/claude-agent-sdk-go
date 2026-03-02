package claude

import (
	"encoding/json"
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
