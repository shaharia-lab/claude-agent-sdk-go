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
	"os/exec"
	"slices"
	"testing"
	"time"
)

// The initialize handshake must actually be awaited and cached against a real
// CLI. Before #19 all four of these methods sent control subtypes that do not
// exist in the protocol, so every call blocked until the context was cancelled
// and then failed.
func TestLiveInitializeResponseIsCached(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude CLI not on PATH")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	sess, err := NewSession(ctx,
		WithModel("claude-haiku-4-5-20251001"),
		WithThinking(ThinkingDisabled),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	// NewSession returns only once initialize has been acknowledged, so all of
	// this is already populated — with no further I/O.
	models := sess.SupportedModels()
	if len(models) == 0 {
		t.Fatal("SupportedModels is empty after a completed handshake")
	}
	for _, m := range models {
		if m.Value == "" {
			t.Errorf("model with an empty Value: %+v", m)
		}
	}
	t.Logf("models: %d (first %q → %q)", len(models), models[0].Value, models[0].ResolvedModel)

	if len(sess.SupportedCommands()) == 0 {
		t.Error("SupportedCommands is empty")
	}
	if len(sess.SupportedAgents()) == 0 {
		t.Error("SupportedAgents is empty")
	}
	if acct := sess.AccountInfo(); acct.APIProvider == "" {
		t.Errorf("AccountInfo did not populate: %+v", acct)
	}
	if style, available := sess.OutputStyle(); style == "" || len(available) == 0 {
		t.Errorf("OutputStyle = %q, available = %v", style, available)
	}

	// Capabilities come from system/init, which arrives with the first turn —
	// so they are legitimately empty until one has started.
	if caps := sess.Capabilities(); len(caps) != 0 {
		t.Errorf("capabilities populated before any turn: %v", caps)
	}

	if err := sess.Send("Reply with exactly: OK"); err != nil {
		t.Fatalf("send: %v", err)
	}
	for ev := range sess.Events() {
		if ev.Type == TypeResult {
			break
		}
	}

	caps := sess.Capabilities()
	if len(caps) == 0 {
		t.Fatal("capabilities still empty after a turn; the system/init capture did not fire")
	}
	t.Logf("capabilities: %v", caps)

	// The documented feature-detection pattern, and the capability #18's receipt
	// depends on. Reported rather than asserted: an older CLI may not have it.
	if !slices.Contains(caps, "interrupt_receipt_v1") {
		t.Logf("note: this CLI does not advertise interrupt_receipt_v1")
	}
}
