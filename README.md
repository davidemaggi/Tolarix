# Tolarix

Keep wikis in sync. One wiki is the **Master**; Tolarix creates and updates one
or more read-only **Mirrors**, possibly in a different wiki format.

Supported (planned for v1): Azure DevOps project and code wikis, Confluence
Cloud and Data Center, Obsidian vaults.

Tolarix ships as one binary that is at once the web server, the scheduler and
the CLI, plus a Docker image and a desktop app.

> **Status:** early development (milestone 1 of 8). Not usable yet. See
> [docs/dev/plan.md](docs/dev/plan.md).

## Build from source

```sh
pnpm install
pnpm build
./bin/tolarix serve --data-dir ./data
```

Developer setup: [docs/dev/README.md](docs/dev/README.md).

## License

GPL-3.0. See [LICENSE](LICENSE).
