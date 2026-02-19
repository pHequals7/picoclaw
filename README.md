# PicoClaw (pHequals7 Fork)

A Go-based personal AI agent focused on chat-first operations, tool execution, and resilient model routing.

This fork has diverged significantly from the upstream README. It documents the current behavior of this deployment, including:
- planner/executor model split,
- automatic failover strategy,
- Telegram-first workflow,
- usage accounting,
- attachment + voice handling,
- operational wrapper/gateway flow.

## What This Fork Is Optimized For

- Running as a long-lived gateway on a VM.
- Telegram as the primary user interface.
- Executing real workspace tasks (shell + files + web + MCP tools).
- Surviving provider instability with automatic model failover.
- Exposing token usage per message/session/day/provider.

## Core Architecture

1. Planner model
- Generates the execution plan for complex tool-driven turns.
- Current default: `gpt-5.1-mini`.
- Plan is written to `workspace/plans/*.md` and injected back into context for execution.

2. Executor model
- Handles normal responses and tool-calling execution.
- Current default in live config: `moonshotai/kimi-k2.5`.

3. Failover model chain
- Automatically used when the active route is rate-limited/degraded.
- Current fallback: `claude-sonnet-4-6`.
- Failover decisions are persisted in state and can notify users in chat.

## Failover Behavior

Failover manager is wired into the execution path and can switch model routes when provider errors are classified as retryable/rate-limit style events.

Key points:
- Trigger class includes 429 and configured retryable 400 paths.
- Route state is persisted under workspace state.
- Probe/switchback logic is supported.
- Optional user-facing switch notifications are configurable.

Relevant config block:

```json
{
  "agents": {
    "defaults": {
      "model": "moonshotai/kimi-k2.5",
      "fallback_model": "claude-sonnet-4-6",
      "fallback_models": ["claude-sonnet-4-6"]
    },
    "planner": {
      "enabled": true,
      "model": "gpt-5.1-mini"
    },
    "failover": {
      "enabled": true,
      "notify_on_switch": true,
      "notify_on_fallback_use": true
    }
  }
}
```

## Telegram UX (Current Behavior)

- Plan is sent as a persistent message for complex tool tasks.
- Progress/streaming updates are sent as a separate follow-up message.
- `/stop` cancels in-flight execution.
- `/usage` commands expose token accounting:
  - `/usage last`
  - `/usage session`
  - `/usage today`
  - `/usage provider`
- Reply context is included (the replied-to message metadata/text is forwarded into agent context).

## Attachments and Voice

Telegram attachments:
- Files are persisted to the attachment store.
- Attachments are not auto-ingested into model context.
- Use `import_attachment` tool to move content into workspace context.

Voice:
- Telegram voice messages are transcribed via configured voice provider path.
- Voice output sending is supported (`SendVoice`) where applicable.

## Built-In Capability Surface

This fork includes (non-exhaustive):
- Filesystem tools: read/write/edit/list/append.
- Shell execution tool with safety checks.
- Web tools: search/fetch.
- MCP tool loading (configured servers become callable tools).
- Spawn/subagent execution paths.
- Send-file and user-message tools.
- Usage store and usage dashboards.

## Workspace Layout

Default workspace: `~/.picoclaw/workspace`

Typical structure:

```text
~/.picoclaw/workspace/
├── AGENT.md
├── IDENTITY.md
├── SOUL.md
├── USER.md
├── memory/
├── plans/
├── sessions/
├── state/
├── usage/
├── attachments/
└── skills/
```

## Install / Build

### From source

```bash
git clone https://github.com/pHequals7/picoclaw.git
cd picoclaw
make deps
make build
make install
```

Binary output:
- `build/picoclaw`
- or direct build target used in this deployment: `~/.local/bin/picoclaw`

## Run Modes

### One-shot / local agent

```bash
picoclaw agent -m "hello"
```

### Gateway mode (chat channels)

```bash
picoclaw gateway
```

## Operational Pattern on VM

This deployment commonly uses:
- `tmux` session running wrapper script,
- wrapper restarting gateway on crash,
- optional watchdog/health checks,
- logs tailed from tmux pane and/or workspace log file.

Typical wrapper location in this environment:
- `~/.picoclaw/wrapper.sh`

## Security and Secrets

- Provider keys are read from env references in config, e.g. `${PICOCLAW_PROVIDERS_*_API_KEY}`.
- Sensitive runtime env can be sourced from a protected file outside workspace.
- Agent credential-use policy is documented in workspace `AGENT.md`.

## Channels

This codebase includes multiple channel integrations (Telegram, Discord, Slack, WhatsApp, LINE, OneBot, etc.), but this fork is currently operated Telegram-first.

## Notes for Contributors

When changing behavior in this fork:
- keep README aligned with actual runtime behavior,
- prefer documenting concrete command/config paths,
- treat planner/executor/failover as first-class architecture,
- validate with tests and gateway restart when runtime config changes.

## License

MIT (inherited from project).
