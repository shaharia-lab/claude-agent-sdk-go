package claude

import "encoding/json"

// HookEvent identifies the lifecycle event that triggered a hook callback.
type HookEvent string

const (
	HookEventPreToolUse       HookEvent = "PreToolUse"
	HookEventPostToolUse      HookEvent = "PostToolUse"
	// HookEventPostToolUseFailure fires after a tool call fails.
	HookEventPostToolUseFailure HookEvent = "PostToolUseFailure"
	HookEventNotification     HookEvent = "Notification"
	HookEventStop             HookEvent = "Stop"
	HookEventSubagentStop     HookEvent = "SubagentStop"
	// HookEventSubagentStart fires when a sub-agent is started.
	HookEventSubagentStart    HookEvent = "SubagentStart"
	HookEventPreCompact       HookEvent = "PreCompact"
	HookEventUserPromptSubmit HookEvent = "UserPromptSubmit"
	HookEventStart            HookEvent = "Start"
	HookEventPreBash          HookEvent = "PreBash"
	HookEventPostBash         HookEvent = "PostBash"
	HookEventPreEdit          HookEvent = "PreEdit"
	HookEventPostEdit         HookEvent = "PostEdit"
	HookEventSetup            HookEvent = "Setup"
	// HookEventPermissionRequest fires when Claude requests permission to use a tool.
	HookEventPermissionRequest HookEvent = "PermissionRequest"
)

// HookOutput is the return value of a HookFunc. All fields are optional.
type HookOutput struct {
	// Continue, if non-nil, controls whether the operation continues.
	Continue *bool `json:"continue,omitempty"`
	// SuppressOutput prevents the hook output from being shown to the user.
	SuppressOutput bool `json:"suppressOutput,omitempty"`
	// StopReason is the reason provided when the hook stops execution.
	StopReason string `json:"stopReason,omitempty"`
	// Decision is an approval/rejection decision ("approve", "reject", "ask").
	Decision string `json:"decision,omitempty"`
	// SystemMessage is an additional message injected into the context.
	SystemMessage string `json:"systemMessage,omitempty"`
	// Reason is the reason for the decision.
	Reason string `json:"reason,omitempty"`
	// HookSpecificOutput holds hook-type-specific structured output.
	HookSpecificOutput map[string]any `json:"hookSpecificOutput,omitempty"`
}

// HookFunc is the signature for a hook callback function.
// event is the lifecycle event, input is the raw JSON payload from the CLI,
// and toolUseID is the tool use ID (non-empty for tool-related events).
type HookFunc func(event HookEvent, input json.RawMessage, toolUseID string) (*HookOutput, error)

// HookMatcher configures one or more hook functions for a specific tool matcher pattern.
type HookMatcher struct {
	// Matcher is a glob-style pattern matching the tool name (empty = match all).
	Matcher string
	// Hooks are the callback functions to invoke when the matcher fires.
	Hooks []HookFunc
	// Timeout is the timeout in milliseconds for each hook invocation (0 = default).
	Timeout int
}

// hookRegistry maps callback IDs (assigned at init time) to HookFuncs.
// Used by the reader goroutine to dispatch hook_callback control_requests.
type hookRegistry map[string]HookFunc

// buildHooksForInitialize converts the user-supplied hook map into the format
// expected by the claude CLI's initialize message, and returns a registry
// mapping each generated callback ID to its corresponding HookFunc.
func buildHooksForInitialize(hooks map[HookEvent][]HookMatcher) (map[string]any, hookRegistry) {
	if len(hooks) == 0 {
		return map[string]any{}, hookRegistry{}
	}

	reg := hookRegistry{}
	hooksConfig := make(map[string]any, len(hooks))

	for event, matchers := range hooks {
		var matcherConfigs []map[string]any
		for _, matcher := range matchers {
			for _, fn := range matcher.Hooks {
				cbID := newUUID()
				reg[cbID] = fn
				cfg := map[string]any{
					"callback_id": cbID,
				}
				if matcher.Matcher != "" {
					cfg["matcher"] = matcher.Matcher
				}
				if matcher.Timeout > 0 {
					cfg["timeout"] = matcher.Timeout
				}
				matcherConfigs = append(matcherConfigs, cfg)
			}
		}
		if len(matcherConfigs) > 0 {
			hooksConfig[string(event)] = matcherConfigs
		}
	}

	return hooksConfig, reg
}
