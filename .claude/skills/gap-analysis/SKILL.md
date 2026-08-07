---
name: gap-analysis
description: Run a verified gap analysis of this Go SDK against the official TypeScript and Python Claude Agent SDKs, generate a report, and (after explicit user approval) create a GitHub Epic issue with native sub-issues for the work. Use when the user asks to run a gap analysis, check parity with the official SDKs, or plan the next release.
---

# Gap Analysis: Go SDK vs Official Claude Agent SDKs

Produce a fully verified gap analysis between this Go SDK and the official SDKs, then turn approved gaps into a GitHub Epic with native sub-issues. The official SDKs are the source of truth, but **neither official SDK is guaranteed to be ahead** — TypeScript and Python lag each other on different features. Always compare against the union of both.

**Cardinal rule: verify everything.** Never report a gap from memory, a changelog line, or a README claim alone. Every finding must be backed by source code you actually read — the symbol in the official SDK AND its absence/presence in `claude/`. If you cannot verify a point, either resolve it with more research or ask the user via AskUserQuestion; never include unverified points in the report.

## Phase 1 — Prepare the repositories

1. Official SDKs live as siblings of this repo:
   - `../claude-agent-sdk-typescript` ← https://github.com/anthropics/claude-agent-sdk-typescript
   - `../claude-agent-sdk-python` ← https://github.com/anthropics/claude-agent-sdk-python

   Clone any that are missing. For existing ones: `git fetch --all --tags --prune` and fast-forward the default branch. Do not leave them on a stale checkout.

2. For each official SDK, record:
   - Default-branch tip commit (sha + date)
   - Latest release tag (`git tag --sort=-v:refname | head -1`, cross-check with `gh release view -R anthropics/<repo>`)
   - CHANGELOG.md entries since the previous analysis window

3. For our Go SDK, record the last release tag (`git tag --sort=-v:refname | head -1` and `gh release list`). If the repo has no release yet, use the last `GAP_ANALYSIS.md` `> Generated:` date as the baseline and say so in the report.

## Phase 2 — Establish the change window

Identify what changed in each official SDK since our last Go release date:

```bash
git -C ../claude-agent-sdk-typescript log --oneline --since="<our-last-release-date>" <default-branch>
git -C ../claude-agent-sdk-python log --oneline --since="<our-last-release-date>" <default-branch>
```

Read the CHANGELOG diffs and the actual commit diffs for anything that touches public API. Classify each change: new feature / behavior change / bug fix relevant to us / internal-only (ignore).

Because the official SDKs are not always in sync with each other:
- Build one **union feature list** across both SDKs. For each feature note which SDK has it, since which version, and which SDK's implementation is the most complete/recent — that one is the reference for our port.
- A feature present in only one official SDK is still a gap for us (medium priority); present in both, high priority.
- Also check the reverse direction: things we have that neither official SDK has (flag as "Go-only, watch for divergence").

## Phase 3 — Verified comparison

Compare public API surfaces across all three SDKs. Areas to sweep (at minimum):

- Options / configuration (TS `Options` type, Python `ClaudeAgentOptions`, our `WithX` options in `claude/options.go`)
- Message & event types and their fields (`claude/messages.go`)
- Stream/session/control methods (`claude/client.go`, `claude/session.go`, `claude/sessions.go`)
- Hooks: events supported, matcher semantics, output fields (`claude/hooks.go`)
- MCP & tool helpers (`claude/mcp.go`, `claude/tool.go`)
- Permission handling: modes, handler callbacks, permission updates
- Errors and process behavior (spawn args, initialize message fields, graceful shutdown)

**Verification protocol for every candidate gap:**
1. Read the implementing source in the official SDK (not just types/docs) — note file and symbol.
2. Grep our `claude/` package to confirm it is missing, partial, or present under a different name.
3. Confirm the feature actually works through the CLI protocol (i.e., it isn't dead code in the official SDK — check it is wired into their transport/initialize/control-request path).
4. Record the evidence (file paths in both repos) alongside the finding.

Spawn parallel subagents (Agent tool / Explore) per area to keep this fast, but the final synthesis must cross-check subagent claims against the sources before anything enters the report. If findings conflict or something is ambiguous (e.g., a feature exists but is deprecated upstream), resolve by reading the code; if still ambiguous, ask the user with AskUserQuestion.

## Phase 4 — Generate the report

Rewrite `GAP_ANALYSIS.md` (keep the established structure):

- `> Generated: <date>` line, plus the exact versions compared: each official SDK's release tag AND branch-tip sha, and our baseline release.
- Legend: ✅ implemented / 🚧 partial / ❌ missing.
- Priority-grouped tables (High = missing here, present in both official SDKs; Medium = present in one; plus a "Go-only" section), each row with TypeScript / Python / Go columns and a Notes cell naming the reference implementation and evidence file paths.
- A "Changed since last release" section summarizing the Phase 2 window per SDK.

Present a summary of the findings to the user in the conversation as well.

## Phase 5 — Epic + sub-issues (only after explicit approval)

1. Use AskUserQuestion to ask whether to create the GitHub Epic (options: create epic with all gaps / only high-priority gaps / report only, no issues). **Never create issues without this explicit approval.**
2. On approval, create the Epic issue: title like `Epic: parity with official SDKs — <TS version> / <Py version>`, body containing the summary, versions compared, and a checklist of the gaps in scope.
3. Break the work into small, independently shippable sub-issues (one feature or coherent feature group each). Each sub-issue body must include: what the feature does, the reference implementation (SDK + file/symbol), where it lands in our codebase, and acceptance criteria (tests + example + README update if user-facing).
4. Attach each as a **native GitHub sub-issue** of the Epic (REST needs the issue's numeric `id`, not its number):

   ```bash
   sub_id=$(gh api repos/{owner}/{repo}/issues/<sub-number> --jq .id)
   gh api -X POST repos/{owner}/{repo}/issues/<epic-number>/sub_issues -f sub_issue_id=$sub_id
   ```

5. Report the Epic and sub-issue URLs to the user.

## Notes

- Per CONTRIBUTING.md, all implementation PRs must reference an issue — the sub-issues created here serve that purpose.
- Ask with AskUserQuestion whenever a judgment call affects scope (e.g., whether a deprecated upstream feature should be ported, or how to prioritize a partially-implemented area).
