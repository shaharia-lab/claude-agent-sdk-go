# testdata

Wire payloads captured from a real `claude` CLI, used so tests assert against
what the CLI actually sends rather than a hand-written guess. Hooks shipped
broken precisely because the fixtures encoded an invented shape (see #16).

| File | Captured from | Notes |
| --- | --- | --- |
| `hook_callback_pretooluse.json` | `claude` 2.1.224 | A `PreToolUse` hook_callback control_request. The event name is carried at `request.input.hook_event_name` — there is no top-level `hook_event` field. |
| `can_use_tool_write.json` | `claude` 2.1.224 | A `can_use_tool` control_request for the `Write` tool, triggered by writing outside the working directory under `--permission-mode manual --permission-prompt-tool stdio`. Carries `display_name`, `description`, `decision_reason`, `permission_suggestions` and `tool_use_id`. Note it has **no** `title` or `blocked_path`; the reason is conveyed via `decision_reason` plus a `decision_reason_type` the SDK does not yet decode. |

Machine-specific values (`transcript_path`, `cwd`) were replaced with
placeholder paths; every field name and the overall structure are verbatim.

To re-capture, speak the control protocol to the CLI directly: send an
`initialize` control_request registering a hook under
`{"matcher": …, "hookCallbackIds": [...]}`, then a user message that triggers a
tool, and record the `hook_callback` control_request the CLI sends back.
