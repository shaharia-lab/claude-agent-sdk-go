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

// Control requests must round-trip against a real CLI. Before #36 every one of
// these blocked until the context was cancelled, because the router looked for
// request_id at the top level of the control_response envelope while the CLI
// only ever nests it inside `response`. A unit test cannot catch that class of
// bug on its own — the fixtures were wrong in exactly the same way the code was
// — so this asserts against the real wire.
func TestLiveControlRequestRoundTrips(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude CLI not on PATH")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	session, err := NewSession(ctx, WithModel("claude-haiku-4-5-20251001"))
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	defer func() { _ = session.Close() }()

	cases := []struct {
		name string
		call func() error
	}{
		{"SetModel", func() error { return session.SetModel("claude-haiku-4-5-20251001") }},
		{"SetPermissionMode", func() error { return session.SetPermissionMode(PermissionModeDefault) }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The bug's signature is a hang, so bound the call well under the
			// context deadline and fail loudly rather than stalling the suite.
			done := make(chan error, 1)
			go func() { done <- tc.call() }()

			start := time.Now()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("%s returned an error: %v", tc.name, err)
				}
				t.Logf("%s round-tripped in %s", tc.name, time.Since(start).Round(time.Millisecond))
			case <-time.After(30 * time.Second):
				t.Fatalf("%s did not return within 30s — control_response was never routed", tc.name)
			}
		})
	}
}
