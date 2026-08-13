package claude

import (
	"context"
	"encoding/json"
	"testing"
)

// interruptStream returns a Stream whose write captures the control request and
// replies with the supplied body, plus a pointer to the captured request.
func interruptStream(t *testing.T, body string) (*Stream, *map[string]any) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	sent := map[string]any{}
	s := &Stream{
		ctx:     ctx,
		pending: make(map[string]chan controlResponse),
	}
	s.write = func(v any) error {
		b, _ := json.Marshal(v)
		var envelope struct {
			RequestID string         `json:"request_id"`
			Request   map[string]any `json:"request"`
		}
		if err := json.Unmarshal(b, &envelope); err != nil {
			return err
		}
		sent = envelope.Request

		s.pendingMu.Lock()
		ch := s.pending[envelope.RequestID]
		s.pendingMu.Unlock()

		resp := controlResponse{Success: true}
		if body != "" {
			resp.Body = json.RawMessage(body)
		}
		ch <- resp
		return nil
	}
	return s, &sent
}

// Interrupt must send the interrupt control request — the whole point of #18.
// It previously closed stdin and SIGTERMed the subprocess, which is Close's job.
func TestInterrupt_SendsControlRequest(t *testing.T) {
	s, sent := interruptStream(t, "")

	if _, err := s.Interrupt(); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}

	if (*sent)["subtype"] != "interrupt" {
		t.Fatalf("expected subtype interrupt, got %v", (*sent)["subtype"])
	}
	// The reference implementations send the subtype and nothing else.
	if len(*sent) != 1 {
		t.Fatalf("interrupt must carry no extra fields, got %v", *sent)
	}
}

// Interrupt must not touch the process: the shutdown trigger stays untouched so
// the session survives and the next turn can run.
func TestInterrupt_DoesNotTriggerShutdown(t *testing.T) {
	s, _ := interruptStream(t, "")

	shutdownCalled := false
	s.shutdown = func() { shutdownCalled = true }

	if _, err := s.Interrupt(); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	if shutdownCalled {
		t.Fatal("Interrupt triggered shutdown; it must leave the subprocess running")
	}
}

// Close, by contrast, must trigger shutdown, and must stay idempotent.
func TestClose_TriggersShutdownAndIsIdempotent(t *testing.T) {
	s, _ := interruptStream(t, "")

	calls := 0
	s.shutdown = func() { calls++ }

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if calls == 0 {
		t.Fatal("Close did not trigger shutdown")
	}
	// Idempotency is enforced by sync.Once inside the real trigger; Close itself
	// must simply keep delegating without erroring.
}

// The receipt is decoded from the response body when the CLI sends one.
func TestInterrupt_DecodesReceipt(t *testing.T) {
	s, _ := interruptStream(t, `{"still_queued":["uuid-a","uuid-b"]}`)

	receipt, err := s.Interrupt()
	if err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	if receipt == nil {
		t.Fatal("expected a receipt")
	}
	if len(receipt.StillQueued) != 2 || receipt.StillQueued[0] != "uuid-a" || receipt.StillQueued[1] != "uuid-b" {
		t.Fatalf("unexpected still_queued: %v", receipt.StillQueued)
	}
}

// A CLI without the interrupt_receipt_v1 capability replies with an empty
// success. That is not an error, and must not be reported as one.
func TestInterrupt_MissingReceiptIsNotAnError(t *testing.T) {
	for _, body := range []string{"", "null", "{}", `{"still_queued":null}`} {
		t.Run("body="+body, func(t *testing.T) {
			s, _ := interruptStream(t, body)

			receipt, err := s.Interrupt()
			if err != nil {
				t.Fatalf("Interrupt: %v", err)
			}
			if receipt != nil {
				t.Fatalf("expected no receipt, got %+v", receipt)
			}
		})
	}
}

// Decoding is lenient: an acknowledged interrupt has succeeded whatever the
// payload looks like, so junk yields a nil receipt rather than an error, and a
// mixed array keeps only its string elements (as the reference does).
func TestDecodeInterruptReceipt_Lenient(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want []string // nil means "expect no receipt"
	}{
		{"unparseable", `{"still_queued":`, nil},
		{"wrong type", `{"still_queued":"not-an-array"}`, nil},
		{"unrelated fields only", `{"cancelled":["x"]}`, nil},
		{"empty array", `{"still_queued":[]}`, []string{}},
		{"mixed elements", `{"still_queued":["keep",7,null,{"a":1},"also"]}`, []string{"keep", "also"}},
		{"extra fields tolerated", `{"still_queued":["u1"],"unknown":true}`, []string{"u1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := decodeInterruptReceipt(json.RawMessage(tc.body))
			if tc.want == nil {
				if got != nil {
					t.Fatalf("expected no receipt, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected a receipt")
			}
			if len(got.StillQueued) != len(tc.want) {
				t.Fatalf("still_queued = %v, want %v", got.StillQueued, tc.want)
			}
			for i := range tc.want {
				if got.StillQueued[i] != tc.want[i] {
					t.Fatalf("still_queued = %v, want %v", got.StillQueued, tc.want)
				}
			}
		})
	}
}

// A control_response carrying an error must surface as an error, not a silent
// nil receipt — leniency applies to the payload, never to the acknowledgement.
func TestInterrupt_PropagatesControlError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := &Stream{ctx: ctx, pending: make(map[string]chan controlResponse)}
	s.write = func(v any) error {
		b, _ := json.Marshal(v)
		var envelope struct {
			RequestID string `json:"request_id"`
		}
		if err := json.Unmarshal(b, &envelope); err != nil {
			return err
		}
		s.pendingMu.Lock()
		ch := s.pending[envelope.RequestID]
		s.pendingMu.Unlock()
		ch <- controlResponse{Success: false, Error: "no turn in progress"}
		return nil
	}

	receipt, err := s.Interrupt()
	if err == nil {
		t.Fatal("expected an error when the CLI rejects the interrupt")
	}
	if receipt != nil {
		t.Fatalf("expected no receipt alongside an error, got %+v", receipt)
	}
}
