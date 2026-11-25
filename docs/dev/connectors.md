# Connectors: how each wiki maps to the canonical model

Every connector converts between its wiki and the canonical tree (`internal/model`):
pages with a Markdown body, `tolarix://page/<id>` and `tolarix://att/<sha256>` links,
`> [!kind]` callouts, Mermaid fences and `<!-- tolarix:toc -->`. Anything without an
equivalent degrades visibly and emits a `connectors.Warning` (shown in the run report).

The shared HTTP client is `internal/httpc`. Every request is retried on 429 and 503;
GET, HEAD, DELETE and OPTIONS also on 500, 502, 504 and network errors. The backoff honours
`Retry-After` and the rate-limit headers. POST and PUT are never retried on other errors. An HTML answer, which is how Azure
DevOps answers a bad token (203 + sign-in page), is reported as 401.

## Obsidian (`obsidian`)

See the decisions log in [plan.md](plan.md) ("Taken during M2").

## Confluence Data Center / Server (`confluence-dc`)

Code: `internal/connectors/confluence` (REST API v1) and `internal/convert/storage`
(storage format ⇄ Markdown, shared with Confluence Cloud in M5).

**Connection.** `url` is the address of a space (`/display/KEY`, `/spaces/viewspace.action?key=`)
or of a page (`/display/KEY/Title`, `/pages/viewpage.action?pageId=` or `?spaceKey=&title=`).
A page address mirrors that page and everything below it. A context path (`/confluence`)
is kept. `token` is a personal access token (Bearer). With `username` set, it is that
user's password instead (Basic).

**As a Master.** Page ids are the Confluence ids. Sibling order is the order Confluence lists children in. Attachments are downloaded once per run and keyed by content hash.
Links to pages outside the space or subtree become plain text with a warning.

**As a Mirror.** Location `parent` is the title or id of the page that mirrors go under.
Empty means the connection's page, or the top of the space.
- Titles are unique per space. A duplicate gets its parent's title in brackets
  (`Overview (Backend)`), then a number, with a `title-renamed` warning. A page anywhere
  in the space that Tolarix does not own blocks a title, and that page fails as a conflict
  (or is adopted with "adopt existing pages").
- The first line of the encoded content names the parent page, so a move changes the hash
  and is written. It is stripped before the page is saved.
- Archive: the page moves under `Archived – <parent title>` (`Archived – Tolarix` at the top of a
  space) and is renamed `<title> (archived)`.
- Attachments are uploaded to the page that uses them; an attachment used by several pages
  is uploaded to the first one, and the others link to it there.
- Edits made in Confluence are detected through the page version stored in the mapping
  (migration `00003`), then overwritten with a `mirror-edited` warning.

**Storage format ⇄ Markdown.**

| Confluence | Canonical |
|---|---|
| headings, paragraphs, bold/italic/strike/code, lists, task lists, tables, `hr`, `br` | same |
| `code` / `noformat` macro (language mapped, e.g. `py` ⇄ `python`) | fenced code |
| `info` / `tip` / `note` / `warning` / `panel` | `[!info]` / `[!tip]` / `[!warning]` / `[!danger]` / `[!note]` (round-trips to the same macro) |
| `expand` | foldable callout `[!note]-` |
| `toc` | `<!-- tolarix:toc -->` |
| `children`, `pagetree`, `anchor` | dropped (mirrors have their own navigation) |
| `details`, `section`/`column`, `excerpt`, `div` | their content |
| `status`, `jira` (inline) | `[STATUS]`, issue key as text |
| emoticons | emoji |
| `ac:link` to a page / attachment, `ac:image` | `tolarix://page/…` / `tolarix://att/…` |
| `drawio` macro | the diagram attachment (role `diagram-source`) plus its preview PNG, see [renderers.md](renderers.md) |
| Mermaid fence (written) | `code` macro titled `mermaid` (Data Center has no built-in Mermaid) |
| any other macro | a ```` ```confluence-macro ```` block holding the raw XML, with a warning; written back verbatim to Confluence |

## Azure DevOps Code wiki (`azure-devops-code`)

Code: `internal/connectors/azuredevops`. Everything goes through the Git REST API 7.1,
so the Project wiki (M5, a hidden `<name>.wiki` repository) can reuse it.

**Connection.** `url` is a wiki address (`…/_wiki/wikis/<name>`), which is resolved through
the wiki API to its repository, mapped folder and branch, or a repository folder
(`…/_git/<repo>?path=/docs&version=GBbranch`). Supported hosts: `dev.azure.com/<org>`,
`<org>.visualstudio.com[/DefaultCollection]`, and Server (`https://host/tfs/<collection>`).
Without a branch, the repository's default branch is used (`main` for an empty
repository). A project wiki address is rejected until M5. `token` is a PAT with
Code (Read & write), sent as Basic `:token`.

**Wiki conventions.**
- A page is `Name.md`. Its subpages live in the sibling folder `Name/`. A folder with no
  `.md` file is a title-only page (placeholder).
- File names: a space is `-`, and `- : < > * ? | " / \ # %` are percent-encoded
  (`Q&A: why?` → `Q&A%3A-why%3F`). A leading or trailing `.` is encoded as `%2E`.
- `.order` in a folder lists its pages (names without `.md`), one per line. Pages it does
  not list follow alphabetically.
- Attachments: the Master's `/.attachments/` (or any non-Markdown file a page links to).
  Mirrors use `<target folder>/.attachments/`.
- Links are absolute from the wiki root (`/Folder/Page-Name#anchor`) or relative to the
  page. Anchors are the heading in lower case, spaces as `-`, other characters
  percent-encoded (`Install & Run` → `#install-%26-run`). The reader also accepts the
  GitHub style (punctuation dropped).

**Dialect.**

| Azure DevOps | Canonical |
|---|---|
| `::: mermaid` … `:::` | Mermaid fence |
| `[[_TOC_]]` | `<!-- tolarix:toc -->` |
| `[[_TOSP_]]` | dropped, warning `subpages-list` |
| `![alt](img.png =300x200)` | `![alt](tolarix://att/… "width=300x200")` |
| front matter | page meta (kept in mirrors) |
| (written) `> [!tip] Title` | `> **Tip:** Title` (one way: the wiki has no callouts) |

**As a Mirror.** Location `folder` is relative to the wiki root (empty = the root).
- The session reads the target folder once at the branch head, keeps every write in memory
  (attachments are spooled to a temp dir), and `Finish` pushes the difference as **one
  commit** (`refUpdates.oldObjectId` = the head it read). If someone pushed meanwhile,
  the push fails with "run the flow again" and nothing is written.
- Because nothing reaches the wiki before `Finish`, the engine holds this target's mapping
  writes until the push succeeds (`connectors.Buffering`). If the push fails, every
  operation is reported as "not saved" and the next run starts from the same state.
- `.order` files are rewritten in every folder under the target, in Master order.
  Pages Tolarix does not manage follow alphabetically. `.order` files of folders that
  became empty are removed.
- Moves are delete + add. Archiving is a git rename to `<target>/Archived/<same path>`,
  so the file history follows it.
- Branch policies that require pull requests (TF402455) make the push fail with an
  explanation: use a branch without required reviews for mirrors.
- Pushes above 64 MiB of content are split into several commits ("part i of n"). If a
  later part fails, the earlier parts stay pushed (see the follow-ups in plan.md).
- Edits made in the wiki are detected through the git blob id, then overwritten with a
  `mirror-edited` warning.

## Testing

Each HTTP connector has an in-memory fake of the API it uses (`fake_test.go`), built from
the documented request and response shapes. The fakes also enforce the rules the real
service applies, such as Confluence version numbers, title uniqueness and ADO
`oldObjectId` checks. Golden files live in `testdata/` (`go test ./... -update`, then
review the diff). End-to-end tests drive the real engine with an Obsidian vault as the
Master and `enginetest.MemStore` as the mapping store.
