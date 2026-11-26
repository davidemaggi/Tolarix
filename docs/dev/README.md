# Developer guide

Everything needed to pick the project up on a new PC: architecture, setup,
tests, conventions and how to release. Status and decisions: [plan.md](plan.md).
Original scope: [brief.md](brief.md). User-facing docs: [user guide](../user-guide.md),
[admin guide](../admin-guide.md), [scheduling](../scheduling.md).

## Architecture

```
tolarix (one Go binary)
├── api        chi + OpenAPI: auth, CSRF, SSE logs, embedded React UI (webui)   ◀── browser, Tauri window
├── cli        cobra: serve, flow run, run-due, runs, import, admin             ◀── shell, cron, Task Scheduler
├── scheduler  due flows + schedule claims (built-in, shared with run-due)
├── runner     per-flow lock + heartbeat, run row, live events, report
│   └── engine     plan + apply: scope, renderers, moves, delete policy, dry-run
│       ├── connectors   Source / Target / Session: obsidian, confluence (v1, v2), azuredevops
│       └── render       draw.io → PNG
├── store      SQLite + goose migrations: connections, flows, mappings, runs, locks
└── secrets    AES-GCM; key from env, secret.key or the OS keychain
```

- **One binary, three faces.** `tolarix serve` hosts the API, the embedded web UI and
  the built-in scheduler (web mode for Docker, desktop mode for the Tauri sidecar).
  Every other command is a CLI that opens the same SQLite database directly; no
  server is needed. The desktop app (`desktop/`) is a thin Tauri shell around
  `serve --mode desktop`.
- **Canonical model.** Every connector converts its wiki to and from one page tree
  (`internal/model`): CommonMark + GFM bodies, `tolarix://page/<id>` and
  `tolarix://att/<sha256>` links, callouts, Mermaid fences. The engine only sees
  this model, so a new wiki is one new connector.
- **Engine.** Reads a snapshot of the Master, applies the scope, runs the
  renderers, then for each Mirror plans the operations (create, update, move,
  archive/delete/keep, conflict) from the mapping table and content hashes, and
  applies them, saving each mapping as it goes. Dry-runs stop after the plan.
- **Runner and locking.** Every run, from any entry point, goes through
  `internal/runner`: it takes the per-flow lock in the database (heartbeat, stale
  after 2 minutes), records the run, streams events to `run_events` (the UI follows
  them over SSE) and stores the report.
- **Scheduler.** `internal/scheduler` decides which flows are due (schedule +
  anchor) and claims each scheduled time with one conditional UPDATE, so the
  built-in scheduler and any `run-due` process never start the same time twice.
- **Security boundaries.** Secrets are `secrets.String` everywhere (they redact
  themselves) and are encrypted in the database. The API never returns them. The web
  UI talks to the API only; desktop mode accepts only 127.0.0.1 and a launch token.

## Prerequisites

| Tool | Version used | Notes |
|---|---|---|
| Go | 1.27.x (see `core/go.mod`) | no CGO needed (pure-Go SQLite) |
| Node.js | 22+ | |
| pnpm | 10.x (see `packageManager` in `package.json`) | `corepack enable` installs the right one |
| golangci-lint | v2 | `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest` |
| Rust + [Tauri prerequisites](https://v2.tauri.app/start/prerequisites/) | stable (`rustup default stable`) | only for the desktop app (`desktop/`); add `~/.cargo/bin` to PATH |
| Docker | any recent | to build the image (`deploy/`) |

## First run

```sh
git clone https://github.com/davidemaggi/Tolarix.git && cd Tolarix
pnpm install
pnpm build              # builds the web UI, copies it into core/internal/webui/dist, builds ./bin/tolarix
./bin/tolarix serve --data-dir ./data    # http://localhost:8080
```

`./data` is git-ignored. Never commit a `tolarix.db` or a `secret.key`.

On the first start in web mode the server creates the `admin` account and prints a
one-time password once, in a box on stderr. Sign in with it; the UI then asks for a new
password (at least 12 characters). Lost it? `tolarix admin reset-password --data-dir ./data`
prints a new one-time password and signs out every session. Desktop mode
(`--mode desktop`) has no sign-in; it is protected by the launch token instead.

## Docker

```sh
docker build -f deploy/Dockerfile -t tolarix --build-arg VERSION=dev .
docker compose -f deploy/docker-compose.yml up -d      # data volume + ./deploy/vaults mounted at /vaults
docker compose -f deploy/docker-compose.yml logs       # the one-time password
docker exec -it <container> /tolarix admin reset-password
```

The image is distroless (no shell), runs as uid 65532, keeps everything in the `/data`
volume (database + `secret.key`: back them up together) and has a `HEALTHCHECK` that runs
`/tolarix healthcheck`. Obsidian vaults must be mounted; connections use the path inside
the container.

## Configuration (environment)

| Variable | Default | Meaning |
|---|---|---|
| `TOLARIX_DATA_DIR` | OS app-data folder (`/data` in Docker) | database, `secret.key` (shared by the desktop app and the CLI) |
| `TOLARIX_SECRET_KEY` | none | base64 secret key; overrides `secret.key` and the keychain |
| `TOLARIX_SECRET_KEY_STORE` | `file` | `keychain` stores a *new* key in the OS keychain (the desktop app sets it); see plan.md, "Taken during M7" |
| `TOLARIX_DESKTOP_TOKEN` | none | launch token in desktop mode (set by the desktop app; preferred over `--token`) |
| `TOLARIX_MODE` | `web` | `web` (admin login) or `desktop` (launch token, 127.0.0.1 only) |
| `TOLARIX_HOST` / `TOLARIX_PORT` | `0.0.0.0` / `8080` | listen address in web mode |
| `TOLARIX_TIMEZONE` | `TZ`, else `UTC` | default timezone for schedules, shown in the UI |
| `TOLARIX_TRUSTED_PROXIES` | none | comma-separated IPs/CIDRs whose `X-Forwarded-For`/`X-Forwarded-Proto` are trusted (rate limit, Secure cookies) |
| `TOLARIX_COOKIE_SECURE` | `auto` | `auto` (Secure when the request is HTTPS), `true`, `false` |
| `TOLARIX_HEALTHCHECK_URL` | `http://127.0.0.1:$TOLARIX_PORT/healthz` | URL used by `tolarix healthcheck` |
| `TOLARIX_LOG_LEVEL` / `TOLARIX_LOG_FORMAT` | `info` / `text` | logging (stderr) |
| `TOLARIX_DRAWIO_EXPORT_URL` | none | draw.io export server used to render diagrams to PNG (see [renderers.md](renderers.md)) |
| `TOLARIX_DRAWIO_CLI` | `drawio` on PATH, or the macOS app | draw.io Desktop executable used to render diagrams |

## Day-to-day

```sh
# Go (from core/)
go test ./...
go vet ./...
golangci-lint run ./...
gofmt -l .

# Web (from web/)
pnpm dev                # Vite on :5173, proxies /api and /healthz to :8080 (override: TOLARIX_DEV_BACKEND)
pnpm test               # Vitest
pnpm test:e2e           # Playwright: builds ./core into a temp dir and runs it in web mode (run `pnpm build` first)
pnpm lint && pnpm typecheck
pnpm format             # Prettier

# Both (from the root), after editing core/internal/api/openapi.yaml
pnpm generate           # Go server stubs (oapi-codegen) + web/src/api/schema.gen.ts; CI fails if they are stale

# Desktop (from desktop/), after `pnpm --filter web build` (the sidecar embeds the UI)
pnpm dev                # tauri dev: builds the sidecar for this machine, opens the app
pnpm build              # installers for this machine (unsigned); `pnpm tauri build --debug --bundles app` is quicker
pnpm sidecar            # only (re)build src-tauri/binaries/tolarix-<target triple>
pnpm icons              # regenerate src-tauri/icons from icons/icon.svg
# Rust checks (from desktop/src-tauri/)
cargo fmt --check && cargo clippy --all-targets -- -D warnings && cargo test
```

Run `tolarix serve` (web mode) in a second terminal while using `pnpm dev`, and sign in once:
the session lasts 7 days.

## API

- The contract is `core/internal/api/openapi.yaml` (OpenAPI 3). Change it first, run
  `pnpm generate`, then implement the handler (`internal/api/oapi` holds the generated
  strict-server interface) and use the typed client (`web/src/api/client.ts`, openapi-fetch).
- Errors are `{"error": {"code", "message", "fields"}}`. `code` is stable (the UI maps known codes to
  translated text in `web/src/lib/errors.ts`); `fields` holds per-field validation messages
  (English for now).
- Auth (web mode): `POST /api/v1/auth/login` sets an HttpOnly session cookie;
  `GET /api/v1/auth/session` returns the CSRF token, which every POST/PUT/DELETE sends as
  `X-CSRF-Token`. Sessions expire after 7 days idle or 30 days. Logins are rate-limited per client IP
  (10 failures per 15 minutes). While the one-time password is unchanged, only the session,
  password and logout endpoints work (`403 password_change_required`).
- Live logs: `GET /api/v1/runs/{id}/stream` is Server-Sent Events (`log` events with ids, then one
  `end` event). It replays stored lines first and resumes from `Last-Event-ID`.
- `GET /healthz` is unauthenticated (database check + version); `tolarix healthcheck` calls it.

## Layout

```
core/                     Go module github.com/davidemaggi/tolarix/core
  cmd/tolarix/            main: the single binary (server + scheduler + CLI)
  internal/api/           HTTP: /healthz, /api/v1 handlers, SSE logs, desktop token, security headers
    openapi.yaml          the API contract; oapi/ is generated from it (`pnpm generate`)
  internal/auth/          admin password (argon2id), sessions, CSRF, login rate limit, trusted proxies
  internal/buildinfo/     version injected via -ldflags (see scripts/build-core.mjs)
  internal/cli/           cobra commands; exit codes are in root.go and are a public contract
  internal/connectors/    the Source/Target/Session contract + registry
    obsidian/             Obsidian vault connector (decode, encode, write session) + golden tests
    confluence/           Confluence Data Center / Server (REST v1) and Cloud (REST v2) + in-memory API fakes
    azuredevops/          Azure DevOps Code and Project wikis over the Git REST API + in-memory API fake
    all/                  blank-imports every connector (import this to register them)
  internal/convert/mdscan/ Markdown scanning that never touches code blocks/spans/comments
  internal/convert/storage/ Confluence storage format <-> Markdown (shared by DC and Cloud)
  internal/convert/frontmatter/ YAML front matter <-> page meta
  internal/datadir/       data dir resolution (--data-dir > TOLARIX_DATA_DIR > OS default)
  internal/engine/        plan + apply: scope, move detection, delete policies, dry-run
    enginetest/           MemStore, an in-memory mapping store for connector end-to-end tests
  internal/httpc/         shared HTTP client: retries with backoff, rate-limit headers, API errors
  internal/logging/       slog setup; logs go to stderr only
  internal/model/         canonical page tree, hashes, tolarix:// link helpers
  internal/render/        renderer pipeline (derived attachments, e.g. draw.io -> PNG)
    drawio/               draw.io -> PNG: preview, cache, export server, draw.io Desktop
  internal/runner/        runs a stored flow: lock + heartbeat, run row, live events, report
  internal/schedule/      schedule model: validation, cron mapping, en/it sentence, next runs
  internal/scheduler/     when flows are due (anchor + claim), built-in scheduler, shared with run-due
  internal/secrets/       AES-GCM cipher, key loading, secrets.String (redacts itself)
  internal/store/         SQLite open, goose migrations, repositories (connections, flows, mappings, runs, locks)
  internal/testkit/       golden-file helpers and the YAML fixture form of a canonical tree
  internal/webui/         embedded SPA (dist/ is generated; only .gitkeep is tracked)
web/                      React + Vite + TS + Tailwind v4 + shadcn/ui
  src/api/                typed client (schema.gen.ts is generated), TanStack Query hooks, SSE hook
  src/pages/              one file per route (lazy-loaded except the dashboard)
  src/components/ui/      shadcn primitives (add more with `pnpm dlx shadcn@latest add <name>`)
  src/i18n/locales/       en.json, it.json — a test fails if their keys differ
  src/test/               test helpers: renderApp, a fake API (mockApi) and a fake EventSource
  e2e/                    Playwright tests against the real binary
  e2e/a11y.ts             axe checks (WCAG 2.2 AA) called at every step of the e2e flow
desktop/                  Tauri v2 shell: starts the tolarix sidecar and shows its UI
  splash/                 static page shown while the server starts (and its startup errors)
  scripts/sidecar.mjs     builds the sidecar into src-tauri/binaries/tolarix-<triple> (Tauri externalBin)
  src-tauri/src/          lib.rs (window, navigation, single instance), sidecar.rs (start/stop, ready line, token)
deploy/                   Dockerfile, docker-compose.yml
scripts/build-core.mjs    cross-platform go build with version ldflags (TOLARIX_OUT overrides the output)
scripts/package-cli.mjs   release archives for 6 platforms + SHA256SUMS into ./dist (used by release.yml)
.github/workflows/        ci.yml (PRs and main), desktop.yml (installers), release.yml (tags v*)
docs/                     user guide (en, it/), admin guide, scheduling guide
docs/dev/                 this guide, plan + status, design notes
```

## How a run works

```
runner.RunFlow ── lock (flow_locks + heartbeat) ── runs row ── run_events (live log)
   └─ engine.Run
        Source.Snapshot ─▶ canonical tree ─▶ ApplyScope ─▶ render.Pipeline ─▶ banner
        for each mirror:
          Session.Layout ─▶ Session.Encode (every page) ─▶ Session.Inventory + mappings
          plan: orphans (delete/archive/keep) → pages in tree order (create/update/move/unchanged/conflict)
          apply (unless dry-run): uploads → page ops (mapping saved after each) → attachment deletes → Finish
          (sessions that buffer writes, like Azure DevOps, get their mappings saved only after Finish succeeds)
```

Canonical Markdown conventions (links `tolarix://page/<id>`, `tolarix://att/<sha256>`,
callouts, Mermaid) are documented at the top of `internal/model/model.go`.

## Adding a connector

1. Create `core/internal/connectors/<kind>/` and call `connectors.Register` in `init()` with the config fields.
2. Implement `connectors.Source` (snapshot → canonical tree, open attachment, browse) and/or
   `connectors.Target` (open a `Session`: layout, encode, inventory, write/delete/archive, attachments, finish).
3. Add a blank import to `internal/connectors/all/all.go`.
4. Golden tests per direction, like `obsidian/obsidian_test.go`: `testdata/decode/<case>/` (native input →
   `expected.yaml` + `warnings.txt`) and `testdata/encode/<case>/` (`input.yaml` → expected native output).
   Every degradation must emit a `connectors.Warning`, so it shows up in `warnings.txt`.
5. HTTP connectors: an in-memory fake of the API on `httptest` (see `confluence/fake_test.go`,
   `azuredevops/fake_test.go`) plus an end-to-end test through the real engine. Never commit real credentials.
6. Add `connectorKinds.<kind>` and `connectorFields.<kind>.*` to `web/src/i18n/locales/{en,it}.json`.
7. Document the mapping in [connectors.md](connectors.md).

The engine needs no change. Its tests use in-memory fakes (`internal/engine/fakes_test.go`).

## Tests

| What | Where | Run |
|---|---|---|
| Go unit + connector golden tests + engine end-to-end with fakes | `core/**/*_test.go` | `cd core && go test ./...` |
| Web components and pages (Vitest, jsdom, fake API) | `web/src/**/*.test.tsx` | `cd web && pnpm test` |
| Browser end to end + accessibility (Playwright + axe, real binary) | `web/e2e/` | `pnpm --filter web build && node scripts/build-core.mjs && cd web && TOLARIX_BIN=$PWD/../bin/tolarix pnpm test:e2e` |
| Desktop shell (sidecar start/stop, URL rules) | `desktop/src-tauri/src/*.rs` | `cd desktop/src-tauri && cargo test` |

Before asking for review, the full check is:

```sh
cd core && gofmt -l . && go vet ./... && golangci-lint run ./... && go test ./...
cd web && pnpm lint && pnpm typecheck && pnpm test && pnpm build
cd desktop/src-tauri && cargo fmt --check && cargo clippy --all-targets --locked -- -D warnings && cargo test --locked
```

The e2e test calls `expectAccessible(page, "<where>")` after each screen. A new
screen or dialog should get a call too; fix violations instead of disabling rules.

CI (`ci.yml`) also runs `pnpm audit --prod` and `govulncheck` on the built binary
(job `security`).

## Golden files

```sh
cd core && go test ./... -update   # rewrite golden files, then review the diff with git before committing
```

## Releasing

1. On `develop`: update `CHANGELOG.md` (a `## [X.Y.Z] - date` section) and the
   version in `desktop/src-tauri/Cargo.toml` (then `cargo update -p tolarix-desktop`
   in `desktop/src-tauri` to refresh `Cargo.lock`). Run the full check.
2. Merge `develop` into `main`, then tag on `main`: `git tag -a vX.Y.Z -m "Tolarix X.Y.Z"`
   and `git push origin vX.Y.Z`.
3. `release.yml` checks that the tag, `Cargo.toml` and the changelog agree, creates a
   **draft** release with the changelog section as notes, and fills it: CLI archives
   + `SHA256SUMS` (`scripts/package-cli.mjs`), the multi-arch image on
   `ghcr.io/davidemaggi/tolarix` (`X.Y.Z`, `X.Y`, `X`, `latest`) and the desktop
   installers (`desktop.yml`, called with the draft's id).
4. Check the draft (download an archive, `docker run ghcr.io/davidemaggi/tolarix:X.Y.Z version`)
   and publish it. The image is pushed when the job runs, before the draft is published.
5. The first time, make the ghcr package public (GitHub → Packages → tolarix →
   Package settings → Change visibility).

Try the packaging locally with `pnpm --filter web build && TOLARIX_VERSION=0.0.0-test node scripts/package-cli.mjs`
(`dist/` is git-ignored) and the image with
`docker buildx build -f deploy/Dockerfile --platform linux/amd64,linux/arm64 .`.

## Conventions

- Every user-facing string in the web app goes through i18next (`en.json` + `it.json`). The CLI is English only.
- Secrets are always `secrets.String`; never log or return plaintext.
- New DB changes: add `core/internal/store/migrations/NNNNN_name.sql` with `-- +goose Up` / `-- +goose Down`. Never edit an applied migration.
- Line endings are LF everywhere (`.gitattributes`); golden-file tests compare bytes.
- `develop` is the integration branch; `main` gets releases. Work milestone by milestone on `develop` (or a `mN-<name>` branch merged into it); at the end update [plan.md](plan.md) (status + decisions) before asking for review.

## Gotchas found so far

- **TypeScript is pinned to 6.x**: typescript-eslint does not support TS 7 yet.
- The first `go test` on a new machine is slow (minutes): `modernc.org/sqlite` is a large package to compile. Later runs are cached.
- No inline `<script>` in `web/index.html`: the server's Content-Security-Policy blocks it.
- Editing tools on Windows may turn `"﻿"` in Go source into a literal BOM, which Go rejects. Write `string(rune(0xFEFF))` instead.
- Web tests run in jsdom, which lacks `ResizeObserver`, pointer capture and `scrollIntoView`; Radix needs them,
  so `src/test/setup.ts` stubs them. Node's `Request` rejects relative URLs, which is why the API client's
  `baseUrl` is made absolute from `window.location.origin`.
- Playwright's `getByLabel`/`getByRole` match substrings by default ("New password" also matches "Repeat the new
  password"): pass `{ exact: true }`.
- The desktop app's server writes its log to the OS app log folder (`~/Library/Logs/io.github.davidemaggi.tolarix/server.log`
  on macOS, `%LOCALAPPDATA%\io.github.davidemaggi.tolarix\logs` on Windows, `~/.local/share/io.github.davidemaggi.tolarix/logs` on Linux).
- `tauri-build` fails if the sidecar binary is missing: run `pnpm sidecar` (or, for `cargo` checks only, create an empty
  `src-tauri/binaries/tolarix-<host triple>` like CI does).
- Running the desktop app from a debug bundle uses your real data dir and keychain; set `TOLARIX_DATA_DIR` to a temp folder
  to try it, and delete the keychain entry afterwards (`security delete-generic-password -s Tolarix -a secret-key:<dir>` on macOS).
- `govulncheck ./...` (source mode) fails on Go 1.27 code because its type checker does not support 1.27 yet.
  CI scans the built binary instead (`govulncheck -mode=binary bin/tolarix`), which needs no type checking.
- axe's `label-content-name-mismatch`: a control's accessible name must contain its visible text. Do not put
  an `aria-label` on a button that shows different text; use visible text plus `sr-only` spans
  (see the weekday toggles in `schedule-editor.tsx` and `language-switcher.tsx`).
- Button and badge hovers darken (`hover:brightness-90`) instead of the shadcn default `hover:bg-primary/90`:
  the translucent variant lightens the teal to 4.28:1 against white text, and axe flags it whenever the
  mouse is still over a button after a click.
- Radix selects leave the page `aria-hidden` while their list is open; in e2e tests, wait for the listbox
  to close (`expect(page.getByRole("listbox")).toHaveCount(0)`) before running axe.
- File names with spaces in fixtures: quote them in shell loops (`find -print0 | while read -d ''`), and never `cat` an unquoted name that might be `-`.
