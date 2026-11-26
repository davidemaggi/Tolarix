# Tolarix — development plan and status

This is the source of truth for where the project stands. Update the **Status**
table and the **Decisions log** at the end of every milestone (and whenever a
decision changes), so work can continue on any machine.

The original product brief is in [brief.md](brief.md).

## Status

| # | Milestone | Status | Branch / notes |
|---|---|---|---|
| 1 | Scaffold: Go core, SQLite + migrations, CLI skeleton, React + shadcn, i18n en/it, theme, CI | done | `develop` |
| 2 | Canonical model, Obsidian connector, sync engine + mapping, dry-run, `flow run` CLI, renderer pipeline (interface) | done | `develop` |
| 3 | OpenAPI + web auth, dashboard, connection & flow wizards, run history + live logs, Docker image | done | `develop` |
| 4 | **Priority connectors:** Confluence Data Center/Server + Azure DevOps Code wiki, storage-format converter, **draw.io renderer** | done | `develop` |
| 5 | Remaining connectors: Confluence Cloud + Azure DevOps Project wiki | done | `develop` |
| 6 | Built-in scheduler, schedule wizard, `run-due`, run locking end to end | **done, awaiting review** | `develop` |
| 7 | Tauri desktop app with Go sidecar, installers | | |
| 8 | Docs (incl. external scheduling guide), hardening, a11y pass, release | | |

Each milestone ends with tests green and a stop for review before the next one starts.

### Where we left off (2026-11-26)

- M5 was approved ("procedi", 2026-11-26). M6 is committed on `develop` (not pushed). Start the next session by asking for the M6 review and approval to begin M7.
- Defaults chosen in M2–M6 that are awaiting review are listed under "Taken during M2/M3/M4/M5/M6" below. How each wiki is mapped: [connectors.md](connectors.md).
- M4 and M5 were tested against in-memory fakes of the Confluence (v1 and v2) and ADO APIs only; nothing has been run against a real instance yet. A smoke test on real wikis (DC, Cloud, ADO code and project wiki) is the first thing to do with the product owner.
- **Next: M7.** Tauri desktop app with the Go sidecar, installers.
- To resume on a new machine, follow [README.md](README.md) (prerequisites, `pnpm install`, `pnpm build`, tests).

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
- CI runs on pushes to `main` and on pull requests only, not on `develop` (2026-11-25).
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

Taken during M3:
- **Auth**: one `admin` account. The first start prints a one-time password once (boxed, on stderr); until it is changed only session/password/logout work (`403 password_change_required`). `tolarix admin reset-password` prints a new one and ends every session. Minimum length 12. Sessions: 7 days idle, 30 days at most; a password change ends the other sessions. Login rate limit: 10 failures per 15 minutes per client IP. Client IPs and HTTPS come from `X-Forwarded-*` only for `TOLARIX_TRUSTED_PROXIES`. Session cookie: HttpOnly, SameSite=Lax, Secure when HTTPS (`TOLARIX_COOKIE_SECURE`).
- **Desktop mode has no sign-in** (launch token instead); the UI hides sign-out and password there.
- **API rules**: a mirror cannot use the Master's connection; the kind of a saved connection cannot change; a flow cannot be edited or deleted while it runs (`409 running`); a connection used by a flow cannot be deleted (`409 in_use`, message lists the flows). Runs started from the CLI can only be stopped from the CLI (`409 not_here`); runs started in the UI can be cancelled (`POST /runs/{id}/cancel`).
- **Manual runs work on disabled flows**: "disabled" only stops automatic schedules.
- **Banner**: on by default for new flows; custom text per flow (max 2000 characters, `{master}` placeholder); empty = the default text, still English (the server writes it into the mirrors).
- **Flow wizard**: the Master of a saved flow cannot be changed in the UI (its page ids and mappings belong to it); create a new flow instead. Steps: Master, what to mirror, Mirrors, options, schedule, review. A disabled flow can still be run by hand.
- **Run detail**: the live log keeps the last 5,000 lines in the browser (the full log stays on the server); the report hides unchanged pages by default.
- **Server validation messages** (`fields` in 400 answers) are English only; the UI translates the error codes it knows. Follow-up below.
- **Docker**: distroless static image, uid 65532, `/data` volume, `HEALTHCHECK` = `/tolarix healthcheck`; compose mounts `./vaults` at `/vaults` for Obsidian.

Taken during M4:
- **Buffered targets**: a session that implements `connectors.Buffering` (ADO) keeps every write in memory; the engine saves its mappings only after `Finish` succeeds. A failed push reports every operation as "not saved" and the next run starts from the same state.
- **ADO: one commit per run**, pushed with `oldObjectId` = the head read at the start. A concurrent push fails the run ("run the flow again"). Pushes over 64 MiB are split into parts.
- **ADO project wikis** (`wikiType: projectWiki`) are rejected with a clear message until M5; code wikis and repository folders work.
- **ADO as a mirror**: moves are delete + add; archive is a git rename to `<target>/Archived/…` (history follows); `.order` is regenerated in every folder under the target (unmanaged pages after, alphabetically); attachments go to `<target folder>/.attachments/`; front matter (page meta) is kept; callouts become `> **Tip:** …` (the wiki has none; one way).
- **ADO anchors** are written as the wiki makes them (lower case, spaces → `-`, other characters percent-encoded); the reader also accepts GitHub-style anchors.
- **Confluence as a mirror**: titles unique per space (parent title in brackets, then a number, `title-renamed` warning); a title held by a page Tolarix does not own fails that page as a conflict unless "adopt existing pages" is on. Archive = move under `Archived – <parent title>` (`Archived – Tolarix` at the space top) and rename `<title> (archived)`. Edits in Confluence are detected through the page version stored in the mapping (migration `00003`). Attachments go to the first page that uses them. Mermaid becomes a `code` macro titled `mermaid`; unknown macros round-trip as raw XML.
- **Confluence auth**: a PAT (Bearer) by default; with `username`, Basic with that user's password (older Server versions).
- **draw.io → PNG order**: preview next to the diagram, render cache (`<dataDir>/cache/render`), export server (`TOLARIX_DRAWIO_EXPORT_URL`, `jgraph/export-server` as an optional compose service), draw.io Desktop. Without any of them the mirror keeps the `.drawio` link and the run warns how to enable one. First page only.
- **HTTP retries** (`internal/httpc`): 429 and 503 always; 500/502/504 and network errors only for idempotent methods; honours `Retry-After` and rate-limit headers.

Taken during M5:
- **Confluence Cloud** is its own kind (`confluence-cloud`: url, email, API token; Basic `email:token`). It shares the source, target and storage converter with Data Center; every REST call goes through an internal `api` interface with a v1 (Data Center) and a v2 (Cloud) implementation. Cloud uses REST v2 everywhere except attachment upload and download, which v2 does not offer (v1 endpoints, as Atlassian documents).
- **Cloud as a Master**: the whole space is read with one paged `/spaces/{id}/pages?depth=all` listing (bodies included) and the tree is built from `parentId` + `position`; folders are fetched one by one and become placeholder pages. Smart links (plain `<a href>` to a page of the snapshot, `/wiki/spaces/KEY/pages/ID/…`) become page links, with the title as text when the link showed the address. ADF panels (`ac:adf-extension`) become callouts; other ADF blocks use their fallback. Cloud anchors (`#Getting-Started`) are matched to headings.
- **Cloud as a Mirror**: with no parent and a space address, mirrors go under the **space homepage** (where Cloud puts new pages), and the archive page is `Archived – <homepage title>`. Titles are still kept unique per space, as on Data Center.
- **Cloud API tokens**: classic tokens only; a 401 says to check the email and token and that scoped tokens are not supported. An `atlassian.net` address without `/wiki` gets it added.
- **ADO Project wiki** is its own kind (`azure-devops-project`) on the same git reader/writer: the address is the project wiki (`…/<project>/_wiki` or `…/_wiki/wikis/<name>.wiki`); the wiki API gives the hidden `<name>.wiki` repository; branch `wikiMaster`, mapped path `/`. A project without a project wiki gets "create it in Azure DevOps (Overview → Wiki → Create project wiki)". Addresses of the wrong wiki type point to the other kind.

Taken during M6:
- **Schedule model** (`internal/schedule`): kinds `manual`, `interval`, `daily`, `weekly`, `monthly`, `cron`; each kind keeps only its own fields (the API stores the canonical JSON). Intervals are limited to steps that divide the hour or the day (5/10/15/20/30 minutes, 1/2/3/4/6/8/12 hours) so they map to plain cron (`*/N`) and line up with the clock. Advanced cron: 5 fields or `@yearly/@monthly/@weekly/@daily/@hourly`; no seconds, no `TZ=`/`CRON_TZ=` prefix (the flow's timezone field is the only timezone), no `@every`, and expressions that never fire (e.g. 30 February) are rejected. Monthly on day 29–31 skips shorter months, and the sentence says so.
- **Sentence and preview are server-side**: `POST /api/v1/schedules/preview` returns the sentence (en/it), the cron expression and the next 5 runs. The language comes from `lang` in the body or `Accept-Language`, which the web client sends from the UI language; flows carry `scheduleText` the same way and are refetched when the language changes. The sentence ends with the timezone (except for manual).
- **When a flow is due**: the next run is the first schedule time after the flow's *anchor* (`flows.schedule_anchor`, migration `00004`; the creation time at first). Whoever starts a due flow first moves the anchor to *now* with one conditional UPDATE (`ClaimSchedule`), so the server and any number of `run-due` processes never start the same slot twice. Saving a flow resets the anchor when the schedule, timezone, enabled or built-in switch change, so a new schedule never fires for times before it existed.
- **Missed schedules run once, no backfill**: on startup (or after a pause) all missed slots of a flow collapse into one run, because the anchor moves to the claim time. A slot skipped because the flow is already running (lock held) is consumed, not retried.
- **Built-in scheduler** (`internal/scheduler`, started by `serve`): checks every 20 s, at startup, after a flow is saved and after a run ends. Global cap of 2 runs at once, counting every background run of the server (manual ones included); manual runs are never blocked by the cap. Due flows wait for a free slot oldest first. It stops before running runs are shut down.
- **`enabled` vs built-in switch**: a disabled flow never runs by itself. `scheduleEnabled` false (UI: "Run with the built-in scheduler" off) means the server skips the flow and only `tolarix run-due` runs it, at the same times; `nextRunAt` is still shown.
- **`tolarix run-due`**: runs every due flow once, one after the other, and exits; `--json` prints the run results. Exit codes as `flow run` (0 also when nothing is due; 4 if a flow was locked, and that slot is consumed). It ignores the cap (it is its own process) but respects the per-flow DB lock.
- **Wizard**: Simple tab (radio list with inline fields; the weekday toggles default to Monday–Friday, time 07:30) and Advanced tab (cron; it starts from the cron of the simple form). The timezone field defaults to the instance timezone and suggests IANA names. The preview (sentence, cron, next 5 runs in the flow's timezone) updates live, debounced by 300 ms, and shows the server's field errors.
- **YAML import**: flows take `schedule` (same JSON shape), `timezone` (kept on re-import when omitted) and `builtInSchedule`.

### Technical choices
- CI: GitHub Actions. Image: `ghcr.io/davidemaggi/tolarix`. GoReleaser for CLI binaries (M8).
- Migrations: goose (embedded). Router: chi. OpenAPI hand-written as the contract; `oapi-codegen` (server) + `openapi-typescript`/`openapi-fetch` (web). CI fails on stale generated code.
- Passwords argon2id; server-side sessions in SQLite; CSRF synchronizer token per session (from `GET /auth/session`), sent as `X-CSRF-Token`.
- Web data layer: openapi-fetch + TanStack Query; generated types in `web/src/api/schema.gen.ts`. Routes other than the dashboard are lazy-loaded.
- E2E: Playwright starts the real binary in web mode on a temp data dir and reads the one-time password from its output (`web/e2e/global-setup.ts`).
- Live logs: `run_events` table + in-process pub/sub; SSE replays from the DB then tails (CLI-started runs are visible in the UI).
- Locking: `flow_locks` row with heartbeat; stale after 2 min. SQLite WAL + busy_timeout + `_txlock=immediate`.
- Default data dir: `os.UserConfigDir()/Tolarix` (`tolarix` on Linux); `/data` in Docker via `TOLARIX_DATA_DIR`.
- **TypeScript pinned to 6.x**: typescript-eslint does not support TS 7 yet. Revisit when it does.
- The web build is copied into `core/internal/webui/dist` (only `.gitkeep` is tracked) and embedded with `//go:embed all:dist`.
- No inline scripts in `index.html`: the server CSP forbids them. The theme is applied in `main.tsx` before the first render.

## Known follow-ups
- Server validation messages and the default banner text are English only: return message keys + params so the UI (and mirrors) can translate them.
- Connector field labels in the UI come from `connectorFields.<kind>.<field>` translations, falling back to a humanised field name: add the keys with each new connector.
- **M4 follow-ups**:
  - Confluence: page labels and other metadata are not mapped yet (meta only through ADO/Obsidian front matter).
  - Confluence as a mirror does not set sibling order (Confluence orders children itself); Master order is lost there.
  - Confluence mirrors show a draw.io diagram as image + link to the `.drawio`; writing the `drawio` macro when the target has the app is not done.
  - ADO split pushes: if a later part fails, the earlier parts stay pushed but their mappings are not saved; the next run sees them as edits made in the wiki and rewrites them (correct, but noisy warnings).
  - On a first run, an attachment used by the renderer and the mirror is downloaded twice from the Master.
  - Azure DevOps Server (on-premises) URLs are parsed but untested.
- **M5 follow-ups**:
  - Confluence Cloud now allows duplicate page titles in a space; Tolarix still makes mirror titles unique (safe, and links by title keep working). Revisit if the product owner wants exact titles on Cloud.
  - Scoped Atlassian API tokens (which use `api.atlassian.com/ex/confluence/<cloudId>`) are not supported.
  - Cloud: folders that hold no pages are not read (the space listing returns pages only); empty folders are not mirrored. Folders are not created on mirrors either (placeholder pages instead).
  - Cloud browse marks every page and folder as having children (v2 cannot tell cheaply); expanding an empty one shows nothing.
  - Cloud whiteboards, databases and embeds are skipped.
- The web bundle's main chunk is ~375 kB (118 kB gzip); the Radix/shadcn shared chunk could be split further if it grows.
- Playwright has no axe checks yet (M8 a11y pass).
- **Known limitation, swaps**: if two mirrored pages exchange places in one Master change (A takes B's path and B takes A's), both moves are reported as conflicts on every run. Fix: move one of them to a temporary key first (engine `apply`). Rare; still open after M5.
- Run event attributes are stored as strings (`fmt.Sprint`); the UI shows them as `key=value`, which is enough for now.
- **M6 follow-ups**:
  - The cap of 2 lives in the built-in scheduler: `run-due` processes and CLI `flow run` are not counted (only the per-flow lock applies across processes).
  - Schedule sentences and validation messages for the advanced cron field are server-side English/Italian; cron parse errors are English only.
