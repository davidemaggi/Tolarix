# Renderers (derived attachments) — design

Status: interface in M2; the draw.io renderer (`internal/render/drawio`) landed in M4. See "Implementation" at the end for what differs from the first design.

## Problem

Some attachments are source files that a mirror cannot display. The first case:
Confluence pages with **draw.io** diagrams. In the mirror we want both:

- the original `.drawio` file, kept as an attachment (so it can still be edited), and
- a **PNG** shown inline in the page.

## Concept

A **renderer** turns a source attachment into one or more *derived* attachments.
Renderers run in the sync engine after the Master snapshot and before the
mirror encoders, so every connector benefits (a `.drawio` file in an Obsidian
vault or an ADO wiki gets a PNG too).

```go
// internal/render
type Renderer interface {
    Name() string                                   // "drawio"
    Match(a model.Attachment) bool                  // is this a source I handle?
    Render(ctx context.Context, in Input) ([]model.Attachment, error)
}

type Input struct {
    Source   model.Attachment        // e.g. the .drawio file
    Open     func() (io.ReadCloser, error)
    Siblings []model.Attachment      // other attachments of the page (to find an existing preview)
}
```

Model additions (M2):

- `Attachment.Role`: `""` (normal) | `"diagram-source"` | `"rendered"`.
- `Attachment.DerivedFrom`: content hash of the source it was rendered from.
- Canonical Markdown for a diagram: the image links to its source, so it
  degrades to something sensible in any target:
  `[![Architecture](tolarix://att/<png-hash>)](tolarix://att/<drawio-hash>)`

Results are cached by `(renderer, source hash, renderer version)` in the data
directory, so an unchanged diagram is never rendered twice. A failed render is
a **warning** in the run report; the page still syncs with the `.drawio` only.

## draw.io renderer: where the PNG comes from

Tried in this order; the first that works wins:

1. **Existing preview from the source.** The Confluence draw.io app stores the
   diagram as an attachment (`<diagramName>`, sometimes without extension) and a
   PNG preview next to it (`<diagramName>.png`), referenced by the `drawio`
   macro's `diagramName` parameter. Tolarix reuses that PNG: no rendering, no
   extra dependency. Same for `.drawio.png` / `.drawio.svg` files, which are
   images with the diagram embedded.
2. **Export server** (optional, configurable URL): the official
   `jgraph/export-server` image can run as a sidecar in `docker-compose`; Tolarix
   POSTs the XML and gets a PNG back.
3. **draw.io Desktop CLI** (optional, desktop installs): `drawio` on PATH or the
   installers' default folders, run as `drawio --export --format png`.

If none is available, the mirror gets the `.drawio` attachment plus a warning
that explains how to enable 2 or 3.

## Per-target output

| Target | Output |
|---|---|
| Obsidian | `![[Architecture.png]]` followed by a link `[[Architecture.drawio]]`; both files in the attachment folder |
| Azure DevOps | `[![Architecture](/.attachments/Architecture-<id>.png)](/.attachments/Architecture-<id>.drawio)` |
| Confluence | If the target has the draw.io app (connection option): the `drawio` macro + both attachments. Otherwise an image macro + a link to the `.drawio` attachment |

## To verify with real data (M4)

- Exact storage-format XML of the draw.io macro on **Cloud** vs **Data Center**
  (macro name, parameters such as `diagramName`, `revision`, `pageId`; embedded
  `inc-drawio` variant), and the attachment names and media types. Capture
  anonymised fixtures from a real instance before writing the decoder.
- Whether a stale preview can exist (diagram saved without regenerating the PNG).

## Implementation (M4)

- `drawio.New(dataDir)` is the only renderer; `serve` and the CLI runtime pass it to every run.
- **Match**: attachments with role `diagram-source` (the Confluence `drawio` macro) or a
  `.drawio` name or media type. `.drawio.png` / `.drawio.svg` already display and are left alone.
- **Order**: (1) a sibling `<name>.png` or `<name>.drawio.png` that really is a PNG;
  (2) the cache `<dataDir>/cache/render/<xx>/<sha256(drawio|version|source hash)>.png`;
  (3) `TOLARIX_DRAWIO_EXPORT_URL`: a form POST (`format=png`, `xml=…`, `border=10`) as
  accepted by `jgraph/export-server` (see the commented service in `deploy/docker-compose.yml`);
  (4) draw.io Desktop: `TOLARIX_DRAWIO_CLI`, else `drawio` / `draw.io` on PATH, else the installers'
  folders (Windows `%ProgramFiles%`, `%ProgramFiles(x86)%`, `%LOCALAPPDATA%\Programs`; macOS
  `/Applications`, `~/Applications`; Linux `/opt/drawio`, `/snap/bin`, flatpak exports),
  run as `--export --format png --border 10 --output out.png in.drawio` (plus `--no-sandbox` on Linux).
  On Linux without `DISPLAY`/`WAYLAND_DISPLAY` it runs through `xvfb-run -a` when available; otherwise
  the error suggests installing xvfb. The "nothing configured" warning lists the folders searched.
  Every result is checked for the PNG signature. Rendered PNGs (3, 4) are cached; previews are not.
- Each step has a 60 s timeout. When nothing works, the warning names what was tried, or says
  how to enable a renderer when none is configured.
- Multi-page diagrams: only the first page is rendered.
- The PNG is named `<diagram>.png`. A preview with identical bytes has the same content hash,
  so the mirror stores one file.
- Confluence as a mirror writes an image wrapped in a link to the `.drawio` attachment. Writing
  the `drawio` macro (when the target has the app) is a follow-up.
