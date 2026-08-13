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
	"testing"
	"time"
)

// Interrupt must abort the turn in progress and leave the session usable.
//
// Before #18 this was impossible: Interrupt and Close were the same function
// body, so "stop this turn" killed the subprocess and the conversation with it.
// The test therefore asserts the half that used to be unreachable — that a
// SECOND turn completes on the same session after the interrupt.
func TestLiveInterruptAbortsTurnAndSessionSurvives(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude CLI not on PATH")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	sess, err := NewSession(ctx,
		WithModel("claude-haiku-4-5-20251001"),
		WithThinking(ThinkingDisabled),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	// Turn 1: something long enough to still be running when we interrupt.
	if err := sess.Send("Count slowly from 1 to 500, writing out every single number on its own line. Do not skip any."); err != nil {
		t.Fatalf("send turn 1: %v", err)
	}

	// Let the turn get underway, then abort it.
	select {
	case <-time.After(5 * time.Second):
	case <-ctx.Done():
		t.Fatal("context expired before the interrupt")
	}

	if _, err := sess.Interrupt(); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	// The receipt is optional (interrupt_receipt_v1); its absence is not an error,
	// so it is deliberately not asserted on here.

	// The aborted turn must still terminate with a result rather than hanging or
	// silently vanishing, and whatever the assistant produced before the abort
	// must reach the caller rather than being swallowed.
	gotResult, assistantEvents := awaitResult(t, ctx, sess, "aborted turn")
	if !gotResult {
		t.Fatal("aborted turn never produced a result")
	}
	if assistantEvents == 0 {
		t.Error("no assistant message was delivered for the aborted turn; output truncated by the interrupt must still be observable")
	}

	// The session must still be alive: a second turn has to complete normally.
	if err := sess.Send("Reply with exactly: STILL-ALIVE"); err != nil {
		t.Fatalf("send turn 2 after interrupt: %v", err)
	}
	if gotResult, _ := awaitResult(t, ctx, sess, "second turn"); !gotResult {
		t.Fatal("session did not survive the interrupt: second turn produced no result")
	}
}

// awaitResult drains events until the session emits a TypeResult, reporting
// whether one arrived before the stream closed or the context expired, and how
// many assistant messages were delivered along the way.
func awaitResult(t *testing.T, ctx context.Context, sess *Session, label string) (bool, int) {
	t.Helper()

	assistant := 0
	for {
		select {
		case ev, ok := <-sess.Events():
			if !ok {
				t.Logf("%s: event stream closed before a result", label)
				return false, assistant
			}
			if ev.Type == TypeAssistant {
				assistant++
			}
			if ev.Type == TypeResult {
				return true, assistant
			}
		case <-ctx.Done():
			t.Logf("%s: context expired waiting for a result", label)
			return false, assistant
		}
	}
}
