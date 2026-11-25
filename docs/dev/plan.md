# Tolarix — development plan and status

This is the source of truth for where the project stands. Update the **Status**
table and the **Decisions log** at the end of every milestone (and whenever a
decision changes), so work can continue on any machine.

The original product brief is in [brief.md](brief.md).

## Status

| # | Milestone | Status | Branch / notes |
|---|---|---|---|
| 1 | Scaffold: Go core, SQLite + migrations, CLI skeleton, React + shadcn, i18n en/it, theme, CI | done | `develop` |
| 2 | Canonical model, Obsidian connector, sync engine + mapping, dry-run, `flow run` CLI, renderer pipeline (interface) | **done, awaiting review** | `develop` |
| 3 | OpenAPI + web auth, dashboard, connection & flow wizards, run history + live logs, Docker image | | |
| 4 | **Priority connectors:** Confluence Data Center/Server + Azure DevOps Code wiki, storage-format converter, **draw.io renderer** | | |
| 5 | Remaining connectors: Confluence Cloud + Azure DevOps Project wiki | | |
| 6 | Built-in scheduler, schedule wizard, `run-due`, run locking end to end | | |
| 7 | Tauri desktop app with Go sidecar, installers | | |
| 8 | Docs (incl. external scheduling guide), hardening, a11y pass, release | | |

Each milestone ends with tests green and a stop for review before the next one starts.

## Milestone details

1. **Scaffold** — monorepo, Go module, cobra skeleton (`version`, `serve`), data-dir resolution, SQLite + goose migrations (connections, flows, targets, mappings, runs, run_events, locks, settings, admin, sessions), secrets (AES-GCM, key from env/file), slog; React + Vite + TS + Tailwind + shadcn, Router, Query, i18next en/it, theme switch, app shell; ESLint/Prettier/golangci-lint; GitHub Actions.
2. **Canonical model + Obsidian + engine** — model; Obsidian decode/encode (wikilinks, embeds, aliases, frontmatter, callouts, attachment folder from `.obsidian/app.json`); golden tests; sync engine with fake connectors (create/update/move/delete/archive/keep, dry-run, idempotency, crash-resume); mapping table; run reports; run lock; CLI `flow run/list`, `runs list/show`, `connection list/test`, temporary `flow import <yaml>`; **renderer pipeline interface + derived attachments in the model** (see [renderers.md](renderers.md)). E2E: Obsidian → Obsidian.
3. **API + web** — OpenAPI; auth (first-start password block, forced change, `admin reset-password`, rate limit, CSRF); endpoints for connections/flows/runs/tree-browse/test; SSE logs; dashboard; connection wizard; scope tree picker; flow wizard; run history; Dockerfile + compose (vault volume) + `/healthz`; Vitest + Playwright.
4. **Priority connectors (product owner, 2026-11-25)** — shared retry/backoff HTTP client (429/5xx, rate-limit headers).
   - **Confluence Data Center / Server**: PAT auth, REST v1, space + optional root page, URL parsing, storage format ⇄ Markdown incl. code/info/note/warning/tip/toc/expand, attachments, title uniqueness, banner; **draw.io: keep the `.drawio` source and add the PNG** (preview reuse first, optional export server).
   - **Azure DevOps Code wiki**: URL parser, PAT test, git-based reader/writer (Git REST API, one push per run) on repo + branch + folder, `.order`, name encoding, `/.attachments`, `::: mermaid`, `[[_TOC_]]`. Written so the Project wiki can reuse it.
   - httptest fixtures + golden tests for both.
5. **Remaining connectors** — Confluence Cloud (email + API token, REST v2 where possible; reuses the DC converter) and Azure DevOps Project wiki (its hidden `<wiki>.wiki` repo, `wikiMaster` branch, through the same git writer).
6. **Scheduler** — `robfig/cron/v3` with TZ; schedule model ↔ cron + human sentence (en/it) + next 5 runs, server-side; `run-due`; per-flow built-in schedule switch; schedule wizard.
7. **Desktop** — Tauri v2, sidecar on 127.0.0.1 port 0 + launch token (ready line `TOLARIX_READY {"port":…}` on stdout), OS keychain for the secret key with file fallback, installers via CI (unsigned in v1).
8. **Docs + hardening + release** — user/admin/dev guides, `docs/scheduling.md`, Italian user guide, a11y (axe in Playwright), security review, release workflow, v1.0.0.

## Decisions log

### Confirmed with the product owner
- Obsidian hierarchy: `Parent.md` + sibling folder `Parent/` (same as ADO). A folder without a note is a title-only page.
- Confluence duplicate titles: suffix the parent title (`Overview (Backend)`), log a warning, keep it stable through the mapping table.
- Pre-existing pages at a mirror target that Tolarix did not create are never touched. A name collision fails that page with a fix-it message. Optional per-target "adopt existing pages" flag.
- Move detection for path-identity sources (Obsidian, ADO): a delete + create pair with identical body hash is a move; otherwise delete + create. Tolarix never writes ids into the Master.
- Connector priority (2026-11-25): Confluence Data Center/Server and Azure DevOps Code wiki first (M4), then Confluence Cloud and ADO Project wiki (M5).
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

Taken during M2:
- **Default delete policy: archive** (nothing is lost unless the user picks "delete"). Archive = move under `Archived/` in the target folder, keeping the relative path. A folder-only page has nothing to archive and is simply removed once empty.
- **Attachments** are removed from a mirror only with the "delete" policy (archived pages still link to them).
- **Scope "selected"**: each selected subtree becomes a top-level page of the mirror; its Master ancestors are not copied. Exclusions win over inclusions. Missing selected/excluded pages produce a warning, not a failure.
- **Obsidian as a mirror**: links are written as wikilinks with the full vault path (`[[Mirror/Projects/Alpha|text]]`), so they never become ambiguous. Attachments follow the target vault's `attachmentFolderPath`; without one they go to `<target folder>/attachments`. Same-name attachments with different content get ` (<hash8>)` added to the name.
- **Obsidian as a Master**: link resolution follows Obsidian (exact path, then relative to the note, then by name with the shortest path winning). Sibling order is alphabetical (Obsidian has no manual order). Hidden files and folders (`.obsidian`, `.trash`, …) are ignored.
- **Degradations, each with a warning**: embedded notes (`![[Note]]`) become links; links to missing or out-of-scope pages become plain text; invalid front matter is kept as text; names a target cannot store are renamed (illegal characters, case-insensitive sibling clashes, names reserved for `Archived` / the attachment folder).
- **Banner** text is English for now: "This page is mirrored from `Master connection name`. Changes made here will be overwritten." (canonical `> [!warning]` callout; each target renders its own equivalent).
- **Dry-runs take the run lock too**, so a plan is never computed while a real run is writing.
- **CLI short ids** are the last 8 characters of a UUIDv7 (the first characters are a timestamp shared by ids created together). `runs show` accepts them.
- `tolarix flow import <file.yaml>` creates/updates connections and flows by name (mirrors keep their id, and so their history, when the connection and location are unchanged). Secrets come from `secretEnv: VAR`. Example: [../examples/flows.example.yaml](../examples/flows.example.yaml).

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
- **Known limitation, swaps**: if two mirrored pages exchange places in one Master change (A takes B's path and B takes A's), both moves are reported as conflicts on every run. Fix: move one of them to a temporary key first (engine `apply`). Rare; tackle with the ADO connector, where renames are common.
- Run event attributes are stored as strings (`fmt.Sprint`); typed values if the UI needs them (M3).
- Banner text should become translatable/configurable per flow (M3 flow wizard).
- `runner.Runner` has no global concurrency cap yet (the built-in scheduler in M6 adds the cap of 2).
