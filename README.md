# RouterLLM

A lightweight Go proxy/router for LLM APIs. Accepts OpenAI-compatible requests (`/v1/chat/completions`, `/v1/responses`, `/v1/models`) and routes them across multiple upstream providers with multi-key failover and automatic Anthropic↔OpenAI translation.

## Endpoints

| Method | Path                    | Description                                         |
|--------|-------------------------|-----------------------------------------------------|
| GET    | `/health`               | Health check (`{"status":"ok"}`)                    |
| GET    | `/v1/models`            | List configured models                              |
| POST   | `/v1/chat/completions`  | OpenAI chat completions (stream or non-stream)      |
| POST   | `/v1/messages`          | Anthropic `/v1/messages` (translated to OpenAI)     |
| POST   | `/v1/responses`         | OpenAI responses (only `openai`-style providers)    |

## Requirements

- Go 1.26.4+ (build from source)
- Docker + Docker Compose (container run, optional)

## Quick Start

### 1. Clone and configure

```bash
git clone <repo> && cd RouterLLM
copy routerllm.yaml.example routerllm.yaml
copy .env.example .env
```

### 2. Set API keys

Edit `.env` with your provider API keys:

```dotenv
OPENCODE_API_KEY=sk-...
FREEMODEL_API_KEY=key1,key2
```

`.env` is loaded automatically at startup. Real environment variables take precedence over `.env`.

### 3. Run

```bash
go run ./cmd/routerllm
```

Server starts on port **1765** by default.

### 4. Test

```bash
curl http://localhost:1765/health
curl http://localhost:1765/v1/models
```

## Configuration

Config is read from `routerllm.yaml` (override with `ROUTERLLM_CONFIG_FILE` env var).

### Server settings

```yaml
port: 1765                        # listen port
cooldown: 60s                     # key cooldown after 401/402/403
force_stream: false               # force Anthropic SSE output format
system_prompt_file: system_prompt.txt  # prepended as system message
```

### Providers

Each provider maps to an upstream API. All fields must be specified — no defaults.

```yaml
providers:
  - name: forge                   # unique name, referenced in routes
    api_key: ${FORGE_API_KEY}     # env var or list: [key1, key2]
    base_url: https://forge-gateway-api.fly.dev
    style: openai                 # openai | anthropic | cline
    auth_mode: bearer             # bearer | x-api-key | both
    headers:                      # optional extra headers
      Content-Type: application/json
    share: mygroup                # optional key-sharing group name
    query: "?beta=true"           # optional query string
```

**`api_key`** supports:
- `${ENV_VAR}` — reads from environment
- Comma-separated env var values auto-split into multiple keys
- `[key1, key2, key3]` — YAML list of static keys (not recommended in public repos)

### Cline free model

`style: cline` authenticates with Cline accounts instead of API keys. Log in first:

```bash
routerllm --cline-login
```

The command prints a login URL and user code, waits for authorization, then stores the
account refresh token in `cline-accounts.json` next to the executable (`0600`). Override the
location with `CLINE_ACCOUNTS_FILE`. Access tokens are refreshed automatically and kept in
memory only. Run the command again to add more accounts — RouterLLM rotates them and fails
over when one is rejected.

The example config ships the cline provider **commented out**: an enabled `style: cline`
provider refuses to start until at least one account is logged in, so uncomment the provider
and its route only after `--cline-login` has created the accounts file.

In Docker, run `--cline-login` on the host first, then uncomment the
`cline-accounts.json` mount in `docker-compose.yml` — the host file must exist before the
container starts, because Docker silently auto-creates a missing bind-mount path as a
directory, which then breaks both the login and the account loader.

```yaml
providers:
  - name: cline
    style: cline
    base_url: https://api.cline.bot/api

routes:
  - model_id: cline-free/glm-5.2
    routes:
      - provider: cline
        model: cline-free/glm-5.2
```

### Routes

Map client-facing model names to upstream provider+model pairs. Routes are tried in order until one succeeds.

```yaml
routes:
  - model_id: my-model            # ID clients send in requests
    routes:
      - provider: forge           # must match a provider name above
        model: gpt-4              # upstream model name
        defaults:
          reasoning_effort: high  # low | medium | high | max
          enable_thinking: true   # OpenAI-style, required to turn on thinking
          thinking_budget: 32000  # Anthropic-style thinking tokens
```

### Defaults reference

| Field             | Values                       | Provider style | Notes                                                |
|-------------------|------------------------------|----------------|------------------------------------------------------|
| `enable_thinking` | `true` / `false`             | openai/anthropic | **Off by default.** Must set `true` explicitly. Sets both `enable_thinking` (OpenAI) and `thinking` (Anthropic) body fields. |
| `reasoning_effort`| `low` / `medium` / `high` / `max` | openai    | Only sets the `reasoning_effort` body field. Does **not** enable thinking by itself — you must also set `enable_thinking: true`. |
| `thinking_budget` | integer (tokens)             |     anthropic      | Maps to Anthropic `thinking.budget_tokens`. When set positively, creates a `thinking` block automatically. |

### Examples

**Single provider, no defaults:**
```yaml
- model_id: deepseek-v4-flash
  routes:
    - provider: opencode
      model: deepseek-v4-flash-free
```

**Multi-provider fallback with defaults (thinking off):**
```yaml
- model_id: gpt-5.5
  routes:
    - provider: freemodel-api
      model: gpt-5.5
    - provider: forge
      model: gpt-5.5
      defaults:
        reasoning_effort: max
        enable_thinking: true    # required — reasoning_effort alone does not enable thinking
```

## Routing and Failover

1. A request arrives with a `model` name.
2. Router looks up matching routes in config order.
3. For each route, tries available API keys round-robin.
4. On success → returns response immediately.
5. On HTTP 401/402/403 → key marked **dead** (cooldown period), skips to next key.
6. On HTTP 408/429/5xx → **retries** up to 3 times with exponential backoff (100ms, 200ms, 400ms), then skips to next route.
7. On all keys exhausted → tries next route.
8. On all routes exhausted → returns last error with its HTTP status.

**Shared keys** (`share:` in provider config): a key marked dead in one provider is dead for all providers sharing that group.

## Environment Variables

| Variable                 | Default          | Description                           |
|--------------------------|------------------|---------------------------------------|
| `ROUTERLLM_PORT`         | `1765`           | Server listen port                    |
| `ROUTERLLM_CONFIG_FILE`  | `routerllm.yaml` | Config file path                      |
| `ROUTERLLM_DEBUG`        | `false`          | Per-request routing trace (model, chosen provider). Failures — dead keys, retries, exhausted providers — are always logged. |
| `ROUTERLLM_DEBUG_ADVANCED` | `false`        | Log client/system headers and bodies to the log file |
| `ROUTERLLM_LOG_FILE`     | —                | Also write operational logs to file   |
| `AUTHTOKEN`              | —                | Bearer token(s) for `/v1` write endpoints (comma-separated); unset = open |
| `ROUTERLLM_ADMIN_TLS_PORT` | —              | Serve the admin console over HTTPS on this port; main port drops `/admin` |
| `ROUTERLLM_ADMIN_TLS_CERT` / `_KEY` | auto  | TLS cert/key paths; self-signed pair generated beside the config when missing |
| Provider API keys        | —                | `*_API_KEY` vars per your config      |

`.env` files are loaded automatically (real env vars take precedence).

### Admin auth: HMAC challenge–response

The console never sends `ROUTERLLM_ADMIN_TOKEN` over the wire. It proves knowledge of the secret instead:

1. `POST /admin/api/auth/challenge` → `{"challenge_id", "nonce", "expires_in":60}` (nonce is single-use).
2. Compute `proof = hex(HMAC-SHA256(secret, nonce))` client-side.
3. `POST /admin/api/auth/verify {"challenge_id", "proof"}` → `{"session", "expires_in":43200}`.
4. Call every admin API with `Authorization: Bearer <session>`. Sending the raw secret is a 401.

Sessions are in-memory (≤100 live, 12h TTL) — a server restart requires logging in again.

**HTTPS for the console (recommended outside localhost):** set `ROUTERLLM_ADMIN_TLS_PORT=1766` and open `https://<host>:1766/admin/`. A self-signed certificate is generated on first start (`admin-tls.crt`/`admin-tls.key` beside the config — the browser warns once; import the `.crt` as a trusted root to silence it, or point `ROUTERLLM_ADMIN_TLS_CERT`/`_KEY` at your own cert). With TLS on, the main port stops serving `/admin` so the console never exists over plain HTTP.

**`/v1` auth:** set `AUTHTOKEN` in `.env` and pass `Authorization: Bearer $AUTHTOKEN` on every write call — `POST /v1/chat/completions`, `/v1/responses`, `/v1/messages`, file uploads. `GET /v1/models` and `/health` stay open. Unset keeps the old open behaviour.

Scripting example:

```bash
NONCE_JSON=$(curl -s -X POST http://127.0.0.1:1765/admin/api/auth/challenge)
CID=$(echo "$NONCE_JSON" | jq -r .challenge_id)
NONCE=$(echo "$NONCE_JSON" | jq -r .nonce)
PROOF=$(printf %s "$NONCE" | openssl dgst -sha256 -hmac "$ROUTERLLM_ADMIN_TOKEN" -hex | awk '{print $2}')
SESSION=$(curl -s -X POST http://127.0.0.1:1765/admin/api/auth/verify   -d "{"challenge_id":"$CID","proof":"$PROOF"}" | jq -r .session)

# With ROUTERLLM_ADMIN_TLS_PORT set, reach the console over HTTPS instead:
# curl -s https://127.0.0.1:1766/admin/api/status --cacert admin-tls.crt -H "Authorization: Bearer $SESSION"
curl -s http://127.0.0.1:1765/admin/api/status -H "Authorization: Bearer $SESSION"
```

For the proxy API, pass the `AUTHTOKEN` value on write endpoints:

```bash
curl -s http://127.0.0.1:1765/v1/chat/completions \
  -H "Authorization: Bearer $AUTHTOKEN" \
  -d '{"model":"my-model","messages":[{"role":"user","content":"hi"}]}'
```

### Add & remove route legs

With a session, the console gains two write paths on Signal paths:

- **`+`** opens a dialog (Provider, Upstream model, Reasoning Effort, Disabled) that appends a fallback leg to `routerllm.yaml` — same surgery as a hand edit, comments and `${VAR}` placeholders survive — and reloads.
- **`✕`** on a leg removes it. The last leg of a model cannot be removed (disable it or delete the model instead).

## Docker

```bash
docker compose up --build -d
docker compose logs -f routerllm
docker compose down
```

Mounts `routerllm.yaml` (read-write, so the admin console can persist toggles) and
`system_prompt.txt` as volumes. Reads secrets from `.env`.

### Docker + hot reload

The config is hot-reloaded (3s content poll), but **on Docker Desktop a single-file bind
mount does not propagate host-side edits into the container** — hand-editing
`routerllm.yaml` on the host goes unnoticed no matter how long you wait. Two reliable
options:

- Edit through the admin console at `/admin/` — writes happen inside the container, reload
  immediately, and sync back to the host file.
- `docker compose restart routerllm` after hand edits.

On a plain host install (binary / `go run`) hand-edit hot reload works as documented.

## Testing with `requests.http`

The repository includes `requests.http` for VS Code REST Client or JetBrains HTTP Client.

1. Start the server: `go run ./cmd/routerllm`
2. Open `requests.http`
3. Set your `ROUTERLLM_AUTH` variable (e.g. `Authorization: Bearer <key>`)
4. Send requests via the inline "Send Request" links

Or use curl directly:

```bash
# Health check
curl http://localhost:1765/health

# List models
curl http://localhost:1765/v1/models

# Chat completion (streaming)
curl -N http://localhost:1765/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"my-model","messages":[{"role":"user","content":"Hello"}],"stream":true}'

# Chat completion (non-streaming)
curl http://localhost:1765/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"my-model","messages":[{"role":"user","content":"Hello"}]}'

# Anthropic /v1/messages
curl -N http://localhost:1765/v1/messages \
  -H "Content-Type: application/json" \
  -d '{"model":"my-model","messages":[{"role":"user","content":"Hello"}],"max_tokens":1024}'
```

## Building

```bash
go build -o routerllm.exe ./cmd/routerllm   # single binary
go build ./...                                # compile-check all packages
go test ./...                                 # run tests
go vet ./...                                  # static analysis
```

## Operational Notes

- **Hot-reload:** config is polled every 3s and swapped atomically on change; invalid edits are rejected and the previous config keeps serving. Caveat: on Docker Desktop, host-side hand edits don't propagate through the single-file bind mount — use the admin console or restart (see "Docker + hot reload" above).
- **Streaming is forced:** all outbound requests to upstreams have `stream=true`. Non-streaming clients receive a buffered response.
- **Request body limit:** 10 MB.
- **`/v1/responses`** only works with `openai`-style providers; `anthropic` providers are filtered out.
- **`/v1/messages`** accepts Anthropic-format requests, translates them to OpenAI, and translates the response back to Anthropic SSE.

## Security

- Never commit API keys to git. Use `.env` or environment variables.
- Add `routerllm.yaml` to `.gitignore` if it contains secrets.
- Review headers and logging before deploying to production.
- The binary `routerllm.exe` at the repo root is a gitignored build artifact — delete freely.
