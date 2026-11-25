# Tolarix — original product brief

> Kickoff brief from the product owner (2026-11-25), kept verbatim as the
> reference for scope. Decisions taken since then are in [plan.md](plan.md);
> where they differ, plan.md wins.
>
> Additions after kickoff:
> - **draw.io** (2026-11-25): for flows starting from Confluence, keep the
>   `.drawio` as an attachment in the mirror and also provide a PNG. Build a
>   generic "render" capability; draw.io is the first renderer. See
>   [renderers.md](renderers.md).
> - **Connector priority** (2026-11-25): Confluence Data Center/Server and
>   Azure DevOps Code wiki first, then the rest.
> - Keep everything needed to continue development on another PC inside the
>   repo (see [README.md](README.md) and the root `CLAUDE.md`).

---

You are starting a new greenfield project called **Tolarix**: a tool that keeps wikis in sync. One wiki is the **Master** (source of truth); one or more wikis are **Mirrors** that Tolarix creates and keeps up to date, possibly in a different wiki format than the Master.

Before writing code: read this whole brief, then enter plan mode, list any ambiguity as numbered questions for me, and propose a milestone plan. Do not start implementing until I approve the plan. Work milestone by milestone, with tests, and stop for review at the end of each milestone.

## 1. What the product does

- Reads pages (hierarchy, content, attachments) from a Master wiki in one of the supported formats and writes Mirrors in any supported format.
- Sync is **one-way**: Master → Mirrors. Mirrors are treated as read-only copies; local edits on a mirror are overwritten (warn the user about this in the UI and, optionally, with a banner on each mirrored page: "This page is mirrored from <master>. Changes made here will be overwritten.").
- When configuring the source, the user chooses to sync **everything** or **only part** of it (pick subtrees/pages from a lazily loaded tree with checkboxes; include/exclude subtrees).
- One Tolarix instance holds several **flows**. A flow = 1 Master source (+ scope) → 1..N Mirror targets + options + schedule. Each flow can run **on a schedule** or **on demand**.

### Supported formats (v1)

| Format | Read (Master) | Write (Mirror) | Notes |
|---|---|---|---|
| Azure DevOps **Project wiki** | yes | yes | REST API + PAT. Backed by a hidden git repo; `.order` files define ordering; page file names encode spaces as `-` and special chars as `%XX`; attachments live in `/.attachments`. |
| Azure DevOps **Code wiki** (published from a repo branch/folder) | yes | yes | Read/write via the Git REST API (push commits) on the configured repo, branch and folder. Consider using the same git-based writer for both ADO flavours if it simplifies things. |
| **Confluence** | yes | yes | Support Cloud (email + API token, REST v2 where possible) and Data Center/Server (PAT, REST v1). Space + optional root page. Content is storage format (XHTML) — needs a converter to/from Markdown, including common macros (code, info/note/warning panels, toc, expand). |
| **Obsidian** vault | yes | yes | A folder on disk. Handle `[[wikilinks]]`, `![[embeds]]`, aliases, frontmatter, callouts, attachment folder from `.obsidian/app.json`. In Docker the vault is a mounted volume. |

Design connectors behind a common interface so new formats can be added later without touching the sync engine.

### Canonical model & conversion

- Every connector converts to/from one **canonical model**: a page tree where each page has a stable source id, path, title, order, body in canonical Markdown (CommonMark + GFM tables/task lists), front-matter metadata, attachments (with content hash), and internal links expressed as canonical page references (not format-specific URLs).
- Link rewriting is mandatory: internal links and image references must work inside each mirror.
- Features a target cannot represent degrade gracefully and predictably (e.g. Obsidian callout → Confluence info panel → ADO blockquote with a bold label; Mermaid: ADO `::: mermaid`, Obsidian ```` ```mermaid ````, Confluence → code block unless a Mermaid macro is configured). Log every degradation as a warning in the run report.
- Converters must have golden-file tests (input fixture → expected output) per direction.

### Sync engine

- Incremental and idempotent: compute content hashes; keep a mapping table (flow, master page id ↔ mirror page id, last hashes) so renames/moves are detected as moves, not delete+create.
- Handles create, update, move/rename, delete. Delete policy per flow: delete on mirror / archive (move under an "Archived" page/folder) / keep.
- **Dry-run** mode that produces the change plan without writing.
- One run per flow at a time (lock in the DB, also respected by the CLI). Retries with backoff for HTTP 429/5xx; respect rate-limit headers.
- Each run stores a report: start/end, status, counts (created/updated/moved/deleted/skipped), warnings, errors, and a live log streamed to the UI (SSE).

## 2. Deliverables: one core, three shells

1. **Web** — Docker image, multi-user-free: a single **admin** account with authentication.
2. **Desktop** — Tauri app (Windows, macOS, Linux).
3. **CLI** — for scripting and for running syncs from cron / Windows Task Scheduler.

All three share the same core and the same SQLite database format.

## 3. Tech stack (decided — challenge only with a strong reason)

- **Backend/core: Go** (latest stable). Rationale: a single static binary that is at once the HTTP server, the scheduler and the CLI; trivial to ship in Docker and as a Tauri sidecar.
  - CLI: `spf13/cobra`.
  - SQLite: `modernc.org/sqlite` (pure Go, no CGO, easy cross-compilation). Migrations embedded in the binary (e.g. `golang-migrate` or `goose`).
  - Scheduler: `robfig/cron/v3` with timezone support.
  - HTTP: `chi` or the std-lib router; OpenAPI spec as the contract, and generate the TS client for the frontend from it.
  - The built frontend is embedded in the binary with `embed`.
- **Frontend: React + TypeScript + Vite + shadcn/ui + Tailwind**, TanStack Query, React Router, react-hook-form + zod, i18next.
- **Desktop: Tauri v2**, which bundles the Go binary as a **sidecar**, starts it on `127.0.0.1` on a random free port with a per-launch random token, and loads the UI from it.
- Monorepo layout suggestion (adjust in the plan if you have a better one):
  ```
  /core        Go module: cmd/tolarix, internal/{connectors,model,convert,sync,scheduler,store,secrets,api,auth}
  /web         React app
  /desktop     Tauri project
  /docs        user + admin docs
  /deploy      Dockerfile, docker-compose.yml, examples
  ```

## 4. Runtime modes

- `tolarix serve --mode web` — binds `0.0.0.0`, auth **on**. Used in Docker.
- `tolarix serve --mode desktop --port 0 --token <t>` — binds `127.0.0.1` only, no login screen, every request must carry the launch token. Started by Tauri.
- CLI commands work directly on the database (no server needed), and use the same run lock.
- Data directory: configurable via `--data-dir` / `TOLARIX_DATA_DIR`; defaults to `/data` in Docker and the OS app-data folder otherwise (desktop and CLI must default to the same place so they share flows).

## 5. Authentication (web mode)

- Exactly one user: `admin`. No sign-up, no user management.
- On first start, if no admin exists, generate a strong random password, store only its hash (argon2id or bcrypt), and print it **once** to the logs in a clearly visible block. Flag it `must_change_password`: the first login forces a password change.
- Settings page to change the password. CLI: `tolarix admin reset-password` (prints a new one-time password) for when it is lost.
- Session cookie: HttpOnly, SameSite=Lax, Secure when behind HTTPS (configurable), CSRF protection on mutating requests, login rate limiting.

## 6. Secrets

- Connection secrets (PATs, API tokens) are encrypted at rest (AES-GCM). Key from `TOLARIX_SECRET_KEY`, otherwise auto-generated key file in the data dir (document that losing it means re-entering secrets). On desktop, prefer the OS keychain if practical.
- Never log secrets, never return them from the API (show "••••• set" with a "replace" action).

## 7. UX — simple, no technical knowledge required

The target user is not technical. Every configuration happens through guided wizards with plain-language labels, inline help, sensible defaults, and validation messages that say how to fix the problem.

- **Connection wizard**: pick the wiki type (cards with logos) → only the fields that type needs, each with a "where do I find this?" helper (e.g. how to create an ADO PAT and which scopes it needs; how to create a Confluence API token) → **Test connection** button with a clear success/failure message → name it and save. Accept a pasted wiki/space URL and derive organization/project/wiki/space from it whenever possible.
- **Scope selection**: "Everything" or "Choose pages" with a tree picker.
- **Flow wizard**: choose Master connection + scope → add one or more Mirror connections (+ target location: folder/root page) → options (delete policy, attachments on/off, mirror banner on/off) → schedule → review summary → save, with an optional "Run a dry-run now".
- **Schedule wizard** (no cron knowledge needed): "Only when I run it manually", "Every N minutes/hours", "Every day at…", "On these weekdays at…", "Every month on day… at…", plus an "Advanced (cron expression)" tab. Always show a human-readable sentence ("Every weekday at 07:30, Europe/Rome"), the generated cron expression, and the next 5 run times. Timezone selector defaulting to the instance timezone.
- **Dashboard**: flows with status, last run, next run, "Run now" and "Dry-run" buttons; run history with filterable logs and the change report.
- **i18n**: English default, Italian second language; all UI strings in translation files, language switcher in the UI, dates/numbers localized. The CLI is English only.
- **Theme**: light / dark / system, persisted, using shadcn's theming.
- Accessible (keyboard navigation, labels, contrast) and responsive.

## 8. CLI (English)

At least:
```
tolarix serve [--mode web|desktop] [--port] [--data-dir]
tolarix flow list [--json]
tolarix flow run <flow-name-or-id> [--dry-run] [--json]
tolarix flow run --all            # run every enabled flow
tolarix run-due                   # run flows whose schedule is due; meant for external schedulers
tolarix connection list|test <name>
tolarix runs list|show <run-id>
tolarix admin reset-password
tolarix version
```
Meaningful exit codes (0 success, distinct codes for partial failure, failure, already-running lock), `--json` for machine output, logs to stderr.

## 9. Docker

- Multi-stage build (Node build of the web app → Go build → minimal runtime image, non-root user), volume `/data`, `HEALTHCHECK` on `/healthz`, configuration via env vars.
- `deploy/docker-compose.yml` example including an Obsidian vault mounted as a volume.
- Document reverse-proxy/HTTPS setup briefly.

## 10. Documentation (in `/docs`, English; Italian translation of the user guide is a nice-to-have)

- User guide: connecting each wiki type (with required permissions/scopes), creating flows, scopes, schedules, reading run reports, known conversion limitations per format pair.
- Admin guide: Docker install, first-login password from logs, password reset, backup/restore of the data dir, secret key handling, upgrades.
- **External scheduling guide** (`docs/scheduling.md`) for people who prefer the OS scheduler over the built-in one, using `tolarix flow run <flow>` or `tolarix run-due`:
  - Linux/macOS **cron** (crontab examples, PATH and env notes, logging to file),
  - **systemd timer** unit example,
  - **Windows Task Scheduler**: GUI step-by-step, `schtasks` command, and PowerShell `Register-ScheduledTask` example,
  - **Docker**: host cron calling `docker exec`, and a Kubernetes `CronJob` example,
  - how the run lock prevents overlaps between the built-in scheduler and external ones, and how to disable the built-in schedule for a flow.
- Developer guide: architecture, how to add a new connector, how to run tests.

## 11. Quality bar

- Go: unit tests for converters (golden files), sync engine (fake connectors, all change types, moves, deletes, dry-run), scheduler/schedule-wizard translation; connector tests against `httptest` servers with recorded fixtures (no real credentials in the repo).
- Optional integration tests against real ADO/Confluence instances, enabled only via env vars.
- Frontend: component tests (Vitest + Testing Library) and Playwright e2e for login, connection wizard, flow wizard and schedule wizard.
- Lint/format: `golangci-lint`, `gofmt`, ESLint, Prettier. A CI workflow that builds and tests everything and builds the Docker image.
- Structured logging (`slog`), no secrets in logs.

## 12. Suggested milestones (refine in your plan)

1. Repo scaffold, Go core skeleton, SQLite + migrations, CLI skeleton, React app with shadcn, i18n (en/it) and theme switch, CI.
2. Canonical model, Obsidian connector (read + write), sync engine with mapping table, dry-run, `flow run` from CLI — end-to-end Obsidian → Obsidian.
3. HTTP API + OpenAPI, web auth (single admin), dashboard, connection & flow wizards, run history with live logs, Docker image.
4. Azure DevOps project wiki + code wiki connectors and converters.
5. Confluence connector (Cloud + Data Center) and storage-format converter.
6. Built-in scheduler + schedule wizard, `run-due`, run locking.
7. Tauri desktop app with Go sidecar, installers for Windows/macOS/Linux.
8. Documentation (incl. external scheduling guide), hardening, accessibility pass, release.
