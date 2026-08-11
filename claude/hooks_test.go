package claude

import (
	"encoding/json"
	"testing"
)

func TestBuildHooksForInitialize_Empty(t *testing.T) {
	cfg, reg := buildHooksForInitialize(nil)
	if len(cfg) != 0 {
		t.Fatalf("expected empty config, got %v", cfg)
	}
	if len(reg) != 0 {
		t.Fatalf("expected empty registry, got %v", reg)
	}
}

// matcherConfigs pulls the per-event matcher list out of the initialize config.
func matcherConfigs(t *testing.T, cfg map[string]any, event HookEvent) []map[string]any {
	t.Helper()
	raw, ok := cfg[string(event)]
	if !ok {
		t.Fatalf("expected %s key in config, got keys %v", event, cfg)
	}
	matchers, ok := raw.([]map[string]any)
	if !ok {
		t.Fatalf("expected matchers to be []map[string]any, got %T", raw)
	}
	return matchers
}

func callbackIDs(t *testing.T, m map[string]any) []string {
	t.Helper()
	ids, ok := m["hookCallbackIds"].([]string)
	if !ok {
		t.Fatalf("expected hookCallbackIds to be []string, got %T", m["hookCallbackIds"])
	}
	return ids
}

func noopHook(*HookOutput) HookFunc {
	return func(HookEvent, json.RawMessage, string) (*HookOutput, error) { return nil, nil }
}

// One matcher with two callbacks must produce exactly ONE matcher entry whose
// hookCallbackIds carries both IDs in registration order — the shape the CLI
// validator requires ("arrays of matchers carrying hookCallbackIds arrays").
func TestBuildHooksForInitialize_GroupsCallbacksPerMatcher(t *testing.T) {
	firstCalled, secondCalled := false, false
	hooks := map[HookEvent][]HookMatcher{
		HookEventPreToolUse: {{
			Matcher: "Bash",
			Hooks: []HookFunc{
				func(HookEvent, json.RawMessage, string) (*HookOutput, error) {
					firstCalled = true
					return nil, nil
				},
				func(HookEvent, json.RawMessage, string) (*HookOutput, error) {
					secondCalled = true
					return nil, nil
				},
			},
			Timeout: 30,
		}},
	}

	cfg, reg := buildHooksForInitialize(hooks)

	matchers := matcherConfigs(t, cfg, HookEventPreToolUse)
	if len(matchers) != 1 {
		t.Fatalf("expected 1 matcher entry for 1 matcher x 2 callbacks, got %d", len(matchers))
	}
	if _, legacy := matchers[0]["callback_id"]; legacy {
		t.Fatal("matcher entry must not carry the legacy per-function callback_id field")
	}
	if matchers[0]["matcher"] != "Bash" {
		t.Fatalf("expected matcher 'Bash', got %v", matchers[0]["matcher"])
	}
	// Timeout passes through unconverted: the wire unit is seconds.
	if matchers[0]["timeout"] != 30 {
		t.Fatalf("expected timeout 30 (seconds, no conversion), got %v", matchers[0]["timeout"])
	}

	ids := callbackIDs(t, matchers[0])
	if len(ids) != 2 {
		t.Fatalf("expected 2 callback IDs grouped under one matcher, got %d", len(ids))
	}
	if len(reg) != 2 {
		t.Fatalf("expected 2 registry entries, got %d", len(reg))
	}

	// IDs must be in registration order and resolve to the right function.
	if _, err := reg[ids[0]](HookEventPreToolUse, nil, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !firstCalled || secondCalled {
		t.Fatal("hookCallbackIds[0] must resolve to the first registered callback")
	}
	if _, err := reg[ids[1]](HookEventPreToolUse, nil, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !secondCalled {
		t.Fatal("hookCallbackIds[1] must resolve to the second registered callback")
	}
}

// Two matchers on one event produce two entries, each with its own matcher
// string and its own IDs.
func TestBuildHooksForInitialize_MultipleMatchers(t *testing.T) {
	hooks := map[HookEvent][]HookMatcher{
		HookEventPreToolUse: {
			{Matcher: "Bash", Hooks: []HookFunc{noopHook(nil)}},
			{Matcher: "Write|Edit", Hooks: []HookFunc{noopHook(nil), noopHook(nil)}},
		},
	}

	cfg, reg := buildHooksForInitialize(hooks)

	matchers := matcherConfigs(t, cfg, HookEventPreToolUse)
	if len(matchers) != 2 {
		t.Fatalf("expected 2 matcher entries, got %d", len(matchers))
	}
	if matchers[0]["matcher"] != "Bash" || len(callbackIDs(t, matchers[0])) != 1 {
		t.Fatalf("first entry wrong: %v", matchers[0])
	}
	if matchers[1]["matcher"] != "Write|Edit" || len(callbackIDs(t, matchers[1])) != 2 {
		t.Fatalf("second entry wrong: %v", matchers[1])
	}
	if len(reg) != 3 {
		t.Fatalf("expected 3 registry entries across both matchers, got %d", len(reg))
	}
}

// An unset matcher is transmitted as JSON null (present, not omitted), matching
// the official Python SDK, which always emits the key.
func TestBuildHooksForInitialize_UnsetMatcherIsNull(t *testing.T) {
	hooks := map[HookEvent][]HookMatcher{
		HookEventUserPromptSubmit: {{Hooks: []HookFunc{noopHook(nil)}}},
	}

	cfg, _ := buildHooksForInitialize(hooks)
	matchers := matcherConfigs(t, cfg, HookEventUserPromptSubmit)

	if _, present := matchers[0]["matcher"]; !present {
		t.Fatal("matcher key must always be present")
	}
	if matchers[0]["matcher"] != nil {
		t.Fatalf("expected nil matcher, got %v", matchers[0]["matcher"])
	}

	encoded, err := json.Marshal(matchers[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(decoded["matcher"]) != "null" {
		t.Fatalf("expected matcher to serialise as null, got %s", decoded["matcher"])
	}
}

// A zero timeout is omitted so the CLI applies its own default (60s).
func TestBuildHooksForInitialize_ZeroTimeoutOmitted(t *testing.T) {
	hooks := map[HookEvent][]HookMatcher{
		HookEventPreToolUse: {{Matcher: "Bash", Hooks: []HookFunc{noopHook(nil)}}},
	}

	cfg, _ := buildHooksForInitialize(hooks)
	matchers := matcherConfigs(t, cfg, HookEventPreToolUse)

	if _, present := matchers[0]["timeout"]; present {
		t.Fatalf("expected timeout to be omitted when zero, got %v", matchers[0]["timeout"])
	}
}

// A matcher registering no callbacks contributes nothing.
func TestBuildHooksForInitialize_SkipsEmptyMatchers(t *testing.T) {
	hooks := map[HookEvent][]HookMatcher{
		HookEventPreToolUse: {{Matcher: "Bash"}},
	}

	cfg, reg := buildHooksForInitialize(hooks)
	if len(cfg) != 0 {
		t.Fatalf("expected no event keys for a callback-less matcher, got %v", cfg)
	}
	if len(reg) != 0 {
		t.Fatalf("expected empty registry, got %v", reg)
	}
}

// The serialised payload must match the shape the real CLI accepts. The
// expected JSON below was captured by sending candidate initialize payloads to
// claude 2.1.224: this shape returns subtype "success", while the previous
// per-function {"callback_id": …} shape is rejected with "hooks must map hook
// events to arrays of matchers carrying hookCallbackIds arrays and string
// matchers".
func TestBuildHooksForInitialize_MatchesCapturedCLIShape(t *testing.T) {
	hooks := map[HookEvent][]HookMatcher{
		HookEventPreToolUse: {{
			Matcher: "Bash",
			Hooks:   []HookFunc{noopHook(nil), noopHook(nil)},
			Timeout: 30,
		}},
	}

	cfg, _ := buildHooksForInitialize(hooks)
	matchers := matcherConfigs(t, cfg, HookEventPreToolUse)
	ids := callbackIDs(t, matchers[0])

	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	expected, err := json.Marshal(map[string]any{
		"PreToolUse": []map[string]any{{
			"matcher":         "Bash",
			"hookCallbackIds": ids,
			"timeout":         30,
		}},
	})
	if err != nil {
		t.Fatalf("marshal expected: %v", err)
	}

	if string(encoded) != string(expected) {
		t.Fatalf("initialize hooks payload does not match the CLI-accepted shape:\n got: %s\nwant: %s",
			encoded, expected)
	}
}

func TestHookEventConstants(t *testing.T) {
	// Every constant must name an event that exists in the CLI's enum.
	events := []HookEvent{
		HookEventPreToolUse, HookEventPostToolUse, HookEventPostToolUseFailure,
		HookEventNotification, HookEventStop, HookEventSubagentStop,
		HookEventSubagentStart, HookEventPreCompact, HookEventUserPromptSubmit,
		HookEventSessionStart, HookEventSetup,
		HookEventPermissionRequest,
		HookEventSessionEnd, HookEventTeammateIdle, HookEventTaskCompleted,
		HookEventElicitation, HookEventElicitationResult,
		HookEventConfigChange, HookEventWorktreeCreate, HookEventWorktreeRemove,
	}
	for _, e := range events {
		if string(e) == "" {
			t.Fatal("hook event constant has empty string value")
		}
	}

	if HookEventSessionStart != "SessionStart" {
		t.Fatalf("expected SessionStart, got %q", HookEventSessionStart)
	}
}
