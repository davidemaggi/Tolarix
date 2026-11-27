# Tolarix manual: a guided tour

This manual walks through Tolarix from the first sign-in to the first run
report, one screen at a time. It follows one example: a team keeps its
engineering handbook in an Obsidian vault (the **Master**) and wants copies in
three other places (the **Mirrors**):

- a second vault for the public site;
- a Markdown folder for a static site generator;
- a GitHub wiki.

The screens are the same in the Docker web app and in the desktop app. Every
field is explained in the [user guide](user-guide.md), which is the reference
to keep at hand. There is an Italian version of this manual:
[it/manuale.md](it/manuale.md).

1. [Sign in](#1-sign-in)
2. [Connect your wikis](#2-connect-your-wikis)
3. [Create a flow](#3-create-a-flow)
4. [Run it and read the report](#4-run-it-and-read-the-report)
5. [Day to day](#5-day-to-day)

## 1. Sign in

> The desktop app has no sign-in: skip to [step 2](#2-connect-your-wikis).

On a new server, Tolarix prints a **one-time password** in its log
(`docker logs tolarix`). Open the web address, enter that password and sign in.

![The sign-in page](images/en/sign-in.png)

Tolarix then asks you to choose your own password, at least 12 characters long.
Nothing else works until you do. The [admin guide](admin-guide.md#resetting-the-admin-password)
explains how to reset a forgotten password.

![Choosing a new password](images/en/choose-password.png)

You land on the **Dashboard**. It is empty for now: it will show every flow and
how its last run went.

![The empty dashboard](images/en/dashboard-empty.png)

## 2. Connect your wikis

A **connection** tells Tolarix how to reach one wiki. Open **Connections** →
**Add connection** and choose the type of wiki. Each card says whether that
type can be a Master, a Mirror or both. The local folder types are Mirrors only.

![Choosing the type of wiki](images/en/connection-type.png)

Give the connection a name you will recognise, then fill in the details for
that type: here, the vault folder. For Azure DevOps and Confluence it is the
address copied from the browser and a token. **Test connection** checks the
settings before you save them. Tokens and passwords are stored encrypted and
are never shown again.

![The connection details, tested](images/en/connection-form.png)

Add one connection per wiki. In the example there are four: the Master vault,
the public vault and two export folders. Connections to Azure DevOps and
Confluence are added the same way. A connection can be used by any number of
flows.

![The list of connections](images/en/connections.png)

Details for every type of wiki (addresses, tokens, permissions):
[user guide → Connecting your wikis](user-guide.md#connecting-your-wikis).

## 3. Create a flow

A **flow** says what to copy, from which Master, to which Mirrors and when.
Open **Flows** → **New flow**. The wizard has six steps; the list on the left
shows where you are, and you can go back at any time.

**Master.** Name the flow and choose the Master wiki. Tolarix only ever reads
the Master.

![Step 1: the Master](images/en/flow-master.png)

**What to mirror.** Copy the whole wiki and untick what to leave out, or tick
only the pages and folders you want. Here the *Drafts* folder is left out.

![Step 2: what to mirror](images/en/flow-scope.png)

**Mirrors.** Add one or more Mirrors. For each one you can choose a folder
inside it, so the copied pages do not mix with pages that are already there.
Here the public vault gets the pages under *Handbook* and the site export under
*docs*. The GitHub wiki gets them at its top level.

![Step 3: the Mirrors](images/en/flow-mirrors.png)

**Options.** Choose what happens on the Mirrors when a page is removed from the
Master: archive it (the default, so nothing is lost), delete it, or keep it. You
can also choose whether to copy attachments and whether to add a notice at the
top of every copied page telling readers to edit the Master instead.

![Step 4: options](images/en/flow-options.png)

**Schedule.** Choose when the flow runs: only by hand, every few minutes or
hours, every day, on some weekdays or once a month. The preview shows the next
runs, so you can check the schedule before saving. **Advanced** accepts a cron
expression. Here it runs every weekday at 07:30.

![Step 5: schedule](images/en/flow-schedule.png)

**Review.** Check everything and use **Edit** to go back to a step.
**Create flow** saves it.

![Step 6: review](images/en/flow-review.png)

## 4. Run it and read the report

On the flow's page, **Dry-run** shows what a run would change without changing
anything. It is a good idea before the first real run. **Run now** starts
the run. The run page shows the totals and follows the log live. You can leave
the page: the run carries on in the server.

![A finished run, with its log](images/en/run-log.png)

The **Report** tab lists, for each Mirror, every page that was created,
updated, moved, archived or deleted, and where it went. **Warnings** lists what
could not be copied exactly. Here, the *Deploy* runbook links to a page in
*Drafts*, which the flow leaves out, so in the Mirrors that link is shown as
plain text. Each warning code is explained in the
[user guide](user-guide.md#reading-a-run-report).

![The run report, with a warning opened](images/en/run-report.png)

Runs are incremental: the next run touches only what changed in the Master.
After the *Deploy* page was edited, the second run updated just that page in
each Mirror ("3 updated"). The flow's page shows the schedule, the next run and
the recent runs.

![The flow after two runs](images/en/flow-detail.png)

## 5. Day to day

The **Dashboard** shows every flow, how its last run went and its **Run now**
and **Dry-run** buttons. Here a second flow copies only the onboarding pages,
and has been tried with a dry-run so far.

![The dashboard with two flows](images/en/dashboard.png)

**Runs** lists every run and dry-run, newest first, and can be filtered by flow,
status and kind.

![The run history](images/en/runs.png)

The theme menu at the top switches between light, dark and the system setting,
and the language menu switches between English and Italian.

![The dashboard in the dark theme](images/en/dashboard-dark.png)

### Where to go next

- [User guide](user-guide.md): every option, the conversion limits of each wiki,
  the command line and the FAQ.
- [Scheduling](scheduling.md): run flows from cron, systemd, launchd or Windows
  Task Scheduler instead of the built-in scheduler.
- [Admin guide](admin-guide.md): installing with Docker, backups, upgrades and
  HTTPS.
