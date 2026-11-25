# Tolarix — notes for Claude Code

One-way wiki sync (Master → Mirrors) across Azure DevOps, Confluence and Obsidian.
One Go binary (server + scheduler + CLI) with an embedded React UI, a Tauri desktop shell, and Docker.

Before working, read:
1. `docs/dev/plan.md`: milestone status, confirmed decisions, defaults. **This is the project's memory.** Update it at the end of each milestone and whenever a decision changes.
2. `docs/dev/README.md`: setup, commands, layout, conventions, gotchas.
3. `docs/dev/brief.md`: the original product brief (scope reference).
4. Design notes in `docs/dev/*.md` (e.g. `renderers.md` for draw.io → PNG).

Working agreement with the product owner:
- Work milestone by milestone on `develop` (integration branch; `main` is for releases), with tests. **Stop for review at the end of each milestone** and do not start the next one without approval.
- Do not push or open PRs unless asked.
- The product owner writes in Italian or English; code, comments, docs and the CLI are in English. The UI ships in English and Italian.

Verify before claiming done:
- `cd core && gofmt -l . && go vet ./... && golangci-lint run ./... && go test ./...`
- `cd web && pnpm lint && pnpm typecheck && pnpm test && pnpm build`
