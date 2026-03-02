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

func TestBuildHooksForInitialize_WithHooks(t *testing.T) {
	called := false
	hooks := map[HookEvent][]HookMatcher{
		HookEventPreToolUse: {
			{
				Matcher: "Bash",
				Hooks: []HookFunc{
					func(event HookEvent, input json.RawMessage, toolUseID string) (*HookOutput, error) {
						called = true
						return &HookOutput{Decision: "approve"}, nil
					},
				},
				Timeout: 5000,
			},
		},
	}

	cfg, reg := buildHooksForInitialize(hooks)

	// Should have one event key.
	if len(cfg) != 1 {
		t.Fatalf("expected 1 event key, got %d", len(cfg))
	}
	preToolUse, ok := cfg["PreToolUse"]
	if !ok {
		t.Fatal("expected PreToolUse key in config")
	}

	matchers, ok := preToolUse.([]map[string]any)
	if !ok {
		t.Fatal("expected matchers to be []map[string]any")
	}
	if len(matchers) != 1 {
		t.Fatalf("expected 1 matcher, got %d", len(matchers))
	}

	cbID, ok := matchers[0]["callback_id"].(string)
	if !ok || cbID == "" {
		t.Fatal("expected non-empty callback_id")
	}
	if matchers[0]["matcher"] != "Bash" {
		t.Fatalf("expected matcher 'Bash', got %v", matchers[0]["matcher"])
	}
	if matchers[0]["timeout"] != 5000 {
		t.Fatalf("expected timeout 5000, got %v", matchers[0]["timeout"])
	}

	// Registry should have the callback.
	if len(reg) != 1 {
		t.Fatalf("expected 1 registry entry, got %d", len(reg))
	}
	fn, ok := reg[cbID]
	if !ok {
		t.Fatal("expected callback in registry")
	}

	// Invoke the callback to verify it works.
	output, err := fn(HookEventPreToolUse, nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("expected hook to be called")
	}
	if output.Decision != "approve" {
		t.Fatalf("expected decision 'approve', got %s", output.Decision)
	}
}

func TestHookEventConstants(t *testing.T) {
	// Verify all hook event constants have non-empty string values.
	events := []HookEvent{
		HookEventPreToolUse, HookEventPostToolUse, HookEventPostToolUseFailure,
		HookEventNotification, HookEventStop, HookEventSubagentStop,
		HookEventSubagentStart, HookEventPreCompact, HookEventUserPromptSubmit,
		HookEventStart, HookEventPreBash, HookEventPostBash,
		HookEventPreEdit, HookEventPostEdit, HookEventSetup,
		HookEventPermissionRequest,
		// New events:
		HookEventSessionEnd, HookEventTeammateIdle, HookEventTaskCompleted,
		HookEventElicitation, HookEventElicitationResult,
		HookEventConfigChange, HookEventWorktreeCreate, HookEventWorktreeRemove,
	}
	for _, e := range events {
		if string(e) == "" {
			t.Fatal("hook event constant has empty string value")
		}
	}
}
