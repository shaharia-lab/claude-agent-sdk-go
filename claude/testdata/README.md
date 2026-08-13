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
| `messages/*.json` | `claude` 2.1.224 | Corpus of real stdout lines for the decode tests (#23), one message per file: `result_success.json` (carries `modelUsage` with camelCase fields and `usage.server_tool_use`), `system_init_with_plugins.json` (plugins as objects, with a `source` field), `system_task_started.json` (proves the task lifecycle is a **system subtype**, not a top-level type), plus `system_thinking_tokens.json` and `system_background_tasks_changed.json` — two subtypes not previously known to this SDK. Home paths and the account email are placeholders; long `skills`/`tools`/`commands` arrays are truncated. Everything else is verbatim. |
| `messages/user_tool_result.json`, `messages/assistant_tool_use.json`, `messages/stream_tool_use_sequence.jsonl` | `claude` 2.1.224 | One real tool-calling turn, captured together (#27): the assistant turn requesting `Read`, the user turn carrying its `tool_result`, and the full ordered stream of that turn (`message_start` → `content_block_start` → `input_json_delta`×4 → `content_block_stop` → `message_delta` → `message_stop`). The `.jsonl` file is the only multi-message fixture — ordering is the point. Concatenating its `partial_json` fragments reproduces the `tool_use.input` of the assistant file exactly, which is what the accumulation test asserts. Note `tool_use` blocks carry a `caller` object no type definition mentions, and `usage` rides `message_delta` as a **sibling** of `delta`, not inside it. |
| `messages/user_tool_result_error.json` | `claude` 2.1.224 | A failed tool call (#27): `is_error: true` on the `tool_result` block, and `tool_use_result` as a bare **string** — the same field is an **object** in `user_tool_result.json`. That contradiction, from one session, is why `ToolUseResult` and block `Content` are `json.RawMessage` rather than typed structs. |
| `messages/result_max_turns.json`, `messages/result_aborted_streaming.json` | `claude` 2.1.224 | The two terminal states a result can reach besides success (#28). `result_max_turns.json` came from `--max-turns 1`: `subtype: error_max_turns` + `terminal_reason: max_turns`. `result_aborted_streaming.json` came from sending an `interrupt` control_request mid-turn: `terminal_reason: aborted_streaming` with `subtype: error_during_execution` — the **same subtype as any other execution failure**, which is exactly why `terminal_reason` is needed to tell a cancelled turn from a failed one (#18). Neither run produced an `api_error_status`; that field is exercised by table tests rather than a fabricated fixture. |
| `control_response_initialize.json` | `claude` 2.1.224 | The full `initialize` control response — the SDK's only source of truth for models, slash commands, agents, account and output styles (#19). Field names are verbatim and **camelCase inside `models`/`commands` but snake_case at the top level**. Note what is *absent*: there is no `capabilities` field here; the CLI advertises capabilities on the `system`/`init` event instead. `commands`, `agents` and `models` are truncated to three entries each, `account` and `pid` are placeholders; everything else is as captured. |
| `sdk_mcp_servers_initialize_matrix.json` | `claude` 2.1.224 | Every `sdkMcpServers` shape probed against a real `initialize`, with the CLI's verdict for each. Only an array of strings is accepted; any object or array-of-objects fails the **entire** initialize, taking hooks, agents, system prompt and output format with it. Naming servers is accepted but wrong for this SDK — it marks them SDK-hosted, so the CLI drops their transports and expects `mcp_message` routing we do not implement. Hence the key is never sent (#38). |
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

To re-capture the `sdkMcpServers` matrix, send an `initialize` control_request
per candidate value and record `response.subtype` plus `response.error`:

```sh
for v in 'null' '[]' '["s"]' '{}' '{"s":{"type":"http","url":"http://127.0.0.1:1"}}'; do
  echo "{\"type\":\"control_request\",\"request_id\":\"cap\",\"request\":{\"subtype\":\"initialize\",\"sdkMcpServers\":$v}}"
done | claude --input-format stream-json --output-format stream-json --verbose \
  | grep '"type":"control_response"'
```
