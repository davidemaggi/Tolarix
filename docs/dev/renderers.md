# Renderers (derived attachments) — design

Status: designed 2026-11-25; interface lands in M2, the draw.io renderer in M5.

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
3. **draw.io Desktop CLI** (optional, desktop installs): if `drawio` is on PATH,
   run `drawio --export --format png`.

If none is available, the mirror gets the `.drawio` attachment plus a warning
that explains how to enable 2 or 3.

## Per-target output

| Target | Output |
|---|---|
| Obsidian | `![[Architecture.png]]` followed by a link `[[Architecture.drawio]]`; both files in the attachment folder |
| Azure DevOps | `[![Architecture](/.attachments/Architecture-<id>.png)](/.attachments/Architecture-<id>.drawio)` |
| Confluence | If the target has the draw.io app (connection option): the `drawio` macro + both attachments. Otherwise an image macro + a link to the `.drawio` attachment |

## To verify with real data (M5)

- Exact storage-format XML of the draw.io macro on **Cloud** vs **Data Center**
  (macro name, parameters such as `diagramName`, `revision`, `pageId`; embedded
  `inc-drawio` variant), and the attachment names and media types. Capture
  anonymised fixtures from a real instance before writing the decoder.
- Whether a stale preview can exist (diagram saved without regenerating the PNG).
