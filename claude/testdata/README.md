# testdata

Wire payloads captured from a real `claude` CLI, used so tests assert against
what the CLI actually sends rather than a hand-written guess. Hooks shipped
broken precisely because the fixtures encoded an invented shape (see #16).

| File | Captured from | Notes |
| --- | --- | --- |
| `hook_callback_pretooluse.json` | `claude` 2.1.224 | A `PreToolUse` hook_callback control_request. The event name is carried at `request.input.hook_event_name` — there is no top-level `hook_event` field. |
| `can_use_tool_write.json` | `claude` 2.1.224 | A `can_use_tool` control_request for the `Write` tool, triggered by writing outside the working directory under `--permission-mode manual --permission-prompt-tool stdio`. Carries `display_name`, `description`, `decision_reason`, `permission_suggestions` and `tool_use_id`. Note it has **no** `title` or `blocked_path`; the reason is conveyed via `decision_reason` plus a `decision_reason_type` the SDK does not yet decode. |
| `control_response_set_permission_mode_success.json` | `claude` 2.1.224 | A successful `control_response` **carrying** a payload. `request_id` lives at `response.request_id` — there is no top-level one — and the caller's value is the innermost `response` (`{"mode":"default"}`), not the wrapper. |
| `control_response_set_model_success.json` | `claude` 2.1.224 | A successful `control_response` with **no** payload at all: `response` holds only `subtype` and `request_id`. Proves an absent inner `response` means success-without-data, not an error (see #36). |
| `control_response_error_unsupported_subtype.json` | `claude` 2.1.224 | The error variant: `subtype:"error"` plus a human-readable `error`, again with no inner `response`. Also incidental evidence that this CLI rejects the `supported_commands` subtype outright (that gap is #19, not #36). |

Machine-specific values (`transcript_path`, `cwd`) were replaced with
placeholder paths; every field name and the overall structure are verbatim.
The `control_response_*` request ids are the literal ids of the probe requests
that produced them (`cap-model`, `cap-perm`, `cap-err`).

To re-capture a hook payload, speak the control protocol to the CLI directly:
send an `initialize` control_request registering a hook under
`{"matcher": …, "hookCallbackIds": [...]}`, then a user message that triggers a
tool, and record the `hook_callback` control_request the CLI sends back.

To re-capture the `control_response` payloads, pipe control_requests straight
into the CLI and keep the replies:

```sh
{ echo '{"type":"control_request","request_id":"cap-model","request":{"subtype":"set_model","model":"claude-sonnet-4-5"}}'; sleep 3
  echo '{"type":"control_request","request_id":"cap-perm","request":{"subtype":"set_permission_mode","mode":"default"}}'; sleep 3
  echo '{"type":"control_request","request_id":"cap-err","request":{"subtype":"supported_commands"}}'; sleep 3
} | claude --input-format stream-json --output-format stream-json --verbose \
  | grep '"type":"control_response"'
```
