package claude

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// The wire field for set_permission_mode is "mode"; the SDK previously sent
// "permission_mode", which the CLI does not read.
func TestSetPermissionMode_WireField(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var sent map[string]any
	s := &Stream{
		ctx:     ctx,
		pending: make(map[string]chan controlResponse),
	}
	s.write = func(v any) error {
		b, _ := json.Marshal(v)
		var envelope struct {
			RequestID string         `json:"request_id"`
			Request   map[string]any `json:"request"`
		}
		if err := json.Unmarshal(b, &envelope); err != nil {
			return err
		}
		sent = envelope.Request
		// Acknowledge so the blocking call returns.
		s.pendingMu.Lock()
		ch := s.pending[envelope.RequestID]
		s.pendingMu.Unlock()
		ch <- controlResponse{Success: true}
		return nil
	}

	if err := s.SetPermissionMode(PermissionModePlan); err != nil {
		t.Fatalf("SetPermissionMode: %v", err)
	}

	if sent["subtype"] != "set_permission_mode" {
		t.Fatalf("expected subtype set_permission_mode, got %v", sent["subtype"])
	}
	if sent["mode"] != "plan" {
		t.Fatalf(`expected {"mode":"plan"}, got %v`, sent)
	}
	if _, legacy := sent["permission_mode"]; legacy {
		t.Fatal("permission_mode is not a wire field and must not be sent")
	}
}

// The mutual-exclusion check must fire when a session is constructed, not just
// when validate() is called directly — that wiring is the contract callers see.
// validate() runs before any exec, so no process is spawned here.
func TestQuery_RejectsHandlerWithPromptToolName(t *testing.T) {
	stream, err := Query(context.Background(), "hello",
		WithPermissionHandler(allowAllHandler),
		WithPermissionPromptToolName("mcp__x__y"),
	)
	if err == nil {
		t.Fatal("expected Query to reject a handler combined with a prompt tool name")
	}
	if stream != nil {
		t.Fatal("expected no stream when validation fails")
	}
	for _, want := range []string{"WithPermissionHandler", "WithPermissionPromptToolName"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error should name %s, got: %v", want, err)
		}
	}
}

// The shadowing warning must also be wired into the session construction path.
func TestQuery_WarnsWhenHandlerShadowed(t *testing.T) {
	var warnings []string

	// Point at an executable that does not exist so the spawn fails fast; the
	// warning is emitted before the process is started.
	_, _ = Query(context.Background(), "hello",
		WithPermissionHandler(allowAllHandler),
		WithStderr(func(line string) { warnings = append(warnings, line) }),
		func(o *Options) { o.ClaudeExecutable = "/nonexistent/claude-binary" },
	)

	if len(warnings) != 1 {
		t.Fatalf("expected the shadowing warning at spawn time, got %v", warnings)
	}
	if !strings.Contains(warnings[0], "bypassPermissions") {
		t.Fatalf("expected the default bypassPermissions to be named, got %q", warnings[0])
	}
}

// parseLine leaves Result nil when a result message does not decode. Run must
// report that rather than dereferencing it — this is the panic that a denied
// tool call used to cause.
func TestRun_UndecodableResultDoesNotPanic(t *testing.T) {
	events := make(chan Event, 1)
	// permission_denials as strings no longer decodes into Result.
	raw := []byte(`{"type":"result","subtype":"success","permission_denials":[{"tool_name":"Write"}],"result":"x"}`)
	event, err := parseLine(raw)
	if err != nil {
		t.Fatalf("parseLine: %v", err)
	}
	// Simulate the decode having failed, which is what parseLine does silently.
	event.Result = nil
	events <- event
	close(events)

	s := &Stream{events: events, ctx: context.Background()}

	result, err := resultFromStream(s)
	if err == nil {
		t.Fatal("expected an error for an undecodable result, got none")
	}
	if result != nil {
		t.Fatal("expected no result")
	}
	if !strings.Contains(err.Error(), "could not decode the result") {
		t.Fatalf("expected a decode error, got: %v", err)
	}
}

// Run() returns only an error on a failed result, so whatever a caller needs to
// react to the failure has to be in the message — otherwise they must drop to
// Query() just to read why it stopped (#28).
func TestRun_ErrorSurfacesTerminalReasonAndStatus(t *testing.T) {
	for _, tc := range []struct {
		name string
		line string
		want []string
	}{
		{
			name: "interrupted turn",
			line: `{"type":"result","subtype":"error_during_execution","is_error":true,
				"terminal_reason":"aborted_streaming"}`,
			want: []string{"error_during_execution", "aborted_streaming"},
		},
		{
			name: "rate limited",
			line: `{"type":"result","subtype":"success","is_error":true,
				"terminal_reason":"api_error","api_error_status":429}`,
			want: []string{"api_error", "HTTP 429"},
		},
		{
			name: "turn limit with errors list",
			line: `{"type":"result","subtype":"error_max_turns","is_error":true,
				"terminal_reason":"max_turns","errors":["ran out of turns"]}`,
			want: []string{"error_max_turns", "max_turns", "ran out of turns"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event, err := parseLine([]byte(tc.line))
			if err != nil {
				t.Fatalf("parseLine: %v", err)
			}

			events := make(chan Event, 1)
			events <- event
			close(events)

			result, err := resultFromStream(&Stream{events: events, ctx: context.Background()})
			if err == nil {
				t.Fatal("expected an error for a failed result")
			}
			if result != nil {
				t.Fatal("expected no result alongside the error")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error must mention %q; got: %v", want, err)
				}
			}
		})
	}
}
