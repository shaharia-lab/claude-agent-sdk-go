package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
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

// readMessageFixture loads one captured CLI line from testdata/messages.
// These are real wire payloads, not hand-written literals — fabricated fixtures
// are exactly how the defects this file now guards against reached main (#23).
func readMessageFixture(t *testing.T, name string) []byte {
	t.Helper()

	line, err := os.ReadFile(filepath.Join("testdata", "messages", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return line
}

func TestParseLine_Result(t *testing.T) {
	line := readMessageFixture(t, "result_success.json")

	event, err := parseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event.Type != TypeResult {
		t.Fatalf("expected type %q, got %q", TypeResult, event.Type)
	}
	if event.Result == nil {
		t.Fatal("expected Result to be non-nil")
	}
	if event.DecodeErr != nil {
		t.Fatalf("captured result did not decode cleanly: %v", event.DecodeErr)
	}

	// server_tool_use is nested under usage on the wire, NOT top-level. The old
	// fixture asserted a top-level web_search_requests the CLI never sends, so
	// that field could never populate from real output. Assert the wire shape
	// structurally: the captured counters happen to be zero, so comparing the
	// decoded number alone would pass either way.
	var wire struct {
		Usage map[string]json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal(line, &wire); err != nil {
		t.Fatalf("unmarshal captured usage: %v", err)
	}
	if _, topLevel := wire.Usage["web_search_requests"]; topLevel {
		t.Error("captured usage has a top-level web_search_requests; revisit the nesting")
	}
	nested, ok := wire.Usage["server_tool_use"]
	if !ok {
		t.Fatal("captured usage has no server_tool_use object")
	}
	var counters map[string]int
	if err := json.Unmarshal(nested, &counters); err != nil {
		t.Fatalf("server_tool_use is not an object of counters: %v", err)
	}
	if _, has := counters["web_search_requests"]; !has {
		t.Error("server_tool_use does not carry web_search_requests")
	}
	if event.Result.Usage.ServerToolUse.WebSearchRequests != counters["web_search_requests"] {
		t.Errorf("ServerToolUse.WebSearchRequests = %d, wire says %d",
			event.Result.Usage.ServerToolUse.WebSearchRequests, counters["web_search_requests"])
	}
	if event.Result.Usage.ServerToolUse.WebFetchRequests != counters["web_fetch_requests"] {
		t.Errorf("ServerToolUse.WebFetchRequests = %d, wire says %d",
			event.Result.Usage.ServerToolUse.WebFetchRequests, counters["web_fetch_requests"])
	}
	if event.Result.Usage.InputTokens == 0 || event.Result.Usage.CacheReadInputTokens == 0 {
		t.Errorf("usage did not decode: %+v", event.Result.Usage)
	}
	if event.Result.Usage.ServiceTier == "" {
		t.Error("service_tier did not decode")
	}
	if len(event.Raw) == 0 {
		t.Error("Raw must stay populated on a fully decoded event")
	}
}

func TestParseLine_ResultWithModelUsages(t *testing.T) {
	line := readMessageFixture(t, "result_success.json")

	event, err := parseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event.Result == nil {
		t.Fatal("expected Result to be non-nil")
	}

	// The wire key is modelUsage, and its fields are camelCase — unlike the rest
	// of the protocol. The old fixture used model_usages with snake_case, which
	// the CLI never emits, so this map was always empty against real output.
	if len(event.Result.ModelUsages) == 0 {
		t.Fatal("modelUsage did not decode")
	}
	for model, mu := range event.Result.ModelUsages {
		if mu.InputTokens == 0 && mu.OutputTokens == 0 {
			t.Errorf("%s: token counts did not decode: %+v", model, mu)
		}
		if mu.CostUSD == 0 {
			t.Errorf("%s: costUSD did not decode: %+v", model, mu)
		}
		if mu.ContextWindow == 0 {
			t.Errorf("%s: contextWindow did not decode: %+v", model, mu)
		}
		if mu.CanonicalModel == "" {
			t.Errorf("%s: canonicalModel did not decode: %+v", model, mu)
		}
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

// Task lifecycle messages arrive as system subtypes. The old tests fed lines
// with type:"task_started", which nothing on the wire ever sends — so they
// exercised a branch that could not fire in production.
func TestParseLine_TaskStartedIsASystemSubtype(t *testing.T) {
	line := readMessageFixture(t, "system_task_started.json")

	event, err := parseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event.Type != TypeSystem {
		t.Fatalf("expected type %q, got %q", TypeSystem, event.Type)
	}
	if event.System == nil || event.System.Subtype != SubtypeTaskStarted {
		t.Fatalf("expected a system/%s message, got %+v", SubtypeTaskStarted, event.System)
	}
	if event.Task == nil {
		t.Fatal("expected Task to be populated from the system subtype")
	}
	if event.Task.TaskID == "" {
		t.Errorf("task_id did not decode: %+v", event.Task)
	}
}

// The other system subtypes seen while capturing the corpus decode as system
// messages and keep Raw, even though their payloads are not typed yet.
func TestParseLine_OtherSystemSubtypes(t *testing.T) {
	for _, tc := range []struct {
		fixture string
		subtype string
	}{
		{"system_thinking_tokens.json", SubtypeThinkingTokens},
		{"system_background_tasks_changed.json", SubtypeBackgroundTasksChange},
	} {
		t.Run(tc.subtype, func(t *testing.T) {
			event, err := parseLine(readMessageFixture(t, tc.fixture))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if event.System == nil || event.System.Subtype != tc.subtype {
				t.Fatalf("expected system/%s, got %+v", tc.subtype, event.System)
			}
			if len(event.Raw) == 0 {
				t.Error("Raw must be populated")
			}
		})
	}
}

// A system/init from a session with plugins loaded must decode. Before #23
// Plugins was []string while the wire carries objects, so the type mismatch
// nilled the entire SystemMessage.
func TestParseLine_SystemInitWithPlugins(t *testing.T) {
	line := readMessageFixture(t, "system_init_with_plugins.json")

	event, err := parseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event.System == nil {
		t.Fatal("expected System to be non-nil")
	}
	if event.DecodeErr != nil {
		t.Fatalf("captured init did not decode cleanly: %v", event.DecodeErr)
	}
	if event.System.Subtype != SubtypeInit {
		t.Fatalf("expected subtype %q, got %q", SubtypeInit, event.System.Subtype)
	}
	if len(event.System.Plugins) == 0 {
		t.Fatal("plugins did not decode")
	}
	for _, pl := range event.System.Plugins {
		if pl.Name == "" || pl.Path == "" {
			t.Errorf("plugin decoded with empty fields: %+v", pl)
		}
	}
	// Other init fields must survive alongside the plugins.
	if event.System.SessionID == "" {
		t.Error("session_id did not decode")
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
	// still have Type set and Raw populated. The task/hook lifecycle names are
	// deliberately absent: they are system subtypes, not top-level types (#23).
	types := []MessageType{
		TypeToolUseSummary, TypeAuthStatus, TypePromptSuggestion, TypeRateLimitEvent,
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
		if len(event.Raw) == 0 {
			t.Fatalf("expected Raw to be populated for type %q", typ)
		}
	}
}

func TestParseLine_ResultWithPermissionDenials(t *testing.T) {
	line, err := os.ReadFile("testdata/result_with_permission_denials.json")
	if err != nil {
		t.Fatalf("read captured payload: %v", err)
	}

	event, err := parseLine(line)
	if err != nil {
		t.Fatalf("parseLine: %v", err)
	}
	if event.Type != TypeResult {
		t.Fatalf("expected a result event, got %s", event.Type)
	}
	if event.Result == nil {
		t.Fatal("Result is nil — the captured payload failed to decode")
	}

	denials := event.Result.PermissionDenials
	if len(denials) != 2 {
		t.Fatalf("expected 2 denials, got %d", len(denials))
	}
	if denials[0].ToolName != "Write" {
		t.Fatalf("expected first denial for Write, got %q", denials[0].ToolName)
	}
	if denials[0].ToolUseID != "toolu_01JZCDnureJXbXNHAAfRDt36" {
		t.Fatalf("unexpected tool_use_id %q", denials[0].ToolUseID)
	}
	var input map[string]any
	if err := json.Unmarshal(denials[0].ToolInput, &input); err != nil {
		t.Fatalf("tool_input should be preserved as raw JSON: %v", err)
	}
	if input["file_path"] != "/tmp/claude-sdk-raw.txt" {
		t.Fatalf("unexpected tool_input %v", input)
	}
	if denials[1].ToolName != "Bash" {
		t.Fatalf("expected second denial for Bash, got %q", denials[1].ToolName)
	}
}
