package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

// SessionSummary holds metadata about a stored session as returned by
// `claude sessions list --output-format json`.
type SessionSummary struct {
	ID        string `json:"id"`
	Project   string `json:"project,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
	Summary   string `json:"summary,omitempty"`
}

// ListSessions runs `claude sessions list --output-format json` and returns
// the parsed session list. Options (WithClaudeExecutable, WithEnv, etc.) are
// respected for locating the CLI binary and setting the environment.
func ListSessions(ctx context.Context, opts ...Option) ([]SessionSummary, error) {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}

	args := []string{"sessions", "list", "--output-format", "json"}
	cmd := exec.CommandContext(ctx, o.ClaudeExecutable, args...)
	cmd.Env = buildEnv(o)
	if o.CWD != "" {
		cmd.Dir = o.CWD
	}

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("claude: sessions list: %w", err)
	}

	var sessions []SessionSummary
	if err := json.Unmarshal(out, &sessions); err != nil {
		return nil, fmt.Errorf("claude: sessions list: unmarshal: %w", err)
	}
	return sessions, nil
}

// SessionTranscript holds the messages from a stored session as returned by
// `claude sessions get <id> --output-format json`.
type SessionTranscript struct {
	ID       string            `json:"id"`
	Messages []json.RawMessage `json:"messages,omitempty"`
}

// GetSessionMessages runs `claude sessions get <id> --output-format json` and
// returns the raw transcript. Options are respected for locating the CLI.
func GetSessionMessages(ctx context.Context, sessionID string, opts ...Option) (*SessionTranscript, error) {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}

	args := []string{"sessions", "get", sessionID, "--output-format", "json"}
	cmd := exec.CommandContext(ctx, o.ClaudeExecutable, args...)
	cmd.Env = buildEnv(o)
	if o.CWD != "" {
		cmd.Dir = o.CWD
	}

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("claude: sessions get %s: %w", sessionID, err)
	}

	var transcript SessionTranscript
	if err := json.Unmarshal(out, &transcript); err != nil {
		return nil, fmt.Errorf("claude: sessions get %s: unmarshal: %w", sessionID, err)
	}
	return &transcript, nil
}
