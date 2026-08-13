package claude

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeCLI writes an executable stand-in for the claude binary that logs every
// stdin line it receives to logPath, and — when respond is true — answers the
// initialize control_request with a success carrying a small payload.
//
// Driving the real spawn path against a scripted process is what makes the
// ordering and timeout guarantees testable: both are properties of the sequence
// spawnAndStream performs, not of any single function.
func fakeCLI(t *testing.T, logPath string, respond bool) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("fake CLI script is POSIX-only")
	}

	script := `#!/usr/bin/env python3
import json, sys, threading, time

RESPOND = ` + boolLiteral(respond) + `
LOG = ` + pyString(logPath) + `

log = open(LOG, "a")
lock = threading.Lock()

def record(entry):
    with lock:
        log.write(json.dumps(entry) + "\n")
        log.flush()

def ack(request_id):
    # Deliberately slow, and on its own thread so stdin keeps being read and
    # logged meanwhile. That is what lets the test distinguish "waited for the
    # acknowledgement" from "wrote the user message immediately": in the old
    # racing sequence the user message lands during this window, before the
    # __ack_sent__ marker.
    time.sleep(0.5)
    body = {"models": [{"value": "fake", "displayName": "Fake"}], "account": {"apiProvider": "fake"}}
    out = {"type": "control_response",
           "response": {"subtype": "success", "request_id": request_id, "response": body}}
    # Mark BEFORE flushing: once the response is on the wire the SDK may write
    # the user message immediately, and the reader thread would log it ahead of
    # a marker recorded afterwards — a race in the test, not in the SDK.
    record({"type": "__ack_sent__"})
    sys.stdout.write(json.dumps(out) + "\n")
    sys.stdout.flush()

for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    try:
        msg = json.loads(line)
    except Exception:
        continue
    record(msg)
    req = msg.get("request") or {}
    if RESPOND and msg.get("type") == "control_request" and req.get("subtype") == "initialize":
        threading.Thread(target=ack, args=(msg.get("request_id"),), daemon=True).start()
`

	path := filepath.Join(t.TempDir(), "fake-claude")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake CLI: %v", err)
	}
	return path
}

// boolLiteral renders a Go bool as a Python bool literal.
func boolLiteral(b bool) string {
	if b {
		return "True"
	}
	return "False"
}

// pyString renders a Go string as a Python string literal.
func pyString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// readLoggedMessages returns the subtypes/types the fake CLI received, in order.
func readLoggedMessages(t *testing.T, logPath string) []string {
	t.Helper()

	raw, err := os.ReadFile(logPath)
	if err != nil {
		return nil
	}
	var seen []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var msg struct {
			Type    string `json:"type"`
			Request struct {
				Subtype string `json:"subtype"`
			} `json:"request"`
		}
		if json.Unmarshal([]byte(line), &msg) != nil {
			continue
		}
		if msg.Type == "control_request" {
			seen = append(seen, "control_request:"+msg.Request.Subtype)
			continue
		}
		seen = append(seen, msg.Type)
	}
	return seen
}

// waitForLoggedMessages polls the log until it holds at least n entries, so the
// assertions do not race the fake CLI's own writes.
func waitForLoggedMessages(t *testing.T, logPath string, n int) []string {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	var seen []string
	for time.Now().Before(deadline) {
		seen = readLoggedMessages(t, logPath)
		if len(seen) >= n {
			return seen
		}
		time.Sleep(20 * time.Millisecond)
	}
	return seen
}

// The user message must not be written until the CLI has acknowledged
// initialize. Before #19 it went out immediately after the initialize write,
// racing MCP/agent setup — the whole point of the spawn-sequence reorder.
func TestSpawn_UserMessageWaitsForInitializeAck(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "stdin.log")
	exe := fakeCLI(t, logPath, true)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	opts := defaultOptions()
	opts.ClaudeExecutable = exe
	opts.InitTimeout = 10 * time.Second

	stream, err := spawnAndStream(ctx, opts, "hello")
	if err != nil {
		t.Fatalf("spawnAndStream: %v", err)
	}
	defer stream.Close()

	seen := waitForLoggedMessages(t, logPath, 3)
	if len(seen) < 3 {
		t.Fatalf("expected initialize, its acknowledgement, then the user message, got %v", seen)
	}
	if seen[0] != "control_request:initialize" {
		t.Fatalf("first message was %q, want the initialize control_request", seen[0])
	}
	// The acknowledgement must come between them: the old code wrote the user
	// message straight after initialize, which logs in the same order, so this
	// marker is what actually distinguishes the two sequences.
	if seen[1] != "__ack_sent__" {
		t.Fatalf("got %v; the user message was written before the initialize acknowledgement", seen)
	}
	if seen[2] != "user" {
		t.Fatalf("third message was %q, want the user message", seen[2])
	}

	// The acknowledged payload must have been cached, not discarded.
	if models := stream.SupportedModels(); len(models) != 1 || models[0].Value != "fake" {
		t.Fatalf("initialize payload was not cached: %+v", models)
	}
}

// Session mode sends its first message through Send, so the handshake must be
// the only thing written at spawn time.
func TestSpawn_SessionModeWritesOnlyInitialize(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "stdin.log")
	exe := fakeCLI(t, logPath, true)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	opts := defaultOptions()
	opts.ClaudeExecutable = exe
	opts.InitTimeout = 10 * time.Second
	opts.sessionMode = true

	stream, err := spawnAndStream(ctx, opts, "")
	if err != nil {
		t.Fatalf("spawnAndStream: %v", err)
	}
	defer stream.Close()

	// __ack_sent__ is the fake CLI's own marker, not something the SDK wrote.
	seen := waitForLoggedMessages(t, logPath, 2)
	for _, m := range seen {
		if m == "user" {
			t.Fatalf("session mode wrote a user message at spawn time: %v", seen)
		}
	}
	if len(seen) == 0 || seen[0] != "control_request:initialize" {
		t.Fatalf("session mode wrote %v, want the initialize control_request first", seen)
	}
}

// A CLI that never acknowledges initialize must fail with a typed timeout
// error, and must not leave the subprocess running.
func TestSpawn_InitializeTimeoutIsTypedAndKillsTheProcess(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "stdin.log")
	exe := fakeCLI(t, logPath, false) // reads stdin, never replies

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	opts := defaultOptions()
	opts.ClaudeExecutable = exe
	opts.InitTimeout = 750 * time.Millisecond

	start := time.Now()
	stream, err := spawnAndStream(ctx, opts, "hello")
	elapsed := time.Since(start)

	if err == nil {
		stream.Close()
		t.Fatal("expected a timeout error when initialize is never acknowledged")
	}
	var initErr *InitializeError
	if !errors.As(err, &initErr) {
		t.Fatalf("expected *InitializeError, got %T: %v", err, err)
	}
	if !initErr.Timeout {
		t.Errorf("expected Timeout to be set: %+v", initErr)
	}
	if elapsed > 30*time.Second {
		t.Errorf("waited %v; the configured timeout was ignored", elapsed)
	}

	// The user message must never have been written — the turn never started.
	for _, m := range readLoggedMessages(t, logPath) {
		if m == "user" {
			t.Error("a user message was written despite the failed handshake")
		}
	}

	// The subprocess must be shut down rather than leaked. Close() escalates to
	// SIGKILL after 5s, so allow for that before declaring a leak.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(exe) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Error("the subprocess outlived the failed handshake")
}

// processAlive reports whether any process is still running the given binary.
func processAlive(exe string) bool {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false // not Linux; the timeout assertions above still hold
	}
	for _, e := range entries {
		cmdline, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil {
			continue
		}
		if strings.Contains(string(cmdline), exe) {
			return true
		}
	}
	return false
}
