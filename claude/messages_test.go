package claude

import (
	"encoding/json"
	"testing"
)

func TestParseLine_Assistant(t *testing.T) {
	line := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hello"}]},"session_id":"s1","uuid":"u1"}`
	event, err := parseLine([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event.Type != TypeAssistant {
		t.Fatalf("expected type %q, got %q", TypeAssistant, event.Type)
	}
	if event.Assistant == nil {
		t.Fatal("expected Assistant to be non-nil")
	}
	if got := event.Assistant.Text(); got != "hello" {
		t.Fatalf("expected text %q, got %q", "hello", got)
	}
	if event.Assistant.SessionID != "s1" {
		t.Fatalf("expected session_id %q, got %q", "s1", event.Assistant.SessionID)
	}
}

func TestParseLine_Result(t *testing.T) {
	line := `{"type":"result","subtype":"success","duration_ms":100,"is_error":false,"num_turns":1,"result":"done","total_cost_usd":0.01,"usage":{"input_tokens":10,"output_tokens":20,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"web_search_requests":3},"session_id":"s1","uuid":"u1"}`
	event, err := parseLine([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event.Type != TypeResult {
		t.Fatalf("expected type %q, got %q", TypeResult, event.Type)
	}
	if event.Result == nil {
		t.Fatal("expected Result to be non-nil")
	}
	if event.Result.Usage.WebSearchRequests != 3 {
		t.Fatalf("expected WebSearchRequests=3, got %d", event.Result.Usage.WebSearchRequests)
	}
}

func TestParseLine_ResultWithModelUsages(t *testing.T) {
	line := `{"type":"result","subtype":"success","duration_ms":100,"is_error":false,"num_turns":1,"result":"done","total_cost_usd":0.05,"usage":{"input_tokens":10,"output_tokens":20,"cache_read_input_tokens":0,"cache_creation_input_tokens":0},"model_usages":{"claude-sonnet-4-6":{"input_tokens":10,"output_tokens":20,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"cost_usd":0.05,"context_window":200000,"max_output_tokens":8192}},"session_id":"s1","uuid":"u1"}`
	event, err := parseLine([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event.Result == nil {
		t.Fatal("expected Result to be non-nil")
	}
	mu, ok := event.Result.ModelUsages["claude-sonnet-4-6"]
	if !ok {
		t.Fatal("expected model usage for claude-sonnet-4-6")
	}
	if mu.CostUSD != 0.05 {
		t.Fatalf("expected CostUSD=0.05, got %f", mu.CostUSD)
	}
	if mu.ContextWindow != 200000 {
		t.Fatalf("expected ContextWindow=200000, got %d", mu.ContextWindow)
	}
}

func TestParseLine_ToolProgress(t *testing.T) {
	line := `{"type":"tool_progress","tool_use_id":"tu1","progress":0.5,"message":"halfway done"}`
	event, err := parseLine([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event.Type != TypeToolProgress {
		t.Fatalf("expected type %q, got %q", TypeToolProgress, event.Type)
	}
	if event.ToolProgress == nil {
		t.Fatal("expected ToolProgress to be non-nil")
	}
	if event.ToolProgress.ToolUseID != "tu1" {
		t.Fatalf("expected tool_use_id %q, got %q", "tu1", event.ToolProgress.ToolUseID)
	}
	if event.ToolProgress.Progress != 0.5 {
		t.Fatalf("expected progress 0.5, got %f", event.ToolProgress.Progress)
	}
	if event.ToolProgress.Message != "halfway done" {
		t.Fatalf("expected message %q, got %q", "halfway done", event.ToolProgress.Message)
	}
}

func TestParseLine_TaskStarted(t *testing.T) {
	line := `{"type":"task_started","task_id":"t1","status":"running","message":"starting"}`
	event, err := parseLine([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event.Type != TypeTaskStarted {
		t.Fatalf("expected type %q, got %q", TypeTaskStarted, event.Type)
	}
	if event.Task == nil {
		t.Fatal("expected Task to be non-nil")
	}
	if event.Task.TaskID != "t1" {
		t.Fatalf("expected task_id %q, got %q", "t1", event.Task.TaskID)
	}
}

func TestParseLine_TaskProgress(t *testing.T) {
	line := `{"type":"task_progress","task_id":"t1","message":"50%"}`
	event, err := parseLine([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event.Type != TypeTaskProgress {
		t.Fatalf("expected type %q, got %q", TypeTaskProgress, event.Type)
	}
	if event.Task == nil {
		t.Fatal("expected Task to be non-nil")
	}
}

func TestParseLine_TaskNotification(t *testing.T) {
	line := `{"type":"task_notification","task_id":"t1","message":"done"}`
	event, err := parseLine([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event.Type != TypeTaskNotification {
		t.Fatalf("expected type %q, got %q", TypeTaskNotification, event.Type)
	}
	if event.Task == nil {
		t.Fatal("expected Task to be non-nil")
	}
}

func TestParseLine_UnknownType_RawOnly(t *testing.T) {
	line := `{"type":"rate_limit_event","retry_after":5}`
	event, err := parseLine([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event.Type != TypeRateLimitEvent {
		t.Fatalf("expected type %q, got %q", TypeRateLimitEvent, event.Type)
	}
	if event.Raw == nil {
		t.Fatal("expected Raw to be non-nil")
	}
	// Typed fields should all be nil.
	if event.Assistant != nil || event.StreamEvent != nil || event.Result != nil || event.System != nil || event.ToolProgress != nil || event.Task != nil {
		t.Fatal("expected all typed fields to be nil for unknown type")
	}
}

func TestParseLine_System(t *testing.T) {
	line := `{"type":"system","subtype":"init","session_id":"s1","model":"claude-sonnet-4-6","tools":["Bash","Read"]}`
	event, err := parseLine([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event.Type != TypeSystem {
		t.Fatalf("expected type %q, got %q", TypeSystem, event.Type)
	}
	if event.System == nil {
		t.Fatal("expected System to be non-nil")
	}
	if event.System.Subtype != SubtypeInit {
		t.Fatalf("expected subtype %q, got %q", SubtypeInit, event.System.Subtype)
	}
	if event.System.Model != "claude-sonnet-4-6" {
		t.Fatalf("expected model %q, got %q", "claude-sonnet-4-6", event.System.Model)
	}
}

func TestParseLine_StreamEvent(t *testing.T) {
	line := `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"}},"session_id":"s1","uuid":"u1"}`
	event, err := parseLine([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event.Type != TypeStreamEvent {
		t.Fatalf("expected type %q, got %q", TypeStreamEvent, event.Type)
	}
	if event.StreamEvent == nil {
		t.Fatal("expected StreamEvent to be non-nil")
	}
	if event.StreamEvent.Event.Delta == nil {
		t.Fatal("expected delta to be non-nil")
	}
	if event.StreamEvent.Event.Delta.Text != "hi" {
		t.Fatalf("expected delta text %q, got %q", "hi", event.StreamEvent.Event.Delta.Text)
	}
}

func TestParseLine_InvalidJSON(t *testing.T) {
	_, err := parseLine([]byte("not json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParseLine_NewTypesRawOnly(t *testing.T) {
	// Types declared as constants but not parsed into typed fields should
	// still have Type set and Raw populated.
	types := []MessageType{
		TypeToolUseSummary, TypeHookStarted, TypeHookProgress,
		TypeHookResponse, TypeCompactBoundary, TypeFilesPersisted,
		TypeAuthStatus, TypePromptSuggestion,
	}
	for _, typ := range types {
		line, _ := json.Marshal(map[string]any{"type": string(typ), "data": "test"})
		event, err := parseLine(line)
		if err != nil {
			t.Fatalf("unexpected error for type %q: %v", typ, err)
		}
		if event.Type != typ {
			t.Fatalf("expected type %q, got %q", typ, event.Type)
		}
		if event.Raw == nil {
			t.Fatalf("expected Raw to be non-nil for type %q", typ)
		}
	}
}
