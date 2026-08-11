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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// A denying handler must actually block a real tool call. The target path is
// outside the working directory, which is what makes the CLI ask rather than
// auto-approving; without --permission-prompt-tool stdio no request arrives at
// all and the write would simply succeed.
func TestLivePermissionDenyBlocksToolCall(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude CLI not on PATH")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Deliberately outside the working directory so the CLI must ask.
	target := filepath.Join(os.TempDir(), "claude-sdk-live-deny-test.txt")
	_ = os.Remove(target)
	t.Cleanup(func() { _ = os.Remove(target) })

	var (
		mu       sync.Mutex
		asked    []string
		contexts []PermissionContext
	)

	_, err := Run(ctx,
		"Use the Write tool to create the file "+target+" containing the word hello. Do not use any other tool.",
		WithPermissionHandler(func(tool string, _ json.RawMessage, pctx PermissionContext) PermissionResult {
			mu.Lock()
			asked = append(asked, tool)
			contexts = append(contexts, pctx)
			mu.Unlock()
			return PermissionResult{Behavior: "deny", Message: "blocked by the live smoke test"}
		}),
		WithDefaultPermissions(),
		WithModel("claude-haiku-4-5-20251001"),
	)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(asked) == 0 {
		t.Fatal("the permission handler was never consulted — the can_use_tool route is not enabled")
	}
	if !strings.Contains(strings.Join(asked, ","), "Write") {
		t.Fatalf("expected to be asked about Write, got %v", asked)
	}
	// The display fields must survive the round trip to the handler.
	if contexts[0].DisplayName == "" && contexts[0].Description == "" {
		t.Error("expected DisplayName or Description to be populated from the request")
	}

	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("the denied Write was executed anyway: %s exists", target)
	}
}

// With no handler registered the SDK must refuse rather than silently allow.
// Nothing should be granted that no one approved.
func TestLivePermissionNoHandlerDoesNotAutoAllow(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude CLI not on PATH")
	}

	// Reaching can_use_tool requires the stdio route, which only a handler
	// enables; this asserts the unit-level contract stays fail-closed.
	var written []any
	write := func(v any) error {
		written = append(written, v)
		return nil
	}

	line, err := os.ReadFile("testdata/can_use_tool_write.json")
	if err != nil {
		t.Fatalf("read captured payload: %v", err)
	}
	handleControlRequest(line, write, defaultOptions(), hookRegistry{})

	b, _ := json.Marshal(written[0])
	if !strings.Contains(string(b), `"subtype":"error"`) {
		t.Fatalf("expected a fail-closed error response, got %s", b)
	}
}
