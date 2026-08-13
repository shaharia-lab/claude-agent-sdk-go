package claude

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

// This test used to assert `progress: 0.5` and `message: "halfway done"` against
// a hand-written line. Neither field exists on the wire — the CLI emits
// tool_name/elapsed_time_seconds/heartbeat — so the test passed while proving
// nothing, which is the fabricated-fixture defect class from #23. The real
// shape is asserted by TestParseLine_ToolProgressRealFields (#29).
func TestParseLine_ToolProgress(t *testing.T) {
	line := `{"type":"tool_progress","tool_use_id":"tu1","tool_name":"Bash","elapsed_time_seconds":0.5}`
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
	if event.TaskStarted == nil {
		t.Fatal("expected TaskStarted to be populated from the system subtype")
	}
	if event.TaskStarted.TaskID == "" {
		t.Errorf("task_id did not decode: %+v", event.TaskStarted)
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

// A message type this SDK does not model at all keeps Raw and nothing else.
// rate_limit_event used to be the example here; it is typed as of #29, so this
// uses a type the CLI has never sent.
func TestParseLine_UnknownType_RawOnly(t *testing.T) {
	line := `{"type":"telepathy_event","retry_after":5}`
	event, err := parseLine([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event.Type != MessageType("telepathy_event") {
		t.Fatalf("unexpected type %q", event.Type)
	}
	if event.Raw == nil {
		t.Fatal("expected Raw to be non-nil")
	}
	// Typed fields should all be nil.
	if event.Assistant != nil || event.User != nil || event.StreamEvent != nil ||
		event.Result != nil || event.System != nil || event.ToolProgress != nil ||
		event.RateLimit != nil || event.TaskStarted != nil || event.TaskUpdated != nil ||
		event.TaskNotification != nil || event.TaskProgress != nil || event.HookLifecycle != nil {
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

// ─── User messages and the content-block union (#27) ──────────────────────────

// The user side of a conversation is where every tool's output arrives. Before
// #27 parseLine had no "user" case at all, so all of it was invisible to typed
// consumers.
func TestParseLine_UserMessageWithToolResult(t *testing.T) {
	line := readMessageFixture(t, "user_tool_result.json")

	event, err := parseLine(line)
	if err != nil {
		t.Fatalf("parseLine: %v", err)
	}
	if event.Type != TypeUser {
		t.Fatalf("expected type %q, got %q", TypeUser, event.Type)
	}
	if event.User == nil {
		t.Fatal("Event.User is nil — the captured user turn failed to decode")
	}
	if event.DecodeErr != nil {
		t.Fatalf("captured payload should decode cleanly: %v", event.DecodeErr)
	}

	results := event.User.ToolResults()
	if len(results) != 1 {
		t.Fatalf("expected 1 tool_result block, got %d", len(results))
	}
	got := results[0]
	if got.ToolUseID != "toolu_014V681DGmsapzsqS82K773b" {
		t.Errorf("unexpected tool_use_id %q", got.ToolUseID)
	}
	if got.Failed() {
		t.Error("this result succeeded; Failed() must be false when is_error is absent")
	}
	text, ok := got.ContentText()
	if !ok {
		t.Fatal("this tool sent its content as a string; ContentText must report it")
	}
	if !strings.Contains(text, "hello-from-corpus") {
		t.Errorf("tool output was lost: %q", text)
	}

	// tool_use_result is the tool's own structured payload — an object here.
	var structured map[string]any
	if err := json.Unmarshal(event.User.ToolUseResult, &structured); err != nil {
		t.Fatalf("tool_use_result should be preserved as raw JSON: %v", err)
	}
	if structured["type"] != "text" {
		t.Errorf("unexpected tool_use_result %v", structured)
	}
	if event.User.SessionID == "" || event.User.UUID == "" {
		t.Error("session_id/uuid were lost")
	}
}

// The same field is a plain string when the tool fails, which is why
// ToolUseResult is raw rather than a typed struct.
func TestParseLine_UserToolResultError(t *testing.T) {
	line := readMessageFixture(t, "user_tool_result_error.json")

	event, err := parseLine(line)
	if err != nil {
		t.Fatalf("parseLine: %v", err)
	}
	if event.User == nil {
		t.Fatal("Event.User is nil")
	}
	if event.DecodeErr != nil {
		t.Fatalf("captured payload should decode cleanly: %v", event.DecodeErr)
	}

	results := event.User.ToolResults()
	if len(results) != 1 {
		t.Fatalf("expected 1 tool_result block, got %d", len(results))
	}
	if !results[0].Failed() {
		t.Error("is_error was true on the wire; Failed() must report it")
	}

	var s string
	if err := json.Unmarshal(event.User.ToolUseResult, &s); err != nil {
		t.Fatalf("tool_use_result arrives as a bare string when a tool fails: %v", err)
	}
	if !strings.Contains(s, "does not exist") {
		t.Errorf("unexpected tool_use_result %q", s)
	}
}

// Assistant turns carry the model, stop reason and per-turn usage that
// MessagePayload used to drop on the floor.
func TestParseLine_AssistantToolUseMetadata(t *testing.T) {
	line := readMessageFixture(t, "assistant_tool_use.json")

	event, err := parseLine(line)
	if err != nil {
		t.Fatalf("parseLine: %v", err)
	}
	if event.Assistant == nil {
		t.Fatal("Event.Assistant is nil")
	}
	if event.DecodeErr != nil {
		t.Fatalf("captured payload should decode cleanly: %v", event.DecodeErr)
	}

	msg := event.Assistant.Message
	if msg.ID != "msg_011Cdzm9PJgJPQp5eEUJsQDL" {
		t.Errorf("message id was lost: %q", msg.ID)
	}
	if msg.Model != "claude-opus-5" {
		t.Errorf("model was lost: %q", msg.Model)
	}
	if len(msg.Usage) == 0 {
		t.Error("per-turn usage was lost")
	}
	if event.Assistant.RequestID == "" {
		t.Error("request_id was lost")
	}

	uses := event.Assistant.ToolUses()
	if len(uses) != 1 {
		t.Fatalf("expected 1 tool_use block, got %d", len(uses))
	}
	if uses[0].Name != "Read" || uses[0].ID == "" {
		t.Errorf("tool_use id/name were lost: %+v", uses[0])
	}
	var input map[string]any
	if err := json.Unmarshal(uses[0].Input, &input); err != nil {
		t.Fatalf("tool input should be preserved as raw JSON: %v", err)
	}
	if input["file_path"] == "" {
		t.Errorf("tool input was lost: %v", input)
	}
	if len(uses[0].Caller) == 0 {
		t.Error("caller was dropped; the CLI sends one on every tool_use block")
	}
}

// The pairing that makes a transcript renderable: every tool_use on the
// assistant side has a matching tool_result on the following user turn.
func TestToolUseResultsPairByID(t *testing.T) {
	assistantEvent, err := parseLine(readMessageFixture(t, "assistant_tool_use.json"))
	if err != nil {
		t.Fatalf("parseLine assistant: %v", err)
	}
	userEvent, err := parseLine(readMessageFixture(t, "user_tool_result.json"))
	if err != nil {
		t.Fatalf("parseLine user: %v", err)
	}

	resultIDs := make(map[string]bool)
	for _, r := range userEvent.User.ToolResults() {
		resultIDs[r.ToolUseID] = true
	}

	uses := assistantEvent.Assistant.ToolUses()
	if len(uses) == 0 {
		t.Fatal("no tool_use blocks to pair")
	}
	for _, u := range uses {
		if !resultIDs[u.ID] {
			t.Errorf("tool_use %q has no matching tool_result; ids are %v", u.ID, resultIDs)
		}
	}
}

// A block type this SDK has never seen must survive as Type + Raw rather than
// being dropped, and must not damage the blocks around it.
func TestContentBlock_UnknownTypePreserved(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"role":"assistant","content":[
		{"type":"text","text":"before"},
		{"type":"telepathy_block","waves":42,"nested":{"a":1}},
		{"type":"text","text":"after"}
	]},"session_id":"s","uuid":"u"}`)

	event, err := parseLine(line)
	if err != nil {
		t.Fatalf("parseLine: %v", err)
	}
	if event.Assistant == nil {
		t.Fatal("an unknown block nilled the whole message")
	}

	blocks := event.Assistant.Message.Content
	if len(blocks) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(blocks))
	}
	if blocks[1].Type != "telepathy_block" {
		t.Errorf("unknown block type was lost: %q", blocks[1].Type)
	}

	var raw map[string]any
	if err := json.Unmarshal(blocks[1].Raw, &raw); err != nil {
		t.Fatalf("unknown block must keep its payload in Raw: %v", err)
	}
	if raw["waves"] != float64(42) {
		t.Errorf("unknown block payload was lost: %v", raw)
	}
	if event.Assistant.Text() != "beforeafter" {
		t.Errorf("surrounding text blocks were damaged: %q", event.Assistant.Text())
	}
}

// Content is an array of blocks on everything the CLI originates, but a bare
// string on what this SDK sends and what transcripts replay. Both must decode.
func TestContentBlocks_AcceptsBareString(t *testing.T) {
	line := []byte(`{"type":"user","message":{"role":"user","content":"just text"},
		"session_id":"s","uuid":"u"}`)

	event, err := parseLine(line)
	if err != nil {
		t.Fatalf("parseLine: %v", err)
	}
	if event.User == nil {
		t.Fatal("Event.User is nil for a string-content user turn")
	}
	if event.DecodeErr != nil {
		t.Fatalf("string content is a valid wire shape, not a decode error: %v", event.DecodeErr)
	}

	blocks := event.User.Message.Content
	if len(blocks) != 1 || blocks[0].Type != BlockText {
		t.Fatalf("a bare string should become one text block, got %+v", blocks)
	}
	if event.User.Text() != "just text" {
		t.Errorf("text was lost: %q", event.User.Text())
	}
}

// ─── stream_event passthrough (#27) ───────────────────────────────────────────

// readStreamSequence returns the captured stream_event lines of one assistant
// turn, in arrival order.
func readStreamSequence(t *testing.T) [][]byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", "messages", "stream_tool_use_sequence.jsonl"))
	if err != nil {
		t.Fatalf("read stream sequence: %v", err)
	}

	var lines [][]byte
	for _, l := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		if len(bytes.TrimSpace(l)) > 0 {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		t.Fatal("stream sequence fixture is empty")
	}
	return lines
}

// The point of streaming a tool call: reassembling input_json_delta fragments
// yields exactly the input the non-streaming assistant message reports. The
// fragments split mid-string, so this only works if every one is preserved.
func TestStreamEvent_InputJSONAccumulation(t *testing.T) {
	var (
		toolName    string
		toolID      string
		accumulated = map[int]string{}
		blockIdx    = -1
	)

	for _, line := range readStreamSequence(t) {
		event, err := parseLine(line)
		if err != nil {
			t.Fatalf("parseLine: %v", err)
		}
		if event.StreamEvent == nil {
			t.Fatal("Event.StreamEvent is nil")
		}
		ev := event.StreamEvent.Event

		if block, ok := ev.ContentBlockStart(); ok && block.Type == BlockToolUse {
			toolName, toolID, blockIdx = block.Name, block.ID, ev.Index
		}
		if fragment, ok := ev.PartialJSON(); ok {
			accumulated[ev.Index] += fragment
		}
	}

	if toolName != "Read" || toolID == "" {
		t.Fatalf("content_block_start did not announce the tool: name=%q id=%q", toolName, toolID)
	}
	if blockIdx < 0 {
		t.Fatal("no tool_use block was started")
	}

	assembled := accumulated[blockIdx]
	if assembled == "" {
		t.Fatal("no input_json_delta fragments were accumulated")
	}

	// Compare semantically: the streamed fragments carry the model's own
	// whitespace, the assembled assistant message does not.
	var streamed, complete map[string]any
	if err := json.Unmarshal([]byte(assembled), &streamed); err != nil {
		t.Fatalf("accumulated fragments are not valid JSON (%q): %v", assembled, err)
	}

	assistantEvent, err := parseLine(readMessageFixture(t, "assistant_tool_use.json"))
	if err != nil {
		t.Fatalf("parseLine assistant: %v", err)
	}
	if err := json.Unmarshal(assistantEvent.Assistant.ToolUses()[0].Input, &complete); err != nil {
		t.Fatalf("unmarshal complete input: %v", err)
	}

	if !reflect.DeepEqual(streamed, complete) {
		t.Errorf("streamed input %v != complete input %v", streamed, complete)
	}
}

// The tail of a turn — why it stopped and what it cost — rides message_delta,
// with usage as a sibling of delta rather than inside it.
func TestStreamEvent_MessageDeltaTail(t *testing.T) {
	var seen bool

	for _, line := range readStreamSequence(t) {
		event, err := parseLine(line)
		if err != nil {
			t.Fatalf("parseLine: %v", err)
		}

		stopReason, usage, ok := event.StreamEvent.Event.MessageDelta()
		if !ok {
			continue
		}
		seen = true

		if stopReason != "tool_use" {
			t.Errorf("expected stop_reason tool_use, got %q", stopReason)
		}
		if len(usage) == 0 {
			t.Fatal("message_delta usage was dropped")
		}
		var u map[string]any
		if err := json.Unmarshal(usage, &u); err != nil {
			t.Fatalf("usage should be preserved as raw JSON: %v", err)
		}
		if u["output_tokens"] == nil {
			t.Errorf("usage lost its token counts: %v", u)
		}
	}

	if !seen {
		t.Fatal("the sequence contains a message_delta but MessageDelta() never matched")
	}
}

// Text streaming must keep working exactly as before — this is the path every
// current caller of Delta.Text is on.
func TestStreamEvent_TextDeltaStillWorks(t *testing.T) {
	var viaField, viaAccessor string

	for _, line := range readStreamSequence(t) {
		event, err := parseLine(line)
		if err != nil {
			t.Fatalf("parseLine: %v", err)
		}
		ev := event.StreamEvent.Event

		if ev.Delta != nil {
			viaField += ev.Delta.Text
		}
		if text, ok := ev.TextDelta(); ok {
			viaAccessor += text
		}
	}

	if viaField == "" {
		t.Fatal("Delta.Text no longer accumulates; existing callers would break")
	}
	if viaField != viaAccessor {
		t.Errorf("accessor disagrees with the field: %q vs %q", viaAccessor, viaField)
	}
	if !strings.Contains(viaField, "read the file") {
		t.Errorf("streamed text was lost: %q", viaField)
	}
}

// Every stream event keeps its verbatim payload, so a field this struct does
// not model is still reachable.
func TestStreamEvent_RawPreserved(t *testing.T) {
	for _, line := range readStreamSequence(t) {
		event, err := parseLine(line)
		if err != nil {
			t.Fatalf("parseLine: %v", err)
		}
		ev := event.StreamEvent.Event

		if len(ev.Raw) == 0 {
			t.Fatalf("StreamEvent.Raw is empty for a %q event", ev.Type)
		}
		var decoded map[string]any
		if err := json.Unmarshal(ev.Raw, &decoded); err != nil {
			t.Fatalf("Raw is not the event payload: %v", err)
		}
		if decoded["type"] != ev.Type {
			t.Errorf("Raw holds a different event: %v vs %q", decoded["type"], ev.Type)
		}
	}
}

// Go unmarshals JSON null into a string as a no-op, so the bare-string branch
// would quietly turn absent content into one empty text block. Absent content
// must stay absent — this is the same trap that admitted empty ids in #18.
func TestContentBlocks_NullIsNotAnEmptyTextBlock(t *testing.T) {
	line := []byte(`{"type":"user","message":{"role":"user","content":null},
		"session_id":"s","uuid":"u"}`)

	event, err := parseLine(line)
	if err != nil {
		t.Fatalf("parseLine: %v", err)
	}
	if event.User == nil {
		t.Fatal("Event.User is nil")
	}
	if blocks := event.User.Message.Content; len(blocks) != 0 {
		t.Errorf("null content must decode to no blocks, got %+v", blocks)
	}
}

// ─── Result completeness (#28) ────────────────────────────────────────────────

// A run that hit the turn limit reports both the subtype and the reason. The
// captured fixture comes from `--max-turns 1`.
func TestParseLine_ResultMaxTurns(t *testing.T) {
	event, err := parseLine(readMessageFixture(t, "result_max_turns.json"))
	if err != nil {
		t.Fatalf("parseLine: %v", err)
	}
	if event.Result == nil {
		t.Fatal("Result is nil")
	}
	if event.DecodeErr != nil {
		t.Fatalf("captured payload should decode cleanly: %v", event.DecodeErr)
	}

	r := event.Result
	if r.Subtype != SubtypeErrorMaxTurns {
		t.Errorf("expected subtype %q, got %q", SubtypeErrorMaxTurns, r.Subtype)
	}
	if r.TerminalReason != TerminalMaxTurns {
		t.Errorf("expected terminal_reason %q, got %q", TerminalMaxTurns, r.TerminalReason)
	}
	if r.TerminalReason.Aborted() {
		t.Error("hitting the turn limit is not a cancellation")
	}
	if !r.IsError {
		t.Error("is_error was true on the wire")
	}
}

// An interrupted turn is the case Subtype alone cannot express: it reports
// error_during_execution, exactly like other execution failures. terminal_reason
// is what distinguishes "the user stopped it" (#18).
func TestParseLine_ResultAbortedByInterrupt(t *testing.T) {
	event, err := parseLine(readMessageFixture(t, "result_aborted_streaming.json"))
	if err != nil {
		t.Fatalf("parseLine: %v", err)
	}
	if event.Result == nil {
		t.Fatal("Result is nil")
	}
	if event.DecodeErr != nil {
		t.Fatalf("captured payload should decode cleanly: %v", event.DecodeErr)
	}

	r := event.Result
	if r.TerminalReason != TerminalAbortedStreaming {
		t.Fatalf("expected terminal_reason %q, got %q", TerminalAbortedStreaming, r.TerminalReason)
	}
	if !r.TerminalReason.Aborted() {
		t.Error("aborted_streaming must report as a cancellation")
	}
	// The point of the field: the subtype is indistinguishable from any other
	// execution error, so only terminal_reason identifies the interrupt.
	if r.Subtype != SubtypeErrorDuringExecution {
		t.Errorf("expected subtype %q, got %q", SubtypeErrorDuringExecution, r.Subtype)
	}
}

// A successful run reports terminal_reason too, and carries no api_error_status.
func TestParseLine_ResultSuccessTerminalReason(t *testing.T) {
	event, err := parseLine(readMessageFixture(t, "result_success.json"))
	if err != nil {
		t.Fatalf("parseLine: %v", err)
	}

	r := event.Result
	if r.TerminalReason != TerminalCompleted {
		t.Errorf("expected terminal_reason %q, got %q", TerminalCompleted, r.TerminalReason)
	}
	if r.APIErrorStatus != nil {
		t.Errorf("api_error_status was null on the wire; want nil, got %d", *r.APIErrorStatus)
	}
	if r.DeferredToolUse != nil {
		t.Error("no tool was deferred in this run")
	}
}

// api_error_status must distinguish "absent" from any value, including 0 —
// which is why it is a pointer. No session here produced an upstream failure,
// so this drives the decoder with the shape the CLI documents.
func TestResult_APIErrorStatusAbsentVsPresent(t *testing.T) {
	for _, tc := range []struct {
		name  string
		wire  string
		want  int
		isNil bool
	}{
		{name: "absent", wire: `{"type":"result"}`, isNil: true},
		{name: "null", wire: `{"type":"result","api_error_status":null}`, isNil: true},
		{name: "rate limited", wire: `{"type":"result","api_error_status":429}`, want: 429},
		{name: "overloaded", wire: `{"type":"result","api_error_status":529}`, want: 529},
		{name: "zero", wire: `{"type":"result","api_error_status":0}`, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event, err := parseLine([]byte(tc.wire))
			if err != nil {
				t.Fatalf("parseLine: %v", err)
			}
			got := event.Result.APIErrorStatus

			if tc.isNil {
				if got != nil {
					t.Fatalf("expected nil, got %d", *got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected a status, got nil")
			}
			if *got != tc.want {
				t.Errorf("expected %d, got %d", tc.want, *got)
			}
		})
	}
}

// TerminalReason is a named string, not a closed enum: a reason this SDK has
// never heard of must survive decoding so callers can log or branch on it.
func TestTerminalReason_UnknownValueSurvives(t *testing.T) {
	event, err := parseLine([]byte(`{"type":"result","terminal_reason":"abducted_by_aliens"}`))
	if err != nil {
		t.Fatalf("parseLine: %v", err)
	}
	if got := event.Result.TerminalReason; got != "abducted_by_aliens" {
		t.Errorf("unknown terminal_reason was not preserved: %q", got)
	}
	if event.Result.TerminalReason.Aborted() {
		t.Error("an unknown reason must not be reported as a cancellation")
	}
}

// The deferred tool call a PreToolUse "defer" decision parks. Hooks cannot
// return "defer" from this SDK yet (#32), so this proves the decode path only.
func TestResult_DeferredToolUseDecodes(t *testing.T) {
	line := []byte(`{"type":"result","subtype":"success","deferred_tool_use":
		{"id":"toolu_01ABC","name":"Bash","input":{"command":"ls -la"}}}`)

	event, err := parseLine(line)
	if err != nil {
		t.Fatalf("parseLine: %v", err)
	}
	d := event.Result.DeferredToolUse
	if d == nil {
		t.Fatal("deferred_tool_use did not decode")
	}
	if d.ID != "toolu_01ABC" || d.Name != "Bash" {
		t.Errorf("id/name did not decode: %+v", d)
	}
	var input map[string]any
	if err := json.Unmarshal(d.Input, &input); err != nil {
		t.Fatalf("input should be preserved as raw JSON: %v", err)
	}
	if input["command"] != "ls -la" {
		t.Errorf("input did not decode: %v", input)
	}
}

// The captured modelUsage entry carries the fields that identify which model
// and provider a cost belongs to.
func TestParseLine_ModelUsageIdentityFields(t *testing.T) {
	event, err := parseLine(readMessageFixture(t, "result_success.json"))
	if err != nil {
		t.Fatalf("parseLine: %v", err)
	}
	if len(event.Result.ModelUsages) == 0 {
		t.Fatal("modelUsage did not decode")
	}

	for model, mu := range event.Result.ModelUsages {
		if mu.CanonicalModel == "" {
			t.Errorf("%s: canonicalModel did not decode", model)
		}
		if mu.Provider != ProviderFirstParty {
			t.Errorf("%s: expected provider %q, got %q", model, ProviderFirstParty, mu.Provider)
		}
	}
}

// ─── Task, rate-limit, init and hook lifecycle payloads (#29) ─────────────────

// readLifecycle returns a captured background-task sequence in arrival order.
func readLifecycle(t *testing.T, name string) []Event {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", "messages", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}

	var events []Event
	for i, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		event, err := parseLine(line)
		if err != nil {
			t.Fatalf("%s line %d: parseLine: %v", name, i, err)
		}
		if event.DecodeErr != nil {
			t.Fatalf("%s line %d: captured line did not decode cleanly: %v", name, i, event.DecodeErr)
		}
		events = append(events, event)
	}
	if len(events) == 0 {
		t.Fatalf("%s is empty", name)
	}
	return events
}

// The whole point of typing these: a caller must be able to track a background
// task to its terminal state without touching raw JSON. The tracker below is
// what a real consumer writes — add on task_started, clear on any terminal
// status — and it must end empty for both a completed and a killed task.
func TestTaskLifecycle_TrackedToTerminalState(t *testing.T) {
	for _, tc := range []struct {
		fixture    string
		wantStatus TaskStatus
	}{
		{"task_lifecycle_completed.jsonl", TaskCompleted},
		{"task_lifecycle_killed.jsonl", TaskKilled},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			active := map[string]string{}
			var sawTerminal TaskStatus

			for _, event := range readLifecycle(t, tc.fixture) {
				switch {
				case event.TaskStarted != nil:
					active[event.TaskStarted.TaskID] = event.TaskStarted.Description

				case event.TaskUpdated != nil:
					if event.TaskUpdated.Status.IsTerminal() {
						delete(active, event.TaskUpdated.TaskID)
						if sawTerminal == "" {
							sawTerminal = event.TaskUpdated.Status
						}
					}

				case event.TaskNotification != nil:
					if event.TaskNotification.Status.IsTerminal() {
						delete(active, event.TaskNotification.TaskID)
					}
				}
			}

			if len(active) != 0 {
				t.Errorf("task tracking leaked %d task(s): %v", len(active), active)
			}
			if sawTerminal != tc.wantStatus {
				t.Errorf("expected terminal status %q from task_updated, got %q", tc.wantStatus, sawTerminal)
			}
		})
	}
}

// task_updated carries the new status inside `patch`, not at the top level.
// A consumer reading a top-level `status` sees nothing at all.
func TestTaskUpdated_StatusComesFromPatch(t *testing.T) {
	var updated *TaskUpdatedMessage
	for _, event := range readLifecycle(t, "task_lifecycle_killed.jsonl") {
		if event.TaskUpdated != nil {
			updated = event.TaskUpdated
		}
	}
	if updated == nil {
		t.Fatal("no task_updated in the captured sequence")
	}

	if updated.Status != TaskKilled {
		t.Errorf("expected status %q lifted from the patch, got %q", TaskKilled, updated.Status)
	}
	if updated.TaskID == "" {
		t.Error("task_id did not decode")
	}

	// Prove the status is not available at the top level — that's why it is lifted.
	var wire struct {
		Status string          `json:"status"`
		Patch  json.RawMessage `json:"patch"`
	}
	if err := json.Unmarshal(updated.Raw, &wire); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	if wire.Status != "" {
		t.Errorf("captured task_updated has a top-level status %q; revisit the lift", wire.Status)
	}
	if len(wire.Patch) == 0 {
		t.Error("captured task_updated has no patch")
	}
}

// The two vocabularies overlap: a stopped task reports "killed" via task_updated
// and "stopped" via task_notification. Both must count as terminal, which is
// why TerminalTaskStatuses spans them.
func TestTaskStatus_TerminalSpansBothVocabularies(t *testing.T) {
	for _, s := range []TaskStatus{TaskCompleted, TaskFailed, TaskStopped, TaskKilled} {
		if !s.IsTerminal() {
			t.Errorf("%q must be terminal", s)
		}
	}
	for _, s := range []TaskStatus{TaskPending, TaskRunning, TaskPaused, "invented"} {
		if s.IsTerminal() {
			t.Errorf("%q must not be terminal", s)
		}
	}

	// The captured killed session reports both names for one stop.
	var updatedStatus, notificationStatus TaskStatus
	for _, event := range readLifecycle(t, "task_lifecycle_killed.jsonl") {
		if event.TaskUpdated != nil {
			updatedStatus = event.TaskUpdated.Status
		}
		if event.TaskNotification != nil {
			notificationStatus = event.TaskNotification.Status
		}
	}
	if updatedStatus != TaskKilled || notificationStatus != TaskStopped {
		t.Errorf("expected killed/stopped from one stop, got %q/%q", updatedStatus, notificationStatus)
	}
}

func TestParseLine_TaskStartedAndNotificationFields(t *testing.T) {
	var (
		started      *TaskStartedMessage
		notification *TaskNotificationMessage
	)
	for _, event := range readLifecycle(t, "task_lifecycle_completed.jsonl") {
		if event.TaskStarted != nil {
			started = event.TaskStarted
		}
		if event.TaskNotification != nil {
			notification = event.TaskNotification
		}
	}

	if started == nil || notification == nil {
		t.Fatal("captured sequence is missing task_started or task_notification")
	}
	if started.ToolUseID == "" || started.Description == "" || started.TaskType == "" {
		t.Errorf("task_started fields did not decode: %+v", started)
	}
	if started.SessionID == "" || started.UUID == "" {
		t.Errorf("task_started envelope did not decode: %+v", started)
	}
	if notification.OutputFile == "" || notification.Summary == "" {
		t.Errorf("task_notification fields did not decode: %+v", notification)
	}
	if notification.TaskID != started.TaskID {
		t.Errorf("notification %q does not match started %q", notification.TaskID, started.TaskID)
	}
	if len(started.Raw) == 0 || len(notification.Raw) == 0 {
		t.Error("every lifecycle struct must keep its Raw tail")
	}
}

// A rate-limit event tells a caller when to back off. Its inner object is
// camelCase on the wire — like modelUsage, unlike the message around it.
func TestParseLine_RateLimitEvent(t *testing.T) {
	event, err := parseLine(readMessageFixture(t, "rate_limit_event.json"))
	if err != nil {
		t.Fatalf("parseLine: %v", err)
	}
	if event.Type != TypeRateLimitEvent {
		t.Fatalf("expected type %q, got %q", TypeRateLimitEvent, event.Type)
	}
	if event.RateLimit == nil {
		t.Fatal("RateLimit is nil — rate_limit_event used to decode to Raw only")
	}
	if event.DecodeErr != nil {
		t.Fatalf("captured payload should decode cleanly: %v", event.DecodeErr)
	}

	info := event.RateLimit.RateLimitInfo
	if info.Status != RateLimitAllowed {
		t.Errorf("expected status %q, got %q", RateLimitAllowed, info.Status)
	}
	if info.Limited() {
		t.Error("an allowed status is not rate limited")
	}
	if info.RateLimitType != RateLimitFiveHour {
		t.Errorf("expected rateLimitType %q, got %q", RateLimitFiveHour, info.RateLimitType)
	}
	if info.ResetsAt == 0 {
		t.Error("resetsAt did not decode — check the camelCase tag")
	}
	if info.OverageStatus == "" || info.OverageDisabledReason == "" {
		t.Errorf("overage fields did not decode: %+v", info)
	}
	if len(info.Raw) == 0 {
		t.Error("RateLimitInfo must keep its Raw tail")
	}
	if event.RateLimit.SessionID == "" || event.RateLimit.UUID == "" {
		t.Error("envelope fields did not decode")
	}
}

// The rate-limit vocabularies are open: a window or status this SDK has never
// heard of must decode through rather than being dropped.
func TestRateLimit_UnknownValuesSurvive(t *testing.T) {
	line := []byte(`{"type":"rate_limit_event","rate_limit_info":
		{"status":"allowed_unless_tuesday","rateLimitType":"seven_day_overage_included",
		 "utilization":0.42,"unmodelled":"keep me"}}`)

	event, err := parseLine(line)
	if err != nil {
		t.Fatalf("parseLine: %v", err)
	}
	info := event.RateLimit.RateLimitInfo
	if info.Status != "allowed_unless_tuesday" {
		t.Errorf("unknown status was not preserved: %q", info.Status)
	}
	if info.RateLimitType != "seven_day_overage_included" {
		t.Errorf("unknown rateLimitType was not preserved: %q", info.RateLimitType)
	}
	if info.Utilization != 0.42 {
		t.Errorf("utilization did not decode: %v", info.Utilization)
	}

	var raw map[string]any
	if err := json.Unmarshal(info.Raw, &raw); err != nil {
		t.Fatalf("unmarshal Raw: %v", err)
	}
	if raw["unmodelled"] != "keep me" {
		t.Error("Raw must retain fields the struct does not model")
	}
}

// system/init is where the CLI advertises what it can do. capabilities is the
// list Session.Capabilities() serves (#19) — it is not in the initialize
// response.
func TestParseLine_InitCompleteness(t *testing.T) {
	event, err := parseLine(readMessageFixture(t, "system_init_full.json"))
	if err != nil {
		t.Fatalf("parseLine: %v", err)
	}
	if event.System == nil {
		t.Fatal("System is nil")
	}
	if event.DecodeErr != nil {
		t.Fatalf("captured payload should decode cleanly: %v", event.DecodeErr)
	}

	s := event.System
	if len(s.Capabilities) == 0 {
		t.Error("capabilities did not decode; feature detection depends on it")
	}
	if len(s.MCPServers) == 0 {
		t.Fatal("mcp_servers did not decode")
	}
	if s.MCPServers[0].Name == "" || s.MCPServers[0].Status == "" {
		t.Errorf("mcp_servers entries did not decode: %+v", s.MCPServers[0])
	}
	if s.OutputStyle == "" {
		t.Error("output_style did not decode")
	}
	if s.FastModeState == "" {
		t.Error("fast_mode_state did not decode")
	}
	if s.UUID == "" {
		t.Error("uuid did not decode")
	}
}

// Hook lifecycle messages arrive as system subtypes carrying the hook that
// fired. The CLI only emits them when hook events are enabled (#31), so this
// drives the decoder with the documented shape.
func TestParseLine_HookLifecycle(t *testing.T) {
	for _, subtype := range []string{SubtypeHookStarted, SubtypeHookProgress, SubtypeHookResponse} {
		t.Run(subtype, func(t *testing.T) {
			line := []byte(`{"type":"system","subtype":"` + subtype +
				`","hook_event":"PreToolUse","session_id":"s","uuid":"u"}`)

			event, err := parseLine(line)
			if err != nil {
				t.Fatalf("parseLine: %v", err)
			}
			if event.HookLifecycle == nil {
				t.Fatal("HookLifecycle was not populated")
			}
			if event.HookLifecycle.HookEvent != "PreToolUse" {
				t.Errorf("hook_event did not decode: %q", event.HookLifecycle.HookEvent)
			}
			if event.HookLifecycle.Subtype != subtype {
				t.Errorf("subtype did not decode: %q", event.HookLifecycle.Subtype)
			}
			if len(event.HookLifecycle.Raw) == 0 {
				t.Error("Raw tail is missing")
			}
			// System stays populated alongside the lifecycle field.
			if event.System == nil {
				t.Error("System must still be set for a system message")
			}
		})
	}
}

// tool_progress reports that a tool is still running. Its previous Progress and
// Message fields existed nowhere on the wire; this asserts the shape the CLI
// actually emits.
func TestParseLine_ToolProgressRealFields(t *testing.T) {
	line := []byte(`{"type":"tool_progress","tool_use_id":"toolu_1","tool_name":"Bash",
		"parent_tool_use_id":null,"elapsed_time_seconds":12.5,"task_id":"bg1",
		"session_id":"s","uuid":"u"}`)

	event, err := parseLine(line)
	if err != nil {
		t.Fatalf("parseLine: %v", err)
	}
	tp := event.ToolProgress
	if tp == nil {
		t.Fatal("ToolProgress is nil")
	}
	if tp.ToolName != "Bash" {
		t.Errorf("tool_name did not decode: %q", tp.ToolName)
	}
	if tp.ElapsedTimeSeconds != 12.5 {
		t.Errorf("elapsed_time_seconds did not decode: %v", tp.ElapsedTimeSeconds)
	}
	if tp.TaskID != "bg1" {
		t.Errorf("task_id did not decode: %q", tp.TaskID)
	}
	if len(tp.Raw) == 0 {
		t.Error("Raw tail is missing")
	}
}

// A heartbeat tick reports no progress, only that the tool is alive.
func TestParseLine_ToolProgressHeartbeat(t *testing.T) {
	line := []byte(`{"type":"tool_progress","tool_use_id":"toolu_1","tool_name":"WebFetch",
		"elapsed_time_seconds":30,"heartbeat":true,"session_id":"s","uuid":"u"}`)

	event, err := parseLine(line)
	if err != nil {
		t.Fatalf("parseLine: %v", err)
	}
	if !event.ToolProgress.Heartbeat {
		t.Error("heartbeat did not decode")
	}
}

// A top-level status, which the official SDKs' types declare but CLI 2.1.224
// does not send, must win over the patch rather than being ignored.
func TestTaskUpdated_TopLevelStatusWins(t *testing.T) {
	line := []byte(`{"type":"system","subtype":"task_updated","task_id":"t1",
		"status":"failed","patch":{"status":"running"}}`)

	event, err := parseLine(line)
	if err != nil {
		t.Fatalf("parseLine: %v", err)
	}
	if event.TaskUpdated.Status != TaskFailed {
		t.Errorf("expected the top-level status to win, got %q", event.TaskUpdated.Status)
	}
}
