# Developer guide — setting up a machine

Everything needed to pick the project up on a new PC. Status and decisions:
[plan.md](plan.md). Original scope: [brief.md](brief.md).

## Prerequisites

| Tool | Version used | Notes |
|---|---|---|
| Go | 1.27.x (see `core/go.mod`) | no CGO needed (pure-Go SQLite) |
| Node.js | 22+ | |
| pnpm | 10.x (see `packageManager` in `package.json`) | `corepack enable` installs the right one |
| golangci-lint | v2 | `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest` |
| Rust + Tauri prerequisites | stable | only from milestone 7 (desktop) |
| Docker | any recent | only from milestone 3 (image) |

## First run

```sh
git clone https://github.com/davidemaggi/Tolarix.git && cd Tolarix
pnpm install
pnpm build              # builds the web UI, copies it into core/internal/webui/dist, builds ./bin/tolarix
./bin/tolarix serve --data-dir ./data    # http://localhost:8080
```

`./data` is git-ignored. Never commit a `tolarix.db` or a `secret.key`.

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
pnpm lint && pnpm typecheck
pnpm format             # Prettier
```

Run `tolarix serve` in a second terminal while using `pnpm dev`.

## Layout

```
core/                     Go module github.com/davidemaggi/tolarix/core
  cmd/tolarix/            main: the single binary (server + scheduler + CLI)
  internal/api/           HTTP: /healthz, /api/v1, desktop token, security headers
  internal/buildinfo/     version injected via -ldflags (see scripts/build-core.mjs)
  internal/cli/           cobra commands; exit codes are in root.go and are a public contract
  internal/datadir/       data dir resolution (--data-dir > TOLARIX_DATA_DIR > OS default)
  internal/logging/       slog setup; logs go to stderr only
  internal/secrets/       AES-GCM cipher, key loading, secrets.String (redacts itself)
  internal/store/         SQLite open + goose migrations (migrations/*.sql)
  internal/webui/         embedded SPA (dist/ is generated; only .gitkeep is tracked)
web/                      React + Vite + TS + Tailwind v4 + shadcn/ui
  src/components/ui/      shadcn primitives (add more with `pnpm dlx shadcn@latest add <name>`)
  src/i18n/locales/       en.json, it.json — a test fails if their keys differ
scripts/build-core.mjs    cross-platform go build with version ldflags
docs/dev/                 this guide, plan + status, design notes
```

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
