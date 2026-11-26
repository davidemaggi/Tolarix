# Changelog

All notable changes to Tolarix are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and versions follow
[Semantic Versioning](https://semver.org/). The command-line exit codes, the
YAML import format and the REST API under `/api/v1` are the public interface.

The release workflow publishes the section matching the tag as the release
notes, so every release needs a `## [X.Y.Z]` heading here.

## [1.0.0] - 2026-11-26

First public release.

### Sync

- One-way sync from a Master wiki to any number of Mirrors, through one canonical
  model (CommonMark + GFM, stable page and attachment references).
- Connectors, each usable as Master or Mirror: Obsidian vault, Confluence Data
  Center / Server, Confluence Cloud, Azure DevOps Code wiki, Azure DevOps Project wiki.
- Incremental runs: content hashes and a mapping table mean only changed pages are
  written; renames and moves are real moves.
- Scope: the whole wiki, or selected subtrees, with exclusions.
- Delete policies: archive (default), delete or keep.
- Dry-runs that compute the exact plan without writing.
- Links, images and attachments rewritten for every Mirror. Conversions that lose
  something produce a warning in the run report.
- Pages created by others on a Mirror are never touched, unless "take over
  existing pages" is on. Edits made on a Mirror are detected, overwritten and reported.
- Optional notice ("This page is mirrored from …") on every mirrored page.
- draw.io diagrams kept as `.drawio` attachments and shown as PNG (preview, export
  server or draw.io Desktop).
- Azure DevOps mirrors are written as one Git commit per run. Code wiki mirrors can
  push to another branch, created from the wiki's branch when it does not exist.
- Local folder exports (Mirror only): write a Mirror to a folder on disk in the
  Azure DevOps wiki layout, the GitHub wiki layout (with a generated `_Sidebar.md`)
  or plain Markdown with relative links, ready to commit or publish.

### Scheduling

- Built-in scheduler with human-friendly schedules (every N minutes/hours, daily,
  weekdays, monthly, or cron), per-flow timezone, sentence and next-runs preview
  in English and Italian.
- `tolarix run-due` for cron, systemd timers, Windows Task Scheduler and Kubernetes.
  A per-flow lock and claimed schedule times prevent overlaps between all of them.

### Apps

- Web interface (English and Italian, light and dark themes, keyboard and
  screen-reader friendly, checked with axe for WCAG 2.2 AA): dashboard, connection
  and flow wizards, scope tree picker, live run logs, run history and reports.
- Docker image `ghcr.io/davidemaggi/tolarix` (linux/amd64, linux/arm64), distroless,
  non-root, with a health check.
- Desktop app for Windows, macOS and Linux (Tauri), with the secret key in the OS
  keychain. Installers are unsigned in this release.
- Command line: `flow list|run|import`, `run-due`, `runs list|show`,
  `connection list|test`, `admin reset-password`, `healthcheck`, `version`, with
  `--json` output and stable exit codes.

### Security

- Connection secrets encrypted at rest (AES-256-GCM); never logged or returned by
  the API.
- Single admin account with a one-time first password, argon2id hashing, sign-in
  rate limiting, CSRF protection, strict security headers.
- Data folder 0700, database and key files 0600.

### Documentation

- [User guide](https://github.com/davidemaggi/Tolarix/blob/main/docs/user-guide.md) (also in [Italian](https://github.com/davidemaggi/Tolarix/blob/main/docs/it/guida-utente.md)),
  [admin guide](https://github.com/davidemaggi/Tolarix/blob/main/docs/admin-guide.md), [scheduling guide](https://github.com/davidemaggi/Tolarix/blob/main/docs/scheduling.md) and
  [developer guide](https://github.com/davidemaggi/Tolarix/blob/main/docs/dev/README.md).

[1.0.0]: https://github.com/davidemaggi/Tolarix/releases/tag/v1.0.0
