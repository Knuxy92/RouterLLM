# Astro Admin Console Migration

## Context

The admin console is currently a Vite 8 + vanilla-JS SPA: one 884-line `web/index.html` with all markup static, ~3k lines of JS modules driven by document-global ids, Tailwind 4 via `@tailwindcss/vite`, and lucide via a vendored UMD script. State, rendering, and UI wiring are tangled module singletons (`state.js`, `render.js`, `api.js`, `auth.js`); every new screen means more `innerHTML` plus `querySelector` wiring.

Goal: rebase the console onto **Astro + React islands in TypeScript** with real component boundaries. A "Models detail" page is wanted, but as **Phase 2** after the Astro migration is green.

Why TypeScript: `.oxlintrc.json` already carries React rules (vestigial from an earlier React version); the repo owner writes Go; porting the stateful `state.js` store is the riskiest part and types catch payload-shape drift.

### Locked constraint

**Do not touch the Get Models API or any Go model-catalog code.** `GET /admin/api/providers/{name}/models` and its response `{provider, style, source, models: string[]}` stay byte-for-byte as they are. Phase 2's Models detail is read-only UI consuming that existing endpoint — no parser changes, no `ModelCatalog` struct change, no new upstream fetch.

### Key facts

- Build contract: `web/vite.config.ts:7-11` sets `base: "/admin/"`, `outDir: ../internal/admin/dist`; `internal/admin/ui.go:13-14` does `//go:embed all:dist`, `fs.Sub`, SPA fallback, immutable asset cache + `no-store` index; `Dockerfile:3-16` builds with `node:22-alpine` + `pnpm --frozen-lockfile`, then copies dist. Zero Go changes in Phase 1.
- Runtime paths `/admin/dashboard|logs|providers` use path routing (`web/src/js/router.js:1-37`), served by SPA fallback (`ui.go:32-51`). Auth is challenge–response HMAC (`api.js:95-105`; `hmac.js` is pure JS because `crypto.subtle` is unavailable on plain-HTTP LAN IPs), session in `localStorage`, 401 → `routerllm:unauthorized` teardown.
- `state.js`: 3s `/pulse` poll with signatures, `/requests?since=` delta ring, optimistic toggles. `data.js`: pure adapters. `render.js`: all DOM wiring. No frontend tests.
- Requested nav: **Platform** → Dashboard, Request logs; **Management** → Providers & Models; **docs** → Models detail.

Decisions locked: Astro static output into the existing dist (Go still serves it), React islands in TypeScript (real rewrite), Models detail Phase 2 as read-only UI, plan file committed into git, Get Models API untouched.

## Git plan (checkpoints)

Main thread owns all git operations; subagents never commit or stage. Branch `feat/admin-astro-migration` from `feat/provider-model-catalog` HEAD. Never commit on `master`. No push without explicit ask.

- **CP0** `docs(plan): record Astro migration plan` — this file, committed first.
- **CP1** `chore(web): scaffold Astro + React + TypeScript build` — deps (`astro`, `@astrojs/react`, `@astrojs/check`, `react`, `react-dom`, `typescript`, `@types/react`, `@types/react-dom`), `astro.config.mjs`, `tsconfig.json`, global CSS, output/base alignment; `pnpm typecheck` green.
- **CP2** `refactor(web): rebuild shell, layout, auth and typed store` — layout/nav groups, login gate, Astro pages, typed React store; HMAC/API/adapters → TS with payload interfaces.
- **CP3** `refactor(web): port dashboard island in TSX` — KPIs, traffic chart, TTFT, failures, top models.
- **CP4** `refactor(web): port request logs island in TSX` — filters, table, pagination, trace drawer, export, cmd-K palette hooks.
- **CP5** `refactor(web): port providers & models island in TSX` — provider cards, key toggles, model chains, add/remove/move leg dialogs, provider test panel, provider drawer.
- **CP6** `chore(web): align Docker, lint, typecheck, docs` — Dockerfile/pnpm scripts if needed, `.oxlintrc`, `web/README.md`, project `AGENTS.md`, final checks.
- Phase 2 (later): CP7+ `feat(models): add Models detail UI` — **frontend only; no Go changes**.

`pnpm -C web build` must be run before every Go build check after web changes (dist is embedded).

## Implementation — Phase 1

### Scaffold (`web/`)

Add `astro`, `@astrojs/react`, `@astrojs/check`, `react`, `react-dom`, `typescript`, `@types/react`, `@types/react-dom`; keep `tailwindcss` via `@tailwindcss/vite` in Astro's `vite` config (Context7 confirms `outDir` accepts an absolute path and `base: "/admin/"` prefixes emitted URLs).

`astro.config.mjs`: `base: "/admin/"`, `outDir: fileURLToPath(new URL("../internal/admin/dist", import.meta.url))`, `publicDir: "public"`, `output: "static"`, `integrations: [react()]`, no adapter. Keep `public/vendor/lucide.min.js`, `favicon.svg`, `icons.svg` copied verbatim.

`package.json` scripts: `dev` (`astro dev --port 5173`), `build` (`astro build`), `typecheck` (`astro check`), `lint`. Dev proxy `/admin/api` and `/v1` → `http://127.0.0.1:17701` (preserve `vite.config.ts:12-21` via Astro's `vite.server.proxy`).

`tsconfig.json` extends `astro/tsconfigs/strict`, JSX `react-jsx`, includes `.astro`/`.ts`/`.tsx`. Convert `hmac.js` → `hmac.ts` (typed signature, keep RFC 4231 vectors), `data.js` → `data.ts` (interfaces derived from Go structs `internal/admin/status.go:19-65`, `internal/telemetry/telemetry.go:31-46`), `api.js` → `api.ts` (typed `call<T>`, session/localStorage, 401 event). `state.js` → typed store in `web/src/lib/store.ts` (context + `useSyncExternalStore` or reducer), preserving pulse signatures, `?since=` ring, optimistic toggles, config-driven cadence, unauthorized teardown.

### Shell and pages (`web/src/`)

`layouts/AppLayout.astro` — document head (fonts, CSS, lucide UMD), login gate + app shell, sidebar with three groups, header, drawer/dialog roots. Sidebar "docs → Models detail" links `/admin/models`; Phase 1 ships a placeholder empty-state card so the path is routable.

`pages/index.astro` → redirect to dashboard; `pages/{dashboard,logs,providers,models}.astro` — each mounts one React island with `client:load`. Paths stay `/admin/<page>`; Astro `base` handles prefix. Unknown `/admin/*` still serves the shell via Go SPA fallback.

React islands in `web/src/components/`: `Dashboard.tsx`, `RequestLogs.tsx`, `ProvidersModels.tsx`, plus shared `LoginGate.tsx`, `Sidebar.tsx`, `Drawer.tsx`, `Dialog.tsx`, `ThemeToggle.tsx`. Use 1.6–1.8px `currentColor` SVG icons (lucide source; no emoji icons). Port Tailwind token CSS from `index.css` into a global stylesheet imported by the layout; keep dark-mode `.dark` class + localStorage.

Preserve exactly: HMAC login, session storage, 401 teardown, 3s pulse cadence from `/admin/api/config`, gzip-friendly API calls, drawers/dialogs, provider test panel, leg editing, dark mode. **No Go handler changes in Phase 1.** Delete old Vite-only entry files (`web/src/main.js`, `web/src/js/router.js`, `state.js`, `render.js`, `ui.js`, `auth.js`, `search.js`, `theme.js`) only after behavior is ported; keep `web/wireframe/` and `web/scripts/` as reference.

## Phase 2 spec (written now, executed later — frontend only)

- "docs → Models detail" goes live: `ModelsDetail.tsx` lists per-provider catalogs via the **existing** `GET /admin/api/providers/{name}/models`. It consumes `{provider, style, source, models: string[]}` as-is. **No changes to** `internal/services/proxy_models.go`, `internal/{codex,cline}/catalog.go`, `internal/workbuddy/catalog.go`, `internal/admin/models.go`, settings, schema, or tests.
- Columns: **Model Id** (from `models[]`), **Reasoning / Max Context / Max Response Token** — display em-dash + "upstream does not provide" because the endpoint returns ids only. Adding rich metadata is explicitly out of scope for this plan and needs its own future plan with a real Codex catalog fixture. Field policy deferred by user.
- Phase 2 may add UI-side per-provider grouping/filter/search over the existing response; no new network contract.

## SubAgent fan-out (Phase 1)

Follow `subagent-fanout`: 6 agents, 2 phases; frozen typed contracts let Phase B run independently.

**Phase A (3, parallel):** (1) scaffold/config/global CSS, (2) typed lib (`api.ts`, `store.ts`, `hmac.ts`, `data.ts`), (3) shell (`AppLayout.astro`, pages, `LoginGate.tsx`, `Sidebar.tsx`, `ThemeToggle.tsx`, placeholder models page).

**Phase B (3, parallel, after typed store contract frozen):** (4) Dashboard island, (5) Request logs island, (6) Providers & Models island — each owns its component directory, no cross-edits.

Main thread reconciles, runs `pnpm -C web build && pnpm -C web lint && pnpm -C web typecheck`, `go build ./...`, `go test ./...`, `go vet ./...`, and creates CP1–CP6.

## Verification

- Per checkpoint: `pnpm -C web build` (must land in `internal/admin/dist` with `/admin/`-prefixed assets), `pnpm -C web lint`, `pnpm -C web typecheck`.
- Final: `go build ./...`, `go vet ./...`, `go test ./...`, `git diff --stat master...HEAD` review.
- Manual on **non-production port only** (`ROUTERLLM_PORT=18081`, temp config; never 1765): HMAC login via the corrected README script, load `/admin/dashboard`, `/admin/logs`, `/admin/providers`, `/admin/models`; verify dark mode toggle, provider test panel, a leg add/remove, log filter/pagination, and unknown `/admin/*` SPA fallback. Built `index.html` stays `no-store`, hashed assets immutable — `internal/admin/ui.go` unchanged.
- Optional: `docker compose build` uses the same dist contract.

## Security posture (zero-trust)

- Keep challenge–response HMAC session as the sole admin credential path; no token in JS bundle, URL, or logs. `hmac.ts` stays pure TS for plain-HTTP LAN compatibility.
- Astro is build-time only; no new server, no public endpoint, no CORS. Go remains the sole origin of `/admin/*` and `/admin/api`.
- React island data comes only from existing session-authenticated APIs; never embed secrets or provider keys in static HTML. Get Models API untouched — no new attack surface.
