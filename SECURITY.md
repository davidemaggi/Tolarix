# Security policy

## Supported versions

Security fixes go into the latest release. Tolarix follows semantic versioning,
so upgrading within a major version is safe.

## Reporting a vulnerability

Please do not open a public issue. Use GitHub's private reporting instead:
**Security → Report a vulnerability** on this repository. Include the version
(`tolarix version`), how Tolarix runs (Docker, desktop, CLI) and the steps to
reproduce.

You will get an answer within a week. Once a fix is released, the advisory is
published with credit to the reporter, unless they prefer otherwise.

## Scope

In scope: the `tolarix` binary (server, API, CLI), the web interface, the Docker
image and the desktop app. Hardening advice for deployments is in the
[admin guide](docs/admin-guide.md#security-notes).
