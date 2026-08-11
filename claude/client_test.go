package claude

import (
	"context"
	"encoding/json"
	"testing"
)

// The wire field for set_permission_mode is "mode"; the SDK previously sent
// "permission_mode", which the CLI does not read.
func TestSetPermissionMode_WireField(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var sent map[string]any
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
		// Acknowledge so the blocking call returns.
		s.pendingMu.Lock()
		ch := s.pending[envelope.RequestID]
		s.pendingMu.Unlock()
		ch <- controlResponse{Success: true}
		return nil
	}

	if err := s.SetPermissionMode(PermissionModePlan); err != nil {
		t.Fatalf("SetPermissionMode: %v", err)
	}

	if sent["subtype"] != "set_permission_mode" {
		t.Fatalf("expected subtype set_permission_mode, got %v", sent["subtype"])
	}
	if sent["mode"] != "plan" {
		t.Fatalf(`expected {"mode":"plan"}, got %v`, sent)
	}
	if _, legacy := sent["permission_mode"]; legacy {
		t.Fatal("permission_mode is not a wire field and must not be sent")
	}
}
