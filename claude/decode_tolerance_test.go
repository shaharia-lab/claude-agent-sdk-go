package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Decoding must be best-effort: encoding/json populates every field it processed
// before hitting a bad one, so a single unexpected field degrades that field
// rather than nilling the whole message.
//
// This is the defect class behind #23. `permission_denials` arriving as an array
// of objects into a []string field discarded the entire Result, so Run() — which
// needs Event.Result — failed for anyone whose session denied a tool.
func TestParseLine_ToleratesOneBadField(t *testing.T) {
	line := readMessageFixture(t, "result_success.json")

	var obj map[string]any
	if err := json.Unmarshal(line, &obj); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}

	// A field of a type the struct cannot hold — exactly what a future CLI
	// change, or the old []string mismatch, looks like.
	obj["num_turns"] = []any{"unexpectedly", "an", "array"}

	mutated, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	event, err := parseLine(mutated)
	if err != nil {
		t.Fatalf("parseLine returned an error for a decodable line: %v", err)
	}
	if event.Result == nil {
		t.Fatal("one bad field nilled the whole Result; decoding must be best-effort")
	}
	if event.DecodeErr == nil {
		t.Error("DecodeErr must record the partial decode so drift stays observable")
	}

	// Everything else must have survived.
	if event.Result.SessionID == "" {
		t.Error("session_id was lost")
	}
	if event.Result.Subtype == "" {
		t.Error("subtype was lost")
	}
	if len(event.Raw) == 0 {
		t.Error("Raw must remain the authoritative payload")
	}
}

// Every event carries Raw, including fully decoded ones.
func TestParseLine_RawAlwaysPopulated(t *testing.T) {
	dir := filepath.Join("testdata", "messages")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("captured corpus is empty")
	}

	for _, e := range entries {
		t.Run(e.Name(), func(t *testing.T) {
			line := readMessageFixture(t, e.Name())

			event, err := parseLine(line)
			if err != nil {
				t.Fatalf("parseLine: %v", err)
			}
			if len(event.Raw) == 0 {
				t.Error("Raw is empty")
			}
			if event.Type == "" {
				t.Error("Type did not decode")
			}
			// Nothing in a corpus captured from a real CLI should decode partially.
			if event.DecodeErr != nil {
				t.Errorf("captured line did not decode cleanly: %v", event.DecodeErr)
			}
		})
	}
}

// The captured session performed no web search, so both server_tool_use
// counters are zero and equality alone cannot tell a correct tag from a broken
// one. Bump them on a copy of the captured line — this exercises the decoder
// against the real wire shape, without claiming the CLI emitted these numbers.
func TestParseLine_ServerToolUseCountersDecode(t *testing.T) {
	var obj map[string]any
	if err := json.Unmarshal(readMessageFixture(t, "result_success.json"), &obj); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	usage, ok := obj["usage"].(map[string]any)
	if !ok {
		t.Fatal("captured result has no usage object")
	}
	stu, ok := usage["server_tool_use"].(map[string]any)
	if !ok {
		t.Fatal("captured usage has no server_tool_use object")
	}
	stu["web_search_requests"] = 7
	stu["web_fetch_requests"] = 3

	mutated, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	event, err := parseLine(mutated)
	if err != nil {
		t.Fatalf("parseLine: %v", err)
	}
	if event.Result == nil {
		t.Fatal("expected Result to be non-nil")
	}
	if got := event.Result.Usage.ServerToolUse.WebSearchRequests; got != 7 {
		t.Errorf("WebSearchRequests = %d, want 7 — usage.server_tool_use is not being read", got)
	}
	if got := event.Result.Usage.ServerToolUse.WebFetchRequests; got != 3 {
		t.Errorf("WebFetchRequests = %d, want 3", got)
	}
}
