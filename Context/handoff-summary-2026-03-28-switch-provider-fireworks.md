# Context Handover — Recipe for switching LLM provider/endpoint (OpenRouter to Fireworks)

**Session Date:** 2026-03-28 19:00
**Repository:** picoclaw
**Branch:** main

---

## Session Objective
Replace OpenRouter as the LLM provider with Fireworks AI, using model `accounts/fireworks/routers/kimi-k2p5-turbo` (Kimi K2.5 turbo mode). Deploy to Oracle VM.

## What Got Done
- `pkg/agent/executor.go:57-58` — Added `maxTokens` and `temperature` fields to `LLMExecutor` struct
- `pkg/agent/pipeline.go:92-97` — Added `maxTokens` and `temperature` fields to `CallLLMStage` struct
- `pkg/agent/pipeline.go:102-113` — Added `llmOptions()` helper method that returns config-driven values with sensible defaults (4096 tokens, 0.7 temp)
- `pkg/agent/pipeline.go:136,168` — Replaced two hardcoded `8192`/`0.7` option maps with `s.llmOptions()`
- `pkg/agent/pipeline.go:571-572` — Wired `maxTokens`/`temperature` from executor to `CallLLMStage` construction
- `pkg/agent/loop.go:263-264` — Wired `cfg.Agents.Defaults.MaxTokens`/`Temperature` into main executor
- VM `~/.picoclaw/config.json` — Updated providers, model, planner model
- VM `~/.picoclaw/creds/runtime.env` — Updated API keys to Fireworks key
- VM `~/.picoclaw/workspace/state/state.json` — Reset persisted failover state

## What Didn't Work
This took 5 attempts. Each failure and its root cause:

- **Attempt 1 — model 404**: Config was updated but the **failover manager persisted the old model name** (`moonshotai/kimi-k2.5`) in `~/.picoclaw/workspace/state/state.json`. The `NewManager()` function at `pkg/failover/manager.go:64-74` loads `fs.ActiveModel` and `fs.PrimaryModel` from persisted state and only falls back to config if the values are empty strings. **Fix**: Must also update `state.json` when changing models.

- **Attempt 2 — same 404 after state fix**: The tmux session was stale/zombie. Had to `tmux kill-session` and recreate it cleanly.

- **Attempt 3 — 400 "max_tokens > 4096 must have stream=true"**: Fireworks requires streaming for responses > 4096 tokens. Config was set to `max_tokens: 81920`. Changed to 4096 in config.

- **Attempt 4 — same 400 after config fix**: The `max_tokens` value in config was being **ignored** because `CallLLMStage` in `pipeline.go` had `max_tokens: 8192` **hardcoded** in two places (lines 123 and 156). Config `MaxTokens` was never wired through the executor to the pipeline stage. **Fix**: Added `maxTokens`/`temperature` fields to `LLMExecutor` and `CallLLMStage`, wired from config, replaced hardcoded values.

- **Attempt 5 — success** (pending user confirmation): Deployed new binary with the fix.

## Key Decisions
- **Decision**: Use `openrouter` provider slot with custom `api_base` pointing to Fireworks, rather than adding a new `fireworks` provider type.
  - **Rationale**: OpenRouter uses the generic `HTTPProvider` (OpenAI-compatible `/chat/completions`), which is exactly what Fireworks expects. Zero code changes needed for routing.
  - **Alternatives rejected**: Adding a dedicated Fireworks provider — unnecessary since the API is OpenAI-compatible.

- **Decision**: Set `max_tokens: 4096` instead of enabling streaming.
  - **Rationale**: The codebase doesn't support streaming responses. Enabling streaming would require significant refactoring of `HTTPProvider.Chat()` and the pipeline.

- **Decision**: Cleared all fallback models.
  - **Rationale**: Previous fallbacks (`claude-sonnet-4-6`) don't exist on Fireworks. With a single provider, fallback to a non-existent model would just cause errors.

## Lessons Learned
- **Changing model requires 3 places, not 1**: (1) `config.json` agents.defaults.model, (2) `state.json` failover.active_model + failover.primary_model, (3) verify the binary doesn't hardcode values.
- **Failover state persists across restarts**: `pkg/failover/manager.go:64` loads from `state.Manager.GetFailoverState()`. If `ActiveModel` is non-empty in persisted state, the config model is ignored. This is the #1 gotcha when switching models.
- **Pipeline options were hardcoded**: `CallLLMStage` had `max_tokens: 8192` baked in, completely ignoring the config value. Now fixed but watch for similar patterns in `planner.go:83` (`max_tokens: 300`) and `loop.go:1228,1263` (`max_tokens: 1024`).
- **Fireworks non-streaming limit**: Fireworks rejects non-streaming requests with `max_tokens > 4096`. This is provider-specific — other providers allow much higher values.
- **"Text file busy"**: Cannot `cp` over a running binary on Linux. Must `rm` first, then `cp`.
- **tmux kill-server vs kill-session**: When tmux sessions are zombied, `kill-session` may not work. `kill-server` is the nuclear option but kills ALL sessions including wa-bridge.

## Nuances & Edge Cases
- Model name `accounts/fireworks/routers/kimi-k2p5-turbo` contains "kimi" and "k2", which triggers the temperature override at `http_provider.go:100` forcing `temperature=1.0`. This is actually correct for Kimi K2.5 but means the config temperature is ignored for this model.
- Model name also matches the Moonshot detection case at `http_provider.go:408` (`strings.Contains(lowerModel, "kimi")`), but only fires if `cfg.Providers.Moonshot.APIKey != ""`. Keep Moonshot key empty to avoid misrouting.
- The `anthropic` provider config was set with `api_base: "https://api.fireworks.ai/inference"` per user request. Since `auth_method` was cleared (was `setup-token`), it now creates an `HTTPProvider` that would POST to `.../inference/chat/completions`. Untested — only the `openrouter` path is actually used for the default model.

## Codebase Map (Files Touched)

### Modified
- `pkg/agent/executor.go` — Added `maxTokens`, `temperature` fields to `LLMExecutor`
- `pkg/agent/pipeline.go` — Added fields to `CallLLMStage`, `llmOptions()` helper, replaced hardcoded values
- `pkg/agent/loop.go` — Wired config values into executor construction

### Read / Referenced
- `pkg/providers/http_provider.go` — Provider routing logic, temperature override for kimi models, Chat() request building
- `pkg/providers/claude_provider.go` — Anthropic SDK provider (not used with Fireworks)
- `pkg/config/config.go` — Config structs, env var resolution, `ProvidersConfig`
- `pkg/failover/manager.go` — Persisted state loading, `ResolveRoute()`, model caching

### Related (Not Touched)
- `pkg/agent/planner.go:83` — Also has hardcoded `max_tokens: 300` — may need config-driven value if planner model changes
- `pkg/agent/loop.go:1228,1263` — Hardcoded `max_tokens: 1024` for summarization calls — same pattern

## Recipe: Switching LLM Provider/Endpoint

Follow this exact checklist when switching providers in the future:

1. **Update `~/.picoclaw/config.json` on VM:**
   - `agents.defaults.model` — new model name
   - `agents.defaults.fallback_models` — clear or update for new provider
   - `agents.planner.model` — update if planner should use new provider
   - `providers.<slot>.api_base` — new endpoint URL
   - `providers.<slot>.auth_method` — clear if switching away from Anthropic SDK

2. **Update `~/.picoclaw/creds/runtime.env` on VM:**
   - Set `PICOCLAW_PROVIDERS_<SLOT>_API_KEY` to new API key

3. **CRITICAL — Reset failover state:**
   ```bash
   ssh pico 'jq ".failover.primary_model = \"NEW_MODEL\" | .failover.active_model = \"NEW_MODEL\" | .failover.consecutive_probe_successes = 0" ~/.picoclaw/workspace/state/state.json > /tmp/s.json && mv /tmp/s.json ~/.picoclaw/workspace/state/state.json'
   ```

4. **Check max_tokens compatibility:**
   - Fireworks: max 4096 without streaming
   - Most providers: 8192+ is fine
   - Config value: `agents.defaults.max_tokens`
   - Verify `pipeline.go` uses `s.llmOptions()` (now fixed, but check for regressions)

5. **Check model name side effects:**
   - Does it contain "kimi", "k2", "gpt", "claude", "glm"? → Check `http_provider.go` temperature overrides and model-based routing
   - Does it match any prefix in `createProviderWithSelection()`? → May route to wrong provider if explicit provider isn't set

6. **Deploy:**
   ```bash
   ssh pico 'tmux kill-session -t picoclaw'
   sleep 2
   ssh pico 'rm -f ~/.picoclaw/stop && tmux new-session -d -s picoclaw "bash ~/.picoclaw/wrapper.sh"'
   ```

7. **Verify:**
   ```bash
   ssh pico 'tmux capture-pane -t picoclaw -p | tail -20'  # check startup
   # Send test message, then:
   ssh pico 'tmux capture-pane -t picoclaw -p -S -200 | grep -E "(Processing|ERROR|model=)" | tail -10'
   ```

## Next Steps

1. **Verify Attempt 5 worked** — User needs to send a Telegram message and confirm response came back. Check logs for successful LLM call with `model=accounts/fireworks/routers/kimi-k2p5-turbo`.

2. **Consider adding streaming support** — Fireworks limits non-streaming to 4096 tokens. For longer responses, `HTTPProvider.Chat()` in `http_provider.go` needs a streaming path. This would also unblock higher `max_tokens` values.

3. **Fix remaining hardcoded max_tokens** — `planner.go:83` and `loop.go:1228,1263` still have hardcoded values. Not urgent since they're small (300 and 1024), well under the 4096 limit.

## Open Questions
- Did Attempt 5 actually succeed? User hasn't confirmed yet.
- Does the `anthropic` provider config (`api_base: https://api.fireworks.ai/inference`) actually work? It would POST to `/inference/chat/completions` which may not be a valid Fireworks endpoint. Only matters if something triggers the Anthropic provider path.
