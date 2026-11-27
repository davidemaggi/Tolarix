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

Code: `internal/connectors/confluence` (REST API v1, `v1.go`) and `internal/convert/storage`
(storage format ⇄ Markdown, shared with Confluence Cloud). Source and target code is shared
with Cloud; each REST call goes through the `api` interface (`api.go`).

**Connection.** `url` is the address of a space (`/display/KEY`, `/spaces/viewspace.action?key=`)
or of a page (`/display/KEY/Title`, `/pages/viewpage.action?pageId=` or `?spaceKey=&title=`).
A page address mirrors that page and everything below it. A context path (`/confluence`)
is kept. `token` is a personal access token (Bearer). With `username` set, it is that
user's password instead (Basic). Short links (`/x/<code>`) are decoded to the page id
(the code is the id as little-endian bytes in base64, `-`/`_` for `/`/`+`, trailing `A`s
and padding cut). The bool fields `properties` and `pageDetails` are read only when the
space is a Master, see below.

**As a Master.** Page ids are the Confluence ids. Sibling order is the order Confluence lists children in. Attachments are downloaded once per run and keyed by content hash.
Links to pages outside the space or subtree become plain text with a warning.
- Mentions (`ri:user`) and the `profile` macro become `@Display Name`; names come from
  `GET /rest/api/user?accountId=|key=|username=` (v1, also on Cloud), cached per run. A
  failed lookup keeps the id.
- Links to short links (`/x/<code>`) of pages in the snapshot become page links.
- Front matter (`meta.go`, one extra request per page, 8 in parallel):
  - `properties`: labels (`global` prefix only) as `tags`, then the rows of every
    `details` (Page Properties) table, hidden ones included (`storage.PageProperties`):
    key/value rows, or a header row over one value row. Values are plain text, lists
    become lists, users names. Keys are kept once, case-insensitively; keys that clash
    with `tags` or the page-details keys are skipped.
  - `pageDetails`: `confluence_id`, `confluence_url`, `author`, `created`, `updated`,
    `updated_by`, `version`. v1: `content/{id}?expand=history,version,metadata.labels`;
    v2: `pages/{id}?include-labels=true&include-version=true`, users by account id.
  - A page whose details cannot be read gets `page-info-unreadable` and no front matter.
  - The same details are also read, with both options off, when any page body holds a
    `detailssummary` or `contentbylabel` macro: they need every page's labels. The front
    matter still follows the options.

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
| `details`, `section`/`column`, `div` | their content (a `details` macro with `hidden=true` is dropped) |
| `excerpt` | its content between `<!-- tolarix:excerpt -->` and `<!-- tolarix:excerpt-end -->` (round-trips) |
| `include` / `excerpt-include` | `![Title](tolarix://page/<id>)` (title `excerpt` for an excerpt); outside the Master: text with `link-outside-master` |
| `plantuml`, `plantumlcloud` | ```` ```plantuml ```` (plain, JSON `umlDefinition`, or base64 + raw deflate, capped at 4 MiB); written back as a `code` macro titled `plantuml` |
| `gliffy` | its `<name>.png` attachment (`diagram-missing` when absent) |
| `viewpdf`, `view-file`, `viewfile`, `viewdoc`, `viewxls`, `viewppt`, `multimedia` | a link to the attachment |
| `profile`, `ri:user` | `@Display Name` |
| `sub`, `sup` | `<sub>`, `<sup>` (round-trip) |
| image caption (`ac:caption`) | `*caption*` on the line under the image (not written back) |
| table with `colspan`/`rowspan`, or block content in a cell | an HTML `<table>` block on consecutive lines, cells rendered to one-line HTML (`table-html`); read back into a storage table |
| `status`, `jira` (inline) | `[STATUS]`, issue key as text |
| emoticons | emoji |
| `ac:link` to a page / attachment, `ac:image` | `tolarix://page/…` / `tolarix://att/…` |
| `drawio` macro | the diagram attachment (role `diagram-source`) plus its preview PNG, see [renderers.md](renderers.md) |
| Mermaid fence (written) | `code` macro titled `mermaid` (Data Center has no built-in Mermaid) |
| `detailssummary` (Page Properties Report) | the table Confluence shows: pages of the Master with the labels (parameter or CQL `label =`/`label in`), `firstcolumn`, `headings`, `sortBy`, `reverseSort`; wrapped in live-macro markers (below) |
| `contentbylabel` | a list of page links (`operator`, `excludeCurrent`, `sort`, `reverse`, `max` default 5); wrapped in live-macro markers |
| live-only macros (`livesearch`, `popular-labels`, `recently-updated`, `recently-updated-dashboard`, `change-history`, `pagetreesearch`, `create-from-template`, `listlabels`) | nothing, no warning; the markers alone |
| any other macro | a ```` ```confluence-macro ```` block holding the raw XML, with a warning; written back verbatim to Confluence |

Live-macro markers (`model.MacroMarker`): `<!-- tolarix:macro <base64 storage XML> -->`,
the rendered content, `<!-- tolarix:macro-end -->`. Other targets drop the comments;
the Confluence encoder writes the macro back and skips the content up to the end marker.
Labels come from the optional `storage.LabelIndex` Env extension; without it the macro
renders as markers only.

## Confluence Cloud (`confluence-cloud`)

Code: the same package, REST API v2 in `v2.go`. Everything from the Data Center section
applies, with these differences.

**Connection.** `url` is the address of a space or a page on `<site>.atlassian.net`
(`/wiki/spaces/KEY/overview`, `/wiki/spaces/KEY/pages/ID/Title`); `/wiki` is added when
missing. `email` + `token` are an Atlassian account and a classic API token
(id.atlassian.com → Security → API tokens), sent as Basic. Scoped tokens are not supported.

**API.** REST v2 (`/wiki/api/v2`, cursor pagination, `_links.next`). Attachment upload and
download stay on the v1 endpoints, which v2 does not replace.

**As a Master.**
- The space is read with one paged listing of every current page with its body
  (`/spaces/{id}/pages?depth=all`); the tree is built from `parentId` and `position`.
  A page address mirrors that page and everything below it.
- Folders become title-only pages (placeholders). Only folders that hold pages are seen.
- Smart links (a plain link to the address of a page in the snapshot) become page links;
  when the link text was the address, the page title is used.
- ADF blocks in storage format (`ac:adf-extension`): panels become callouts (info → `[!info]`,
  note/custom → `[!note]`, success/tip → `[!tip]`, warning → `[!warning]`, error → `[!danger]`);
  anything else uses its `ac:adf-fallback`.
- Cloud anchors (`#Getting-Started`) are matched to headings like Data Center ones.
- Whiteboards, databases and embeds are skipped.

**As a Mirror.** With no parent and a space address, mirrors go under the space homepage
(where Cloud puts new pages) and the archive page is `Archived – <homepage title>`. Titles
are kept unique per space as on Data Center, although Cloud would allow duplicates.

## Azure DevOps Code wiki (`azure-devops-code`)

Code: `internal/connectors/azuredevops`. Everything goes through the Git REST API 7.1,
so the Project wiki (a hidden `<name>.wiki` repository) reuses it.

**Connection.** `url` is a wiki address (`…/_wiki/wikis/<name>`), which is resolved through
the wiki API to its repository, mapped folder and branch, or a repository folder
(`…/_git/<repo>?path=/docs&version=GBbranch`). Supported hosts: `dev.azure.com/<org>`,
`<org>.visualstudio.com[/DefaultCollection]`, and Server (`https://host/tfs/<collection>`).
Without a branch, the repository's default branch is used (`main` for an empty
repository). A project wiki address is rejected with a pointer to the Project wiki kind.
`token` is a PAT with Code (Read & write), sent as Basic `:token`.

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
- Full addresses of this same wiki, as copied from the browser (`address.go`), become
  page or attachment links too: `…/_wiki/wikis/<wiki>/<id>/<Title>` (the id is looked
  up with the wiki pages API, falling back to a unique page with that title),
  `…/_wiki/wikis/<wiki>?pagePath=/A/B` and `…/_git/<repo>?path=/<wiki folder>/A/B.md`,
  absolute or server-relative. The organisation matches whatever the host spelling
  (`dev.azure.com/org` = `org.visualstudio.com`); the wiki matches by name or id among
  the wikis published from the same repository folder. Addresses of another wiki,
  repository folder, branch or organisation are kept unchanged. An address of this
  wiki whose page does not exist gives `broken-link`.

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
Location `branch` (Code wiki only; validated by `CleanBranch` with git's ref-name rules)
pushes to another branch. When it does not exist, the session reads the connection's
branch as its base and, in `Apply` (so never in a dry-run or a run with nothing to
write), creates the branch at that commit through the refs API (`oldObjectId` zero),
then pushes on top of it. A branch created by someone else in between fails the
creation with "run the flow again". If the push fails after the creation, the branch
stays at the base commit, which the next run reads as its starting point.
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
- Azure DevOps rejects push requests over 25 MiB, and content travels base64-encoded.
  Pushes are split so each request stays under `maxPushBytes` (22 MiB, counting base64
  and JSON overhead) into several commits ("part i of n"). If a later part fails, the
  earlier parts stay pushed (see the follow-ups in plan.md). A single file too big for
  any request (about 16 MB) fails the run before anything is pushed, naming the file.
- Edits made in the wiki are detected through the git blob id, then overwritten with a
  `mirror-edited` warning.

## Azure DevOps Project wiki (`azure-devops-project`)

The same code as the Code wiki. `url` is the project wiki (`…/<project>/_wiki` or
`…/_wiki/wikis/<name>.wiki`); the wiki API gives its hidden repository. The branch is
`wikiMaster` and the wiki root is the repository root. A project without a project wiki
gets "create it in Azure DevOps (Overview → Wiki → Create project wiki)"; a code wiki
address points to the Code wiki kind. Conventions, dialect and mirror behaviour are the
Code wiki's.

## File-based mirrors (`internal/connectors/filetree`)

Shared by the ADO writer and the local folder exports.
- `Tree` is a virtual copy of the target folder: the blob id of every file at load
  (`Store.Load`), and the entries written since. `Put` with unchanged content is a
  no-op; attachments are spooled to a temp dir (`PutReader`).
- `Diff` gives deletes, then renames (a file moved unchanged), then adds/edits, sorted
  by path. `Commit` hands them to `Store.Apply` in one go.
- Hashes are git blob ids (`BlobID`) for every store, so edits made on the mirror are
  detected the same way everywhere (`mirror-edited`).
- `Session` implements `connectors.Session` + `Buffering` over a `Format`
  (`Layout`, `Encode`, `Hidden`, `Finalize`). `Finalize` runs before the commit
  (ADO `.order` files, the GitHub sidebar). `Finish(commit=false)` writes nothing.
- Stores: `gitStore` (ADO, one push) and `Dir` (local disk: atomic writes, prunes
  emptied folders but never the target folder, ignores `.git` and symbolic links).
  `InRoot` rejects any change outside the target folder.

## Local folder exports (`folder-azure-devops`, `folder-github-wiki`, `folder-markdown`)

Code: `internal/connectors/folder`. Target only (no `Source`). Config `path` is an
existing, writable folder (`Test` checks it with a temp file); location `folder` is
relative to it (no `..`). Pages deleted in the Master are archived under
`<target>/Archived/`.

- **Azure DevOps wiki**: `azuredevops.NewFormat()` on a `filetree.Dir`, so the files
  match the Code wiki mirror byte for byte (names, `.order`, `.attachments/`, dialect).
- **GitHub wiki**: GitHub serves `/wiki/<Name>` whatever the folder, so names are
  unique across the export (case-insensitive). `GitHubName`: spaces → `-`,
  `\ / : * ? " < > | # %` dropped, leading/trailing `.`/`_` trimmed, Windows device
  names get `_`, duplicates `Name-(2)` (warning `renamed`). Children go in a folder
  named after the parent; placeholders are folders. Links use the bare name
  (`[t](Getting-Started#install)`); attachments live in `<target>/_attachments/` and
  are linked by their path from the export folder (the wiki root). Sized images become
  `<img src alt width height>`. `_Sidebar.md` is written in Master order, starting
  with a marker comment; a sidebar without the marker is left alone. `_Sidebar.md`,
  `_Footer.md` and `_attachments/` are hidden from the inventory.
- **Markdown**: `MarkdownName` keeps spaces and replaces forbidden characters. A leaf
  is `Name.md`, a page with children `Name/index.md` (`index` is reserved among its
  children; gaining children is a move). Links are relative, URL-escaped, with `.md`
  and GitHub-style heading slugs. Attachments go in `<target>/assets/`. A `title`
  entry is added to the front matter when missing. No page order.

Shared conversion (`encode.go`): page links outside the scope or to folder-only pages
produce `link-out-of-scope` / `link-to-folder`, attachments not mirrored
`attachment-not-mirrored`. Bodies stay canonical GFM otherwise: Mermaid fences are
kept, and the TOC marker stays an invisible HTML comment (neither GitHub nor plain
Markdown has a TOC macro).

## Testing

Each HTTP connector has an in-memory fake of the API it uses (`fake_test.go`), built from
the documented request and response shapes. The fakes also enforce the rules the real
service applies, such as Confluence version numbers, title uniqueness and ADO
`oldObjectId` checks. Golden files live in `testdata/` (`go test ./... -update`, then
review the diff). End-to-end tests drive the real engine with an Obsidian vault as the
Master and `enginetest.MemStore` as the mapping store.
