package claude

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ThinkingMode controls Claude's extended thinking behaviour.
type ThinkingMode string

const (
	// ThinkingAdaptive lets Claude decide when to think (default).
	ThinkingAdaptive ThinkingMode = "adaptive"
	// ThinkingDisabled turns off extended thinking.
	// Also sets MAX_THINKING_TOKENS=0 in the subprocess environment.
	ThinkingDisabled ThinkingMode = "disabled"
	// ThinkingEnabled always enables extended thinking.
	ThinkingEnabled ThinkingMode = "enabled"
)

// EffortLevel controls reasoning effort via the --effort flag.
type EffortLevel string

const (
	EffortLow    EffortLevel = "low"
	EffortMedium EffortLevel = "medium"
	EffortHigh   EffortLevel = "high"
)

// PermissionMode controls how Claude handles tool permission requests.
type PermissionMode string

const (
	PermissionModeDefault           PermissionMode = "default"
	PermissionModeAcceptEdits       PermissionMode = "acceptEdits"
	PermissionModeBypassPermissions PermissionMode = "bypassPermissions"
)

// ─── Permission types ─────────────────────────────────────────────────────────

// PermissionBehavior is the allow/deny/ask outcome for a permission rule.
type PermissionBehavior string

const (
	PermissionBehaviorAllow PermissionBehavior = "allow"
	PermissionBehaviorDeny  PermissionBehavior = "deny"
	PermissionBehaviorAsk   PermissionBehavior = "ask"
)

// PermissionUpdateDestination controls where a permission update is persisted.
type PermissionUpdateDestination string

const (
	// PermissionUpdateDestinationUserSettings persists to the global user settings file.
	PermissionUpdateDestinationUserSettings PermissionUpdateDestination = "userSettings"
	// PermissionUpdateDestinationProjectSettings persists to the shared project settings file.
	PermissionUpdateDestinationProjectSettings PermissionUpdateDestination = "projectSettings"
	// PermissionUpdateDestinationLocalSettings persists to the gitignored local settings file.
	PermissionUpdateDestinationLocalSettings PermissionUpdateDestination = "localSettings"
	// PermissionUpdateDestinationSession applies the update only for the current session.
	PermissionUpdateDestinationSession PermissionUpdateDestination = "session"
)

// PermissionRuleValue is a single permission rule identifying a tool and optional
// content pattern (e.g. a glob for the Bash tool's command argument).
type PermissionRuleValue struct {
	// ToolName is the name of the tool the rule applies to (e.g. "Bash", "Read").
	ToolName string `json:"toolName"`
	// RuleContent is an optional content pattern (e.g. "git commit:*", "/src/**").
	// When nil the rule matches all invocations of the tool.
	RuleContent *string `json:"ruleContent,omitempty"`
}

// PermissionUpdate is a single permission mutation returned by a PermissionHandler.
// The Type field is the discriminant; fill the corresponding fields only.
//
//   - "addRules"         → Rules, Behavior, Destination
//   - "replaceRules"     → Rules, Behavior, Destination
//   - "removeRules"      → Rules, Behavior, Destination
//   - "setMode"          → Mode, Destination
//   - "addDirectories"   → Directories, Destination
//   - "removeDirectories"→ Directories, Destination
type PermissionUpdate struct {
	// Type is the mutation kind.
	Type string `json:"type"`
	// Rules holds tool+content patterns (addRules / replaceRules / removeRules).
	Rules []PermissionRuleValue `json:"rules,omitempty"`
	// Behavior is the outcome applied to the rules.
	Behavior PermissionBehavior `json:"behavior,omitempty"`
	// Destination controls where the update is persisted.
	Destination PermissionUpdateDestination `json:"destination,omitempty"`
	// Mode is used with setMode.
	Mode PermissionMode `json:"mode,omitempty"`
	// Directories is used with addDirectories / removeDirectories.
	Directories []string `json:"directories,omitempty"`
}

// PermissionContext is passed to PermissionHandler with full context about the
// tool call request.
type PermissionContext struct {
	// Suggestions are permission updates suggested by the CLI.
	Suggestions []PermissionUpdate
	// BlockedPath is populated when a path restriction triggered the request.
	BlockedPath string
	// DecisionReason is the CLI's internal reason for asking.
	DecisionReason string
	// ToolUseID is the tool use identifier for this specific call.
	ToolUseID string
	// AgentID is set when the request originates from a sub-agent.
	AgentID string
}

// PermissionResult is the return value of a PermissionHandler.
// Set Behavior to "allow" or "deny".
//
// When Behavior == "allow":
//   - UpdatedInput optionally replaces the tool input before execution.
//   - UpdatedPermissions optionally applies persistent permission mutations.
//
// When Behavior == "deny":
//   - Message is shown to the user explaining the denial.
//   - Interrupt, if true, signals the agent to stop entirely.
type PermissionResult struct {
	// Behavior is "allow" (default when empty) or "deny".
	Behavior string
	// UpdatedInput replaces the tool input before execution (allow only).
	UpdatedInput map[string]any
	// UpdatedPermissions applies persistent permission mutations (allow only).
	UpdatedPermissions []PermissionUpdate
	// Message is an explanation shown to the user (deny only).
	Message string
	// Interrupt stops the agent after this tool call (deny only).
	Interrupt bool
}

// PermissionHandler is called when claude sends a can_use_tool control_request.
// ctx contains full context about the request.
// Return a PermissionResult with Behavior "allow" or "deny".
// When nil, all tool calls are allowed.
type PermissionHandler func(toolName string, input json.RawMessage, ctx PermissionContext) PermissionResult

// ─── MCP server config types ─────────────────────────────────────────────────

// McpStdioServer configures an external MCP server launched as a subprocess.
// claude spawns the binary and communicates over its stdin/stdout.
type McpStdioServer struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// McpHTTPServer configures an MCP server reachable over HTTP (streamable transport).
// This is how you expose an in-process Go MCP server to claude: start an HTTP
// listener in your process and pass its URL here.
type McpHTTPServer struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

// McpSSEServer configures an MCP server reachable over SSE.
type McpSSEServer struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

// ─── Plugin types ─────────────────────────────────────────────────────────────

// SdkPluginConfig configures a Claude Code plugin loaded for a session.
// Currently only local plugins (type "local") are supported.
// Each plugin directory must contain a .claude-plugin/plugin.json manifest.
type SdkPluginConfig struct {
	// Type is the plugin kind. Currently only "local" is supported.
	Type string `json:"type"`
	// Path is the absolute or relative path to the plugin directory.
	Path string `json:"path"`
}

// ─── Settings source ─────────────────────────────────────────────────────────

// SettingSource identifies which settings file(s) the claude subprocess should load.
// By default the SDK loads NO settings files (SDK isolation mode).
// Explicitly listing sources opts in to loading those files.
type SettingSource string

const (
	// SettingSourceUser loads ~/.claude/settings.json (global user settings).
	SettingSourceUser SettingSource = "user"
	// SettingSourceProject loads .claude/settings.json (shared, version-controlled).
	SettingSourceProject SettingSource = "project"
	// SettingSourceLocal loads .claude/settings.local.json (gitignored local overrides).
	SettingSourceLocal SettingSource = "local"
)

// ─── Agent types ──────────────────────────────────────────────────────────────

// AgentDefinition configures a named sub-agent that claude can spawn.
type AgentDefinition struct {
	// Description is shown to the parent agent to help it choose this sub-agent.
	Description string `json:"description,omitempty"`
	// Prompt is the system prompt for the sub-agent.
	Prompt string `json:"prompt,omitempty"`
	// Tools lists the tools available to this sub-agent.
	Tools []string `json:"tools,omitempty"`
	// DisallowedTools lists tools explicitly blocked for this sub-agent.
	DisallowedTools []string `json:"disallowedTools,omitempty"`
	// Model overrides the model for this sub-agent.
	Model string `json:"model,omitempty"`
	// MaxTurns limits the number of agentic turns for this sub-agent.
	MaxTurns int `json:"maxTurns,omitempty"`
	// McpServers lists the MCP server names available to this sub-agent.
	McpServers []string `json:"mcpServers,omitempty"`
	// Skills lists the skills available to this sub-agent.
	Skills []string `json:"skills,omitempty"`
}

// ─── Output format ────────────────────────────────────────────────────────────

// OutputFormat configures structured output from claude.
type OutputFormat struct {
	// Type is one of "text", "json", or "json_schema".
	Type string `json:"type"`
	// Schema is the JSON schema used when Type is "json_schema".
	Schema map[string]any `json:"schema,omitempty"`
}

// ─── Sandbox settings ─────────────────────────────────────────────────────────

// NetworkSandboxSettings controls network access for sandboxed command execution.
type NetworkSandboxSettings struct {
	// AllowLocalBinding permits binding to local ports (e.g. for dev servers).
	AllowLocalBinding bool `json:"allowLocalBinding,omitempty"`
	// AllowUnixSockets lists specific Unix socket paths that are accessible
	// (e.g. "/var/run/docker.sock").
	AllowUnixSockets []string `json:"allowUnixSockets,omitempty"`
	// AllowAllUnixSockets permits access to all Unix sockets.
	AllowAllUnixSockets bool `json:"allowAllUnixSockets,omitempty"`
	// HTTPProxyPort specifies the HTTP proxy port for outbound network requests.
	HTTPProxyPort int `json:"httpProxyPort,omitempty"`
	// SOCKSProxyPort specifies the SOCKS proxy port for outbound network requests.
	SOCKSProxyPort int `json:"socksProxyPort,omitempty"`
}

// SandboxIgnoreViolations lists patterns for which sandbox violations are silently ignored.
type SandboxIgnoreViolations struct {
	// File is a list of file-path glob patterns to ignore violations for.
	File []string `json:"file,omitempty"`
	// Network is a list of network address patterns to ignore violations for.
	Network []string `json:"network,omitempty"`
}

// SandboxSettings configures command execution sandboxing for the session.
// Sandbox settings control whether shell commands run inside a sandbox;
// they do not configure filesystem or network permissions (those are controlled
// by PermissionHandler and PermissionUpdate rules).
type SandboxSettings struct {
	// Enabled activates sandbox mode for command execution.
	Enabled bool `json:"enabled,omitempty"`
	// AutoAllowBashIfSandboxed auto-approves Bash tool calls when sandbox is active.
	AutoAllowBashIfSandboxed bool `json:"autoAllowBashIfSandboxed,omitempty"`
	// ExcludedCommands lists commands that always bypass the sandbox (e.g. "docker").
	ExcludedCommands []string `json:"excludedCommands,omitempty"`
	// AllowUnsandboxedCommands lets the model request unsandboxed execution via
	// the dangerouslyDisableSandbox tool input flag.
	AllowUnsandboxedCommands bool `json:"allowUnsandboxedCommands,omitempty"`
	// Network controls network access within the sandbox.
	Network *NetworkSandboxSettings `json:"network,omitempty"`
	// IgnoreViolations suppresses sandbox violations for matching patterns.
	IgnoreViolations *SandboxIgnoreViolations `json:"ignoreViolations,omitempty"`
	// EnableWeakerNestedSandbox enables a weaker sandbox for unprivileged Docker
	// on Linux. Has no effect on other platforms.
	EnableWeakerNestedSandbox bool `json:"enableWeakerNestedSandbox,omitempty"`
}

// ─── Options ─────────────────────────────────────────────────────────────────

// Options holds all configuration for a Query call.
// Use the With* functional options rather than constructing this directly.
type Options struct {
	// Model selects the Claude model. Defaults to "claude-sonnet-4-6".
	Model string

	// SystemPrompt overrides the default system prompt.
	// Sent via the initialize message on stdin (not as a CLI flag).
	SystemPrompt string

	// AppendSystemPrompt appends text to the existing system prompt.
	// Sent via the initialize message on stdin.
	AppendSystemPrompt string

	// SessionID resumes an existing session (--resume <id>).
	SessionID string

	// Continue resumes the most recent session (--continue).
	Continue bool

	// ForkSession forks the resumed session into a new ID (--fork-session).
	// Use with SessionID or Continue.
	ForkSession bool

	// AllowedTools restricts which Claude Code built-in tools may be used.
	AllowedTools []string

	// DisallowedTools explicitly blocks specific tools.
	DisallowedTools []string

	// Thinking controls extended thinking mode. Defaults to ThinkingAdaptive.
	Thinking ThinkingMode

	// MaxThinkingTokens caps the thinking token budget via MAX_THINKING_TOKENS env var.
	MaxThinkingTokens int

	// MaxTurns limits the number of agentic turns via --max-turns.
	MaxTurns int

	// Effort controls reasoning effort level via --effort.
	Effort EffortLevel

	// Betas is a list of beta feature flags to enable via --betas.
	Betas []string

	// FallbackModel is the model to use when the primary model is unavailable.
	FallbackModel string

	// MaxBudgetUSD sets the maximum cost budget in USD via --max-budget-usd.
	MaxBudgetUSD float64

	// OutputFormat configures structured output. Sent in the initialize message.
	OutputFormat *OutputFormat

	// EnableFileCheckpointing enables file checkpointing via --enable-file-checkpointing.
	EnableFileCheckpointing bool

	// StrictMcpConfig enables strict MCP config validation via --strict-mcp-config.
	StrictMcpConfig bool

	// CWD sets the working directory for the claude subprocess via --cwd.
	CWD string

	// PermissionMode controls tool permission handling.
	// Defaults to PermissionModeBypassPermissions.
	PermissionMode PermissionMode

	// AllowDangerouslySkipPermissions must be true when using BypassPermissions.
	AllowDangerouslySkipPermissions bool

	// PermissionPromptToolName sets the MCP tool name used for permission prompts.
	PermissionPromptToolName string

	// PermissionHandler is called for each can_use_tool control_request from claude.
	// When nil and PermissionMode is BypassPermissions, no permission requests arrive.
	// When nil and using a non-bypass mode, all tool calls are auto-allowed.
	PermissionHandler PermissionHandler

	// IncludePartialMessages enables streaming of partial assistant messages.
	IncludePartialMessages bool

	// McpServers configures external MCP servers.
	// Keys are server names; values are McpStdioServer, McpHTTPServer, or McpSSEServer.
	McpServers map[string]any

	// Agents configures named sub-agents available to claude.
	// Sent via the initialize message.
	Agents map[string]AgentDefinition

	// Hooks configures lifecycle hook callbacks.
	// Sent via the initialize message.
	Hooks map[HookEvent][]HookMatcher

	// Plugins lists local Claude Code plugins loaded for this session.
	// Each plugin directory must contain a .claude-plugin/plugin.json manifest.
	Plugins []SdkPluginConfig

	// SettingSources controls which settings files are loaded by the subprocess.
	// When empty, no filesystem settings are loaded (SDK isolation mode).
	SettingSources []SettingSource

	// Env contains additional environment variables merged into the subprocess env.
	Env map[string]string

	// Sandbox configures command execution sandboxing.
	// Passed to the CLI via the initialize message.
	Sandbox *SandboxSettings

	// ClaudeExecutable is the path to the claude binary. Defaults to "claude".
	ClaudeExecutable string
}

// Option is a functional option for configuring a Query call.
type Option func(*Options)

func WithModel(model string) Option {
	return func(o *Options) { o.Model = model }
}

func WithSystemPrompt(prompt string) Option {
	return func(o *Options) { o.SystemPrompt = prompt }
}

func WithAppendSystemPrompt(prompt string) Option {
	return func(o *Options) { o.AppendSystemPrompt = prompt }
}

func WithSessionID(id string) Option {
	return func(o *Options) { o.SessionID = id }
}

// WithContinue resumes the most recent conversation session.
func WithContinue() Option {
	return func(o *Options) { o.Continue = true }
}

// WithForkSession forks the resumed session into a new session ID.
// Use together with WithSessionID or WithContinue.
func WithForkSession() Option {
	return func(o *Options) { o.ForkSession = true }
}

func WithAllowedTools(tools ...string) Option {
	return func(o *Options) { o.AllowedTools = tools }
}

func WithDisallowedTools(tools ...string) Option {
	return func(o *Options) { o.DisallowedTools = tools }
}

func WithThinking(mode ThinkingMode) Option {
	return func(o *Options) { o.Thinking = mode }
}

func WithMaxThinkingTokens(n int) Option {
	return func(o *Options) { o.MaxThinkingTokens = n }
}

func WithMaxTurns(n int) Option {
	return func(o *Options) { o.MaxTurns = n }
}

func WithEffort(level EffortLevel) Option {
	return func(o *Options) { o.Effort = level }
}

// WithBetas enables one or more beta feature flags.
func WithBetas(betas ...string) Option {
	return func(o *Options) { o.Betas = append(o.Betas, betas...) }
}

// WithFallbackModel sets the fallback model when the primary model is unavailable.
func WithFallbackModel(model string) Option {
	return func(o *Options) { o.FallbackModel = model }
}

// WithMaxBudgetUSD sets the maximum cost budget in USD.
func WithMaxBudgetUSD(usd float64) Option {
	return func(o *Options) { o.MaxBudgetUSD = usd }
}

// WithOutputFormat sets structured output format.
func WithOutputFormat(f *OutputFormat) Option {
	return func(o *Options) { o.OutputFormat = f }
}

// WithEnableFileCheckpointing enables file checkpointing.
func WithEnableFileCheckpointing() Option {
	return func(o *Options) { o.EnableFileCheckpointing = true }
}

// WithStrictMcpConfig enables strict MCP configuration validation.
func WithStrictMcpConfig() Option {
	return func(o *Options) { o.StrictMcpConfig = true }
}

// WithCWD sets the working directory for the claude subprocess.
func WithCWD(dir string) Option {
	return func(o *Options) { o.CWD = dir }
}

func WithPermissionMode(mode PermissionMode) Option {
	return func(o *Options) { o.PermissionMode = mode }
}

// WithBypassPermissions enables bypassPermissions mode (the SDK default).
func WithBypassPermissions() Option {
	return func(o *Options) {
		o.PermissionMode = PermissionModeBypassPermissions
		o.AllowDangerouslySkipPermissions = true
	}
}

// WithPermissionPromptToolName sets the MCP tool name used for permission prompts.
func WithPermissionPromptToolName(name string) Option {
	return func(o *Options) { o.PermissionPromptToolName = name }
}

// WithPermissionHandler sets a callback invoked for each can_use_tool request.
func WithPermissionHandler(h PermissionHandler) Option {
	return func(o *Options) { o.PermissionHandler = h }
}

func WithIncludePartialMessages() Option {
	return func(o *Options) { o.IncludePartialMessages = true }
}

// WithMcpServers sets external MCP server configurations.
// Values should be McpStdioServer, McpHTTPServer, or McpSSEServer.
func WithMcpServers(servers map[string]any) Option {
	return func(o *Options) { o.McpServers = servers }
}

// WithAgents configures named sub-agents available to claude.
func WithAgents(agents map[string]AgentDefinition) Option {
	return func(o *Options) { o.Agents = agents }
}

// WithHooks configures lifecycle hook callbacks.
func WithHooks(hooks map[HookEvent][]HookMatcher) Option {
	return func(o *Options) { o.Hooks = hooks }
}

// WithPlugins registers one or more local Claude Code plugins for the session.
// Each SdkPluginConfig must have Type "local" and a path to the plugin directory.
func WithPlugins(plugins ...SdkPluginConfig) Option {
	return func(o *Options) { o.Plugins = append(o.Plugins, plugins...) }
}

// WithSettingSources controls which settings files are loaded by the subprocess.
// Pass one or more of SettingSourceUser, SettingSourceProject, SettingSourceLocal.
// When not called, no filesystem settings are loaded (SDK isolation mode).
func WithSettingSources(sources ...SettingSource) Option {
	return func(o *Options) { o.SettingSources = append(o.SettingSources, sources...) }
}

// WithEnv merges additional environment variables into the subprocess environment.
func WithEnv(env map[string]string) Option {
	return func(o *Options) {
		if o.Env == nil {
			o.Env = make(map[string]string)
		}
		for k, v := range env {
			o.Env[k] = v
		}
	}
}

// WithSandbox configures command execution sandboxing for the session.
func WithSandbox(s *SandboxSettings) Option {
	return func(o *Options) { o.Sandbox = s }
}

func WithClaudeExecutable(path string) Option {
	return func(o *Options) { o.ClaudeExecutable = path }
}

func defaultOptions() *Options {
	return &Options{
		Model:                           "claude-sonnet-4-6",
		Thinking:                        ThinkingAdaptive,
		PermissionMode:                  PermissionModeBypassPermissions,
		AllowDangerouslySkipPermissions: true,
		ClaudeExecutable:                "claude",
	}
}

// buildArgs constructs the CLI argument slice for the claude binary.
//
// Uses bidirectional mode: --input-format stream-json + --output-format stream-json
// + --verbose — exactly the same as @anthropic-ai/claude-agent-sdk.
// The prompt and system prompt are NOT passed as CLI args; they are sent on stdin.
func (o *Options) buildArgs() []string {
	args := []string{
		"--output-format", "stream-json",
		"--input-format", "stream-json",
		"--verbose",
	}

	if o.Model != "" {
		args = append(args, "--model", o.Model)
	}

	switch o.Thinking {
	case ThinkingAdaptive:
		args = append(args, "--thinking", "adaptive")
	case ThinkingDisabled:
		args = append(args, "--thinking", "disabled")
	case ThinkingEnabled:
		args = append(args, "--thinking", "enabled")
	}

	if o.MaxTurns > 0 {
		args = append(args, "--max-turns", fmt.Sprintf("%d", o.MaxTurns))
	}

	if o.Effort != "" {
		args = append(args, "--effort", string(o.Effort))
	}

	if o.SessionID != "" {
		args = append(args, "--resume", o.SessionID)
	}

	if o.Continue {
		args = append(args, "--continue")
	}

	if o.ForkSession {
		// The CLI flag is --fork-session, not --fork.
		args = append(args, "--fork-session")
	}

	if len(o.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(o.AllowedTools, ","))
	}

	if len(o.DisallowedTools) > 0 {
		args = append(args, "--disallowedTools", strings.Join(o.DisallowedTools, ","))
	}

	if o.PermissionMode != "" {
		args = append(args, "--permission-mode", string(o.PermissionMode))
	}

	if o.AllowDangerouslySkipPermissions {
		args = append(args, "--allow-dangerously-skip-permissions")
	}

	if o.IncludePartialMessages {
		args = append(args, "--include-partial-messages")
	}

	if len(o.Betas) > 0 {
		args = append(args, "--betas", strings.Join(o.Betas, ","))
	}

	if o.FallbackModel != "" {
		args = append(args, "--fallback-model", o.FallbackModel)
	}

	if o.MaxBudgetUSD > 0 {
		args = append(args, "--max-budget-usd", fmt.Sprintf("%.6f", o.MaxBudgetUSD))
	}

	if o.EnableFileCheckpointing {
		args = append(args, "--enable-file-checkpointing")
	}

	if o.StrictMcpConfig {
		args = append(args, "--strict-mcp-config")
	}

	if o.CWD != "" {
		args = append(args, "--cwd", o.CWD)
	}

	if o.PermissionPromptToolName != "" {
		args = append(args, "--permission-prompt-tool-name", o.PermissionPromptToolName)
	}

	// Plugins: each plugin gets its own --plugin-dir flag.
	for _, p := range o.Plugins {
		if p.Path != "" {
			args = append(args, "--plugin-dir", p.Path)
		}
	}

	// SettingSources: comma-separated list passed to --setting-sources.
	// When empty the subprocess loads no filesystem settings (SDK isolation mode).
	if len(o.SettingSources) > 0 {
		srcs := make([]string, len(o.SettingSources))
		for i, s := range o.SettingSources {
			srcs[i] = string(s)
		}
		args = append(args, "--setting-sources", strings.Join(srcs, ","))
	}

	// MCP servers are passed via --mcp-config as a JSON string.
	// They are also sent in the sdkMcpServers field of the initialize message.
	if len(o.McpServers) > 0 {
		mcpCfg := map[string]any{"mcpServers": o.McpServers}
		if b, err := json.Marshal(mcpCfg); err == nil {
			args = append(args, "--mcp-config", string(b))
		}
	}

	// Note: SandboxSettings is passed via the initialize message (not CLI flags).
	// Note: ResumeSessionAt is omitted — the --resume-at flag does not exist in
	// the current CLI. The field is retained in Options for future compatibility.

	return args
}
