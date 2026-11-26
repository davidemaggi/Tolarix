# Tolarix admin guide

How to install, secure, back up and upgrade a Tolarix server. To use Tolarix
once it runs, see the [user guide](user-guide.md). For external schedulers, see
[scheduling.md](scheduling.md).

- [Ways to run Tolarix](#ways-to-run-tolarix)
- [Docker install](#docker-install)
- [First sign-in](#first-sign-in)
- [Resetting the admin password](#resetting-the-admin-password)
- [Configuration](#configuration)
- [The data folder](#the-data-folder)
- [The secret key](#the-secret-key)
- [Backup and restore](#backup-and-restore)
- [Upgrades](#upgrades)
- [HTTPS and reverse proxies](#https-and-reverse-proxies)
- [draw.io diagrams](#drawio-diagrams)
- [Without Docker](#without-docker)
- [Desktop app](#desktop-app)
- [Security notes](#security-notes)
- [Troubleshooting](#troubleshooting)

## Ways to run Tolarix

| | For | Sign-in |
|---|---|---|
| **Docker** (`ghcr.io/davidemaggi/tolarix`) | a team: one server, web interface, built-in scheduler | one `admin` account |
| **Desktop app** (Windows, macOS, Linux) | one person on their own machine | none: the app starts a private server for its window |
| **Binary** (`tolarix serve`, `tolarix run-due`) | servers without Docker, scripts, external schedulers | `admin` in web mode |

All three are the same program and use the same database format.

## Docker install

The image is multi-arch (linux/amd64, linux/arm64), distroless (no shell),
runs as uid 65532 and keeps all its state in the `/data` volume.

```yaml
# docker-compose.yml
services:
  tolarix:
    image: ghcr.io/davidemaggi/tolarix:1
    restart: unless-stopped
    ports:
      - "8080:8080"
    environment:
      TOLARIX_TIMEZONE: Europe/Rome      # default timezone for schedules
    volumes:
      - tolarix-data:/data               # database + secret key: back them up together
      - ./vaults:/vaults                 # only if you sync Obsidian vaults
volumes:
  tolarix-data:
```

```sh
docker compose up -d
docker compose logs tolarix     # shows the one-time admin password
```

Then open `http://<host>:8080`.

- Tags: `1.0.0` (exact), `1.0`, `1`, `latest`. Pin at least the major version.
- **Obsidian vaults** must be mounted into the container. In the web interface,
  use the path inside the container (`/vaults/team`). If Tolarix writes into a
  vault, the folder must be writable by uid 65532, for example
  `sudo chown -R 65532:65532 vaults/team`, or give a group write access.
- The container has a `HEALTHCHECK` (`/tolarix healthcheck`, which calls `/healthz`).
- `deploy/docker-compose.yml` in the repository is a complete example that builds
  the image locally and includes the optional draw.io export server.

## First sign-in

On its first start the server creates the `admin` account and prints a
**one-time password** once, in a box in the log (stderr):

```sh
docker compose logs tolarix
```

Sign in as `admin` with it. Tolarix then asks for a new password of at least 12
characters; nothing else works until it is changed. The one-time password is
not stored anywhere readable. If you missed it, reset it (next section).

Sessions last 7 days without activity, 30 days at most. Changing the password
signs out every other session. Failed sign-ins are limited to 10 per 15 minutes
per client address.

## Resetting the admin password

```sh
docker compose exec tolarix /tolarix admin reset-password
# or: docker exec -it <container> /tolarix admin reset-password
# without Docker: tolarix admin reset-password --data-dir /path/to/data
```

It prints a new one-time password, signs out every session, and the password
must be changed again at the next sign-in. Anyone who can run commands in the
container, or read the data folder, can do this: protect access to them.

## Configuration

Everything is set through environment variables. The defaults suit Docker.

| Variable | Default | Meaning |
|---|---|---|
| `TOLARIX_DATA_DIR` | `/data` in Docker, otherwise the OS app data folder | database and secret key |
| `TOLARIX_PORT` / `TOLARIX_HOST` | `8080` / `0.0.0.0` | listen address |
| `TOLARIX_TIMEZONE` | `TZ`, else `UTC` | default timezone of new schedules |
| `TOLARIX_SECRET_KEY` | none | the secret key, base64 (see below); overrides the key file |
| `TOLARIX_TRUSTED_PROXIES` | none | comma-separated IPs/CIDRs of reverse proxies whose `X-Forwarded-For` / `X-Forwarded-Proto` are trusted |
| `TOLARIX_COOKIE_SECURE` | `auto` | `auto` (Secure cookie when the request is HTTPS), `true`, `false` |
| `TOLARIX_LOG_LEVEL` / `TOLARIX_LOG_FORMAT` | `info` / `text` | `debug`, `info`, `warn`, `error` / `text`, `json`. Logs go to stderr. |
| `TOLARIX_DRAWIO_EXPORT_URL` | none | draw.io export server (see below) |
| `TOLARIX_DRAWIO_CLI` | `drawio` on PATH | draw.io Desktop executable (not in the Docker image) |
| `TOLARIX_HEALTHCHECK_URL` | `http://127.0.0.1:$TOLARIX_PORT/healthz` | used by `tolarix healthcheck` |

## The data folder

| File | Content |
|---|---|
| `tolarix.db` (+ `-wal`, `-shm`) | SQLite database: connections (secrets encrypted), flows, sync mappings, run history, the admin account |
| `secret.key` | the key that encrypts connection secrets (unless it comes from `TOLARIX_SECRET_KEY` or the OS keychain) |
| `cache/` | rendered diagrams; safe to delete |

The folder is created with mode 0700, the database and key with 0600. Without
Docker the default folder is `%AppData%\Tolarix` on Windows,
`~/Library/Application Support/Tolarix` on macOS and `~/.config/tolarix` on
Linux; `--data-dir` or `TOLARIX_DATA_DIR` override it.

The **sync mappings** in the database are what tell Tolarix which Mirror page
belongs to which Master page. If the database is lost, Tolarix no longer
recognises the pages it created. You then need *Take over existing pages*, or a
clean Mirror, to start again. Back it up.

## The secret key

Connection secrets (tokens, passwords) are encrypted with AES-256-GCM using a
32-byte key. Tolarix looks for the key in this order:

1. `TOLARIX_SECRET_KEY` (base64 of 32 random bytes);
2. `secret.key` in the data folder;
3. the OS keychain, when the data folder holds a `secret.key.keychain` marker (desktop app);
4. none found: a new key is generated into `secret.key`.

Rules:

- **Back up the key together with the database.** A database without its key
  still works, but every saved secret is unreadable: you would have to re-enter
  every token.
- To keep the key out of the volume, generate one with
  `openssl rand -base64 32`, pass it as `TOLARIX_SECRET_KEY` from your secret
  store (Docker/Kubernetes secrets, a vault) *before the first start*, and keep
  a copy somewhere safe. To move an existing `secret.key` to the variable, set
  the variable to the file's content and then delete the file.
- Key rotation is not supported in v1. To change the key, re-enter every secret
  after switching (delete and recreate the connections, or replace each secret).
- Neither the API, the log nor `--json` output ever shows a secret.

## Backup and restore

The data folder is all there is. For a consistent copy, stop Tolarix first, or
at least make sure no run is in progress. SQLite in WAL mode may hold recent
changes in `tolarix.db-wal`, so copy the three files together.

```sh
# Docker named volume -> tolarix-backup-<date>.tar.gz in the current folder
docker compose stop tolarix
docker run --rm -v <project>_tolarix-data:/data:ro -v "$PWD":/backup alpine \
  tar czf /backup/tolarix-backup-$(date +%F).tar.gz -C /data .
docker compose start tolarix
```

Find the exact volume name with `docker volume ls`. Without Docker, stop the
service and copy the data folder.

**Restore:** stop Tolarix, empty the volume (or folder), extract the archive into
it, check that the files belong to uid 65532 in Docker, and start Tolarix again:

```sh
docker compose stop tolarix
docker run --rm -v <project>_tolarix-data:/data -v "$PWD":/backup alpine \
  sh -c 'rm -rf /data/* && tar xzf /backup/tolarix-backup-2026-11-26.tar.gz -C /data && chown -R 65532:65532 /data'
docker compose start tolarix
```

The backup contains the key that decrypts the secrets. Store it as carefully as
the secrets themselves. If the key comes from `TOLARIX_SECRET_KEY`, back that up
separately.

## Upgrades

```sh
docker compose pull && docker compose up -d
```

- Take a backup first.
- Database migrations run automatically at start. Downgrades are not supported:
  to go back, restore the backup taken before the upgrade.
- The server stops gracefully on `docker stop` (SIGTERM). Running syncs are
  cancelled and reported as stopped; the next run picks up from where the
  mappings say.
- Read the [changelog](../CHANGELOG.md) before a major version change.

Without Docker: stop the service, replace the binary, start it again.

## HTTPS and reverse proxies

Tolarix speaks plain HTTP. Put it behind a reverse proxy that terminates TLS,
and tell Tolarix which proxy to trust. The proxy's address is then used to
read the client address (for the sign-in limit) and the protocol (for Secure
cookies).

```yaml
environment:
  TOLARIX_TRUSTED_PROXIES: 172.16.0.0/12   # the proxy's address or network
  TOLARIX_COOKIE_SECURE: "true"            # optional: always mark the cookie Secure
```

Caddy:

```
wiki-sync.example.com {
  reverse_proxy tolarix:8080
}
```

nginx:

```nginx
server {
  listen 443 ssl;
  server_name wiki-sync.example.com;
  # ssl_certificate ...; ssl_certificate_key ...;

  location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    # Live run logs use Server-Sent Events: do not buffer them.
    proxy_buffering off;
    proxy_read_timeout 1h;
  }
}
```

Serve Tolarix at the root of its own host name; a sub-path (`/tolarix/`) is not
supported. Do not publish port 8080 directly when a proxy is in front: bind it
to `127.0.0.1:8080:8080` or use a Docker network only.

## draw.io diagrams

Mirrors show draw.io diagrams as PNG images (and keep the `.drawio` file). Tolarix
uses, in order:

1. a PNG preview saved next to the diagram in the Master (draw.io and the
   Confluence app usually save one);
2. its render cache;
3. an export server: run `jgraph/export-server` next to Tolarix and set
   `TOLARIX_DRAWIO_EXPORT_URL=http://drawio-export:8000/`. Do not publish its port;
4. draw.io Desktop on the same machine (`TOLARIX_DRAWIO_CLI`; not available in the Docker image).

With none of them, the Mirror links to the `.drawio` file and the run report
explains how to enable rendering.

## Without Docker

Download the archive for your platform from the
[releases page](https://github.com/davidemaggi/Tolarix/releases), check it
against `SHA256SUMS`, and extract `tolarix`. A minimal systemd service:

```ini
# /etc/systemd/system/tolarix.service
[Unit]
Description=Tolarix
After=network-online.target

[Service]
User=tolarix
Environment=TOLARIX_DATA_DIR=/var/lib/tolarix
Environment=TOLARIX_HOST=127.0.0.1
ExecStart=/usr/local/bin/tolarix serve
Restart=on-failure
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/var/lib/tolarix

[Install]
WantedBy=multi-user.target
```

```sh
sudo useradd --system --home /var/lib/tolarix --create-home tolarix
sudo systemctl enable --now tolarix
journalctl -u tolarix     # one-time password
```

Add the vault folders Tolarix writes to to `ReadWritePaths`.

## Desktop app

Installers for Windows (`.msi`, `.exe`), macOS (`.dmg`, Apple silicon and Intel)
and Linux (`.deb`, `.rpm`, `.AppImage`) are on the releases page.

- They are **not signed** in v1. On macOS, open the app with right-click → **Open**
  the first time (or run `xattr -dr com.apple.quarantine /Applications/Tolarix.app`).
  On Windows, choose **More info → Run anyway** in SmartScreen.
- The app starts a private server on `127.0.0.1` with a random port and token;
  it has no sign-in and nothing listens on the network.
- It shares the data folder with the `tolarix` command on the same machine, and
  stores a new secret key in the OS keychain (Keychain, Credential Manager, Secret
  Service). Without a keychain it falls back to `secret.key`.
- Its scheduler runs only while the app is open. Closing the window quits it.
- The server log is in `~/Library/Logs/io.github.davidemaggi.tolarix/` (macOS),
  `%LOCALAPPDATA%\io.github.davidemaggi.tolarix\logs` (Windows) or
  `~/.local/share/io.github.davidemaggi.tolarix/logs` (Linux).
- Desktop and Docker on the same machine do not share data.

## Security notes

- One `admin` account; there are no roles. Everyone who signs in can see and
  change every connection and flow, but never read a stored secret.
- Give Tolarix the narrowest tokens that work: read-only on Masters, write only
  on the spaces or wikis it mirrors into. Use a dedicated service account where
  you can, so changes made by Tolarix are easy to recognise.
- The web interface sends a strict Content-Security-Policy, `X-Frame-Options:
  DENY`, `Referrer-Policy: no-referrer` and similar headers. Every change
  request needs a CSRF token.
- Report vulnerabilities privately as described in [SECURITY.md](../SECURITY.md),
  not in public issues.

## Troubleshooting

| Symptom | What to check |
|---|---|
| No one-time password in the log | It is printed only on the very first start. Run `admin reset-password`. |
| `403 password_change_required` from the API | The admin still has the one-time password: sign in and change it. |
| Sign-in locked out | 10 failures per 15 minutes per address. Behind a proxy without `TOLARIX_TRUSTED_PROXIES`, every user shares the proxy's address. |
| "secret key … invalid" at start | `TOLARIX_SECRET_KEY` is not base64 of 32 bytes, or `secret.key` was edited. Restore it from the backup. |
| Saved secrets fail after a restore | The database was restored without its `secret.key` (or with another key). |
| Obsidian connection: permission denied | Docker runs as uid 65532: make the mounted vault readable (Master) or writable (Mirror). |
| Azure DevOps push rejected (TF402455) | The branch requires pull requests. Use a branch without required reviewers. |
| Live log never updates behind a proxy | The proxy buffers responses. Turn buffering off for Tolarix (`proxy_buffering off`). |
| "the flow is already running" | Another run (UI, scheduler, CLI or `run-due`) holds the lock. A crashed run's lock expires after 2 minutes. |
