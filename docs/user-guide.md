# Tolarix user guide

Tolarix copies one wiki, the **Master**, into one or more other wikis, the
**Mirrors**. People write in the Master. Tolarix keeps the Mirrors in step:
same pages, same hierarchy, working links, attachments and diagrams, converted
to each wiki's own format. The copy goes one way only, so Tolarix never writes
to the Master.

This guide covers the web interface, which is the same in Docker and in the
desktop app, and the command line. To install and run a server, see the
[admin guide](admin-guide.md). To run flows from cron or Task Scheduler, see
[scheduling.md](scheduling.md). There is an Italian version of this guide in
[it/guida-utente.md](it/guida-utente.md).

- [Concepts](#concepts)
- [Connecting your wikis](#connecting-your-wikis)
- [Creating a flow](#creating-a-flow)
- [Schedules](#schedules)
- [Running a flow](#running-a-flow)
- [Reading a run report](#reading-a-run-report)
- [Conversion limitations](#conversion-limitations)
- [The command line](#the-command-line)
- [FAQ](#faq)

## Concepts

| Term | Meaning |
|---|---|
| **Connection** | How Tolarix reaches one wiki: its address and credentials. Secrets are stored encrypted and never shown again. |
| **Flow** | Copies a Master, or part of it, into one or more Mirrors, with options and a schedule. |
| **Master** | The wiki people write in. Tolarix only reads it. |
| **Mirror** | A wiki (or a folder or parent page in one) that receives the copy. Pages that Tolarix creates there are overwritten on every run. |
| **Run** | One execution of a flow. A **dry-run** computes the same plan and writes nothing. |

Tolarix remembers which Mirror page belongs to which Master page. Thanks to
that, only changed pages are written, renames become moves, and pages that
someone else created on a Mirror are never touched.

## Connecting your wikis

Open **Connections → Add connection**, pick the type of wiki, fill in the
details and press **Test connection** before saving. Every type can be a Master
or a Mirror.

The user or token you give Tolarix needs read access on a Master and
write access on a Mirror. To limit what Tolarix can change, use a dedicated
account that only has access to the spaces or wikis it syncs.

### Obsidian vault

| Field | Value |
|---|---|
| Vault folder | The folder that contains the vault (the one with `.obsidian/` in it), for example `/srv/team-vault`. |

- The folder must be on the machine where Tolarix runs. In Docker that means a
  mounted volume, and you enter the path *inside* the container (for example
  `/vaults/team`, see the admin guide).
- Tolarix needs read permission on a Master vault and write permission on a
  Mirror vault. The Docker image runs as uid 65532.
- **Mirror location:** *Folder inside the vault*. Empty means the top of the vault.

### Confluence Data Center / Server

| Field | Value |
|---|---|
| Space or page address | Copy it from the browser. A space (`…/display/KEY`, `…/spaces/viewspace.action?key=KEY`) syncs the whole space; a page (`…/display/KEY/Title`, `…/pages/viewpage.action?pageId=123`) syncs that page and everything below it. |
| Personal access token or password | A personal access token (avatar → **Profile → Personal Access Tokens → Create token**). |
| Username | Only for older versions without tokens. The token field then holds that user's password. |

- **Permissions:** *View* on the space for a Master. For a Mirror, *Add* pages
  and attachments, plus *Delete* pages and attachments when the flow uses the
  delete policy. Archived pages are moved under an archive page, not deleted.
- **Mirror location:** *Parent page*, given as a title or numeric id. Empty means
  the connection's page, or the top of the space.

### Confluence Cloud

| Field | Value |
|---|---|
| Space or page address | For example `https://your-site.atlassian.net/wiki/spaces/KEY/overview`, or the address of a page to sync it and everything below it. |
| Email | The email of the Atlassian account the token belongs to. |
| API token | A **classic** API token: [id.atlassian.com](https://id.atlassian.com) → **Security → Create and manage API tokens → Create API token**. Scoped tokens are not supported yet. |

- **Permissions:** the same space permissions as on Data Center: *View* for a
  Master; *Add* pages and attachments for a Mirror, plus *Delete* for the delete
  policy.
- **Mirror location:** *Parent page*. Empty with a space address means the
  space homepage, which is where Cloud puts new pages.

### Azure DevOps — Code wiki

| Field | Value |
|---|---|
| Wiki or repository address | Open the wiki and copy the address (`https://dev.azure.com/org/project/_wiki/wikis/Name`), or a folder of a repository (`…/_git/repo?path=/docs&version=GBmain`). `org.visualstudio.com` and Azure DevOps Server (`https://host/tfs/Collection/…`) addresses work too. |
| Personal access token | **User settings → Personal access tokens → New token**, scope **Code (Read & write)**. For a Master only, **Code (Read)** is enough. |

- Tolarix works through Git: each run that changes something becomes **one
  commit** on the wiki's branch.
- **Branch policies** that require pull requests block direct pushes. Use a
  branch without required reviewers for Mirrors.
- **Mirror location:** *Folder inside the wiki*, for example `Mirrors/Team`.
  Empty means the top of the wiki.
- **Branch** (Mirror, optional): push to another branch, for example
  `wiki/mirror`, instead of the wiki's own. If it does not exist, the first run
  that has something to write creates it from the wiki's branch (a dry run
  creates nothing). The token then also needs the *Create branch* permission.
  The wiki shows that branch only if you publish it as a new wiki version (wiki
  menu → **Publish new version**); otherwise review the
  changes in **Repos** and merge them with a pull request. This is also the way
  to mirror into a branch protected by required reviewers.
- As a Master, the branch comes from the address: add `&version=GBbranch` to a
  repository address to read another branch.

### Azure DevOps — Project wiki

| Field | Value |
|---|---|
| Project wiki address | `https://dev.azure.com/org/project/_wiki` (or `…/_wiki/wikis/project.wiki`). |
| Personal access token | Scope **Code (Read & write)**, as for the Code wiki. |

The project must already have a project wiki (**Overview → Wiki → Create
project wiki**). Tolarix writes to its hidden repository on branch `wikiMaster`.
Everything else works as for the Code wiki.

### Local folder exports

Three connection types write a Mirror to a folder on disk instead of to a wiki.
You can then commit the folder to a repository, publish it, or copy it
elsewhere. They can only be Mirrors.

| Type | What you get |
|---|---|
| Local folder: Azure DevOps wiki | The same files Tolarix writes to an Azure DevOps Code wiki (`Name.md`, subpages in `Name/`, `.order`, `.attachments/`). Push the folder to a repository and publish it as a code wiki. |
| Local folder: GitHub wiki | Files for a GitHub wiki (clone `<repo>.wiki.git` into the folder and push after the run). |
| Local folder: Markdown | Plain Markdown with relative links, for static site generators (MkDocs, Docusaurus…), GitLab or browsing in a repository. |

| Field | Value |
|---|---|
| Export folder | An existing folder on the machine where Tolarix runs, for example `/srv/export`. In Docker, a mounted volume and the path inside the container. |

- **Mirror location:** *Folder inside the export folder*. Empty means the export folder itself.
- The folder must already exist and be writable; **Test connection** checks it.
- Changes are written all together at the end of the run. A dry run writes
  nothing.
- Files Tolarix does not manage (a `README.md`, a `.git` folder…) are left
  alone. Edits made to exported pages are overwritten on the next run, with a
  warning.
- Pages deleted in the Master are moved under `Archived/`, as in the wikis.

**GitHub wiki.** GitHub shows every page by name, whatever folder it is in, so
page names are unique across the export: spaces become `-`, characters GitHub
does not allow are dropped, and a duplicate gets `-(2)`. Subpages still go in a
folder named after their parent. Tolarix writes `_Sidebar.md` with the page tree
(in Master order); a sidebar you wrote yourself (without Tolarix's marker on the
first line) is never replaced. Attachments go in `_attachments/`.

**Markdown.** A page is `Name.md`; a page with subpages becomes `Name/index.md`
next to them. Each page gets a `title` in its front matter, links are relative
(`../Other/Notes.md#section`) and attachments go in `assets/`. Plain files have
no page order, so pages sort by name.

### Secrets

The token or password field is write-only. When you edit a connection, the
secret stays as it is until you choose **Replace secret** or **Remove secret**.
Deleting a connection deletes its secret; the wiki itself is not changed. A
connection used by a flow cannot be deleted.

## Creating a flow

Open **Flows → New flow**. The wizard has six steps.

1. **Master.** Choose the wiki people write in. After the flow is saved its
   Master cannot change, because the sync history belongs to it. To mirror a
   different wiki, create a new flow.
2. **What to mirror.**
   - *The whole wiki*: every page. Untick pages in the tree to leave them out.
   - *Only selected pages*: tick pages or folders; everything below them is
     included. Each selected page becomes a top-level page on the Mirrors,
     without its Master ancestors. Pages you leave out always win over pages
     you select.
3. **Mirrors.** Add one or more wikis and, for each one, where the pages go
   (folder or parent page). A Mirror cannot be the Master's own connection, and
   the same wiki and location cannot appear twice.
4. **Options.**
   - *When a page is removed from the Master*: **Archive it on the Mirrors**
     (default: moved to an `Archived` area, so nothing is lost), **Delete it on
     the Mirrors**, or **Keep it on the Mirrors** (it stays and is no longer
     updated).
   - *Copy attachments*: images and files used by the pages (on by default).
     Attachments are removed from Mirrors only with the delete policy.
   - *Add a notice to mirrored pages*: a short note at the top of each copied
     page that tells readers to edit the Master instead. You can write your
     own text; `{master}` becomes the name of the Master. Leave it empty for
     the default text, which is in English.
   - *Take over existing pages*: off by default. When it is off, a page that
     someone else created on a Mirror, at the place where a copied page should
     go, stops that page with a conflict. When it is on, Tolarix takes the page
     over and overwrites it.
5. **Schedule.** See [Schedules](#schedules).
6. **Review.** Check the summary and save. A flow can be **Enabled** or
   disabled: a disabled flow never runs by itself, but you can still run it by
   hand.

After saving, start with a **Dry-run**: it shows what would be created,
updated, moved, archived or deleted, and changes nothing.

Flows can also be imported from a YAML file with `tolarix flow import`. See
[examples/flows.example.yaml](examples/flows.example.yaml).

## Schedules

The **Simple** tab covers the common cases:

| Choice | Example |
|---|---|
| Only when I run it manually | the default |
| Every N minutes or hours | every 5, 10, 15, 20 or 30 minutes; every 1, 2, 3, 4, 6, 8 or 12 hours (steps that line up with the clock) |
| Every day at… | every day at 07:30 |
| On these weekdays at… | Monday to Friday at 07:30 |
| Every month on day… at… | day 1 at 06:00. Days 29–31 skip shorter months. |

The **Advanced** tab takes a cron expression with five fields
(`minute hour day-of-month month day-of-week`, for example `0 7 * * 1-5`) or
`@hourly`, `@daily`, `@weekly`, `@monthly` or `@yearly`. Seconds, `@every` and
time zone prefixes are not accepted. Use the time zone field instead.

- **Timezone**: every time is in the flow's timezone (an IANA name such as
  `Europe/Rome`). The server's default is shown under the field.
- **Preview**: shows the schedule as a sentence, its cron form and the next
  five runs, while you type.
- **Run with the built-in scheduler**: on by default. Turn it off when an
  external scheduler (cron, systemd, Task Scheduler) should start the flow with
  `tolarix run-due`. The times stay the same. See [scheduling.md](scheduling.md).

Missed times (the server was off) are made up with **one** run when the server
is back. Older times are not replayed. A flow that is still running when its
next time comes skips that time.

The desktop app's scheduler runs only while the app is open. For flows that
must run when nobody is signed in, run a server or `tolarix run-due` from an
external scheduler.

## Running a flow

- **Run now** and **Dry-run** are on the dashboard and on the flow's page.
- The run page streams the log live. You can leave the page; the run carries on
  in the server. **Stop run** cancels a run started from the web interface.
  Runs started from the command line can only be stopped there.
- A flow runs only once at a time, wherever it was started: in the web
  interface, by the built-in scheduler, from `tolarix flow run` or from
  `tolarix run-due`. A second start while it runs is refused, and the screen
  or the exit code (4) says the flow is already running.
- A server runs at most two scheduled flows at once. Manual runs are not limited.

## Reading a run report

Open **Runs**, or a run from the dashboard or a flow page. The **Report** tab
appears when the run ends; the **Log** tab shows every line.

**Status**

| Status | Meaning | CLI exit code |
|---|---|---|
| Succeeded | everything was written | 0 |
| Partly failed | the run finished, but some pages failed (see their details) | 3 |
| Failed | the run stopped: Master unreachable, push rejected, … | 1 |
| Stopped | cancelled by a user | 1 |

A dry-run report has the same shape and says that nothing was changed.

**Changes per Mirror**

| Change | Meaning |
|---|---|
| Created | new page on the Mirror |
| Updated | the Master page changed, or someone edited the Mirror copy (see warnings) |
| Moved | the page was renamed or moved in the Master; the Mirror page was moved too |
| Archived / Deleted / Kept | the page was removed from the Master; what happened depends on the delete policy |
| Unchanged | nothing to do (hidden by default: **Show unchanged pages**) |
| Conflict | the page could not be written; the details say why and how to fix it |

**Warnings** list everything that did not convert perfectly, per page. The most
common ones:

| Warning | Meaning |
|---|---|
| `mirror-edited` | someone edited the Mirror copy; Tolarix overwrote it |
| `broken-link`, `link-out-of-scope`, `link-outside-master` | the link target is missing, outside the flow's scope or outside the Master; the link became plain text |
| `title-renamed` | Confluence titles are unique per space: the page got its parent's title in brackets, then a number |
| `renamed` | the name is not allowed on the Mirror (illegal characters, reserved names, clashes); it was changed |
| `attachment-missing`, `attachment-not-mirrored` | a referenced file was not found, or attachments are off |
| `render-failed`, `diagram-missing` | a draw.io diagram could not be turned into a PNG (see the admin guide) |
| `confluence-macro`, `macro-unsupported` | a Confluence macro has no equivalent on the Mirror |
| `scope-missing` | a selected or left-out page no longer exists in the Master |

The attachment line at the end of each Mirror says how many files were uploaded
and deleted.

## Conversion limitations

Every wiki has features the others lack. When a feature has no equivalent on the
Mirror, Tolarix degrades it to something readable and reports a warning. It
never drops content silently. Developer details:
[dev/connectors.md](dev/connectors.md).

**Any Master → any Mirror**

- Links to pages outside the flow's scope become plain text.
- Edits made on a Mirror are overwritten by the next run that writes the page.
- If two pages swap places in one change, both report a conflict until one of them is moved again (known limitation).
- draw.io diagrams: the `.drawio` file is kept as an attachment and a PNG is shown. The PNG comes from the preview next to the diagram, an export server or draw.io Desktop. Only the first page of a diagram is rendered.

| From → To | What changes |
|---|---|
| Obsidian → Confluence | Callouts become info/tip/note/warning panels; foldable callouts become *expand*. Mermaid becomes a `code` block titled `mermaid` (Data Center has no built-in Mermaid). Embedded notes (`![[Note]]`) become links. Front matter is not mapped (Confluence labels are not supported yet). Master order is lost: Confluence orders child pages itself. |
| Obsidian → Azure DevOps | Callouts become a bold label in a quote (`> **Tip:** …`), because the wiki has no callouts. Mermaid becomes `::: mermaid`. Front matter is kept. Page order follows the Master (`.order`). |
| Confluence → Obsidian / Azure DevOps | `info`/`tip`/`note`/`warning`/`panel` become callouts (Azure DevOps: bold label). `expand` becomes a foldable callout. `toc` is kept as a table of contents. `children`, `pagetree` and `anchor` are dropped. `status` becomes its text in brackets (`[DONE]`), Jira issues become the issue key. Any other macro becomes a `confluence-macro` code block holding its XML, with a warning. Confluence Cloud whiteboards, databases and embeds are skipped; folders become title-only pages. |
| Confluence → Confluence | Macros, including unknown ones, are written back as they were. Titles are made unique per space. |
| Azure DevOps → others | `[[_TOC_]]` becomes the target's table of contents. `[[_TOSP_]]` (subpages list) is dropped with a warning. Image sizes (`=300x200`) are kept where the target supports them. |
| any → Obsidian | Links are written as wikilinks with the full vault path. Attachments go to the vault's attachment folder (or `attachments/`); files with the same name but different content get a short hash in the name. Obsidian has no manual page order: pages appear alphabetically. |
| any → Azure DevOps | Special characters in page names are encoded the way the wiki does it (`Q&A: why?` → `Q&A%3A-why%3F`). Everything a run writes lands in one commit; if someone pushed in the meantime, the run fails without writing anything and the next run starts over. |

## The command line

The same `tolarix` binary runs the server and the command line. Commands work
directly on the data folder; no server needs to be running. Add `--json` to get
machine-readable output, and `--data-dir` to use another data folder.

```sh
tolarix connection list                 # connections and their kind
tolarix connection test "Team wiki"     # check the saved settings
tolarix flow list                       # flows, schedules and last runs
tolarix flow run "Handbook" --dry-run   # what would change
tolarix flow run "Handbook"             # run now and wait
tolarix flow run --all                  # every enabled flow, one after the other
tolarix run-due                         # the flows whose schedule says they are due
tolarix runs list                       # recent runs
tolarix runs show <run-id> --log        # a run's report and log
tolarix flow import flows.yaml          # create or update connections and flows by name
tolarix version
```

Exit codes are stable, so scripts and schedulers can rely on them:

| Code | Meaning |
|---|---|
| 0 | success (for `run-due`, also "nothing was due") |
| 1 | the command or the sync failed |
| 2 | bad flags or arguments |
| 3 | the sync finished, but some pages failed |
| 4 | the flow is already running elsewhere |
| 5 | the flow, connection or run does not exist |
| 6 | data folder, database or secret key problem |

## FAQ

**Can I edit pages on a Mirror?** You can, but the next run that touches the page
overwrites your edit and reports `mirror-edited`. Edit the Master.

**Does Tolarix delete pages it did not create?** No. It only changes pages it
created, plus pages it took over when *Take over existing pages* is on.

**I deleted a flow. What happens to the Mirrors?** Nothing: copied pages stay
where they are. A new flow will not recognise them as its own, so turn on
*Take over existing pages* for its first run, or clean up the Mirror first.

**Why does a flow with a next run time never run?** Its built-in schedule is
probably off ("Runs only via tolarix run-due"), and no external scheduler calls
`tolarix run-due`. See [scheduling.md](scheduling.md).
