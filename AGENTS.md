# AGENTS.md

NERVER NERVER TOUNCH PORT 1765

Go LLM proxy/router. Accepts OpenAI-style requests (`/v1/chat/completions`, `/v1/responses`, `/v1/models`) and routes them across multiple upstream providers with multi-key failover and Anthropic<->OpenAI translation.

## Quick start

1. Copy `routerllm.yaml.example` to `routerllm.yaml`
2. Edit `routerllm.yaml` — set your API keys
3. Run `routerllm` (or `go run ./cmd/routerllm`)

## Commands

```bash
go build -o routerllm.exe ./cmd/routerllm  # build single binary
go vet ./...                                # static checks (no linter configured)
go build ./...                              # compile-check all packages
docker compose up --build                    # container run, exposes :1765
```

### Runtime

```bash
routerllm   # starts API server (reads routerllm.yaml, then .env, listens on :1765)
```

No flags, no subcommands — just run it.

## Configuration

Config is read from `routerllm.yaml` by default (override with `ROUTERLLM_CONFIG_FILE` env var).

**Minimal example:**
```yaml
port: 1765
providers:
  - name: forge
    api_key: ${FORGE_API_KEY}
  - name: opencode
    api_key: ${OPENCODE_API_KEY}
```

Provider defaults (style, base_url, headers, auth_mode) are built-in per known name.
Override any field in YAML — `api_keys` for multi-key, `base_url` for custom endpoints, `headers` for extra headers.

**Full example:** see `routerllm.yaml.example`

**Fallback:** if no YAML found, reads `.env` + `routes.json` (legacy mode).

## Architecture

- Entry: `cmd/routerllm/main.go` — loads config, builds registry, starts gin server.
- `internal/config/config.go` — reads `routerllm.yaml` first, falls back to env vars + `routes.json` for backward compat. `ProviderConfig` defines each upstream. Provider list is fixed here (agentrouter, opencode, alibaba, freemodel, aerolink, forge).
- `internal/config/yaml.go` — YAML config loader. Merges YAML fields with built-in provider defaults (style, base_url, headers, auth_mode). Supports `${VAR}` env var substitution.
- `internal/routing/` — routing config as data: `types.go` (Config/Rule/Spec/RequestDefaults), `store.go` (Load JSON), `defaults.go` (`DefaultRules()` is the **hardcoded model→provider routing table**). Edit `DefaultRules()` or provide routes in `routerllm.yaml` under the `routes:` key.
- `internal/provider/provider.go` — `NewRegistry(configs, rules, cooldown)` builds the live route table from a `[]routing.Rule`. A route only activates if its provider is configured (has keys).
- `internal/services/proxy.go` — `Forward` selects routes, applies defaults, translates for anthropic-style providers, iterates keys with retry/backoff.
- `internal/adapter/anthropic.go` — translates OpenAI chat bodies to Anthropic `/v1/messages` and converts Anthropic SSE back to OpenAI shape. `systemText` handles string and array-content system messages.
- `internal/keys/manager.go` — round-robin key selection with per-key cooldown. `ReviveAll()` clears all cooldowns.
- `internal/util/dotenv.go` — minimal `.env` parser. **Only sets a var if not already in env** (does not override real env vars).

## Gotchas

- **Port env var**: code reads `ROUTERLLM_PORT` (default `1765`). `.env.example` instead shows `AGENTROUTER_PORT=1765` — that var is **not** read by the code. Use `ROUTERLLM_PORT`. **Never run/kill the production server on 1765 casually; it is the live port.**
- **Routing config**: if `routes:` is set in `routerllm.yaml`, it overrides `DefaultRules()`. If not set, the built-in defaults are used. If no YAML at all (legacy mode), `routes.json` is loaded with defaults fallback.
- **`envOr` strips URL suffixes**: trailing `/` and `/v1` are trimmed from every `*_BASE_URL`/`*_TARGET` env var before use. Do not rely on `/v1` in the base URL; routes append `/v1/...` paths themselves.
- **Streaming is forced**: `dto.ParseAndForceStream` sets `stream=true` on every `/v1/chat/completions` outbound request regardless of client. Non-stream clients get the SSE buffered into a single JSON response (`serveOpenAI`/`BufferAnthropicToOpenAI`). `/v1/responses` respects the client's `stream` flag and passes through the Responses-API shape unchanged (`serveResponses`).
- **`/v1/responses` is OpenAI-only**: anthropic-style providers are filtered out for that path; only `openai`-style providers serve it.
- **Provider styles**: `openai` (passthrough OpenAI shape) vs `anthropic` (request translated to `/v1/messages`, response translated back). Set via `Style` in `config.go`.
- **AuthMode**: `bearer` (default, `Authorization: Bearer <key>`), `x-api-key`, `both` (sends both headers). Configured per provider in `config.go`.
- **Shared keys**: `freemodel-api` and `freemodel-cc` share one `keys.Manager` via `ShareKeys: "freemodel"` — a key marked dead in one is dead for both.
- **Dead vs transient**: HTTP 401/402/403 marks the key dead (cooldown, skipped until revived). 408/429/5xx triggers up to 3 retries with exponential backoff on the _same_ key before moving on.
- **No hot-reload yet**: config is read at startup. Restart the server to apply changes.
- **`routerllm.exe` binary in root is gitignored build artifact** — delete freely.
- `memory-bank/` is gitignored (Cline memory bank, not part of the app).

## Env vars (legacy fallback, see `routerllm.yaml` instead)

Provider keys (comma-separated for multi-key): `AGENT_ROUTER_API_KEY`, `OPENCODE_API_KEY`, `ALIBABA_API_KEY`, `FREEMODEL_API_KEY`, `AEROLINK_API_KEY`, `FORGE_API_KEY`. Base URLs override via `*_BASE_URL` / `AGENTROUTER_TARGET`. Key cooldown: `AGENTROUTER_KEY_COOLDOWN` (duration, default `60s`). Server port: `ROUTERLLM_PORT` (default `1765`). Routing config file: `ROUTERLLM_ROUTES_FILE` (default `routes.json`).
