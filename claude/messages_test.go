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
