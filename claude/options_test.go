package claude

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func containsFlag(args []string, flag, value string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

func containsBoolFlag(args []string, flag string) bool {
	return slices.Contains(args, flag)
}

func TestBuildArgs_Defaults(t *testing.T) {
	opts := defaultOptions()
	args := opts.buildArgs()

	joined := strings.Join(args, " ")
	for _, required := range []string{
		"--output-format stream-json",
		"--input-format stream-json",
		"--verbose",
		"--model claude-sonnet-4-6",
		"--thinking adaptive",
		"--permission-mode bypassPermissions",
		"--allow-dangerously-skip-permissions",
	} {
		if !strings.Contains(joined, required) {
			t.Errorf("expected args to contain %q, got %q", required, joined)
		}
	}
}

func TestBuildArgs_NoCWDFlag(t *testing.T) {
	opts := defaultOptions()
	opts.CWD = "/some/dir"
	args := opts.buildArgs()

	for _, a := range args {
		if a == "--cwd" {
			t.Fatal("--cwd flag should not be in args (CWD is set via cmd.Dir)")
		}
	}
}

func TestBuildArgs_MaxTurns(t *testing.T) {
	opts := defaultOptions()
	opts.MaxTurns = 5
	args := opts.buildArgs()
	found := false
	for i, a := range args {
		if a == "--max-turns" && i+1 < len(args) && args[i+1] == "5" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected --max-turns 5 in args")
	}
}

func TestBuildArgs_Effort(t *testing.T) {
	opts := defaultOptions()
	opts.Effort = EffortHigh
	args := opts.buildArgs()
	found := false
	for i, a := range args {
		if a == "--effort" && i+1 < len(args) && args[i+1] == "high" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected --effort high in args")
	}
}

// TestBuildArgs_ResumeSessionID verifies that ResumeSessionID produces --resume <id>.
func TestBuildArgs_ResumeSessionID(t *testing.T) {
	opts := defaultOptions()
	opts.ResumeSessionID = "abc123"
	args := opts.buildArgs()
	found := false
	for i, a := range args {
		if a == "--resume" && i+1 < len(args) && args[i+1] == "abc123" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected --resume abc123 in args")
	}
}

// TestBuildArgs_ResumeSessionID_EmptyIsOmitted ensures --resume is absent when empty.
func TestBuildArgs_ResumeSessionID_EmptyIsOmitted(t *testing.T) {
	opts := defaultOptions()
	args := opts.buildArgs()
	if containsBoolFlag(args, "--resume") {
		t.Errorf("expected --resume to be absent when ResumeSessionID is empty; got %v", args)
	}
}

// TestBuildArgs_CustomSessionID verifies that CustomSessionID produces --session-id <uuid>.
func TestBuildArgs_CustomSessionID(t *testing.T) {
	opts := defaultOptions()
	opts.CustomSessionID = "my-custom-uuid-1234"
	args := opts.buildArgs()
	if !containsFlag(args, "--session-id", "my-custom-uuid-1234") {
		t.Errorf("expected --session-id my-custom-uuid-1234 in args; got %v", args)
	}
}

// TestBuildArgs_CustomSessionID_EmptyIsOmitted ensures --session-id is absent when empty.
func TestBuildArgs_CustomSessionID_EmptyIsOmitted(t *testing.T) {
	opts := defaultOptions()
	args := opts.buildArgs()
	if containsBoolFlag(args, "--session-id") {
		t.Errorf("expected --session-id to be absent when CustomSessionID is empty; got %v", args)
	}
}

// TestBuildArgs_BothSessionFlags verifies both fields can be set simultaneously.
func TestBuildArgs_BothSessionFlags(t *testing.T) {
	opts := defaultOptions()
	WithSessionIDToResume("resume-id")(opts)
	WithSessionID("custom-id")(opts)
	args := opts.buildArgs()

	if !containsFlag(args, "--resume", "resume-id") {
		t.Errorf("expected --resume resume-id in args; got %v", args)
	}
	if !containsFlag(args, "--session-id", "custom-id") {
		t.Errorf("expected --session-id custom-id in args; got %v", args)
	}
}

// TestWithSessionID_ProducesSessionIDFlag_NotResume confirms WithSessionID does not
// produce --resume; it must only produce --session-id.
func TestWithSessionID_ProducesSessionIDFlag_NotResume(t *testing.T) {
	opts := defaultOptions()
	WithSessionID("uuid-xyz")(opts)
	args := opts.buildArgs()

	if containsBoolFlag(args, "--resume") {
		t.Errorf("WithSessionID must not produce --resume; got %v", args)
	}
	if !containsFlag(args, "--session-id", "uuid-xyz") {
		t.Errorf("WithSessionID must produce --session-id; got %v", args)
	}
}

// TestWithSessionIDToResume_ProducesResumeFlag_NotSessionID confirms WithSessionIDToResume
// does not produce --session-id; it must only produce --resume.
func TestWithSessionIDToResume_ProducesResumeFlag_NotSessionID(t *testing.T) {
	opts := defaultOptions()
	WithSessionIDToResume("resume-xyz")(opts)
	args := opts.buildArgs()

	if containsBoolFlag(args, "--session-id") {
		t.Errorf("WithSessionIDToResume must not produce --session-id; got %v", args)
	}
	if !containsFlag(args, "--resume", "resume-xyz") {
		t.Errorf("WithSessionIDToResume must produce --resume; got %v", args)
	}
}

// TestWithSessionIDToResume_SetsResumeSessionIDField verifies the correct struct field is set.
func TestWithSessionIDToResume_SetsResumeSessionIDField(t *testing.T) {
	opts := defaultOptions()
	WithSessionIDToResume("field-test-id")(opts)

	if opts.ResumeSessionID != "field-test-id" {
		t.Errorf("expected ResumeSessionID=%q; got %q", "field-test-id", opts.ResumeSessionID)
	}
	if opts.CustomSessionID != "" {
		t.Errorf("expected CustomSessionID to be empty; got %q", opts.CustomSessionID)
	}
}

// TestWithSessionID_SetsCustomSessionIDField verifies the correct struct field is set.
func TestWithSessionID_SetsCustomSessionIDField(t *testing.T) {
	opts := defaultOptions()
	WithSessionID("custom-field-id")(opts)

	if opts.CustomSessionID != "custom-field-id" {
		t.Errorf("expected CustomSessionID=%q; got %q", "custom-field-id", opts.CustomSessionID)
	}
	if opts.ResumeSessionID != "" {
		t.Errorf("expected ResumeSessionID to be empty; got %q", opts.ResumeSessionID)
	}
}

func TestBuildArgs_Continue(t *testing.T) {
	opts := defaultOptions()
	opts.Continue = true
	args := opts.buildArgs()
	found := false
	for _, a := range args {
		if a == "--continue" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected --continue in args")
	}
}

func TestBuildArgs_ForkSession(t *testing.T) {
	opts := defaultOptions()
	opts.ForkSession = true
	args := opts.buildArgs()
	found := false
	for _, a := range args {
		if a == "--fork-session" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected --fork-session in args")
	}
}

func TestBuildArgs_AllowedTools(t *testing.T) {
	opts := defaultOptions()
	opts.AllowedTools = []string{"Bash", "Read"}
	args := opts.buildArgs()
	found := false
	for i, a := range args {
		if a == "--allowedTools" && i+1 < len(args) && args[i+1] == "Bash,Read" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected --allowedTools Bash,Read in args")
	}
}

func TestBuildArgs_McpServers(t *testing.T) {
	opts := defaultOptions()
	opts.McpServers = map[string]any{
		"my-server": McpHTTPServer{Type: "http", URL: "http://localhost:1234"},
	}
	args := opts.buildArgs()
	found := false
	for i, a := range args {
		if a == "--mcp-config" && i+1 < len(args) {
			found = true
			var parsed map[string]any
			if err := json.Unmarshal([]byte(args[i+1]), &parsed); err != nil {
				t.Fatalf("failed to parse --mcp-config value: %v", err)
			}
			if _, ok := parsed["mcpServers"]; !ok {
				t.Fatal("expected mcpServers key in --mcp-config value")
			}
		}
	}
	if !found {
		t.Fatal("expected --mcp-config in args")
	}
}

func TestBuildArgs_ToolsPreset(t *testing.T) {
	opts := defaultOptions()
	opts.ToolsPreset = &ToolsPreset{Type: "preset", Preset: "claude_code"}
	args := opts.buildArgs()
	found := false
	for i, a := range args {
		if a == "--tools" && i+1 < len(args) {
			found = true
			var parsed map[string]any
			if err := json.Unmarshal([]byte(args[i+1]), &parsed); err != nil {
				t.Fatalf("failed to parse --tools value: %v", err)
			}
			if parsed["preset"] != "claude_code" {
				t.Fatalf("expected preset 'claude_code', got %v", parsed["preset"])
			}
		}
	}
	if !found {
		t.Fatal("expected --tools in args")
	}
}

func TestBuildArgs_ExtraArgs(t *testing.T) {
	opts := defaultOptions()
	opts.ExtraArgs = map[string]string{
		"--my-flag": "my-value",
	}
	args := opts.buildArgs()
	found := false
	for i, a := range args {
		if a == "--my-flag" && i+1 < len(args) && args[i+1] == "my-value" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected --my-flag my-value in args")
	}
}

func TestBuildArgs_ExtraArgsBoolFlag(t *testing.T) {
	opts := defaultOptions()
	opts.ExtraArgs = map[string]string{
		"--bool-flag": "",
	}
	args := opts.buildArgs()
	found := false
	for _, a := range args {
		if a == "--bool-flag" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected --bool-flag in args")
	}
}

func TestWithOptions(t *testing.T) {
	opts := defaultOptions()

	WithModel("claude-opus-4-6")(opts)
	if opts.Model != "claude-opus-4-6" {
		t.Fatalf("expected model claude-opus-4-6, got %s", opts.Model)
	}

	WithSystemPrompt("test prompt")(opts)
	if opts.SystemPrompt != "test prompt" {
		t.Fatalf("expected system prompt 'test prompt', got %s", opts.SystemPrompt)
	}

	WithMaxTurns(10)(opts)
	if opts.MaxTurns != 10 {
		t.Fatalf("expected max turns 10, got %d", opts.MaxTurns)
	}

	WithResumeSessionAt("msg123")(opts)
	if opts.ResumeSessionAt != "msg123" {
		t.Fatalf("expected resume session at msg123, got %s", opts.ResumeSessionAt)
	}

	WithPromptSuggestions(true)(opts)
	if !opts.PromptSuggestions {
		t.Fatal("expected PromptSuggestions to be true")
	}

	handler := func(req json.RawMessage) map[string]any { return nil }
	WithElicitationHandler(handler)(opts)
	if opts.ElicitationHandler == nil {
		t.Fatal("expected ElicitationHandler to be non-nil")
	}
}

func TestDefaultOptions(t *testing.T) {
	opts := defaultOptions()
	if opts.Model != "claude-sonnet-4-6" {
		t.Fatalf("expected default model claude-sonnet-4-6, got %s", opts.Model)
	}
	if opts.Thinking != ThinkingAdaptive {
		t.Fatalf("expected default thinking adaptive, got %s", opts.Thinking)
	}
	if opts.PermissionMode != PermissionModeBypassPermissions {
		t.Fatalf("expected default permission mode bypassPermissions, got %s", opts.PermissionMode)
	}
	if !opts.AllowDangerouslySkipPermissions {
		t.Fatal("expected AllowDangerouslySkipPermissions to be true by default")
	}
	if opts.ClaudeExecutable != "claude" {
		t.Fatalf("expected default executable 'claude', got %s", opts.ClaudeExecutable)
	}
}
