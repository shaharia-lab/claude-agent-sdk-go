package claude

import "encoding/json"

// The types in this file describe the payload of the initialize control
// response — the CLI's own account of what the connected binary can do. Field
// names are taken from a response captured from a real CLI, not from the SDK
// type definitions; see testdata/control_response_initialize.json.
//
// Decoding is deliberately lenient throughout: an unknown or missing field must
// never fail a session, because the SDK is expected to run against CLI versions
// both older and newer than itself.

// ModelInfo describes one model the connected CLI offers.
type ModelInfo struct {
	// Value is the identifier to pass to WithModel or SetModel (e.g. "default",
	// "sonnet"). It is not necessarily a concrete model id — see ResolvedModel.
	Value string `json:"value"`
	// ResolvedModel is the concrete model Value resolves to, e.g.
	// "claude-opus-5[1m]".
	ResolvedModel string `json:"resolvedModel"`
	// DisplayName is the human-readable name, e.g. "Default (recommended)".
	DisplayName string `json:"displayName"`
	// Description is a one-line summary of what the model is good for.
	Description string `json:"description"`

	SupportsEffort           bool     `json:"supportsEffort"`
	SupportedEffortLevels    []string `json:"supportedEffortLevels"`
	SupportsAdaptiveThinking bool     `json:"supportsAdaptiveThinking"`
	SupportsFastMode         bool     `json:"supportsFastMode"`
	SupportsAutoMode         bool     `json:"supportsAutoMode"`
}

// SlashCommand describes one slash command available in the session.
type SlashCommand struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Aliases     []string `json:"aliases"`
	// ArgumentHint describes the command's expected arguments, when it takes any.
	ArgumentHint string `json:"argumentHint"`
}

// AgentInfo describes one subagent type the session can dispatch to.
type AgentInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Model is the agent's model override, empty when it inherits the session's.
	Model string `json:"model"`
}

// AccountInfo describes the account the CLI is authenticated as.
type AccountInfo struct {
	Email        string `json:"email"`
	Organization string `json:"organization"`
	// SubscriptionType is e.g. "Claude Max"; empty on API-key auth.
	SubscriptionType string `json:"subscriptionType"`
	// APIProvider is e.g. "firstParty", "bedrock", "vertex".
	APIProvider string `json:"apiProvider"`
}

// initializeResponse is the decoded body of the initialize control response.
//
// Raw holds the undecoded body so that fields this SDK does not model yet
// remain reachable, and so a future CLI adding fields costs nothing here.
type initializeResponse struct {
	Commands []SlashCommand `json:"commands"`
	Agents   []AgentInfo    `json:"agents"`
	Models   []ModelInfo    `json:"models"`
	Account  AccountInfo    `json:"account"`

	OutputStyle           string   `json:"output_style"`
	AvailableOutputStyles []string `json:"available_output_styles"`

	// FastModeState is "off", "on", … and FastModeDisabledReason explains why
	// fast mode is unavailable (e.g. "sdk_opt_in_required").
	FastModeState          string `json:"fast_mode_state"`
	FastModeDisabledReason string `json:"fast_mode_disabled_reason"`

	// PID is the CLI process id as the CLI reports it.
	PID int `json:"pid"`

	Raw json.RawMessage `json:"-"`
}

// decodeInitializeResponse parses an initialize control response body.
//
// It never returns an error: the handshake succeeded if the CLI acknowledged it,
// so a body that does not parse yields a response carrying only Raw rather than
// failing the session. Callers get empty slices instead of data in that case.
func decodeInitializeResponse(body json.RawMessage) *initializeResponse {
	resp := &initializeResponse{Raw: body}
	if len(body) == 0 {
		return resp
	}
	_ = json.Unmarshal(body, resp)
	resp.Raw = body
	return resp
}
