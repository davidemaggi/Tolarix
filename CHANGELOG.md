# Changelog

All notable changes to Tolarix are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and versions follow
[Semantic Versioning](https://semver.org/). The command-line exit codes, the
YAML import format and the REST API under `/api/v1` are the public interface.

The release workflow publishes the section matching the tag as the release
notes, so every release needs a `## [X.Y.Z]` heading here.

## [Unreleased]

### Added

- Confluence: short links (`/x/…`) work as a connection address and as links to pages of the Master.
- Confluence: mentions and *profile* macros show the person's name.
- Confluence: PlantUML (also PlantUML Cloud), Gliffy previews, *View file* / PDF macros, *Include Page* and *Excerpt Include*, subscript and superscript are converted.
- Confluence: optional front matter with labels (`tags`) and Page Properties, and with page details (id, address, author, dates, version).
- Tables with merged cells or lists, code or images in their cells are kept as HTML tables (warning `table-html`) instead of being simplified; they are read back into Confluence tables.
- Image captions are kept as an italic line under the image.
- Obsidian embedded notes (`![[Note]]`) become *Include Page* macros in Confluence and stay embeds in Obsidian Mirrors; other Mirrors show a link (warning `page-embed`).
- Confluence: *Page Properties Report* becomes a table and *Content by Label* a list of links to pages of the Master; Confluence Mirrors get the macros back.
- Run warnings show the page title, and a warning repeated on one page is shown once with a count.

### Changed

- Confluence Masters: mirrored pages that mention people, use captions or complex tables change once on the next run.
- The `table-simplified` and `note-embed` warnings are gone (see `table-html` and `page-embed`).
- Confluence: macros that only work live (*Live Search*, *Recently Updated*, *Popular Labels*, *Change History*, …) are hidden without a warning instead of becoming `confluence-macro` blocks.
- Rendered draw.io diagrams showed as broken images on every Mirror except Confluence: the image inside the link to the `.drawio` file kept its internal address. Links nested in link text are now converted too.
- Azure DevOps Mirrors link images and attachments relative to the page, so they show in Code wikis published from a folder of the repository (they were broken there). Every page with an attachment changes once.
- Confluence: an attachment download answered with a web page (login or single sign-on) fails with `attachment-failed` instead of being mirrored as a broken file.
- The run log says how many diagrams were found and rendered.
- draw.io Desktop is found where its installers put it (Windows, `~/Applications` on macOS, snap/flatpak on Linux), not only on PATH; on a Linux machine without a display it runs through `xvfb-run`.
- Tabs and line breaks in page titles become a space (Azure DevOps: `-`) instead of disappearing; affected Azure DevOps pages move once, and Obsidian file names no longer contain tabs.

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
- Links written as the full Azure DevOps address of a page or file of the Master wiki
  (copied from the browser) are rewritten to point at the page in every Mirror; links
  to anything else are kept as they are.
- Large Azure DevOps mirror runs are split into pushes that stay under Azure DevOps'
  25 MiB request limit; a single file too big to push is reported by name.
- An illustrated manual in English and Italian (`docs/manual.md`, `docs/it/manuale.md`)
  and screenshots in the README, regenerated with `pnpm screenshots`.
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
