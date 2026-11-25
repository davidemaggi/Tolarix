# Tolarix — development plan and status

This is the source of truth for where the project stands. Update the **Status**
table and the **Decisions log** at the end of every milestone (and whenever a
decision changes), so work can continue on any machine.

The original product brief is in [brief.md](brief.md).

## Status

| # | Milestone | Status | Branch / notes |
|---|---|---|---|
| 1 | Scaffold: Go core, SQLite + migrations, CLI skeleton, React + shadcn, i18n en/it, theme, CI | **done, awaiting review** | pushed to `develop` |
| 2 | Canonical model, Obsidian connector, sync engine + mapping, dry-run, `flow run` CLI, renderer pipeline (interface) | next | |
| 3 | OpenAPI + web auth, dashboard, connection & flow wizards, run history + live logs, Docker image | | |
| 4 | Azure DevOps project wiki + code wiki connectors and converters | | |
| 5 | Confluence (Cloud + DC) connector, storage-format converter, **draw.io renderer** | | |
| 6 | Built-in scheduler, schedule wizard, `run-due`, run locking end to end | | |
| 7 | Tauri desktop app with Go sidecar, installers | | |
| 8 | Docs (incl. external scheduling guide), hardening, a11y pass, release | | |

Each milestone ends with tests green and a stop for review before the next one starts.

## Milestone details

1. **Scaffold** — monorepo, Go module, cobra skeleton (`version`, `serve`), data-dir resolution, SQLite + goose migrations (connections, flows, targets, mappings, runs, run_events, locks, settings, admin, sessions), secrets (AES-GCM, key from env/file), slog; React + Vite + TS + Tailwind + shadcn, Router, Query, i18next en/it, theme switch, app shell; ESLint/Prettier/golangci-lint; GitHub Actions.
2. **Canonical model + Obsidian + engine** — model; Obsidian decode/encode (wikilinks, embeds, aliases, frontmatter, callouts, attachment folder from `.obsidian/app.json`); golden tests; sync engine with fake connectors (create/update/move/delete/archive/keep, dry-run, idempotency, crash-resume); mapping table; run reports; run lock; CLI `flow run/list`, `runs list/show`, `connection list/test`, temporary `flow import <yaml>`; **renderer pipeline interface + derived attachments in the model** (see [renderers.md](renderers.md)). E2E: Obsidian → Obsidian.
3. **API + web** — OpenAPI; auth (first-start password block, forced change, `admin reset-password`, rate limit, CSRF); endpoints for connections/flows/runs/tree-browse/test; SSE logs; dashboard; connection wizard; scope tree picker; flow wizard; run history; Dockerfile + compose (vault volume) + `/healthz`; Vitest + Playwright.
4. **Azure DevOps** — URL parser, PAT test, git-based reader/writer for both flavours, `.order`, name encoding, `/.attachments`, `::: mermaid`, `[[_TOC_]]`, httptest fixtures, golden tests, shared retry/backoff HTTP client.
5. **Confluence** — Cloud + DC auth, space/root page, storage format ⇄ Markdown incl. code/info/note/warning/tip/toc/expand, attachments, URL parsing, title uniqueness, banner, fixtures + golden tests; **draw.io: keep the `.drawio` source and add the PNG** (preview reuse first, optional export server).
6. **Scheduler** — `robfig/cron/v3` with TZ; schedule model ↔ cron + human sentence (en/it) + next 5 runs, server-side; `run-due`; per-flow built-in schedule switch; schedule wizard.
7. **Desktop** — Tauri v2, sidecar on 127.0.0.1 port 0 + launch token (ready line `TOLARIX_READY {"port":…}` on stdout), OS keychain for the secret key with file fallback, installers via CI (unsigned in v1).
8. **Docs + hardening + release** — user/admin/dev guides, `docs/scheduling.md`, Italian user guide, a11y (axe in Playwright), security review, release workflow, v1.0.0.

## Decisions log

### Confirmed with the product owner
- Obsidian hierarchy: `Parent.md` + sibling folder `Parent/` (same as ADO). A folder without a note is a title-only page.
- Confluence duplicate titles: suffix the parent title (`Overview (Backend)`), log a warning, keep it stable through the mapping table.
- Pre-existing pages at a mirror target that Tolarix did not create are never touched. A name collision fails that page with a fix-it message. Optional per-target "adopt existing pages" flag.
- Move detection for path-identity sources (Obsidian, ADO): a delete + create pair with identical body hash is a move; otherwise delete + create. Tolarix never writes ids into the Master.
- draw.io (2026-11-25): in mirrors keep the `.drawio` as an attachment **and** show a PNG. Build a generic "render" step; draw.io is the first renderer. See [renderers.md](renderers.md).

### Defaults taken (not yet challenged)
- Manual edits on a mirror: detected (mirror hash ≠ last written), overwritten, with a warning naming the page.
- Missed schedules (server down): run once on startup, no backfill.
- Concurrency: different flows in parallel, global cap 2; the mirrors of one flow run sequentially from one Master snapshot.
- Desktop and Docker on the same machine do not share data.
- Installers unsigned in v1.
- Azure DevOps Services only in v1 (base URL configurable; Server untested).
- Mirror banner: ADO/Obsidian blockquote/callout at top; Confluence info panel; stripped if ever re-read.
- Unknown Confluence macros: kept as a fenced `confluence-macro` block with the raw XML + a warning; never silently dropped.

### Technical choices
- CI: GitHub Actions. Image: `ghcr.io/davidemaggi/tolarix`. GoReleaser for CLI binaries (M8).
- Migrations: goose (embedded). Router: chi. OpenAPI hand-written as the contract; `oapi-codegen` (server) + `openapi-typescript`/`openapi-fetch` (web). CI fails on stale generated code.
- Passwords argon2id; server-side sessions in SQLite; CSRF double-submit header `X-CSRF-Token`.
- Live logs: `run_events` table + in-process pub/sub; SSE replays from the DB then tails (CLI-started runs are visible in the UI).
- Locking: `flow_locks` row with heartbeat; stale after 2 min. SQLite WAL + busy_timeout + `_txlock=immediate`.
- Default data dir: `os.UserConfigDir()/Tolarix` (`tolarix` on Linux); `/data` in Docker via `TOLARIX_DATA_DIR`.
- **TypeScript pinned to 6.x**: typescript-eslint does not support TS 7 yet. Revisit when it does.
- The web build is copied into `core/internal/webui/dist` (only `.gitkeep` is tracked) and embedded with `//go:embed all:dist`.
- No inline scripts in `index.html`: the server CSP forbids them. The theme is applied in `main.tsx` before the first render.

## Known follow-ups
- Web bundle > 500 kB warning: add route-level code splitting in M3 when real pages arrive.
- Client IP for login rate limiting (M3): add a `TOLARIX_TRUSTED_PROXIES` setting; chi `middleware.RealIP` was removed because it trusts `X-Forwarded-For` blindly.
- `serve` in web mode currently has no auth: API endpoints arrive with auth in M3. Do not expose an M1/M2 build publicly.
