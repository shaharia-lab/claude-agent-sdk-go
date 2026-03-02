package claude

import (
	"context"
	"encoding/json"
)

// Session maintains a persistent Claude subprocess for multi-turn conversations.
// Unlike Run/Query (which spawn a new subprocess per call), Session keeps the
// subprocess alive between turns.
//
// Typical usage:
//
//	session, err := claude.NewSession(ctx, claude.WithModel("claude-sonnet-4-6"))
//	if err != nil { ... }
//	defer session.Close()
//
//	_ = session.Send("My name is Alice")
//	for event := range session.Events() {
//	    if event.Type == claude.TypeAssistant { fmt.Print(event.Assistant.Text()) }
//	    if event.Type == claude.TypeResult    { break }
//	}
//
//	_ = session.Send("What is my name?")
//	for event := range session.Events() {
//	    if event.Type == claude.TypeAssistant { fmt.Print(event.Assistant.Text()) }
//	    if event.Type == claude.TypeResult    { break }
//	}
type Session struct {
	stream *Stream
}

// NewSession creates a new persistent Claude session. The subprocess is started
// immediately; the first turn begins when Send is called.
func NewSession(ctx context.Context, opts ...Option) (*Session, error) {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}
	stream, err := spawnSession(ctx, o)
	if err != nil {
		return nil, err
	}
	return &Session{stream: stream}, nil
}

// Send sends a user message and starts a new turn.
// Call this before ranging over Events() for each turn.
func (s *Session) Send(msg string) error {
	return s.stream.SendUserMessage(msg)
}

// Events returns the persistent event channel. Range over it until TypeResult
// to consume one turn's events, then call Send for the next turn.
// The channel is closed when the session ends (subprocess exits or Close is called).
func (s *Session) Events() <-chan Event {
	return s.stream.Events()
}

// Close gracefully shuts down the session.
func (s *Session) Close() error {
	return s.stream.Close()
}

// SetModel asks the claude CLI to switch to a different model mid-session.
func (s *Session) SetModel(model string) error { return s.stream.SetModel(model) }

// SetPermissionMode asks the claude CLI to change the permission mode mid-session.
func (s *Session) SetPermissionMode(mode PermissionMode) error {
	return s.stream.SetPermissionMode(mode)
}

// SetMaxThinkingTokens asks the claude CLI to update the max thinking token budget.
func (s *Session) SetMaxThinkingTokens(n int) error { return s.stream.SetMaxThinkingTokens(n) }

// RewindFiles asks the CLI to rewind files to the state at the given user message ID.
func (s *Session) RewindFiles(userMessageID string) error {
	return s.stream.RewindFiles(userMessageID)
}

// SupportedModels queries the CLI for the list of supported models.
func (s *Session) SupportedModels() (json.RawMessage, error) {
	return s.stream.SupportedModels()
}

// SupportedCommands queries the CLI for the list of supported commands.
func (s *Session) SupportedCommands() (json.RawMessage, error) {
	return s.stream.SupportedCommands()
}

// SupportedAgents queries the CLI for the list of supported agents.
func (s *Session) SupportedAgents() (json.RawMessage, error) {
	return s.stream.SupportedAgents()
}

// AccountInfo queries the CLI for the current account information.
func (s *Session) AccountInfo() (json.RawMessage, error) {
	return s.stream.AccountInfo()
}

// StopTask asks the CLI to stop a running background task.
func (s *Session) StopTask(taskID string) error {
	return s.stream.StopTask(taskID)
}

// ReconnectMcpServer asks the CLI to reconnect a named MCP server.
func (s *Session) ReconnectMcpServer(serverName string) error {
	return s.stream.ReconnectMcpServer(serverName)
}

// ToggleMcpServer asks the CLI to enable or disable a named MCP server.
func (s *Session) ToggleMcpServer(serverName string, enabled bool) error {
	return s.stream.ToggleMcpServer(serverName, enabled)
}

// SetMcpServers asks the CLI to replace the current MCP server configuration.
func (s *Session) SetMcpServers(servers map[string]any) error {
	return s.stream.SetMcpServers(servers)
}

// Interrupt initiates graceful shutdown. Equivalent to Close.
func (s *Session) Interrupt() error { return s.stream.Interrupt() }
