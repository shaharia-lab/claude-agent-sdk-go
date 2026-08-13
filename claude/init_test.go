package claude

import (
	"encoding/json"
	"os"
	"slices"
	"sync"
	"testing"
	"time"
)

// initFixtureBody returns the inner response body of the captured initialize
// control response — the payload routeControlResponse hands to the caller.
func initFixtureBody(t *testing.T) json.RawMessage {
	t.Helper()

	raw, err := os.ReadFile("testdata/control_response_initialize.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var envelope struct {
		Response struct {
			Response json.RawMessage `json:"response"`
		} `json:"response"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	return envelope.Response.Response
}

// The captured initialize response must decode field-for-field. These names come
// from a real CLI, not from the SDK type definitions — the issue's field list
// was explicitly unverified, and turned out to be wrong about capabilities.
func TestDecodeInitializeResponse_Fixture(t *testing.T) {
	resp := decodeInitializeResponse(initFixtureBody(t))

	if len(resp.Models) == 0 {
		t.Fatal("no models decoded")
	}
	m := resp.Models[0]
	if m.Value == "" || m.ResolvedModel == "" || m.DisplayName == "" {
		t.Fatalf("model decoded with empty required fields: %+v", m)
	}
	if !m.SupportsEffort || len(m.SupportedEffortLevels) == 0 {
		t.Errorf("effort fields did not decode: %+v", m)
	}

	if len(resp.Commands) == 0 {
		t.Fatal("no commands decoded")
	}
	if resp.Commands[0].Name == "" || resp.Commands[0].Description == "" {
		t.Errorf("command decoded with empty fields: %+v", resp.Commands[0])
	}

	if len(resp.Agents) == 0 {
		t.Fatal("no agents decoded")
	}
	if resp.Agents[0].Name == "" {
		t.Errorf("agent decoded with empty name: %+v", resp.Agents[0])
	}

	if resp.Account.Email == "" || resp.Account.APIProvider == "" {
		t.Errorf("account did not decode: %+v", resp.Account)
	}
	if resp.OutputStyle == "" || len(resp.AvailableOutputStyles) == 0 {
		t.Errorf("output style fields did not decode: %q %v", resp.OutputStyle, resp.AvailableOutputStyles)
	}
	if resp.FastModeState == "" {
		t.Errorf("fast_mode_state did not decode")
	}
	if resp.PID == 0 {
		t.Errorf("pid did not decode")
	}
	if len(resp.Raw) == 0 {
		t.Error("Raw must retain the undecoded body")
	}
}

// Decoding never fails a session: the handshake already succeeded, so junk
// yields an empty response rather than an error.
func TestDecodeInitializeResponse_Lenient(t *testing.T) {
	for _, body := range []string{"", "null", "{}", "not json", `{"models":"wrong type"}`} {
		resp := decodeInitializeResponse(json.RawMessage(body))
		if resp == nil {
			t.Fatalf("body %q produced a nil response", body)
		}
		if len(resp.Models) != 0 {
			t.Errorf("body %q produced models: %+v", body, resp.Models)
		}
	}
}

// The accessors are pure cache reads: no control request is written, so a
// Stream with a nil write func must not panic and must not block.
func TestAccessors_PerformNoIO(t *testing.T) {
	s := &Stream{initResp: decodeInitializeResponse(initFixtureBody(t))}
	s.write = func(any) error {
		t.Fatal("an accessor wrote a control request; these must be cache reads")
		return nil
	}

	if len(s.SupportedModels()) == 0 {
		t.Error("SupportedModels returned nothing")
	}
	if len(s.SupportedCommands()) == 0 {
		t.Error("SupportedCommands returned nothing")
	}
	if len(s.SupportedAgents()) == 0 {
		t.Error("SupportedAgents returned nothing")
	}
	if s.AccountInfo().Email == "" {
		t.Error("AccountInfo returned nothing")
	}
	if style, available := s.OutputStyle(); style == "" || len(available) == 0 {
		t.Errorf("OutputStyle returned %q %v", style, available)
	}
}

// A Stream whose handshake produced nothing must return zero values rather than
// panicking — this is the path a caller hits if they construct a Stream directly.
func TestAccessors_NilInitResponse(t *testing.T) {
	s := &Stream{}

	if s.SupportedModels() != nil || s.SupportedCommands() != nil || s.SupportedAgents() != nil {
		t.Error("expected nil slices with no initialize response")
	}
	if (s.AccountInfo() != AccountInfo{}) {
		t.Error("expected a zero AccountInfo with no initialize response")
	}
	if s.Capabilities() != nil {
		t.Error("expected nil capabilities with no system/init seen")
	}
}

// Capabilities is written by the reader goroutine while callers read it, so it
// must be race-clean. Run under -race, where an unguarded field would trip.
func TestCapabilities_ConcurrentAccess(t *testing.T) {
	s := &Stream{}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = s.Capabilities()
			}
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				s.setCapabilities([]string{"interrupt_receipt_v1", "msg_lifecycle_v1"})
			}
		}()
	}
	wg.Wait()

	if !slices.Contains(s.Capabilities(), "interrupt_receipt_v1") {
		t.Errorf("capabilities not recorded: %v", s.Capabilities())
	}
}

// The returned capability slice must be a copy: a caller mutating it must not
// corrupt the Stream's state.
func TestCapabilities_ReturnsCopy(t *testing.T) {
	s := &Stream{}
	s.setCapabilities([]string{"interrupt_receipt_v1"})

	caps := s.Capabilities()
	caps[0] = "tampered"

	if got := s.Capabilities(); got[0] != "interrupt_receipt_v1" {
		t.Fatalf("caller mutation leaked into the Stream: %v", got)
	}
}

// The initialize timeout resolves from the option, then the env var, then the
// 60s floor the official SDKs use.
func TestInitTimeout_Resolution(t *testing.T) {
	t.Setenv("CLAUDE_CODE_STREAM_CLOSE_TIMEOUT", "")

	if got := (&Options{}).initTimeout(); got != defaultInitTimeout {
		t.Errorf("default = %v, want %v", got, defaultInitTimeout)
	}
	if got := (&Options{InitTimeout: 5 * time.Second}).initTimeout(); got != 5*time.Second {
		t.Errorf("explicit option = %v, want 5s", got)
	}
	// Zero and negative fall through to the default rather than disabling the
	// timeout, which would let a wedged CLI hang startup forever.
	if got := (&Options{InitTimeout: -1}).initTimeout(); got != defaultInitTimeout {
		t.Errorf("negative = %v, want the default", got)
	}

	t.Setenv("CLAUDE_CODE_STREAM_CLOSE_TIMEOUT", "120000")
	if got := (&Options{}).initTimeout(); got != 120*time.Second {
		t.Errorf("env = %v, want 120s", got)
	}
	// The env var raises the floor but never lowers it.
	t.Setenv("CLAUDE_CODE_STREAM_CLOSE_TIMEOUT", "1000")
	if got := (&Options{}).initTimeout(); got != defaultInitTimeout {
		t.Errorf("env below the floor = %v, want the floor %v", got, defaultInitTimeout)
	}
	// The explicit option still wins over the env var.
	t.Setenv("CLAUDE_CODE_STREAM_CLOSE_TIMEOUT", "120000")
	if got := (&Options{InitTimeout: 3 * time.Second}).initTimeout(); got != 3*time.Second {
		t.Errorf("option should beat env, got %v", got)
	}
}

// InitializeError must distinguish a rejection from a timeout in its message.
func TestInitializeError_Message(t *testing.T) {
	rejected := &InitializeError{Message: "invalid agent definition"}
	if got := rejected.Error(); got != "claude: initialize rejected by the CLI: invalid agent definition" {
		t.Errorf("rejection message = %q", got)
	}
	timedOut := &InitializeError{Message: "waited 60s", Timeout: true}
	if got := timedOut.Error(); got != "claude: initialize timed out: waited 60s" {
		t.Errorf("timeout message = %q", got)
	}
}

// The initialize request must carry the caller's request id, so it can be
// registered in pending and awaited. It previously minted its own, which is why
// the response was unroutable and got thrown away.
func TestInitializeMsg_UsesCallerRequestID(t *testing.T) {
	b, err := json.Marshal(initializeMsg("my-req-id", defaultOptions(), map[string]any{}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var envelope struct {
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(b, &envelope); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if envelope.RequestID != "my-req-id" {
		t.Fatalf("request_id = %q, want the caller's id", envelope.RequestID)
	}
}
