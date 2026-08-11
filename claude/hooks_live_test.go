//go:build live

// Live smoke tests run against a real `claude` process and are excluded from
// the default build. Run them with:
//
//	go test -tags live -run TestLive ./claude/
//
// They require the `claude` CLI to be installed, on PATH, and authenticated.
package claude

import (
	"context"
	"encoding/json"
	"os/exec"
	"sync"
	"testing"
	"time"
)

// A PreToolUse hook registered through WithHooks must actually fire against a
// real CLI, and must receive the event name rather than an empty string.
func TestLiveHookFires(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude CLI not on PATH")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	var (
		mu     sync.Mutex
		events []HookEvent
		inputs []map[string]any
	)

	hooks := map[HookEvent][]HookMatcher{
		HookEventPreToolUse: {{
			Matcher: "Bash",
			Hooks: []HookFunc{
				func(event HookEvent, input json.RawMessage, _ string) (*HookOutput, error) {
					var decoded map[string]any
					_ = json.Unmarshal(input, &decoded)
					mu.Lock()
					defer mu.Unlock()
					events = append(events, event)
					inputs = append(inputs, decoded)
					return nil, nil
				},
			},
			Timeout: 30,
		}},
	}

	_, err := Run(ctx,
		"Use the Bash tool to run exactly this command: echo hook-smoke-test",
		WithHooks(hooks),
		WithPermissionMode(PermissionModeBypassPermissions),
		WithModel("claude-haiku-4-5-20251001"),
	)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(events) == 0 {
		t.Fatal("PreToolUse hook never fired against the real CLI")
	}
	for i, event := range events {
		if event != HookEventPreToolUse {
			t.Fatalf("hook %d received event %q, want %q", i, event, HookEventPreToolUse)
		}
		if inputs[i]["hook_event_name"] != string(HookEventPreToolUse) {
			t.Fatalf("hook %d input missing hook_event_name: %v", i, inputs[i])
		}
	}
	t.Logf("PreToolUse hook fired %d time(s) with the correct event name", len(events))
}
