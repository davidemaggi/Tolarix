# Scheduling Tolarix

A flow can run by itself in two ways:

- **The built-in scheduler.** `tolarix serve` (Docker, a service, or the
  desktop app while it is open) checks every 20 seconds which flows are due.
  Nothing else to set up.
- **An external scheduler** (cron, a systemd timer, Windows Task Scheduler,
  Kubernetes) calls `tolarix run-due` or `tolarix flow run`. Use it when no
  Tolarix server is left running, for example with the desktop app or the
  command line on a laptop or a build agent, or when your organisation already
  manages jobs that way.

Both can be used together; the [run lock](#how-overlaps-are-prevented) keeps
them from stepping on each other.

- [Which command to schedule](#which-command-to-schedule)
- [Linux and macOS: cron](#linux-and-macos-cron)
- [Linux: systemd timer](#linux-systemd-timer)
- [macOS: launchd](#macos-launchd)
- [Windows: Task Scheduler](#windows-task-scheduler)
- [Docker](#docker)
- [Kubernetes CronJob](#kubernetes-cronjob)
- [How overlaps are prevented](#how-overlaps-are-prevented)
- [Turning off the built-in schedule for a flow](#turning-off-the-built-in-schedule-for-a-flow)
- [Exit codes and alerts](#exit-codes-and-alerts)

## Which command to schedule

| Command | Runs | Typical trigger |
|---|---|---|
| `tolarix run-due` | every enabled flow whose schedule (set in the UI or YAML) has a time that has passed since its last scheduled run | every 5 minutes |
| `tolarix flow run "<name>"` | that flow, now, regardless of its schedule | the external scheduler's own times |
| `tolarix flow run --all` | every enabled flow, now | the external scheduler's own times |

**Recommended: `run-due` every few minutes.** The schedule stays where people can
see and edit it (the flow's Schedule step, with its preview), and the external
scheduler only needs one entry for all flows. A flow due at 07:30 runs at the
first `run-due` call after 07:30, so call it as often as the precision you need.

`run-due` runs due flows one after the other, then exits. If nothing is due it
prints "Nothing is due." and exits 0. Times missed while nothing was calling it
are made up with one run, never replayed one by one.

`flow run` is useful when the external scheduler should own the timing, for
example a pipeline step after a release. Leave those flows on *Only when I run it
manually* so the built-in scheduler does not also start them.

All commands need to find the same data folder as the server or desktop app:

- The default folder is per user (`~/.config/tolarix`, `~/Library/Application Support/Tolarix`,
  `%AppData%\Tolarix`). The scheduled job must run **as the same user**, or get
  `--data-dir` / `TOLARIX_DATA_DIR`.
- `TOLARIX_SECRET_KEY`, if you use it, must be set for the job too. With the
  desktop app the key is in the user's keychain, which a job running as that user
  can read on Windows and macOS. On Linux, keyring access without a desktop
  session may fail; see the admin guide.

## Linux and macOS: cron

`crontab -e` as the user that owns the data folder:

```cron
# Run due Tolarix flows every 5 minutes; keep a log.
*/5 * * * * /usr/local/bin/tolarix run-due >> "$HOME/.local/state/tolarix-run-due.log" 2>&1
```

Notes:

- **PATH**: cron starts with a minimal environment (`PATH=/usr/bin:/bin`). Use
  the absolute path to `tolarix`, or set `PATH=` at the top of the crontab.
- **Environment**: cron does not read your shell profile. Put variables on the
  line or at the top of the crontab:

  ```cron
  TOLARIX_DATA_DIR=/srv/tolarix
  TOLARIX_LOG_FORMAT=json
  */5 * * * * /usr/local/bin/tolarix run-due >> /var/log/tolarix/run-due.log 2>&1
  ```

  Keep `TOLARIX_SECRET_KEY` out of the crontab: cron mails and `crontab -l` show
  it. Use the key file in the data folder (the default) instead.
- **Time zone**: cron's own time does not matter for `run-due`; each flow uses its
  own timezone. For `flow run` lines, cron uses the system timezone (or
  `CRON_TZ=` where your cron supports it).
- **Logging**: progress goes to stderr, the summary to stdout; `2>&1` keeps both.
  Rotate the file with logrotate, or use `--json` and send it to your log stack.
  Every run is also stored in Tolarix (`tolarix runs list`, the Runs page).
- **macOS**: cron needs *Full Disk Access* for `/usr/sbin/cron` to read some
  folders (System Settings → Privacy & Security). launchd (below) is the native
  alternative.

A specific flow every weekday at 07:30:

```cron
30 7 * * 1-5 /usr/local/bin/tolarix flow run "Handbook" >> "$HOME/.local/state/tolarix-handbook.log" 2>&1
```

## Linux: systemd timer

Two files. The service runs once; the timer starts it.

```ini
# ~/.config/systemd/user/tolarix-run-due.service   (user unit)
[Unit]
Description=Run due Tolarix flows

[Service]
Type=oneshot
ExecStart=/usr/local/bin/tolarix run-due
# Environment=TOLARIX_DATA_DIR=/srv/tolarix
```

```ini
# ~/.config/systemd/user/tolarix-run-due.timer
[Unit]
Description=Run due Tolarix flows every 5 minutes

[Timer]
OnCalendar=*:0/5
Persistent=true
RandomizedDelaySec=30

[Install]
WantedBy=timers.target
```

```sh
systemctl --user daemon-reload
systemctl --user enable --now tolarix-run-due.timer
systemctl --user list-timers                 # next start
journalctl --user -u tolarix-run-due         # output of every run
loginctl enable-linger "$USER"               # keep user timers running after logout
```

For a system-wide setup put the files in `/etc/systemd/system/`, add
`User=tolarix` and `Environment=TOLARIX_DATA_DIR=/var/lib/tolarix` to the
service, and use `systemctl` without `--user`. systemd never starts a oneshot
service twice at the same time, and the journal keeps the logs.

## macOS: launchd

```xml
<!-- ~/Library/LaunchAgents/io.github.davidemaggi.tolarix.run-due.plist -->
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>io.github.davidemaggi.tolarix.run-due</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/local/bin/tolarix</string>
    <string>run-due</string>
  </array>
  <key>StartInterval</key><integer>300</integer>
  <key>StandardOutPath</key><string>/tmp/tolarix-run-due.log</string>
  <key>StandardErrorPath</key><string>/tmp/tolarix-run-due.log</string>
</dict>
</plist>
```

```sh
launchctl load ~/Library/LaunchAgents/io.github.davidemaggi.tolarix.run-due.plist
```

## Windows: Task Scheduler

Install `tolarix.exe` somewhere stable, for example `C:\Program Files\Tolarix\`.
The task must run as the user whose data folder (`%AppData%\Tolarix`) holds the
flows, or pass `--data-dir`.

### With the graphical interface

1. Open **Task Scheduler** → **Create Task…** (not *Basic Task*: it lacks the repeat options).
2. **General**: name `Tolarix run-due`. Choose *Run whether user is logged on or
   not* if it must run without a session, and enter the password when asked.
3. **Triggers** → **New…**: *Daily*, start today at 00:00, **Repeat task every:
   5 minutes**, **for a duration of: Indefinitely**. OK.
4. **Actions** → **New…**: *Start a program*.
   - Program: `C:\Program Files\Tolarix\tolarix.exe`
   - Arguments: `run-due`
   - Start in: `C:\Program Files\Tolarix`
5. **Settings**: *If the task is already running*: **Do not start a new instance**.
   Optionally *Stop the task if it runs longer than* a few hours.
6. OK. Right-click the task → **Run** to try it; the *Last Run Result* column
   shows the exit code (`0x0` is success, `0x3` is a partial failure, `0x4` locked).

Task Scheduler does not keep program output. To log it, use `cmd.exe` as the
program with the arguments
`/c ""C:\Program Files\Tolarix\tolarix.exe" run-due >> "%LOCALAPPDATA%\Tolarix\run-due.log" 2>&1"`.

### With schtasks

```bat
schtasks /Create /TN "Tolarix run-due" /SC MINUTE /MO 5 ^
  /TR "\"C:\Program Files\Tolarix\tolarix.exe\" run-due" /F

schtasks /Run /TN "Tolarix run-due"
schtasks /Query /TN "Tolarix run-due" /V /FO LIST
schtasks /Delete /TN "Tolarix run-due" /F
```

Add `/RU <user> /RP` to run it without a logged-on session (you are asked for the
password).

### With PowerShell

```powershell
$action  = New-ScheduledTaskAction -Execute 'C:\Program Files\Tolarix\tolarix.exe' `
             -Argument 'run-due' -WorkingDirectory 'C:\Program Files\Tolarix'
$trigger = New-ScheduledTaskTrigger -Once -At (Get-Date) `
             -RepetitionInterval (New-TimeSpan -Minutes 5)
$settings = New-ScheduledTaskSettingsSet -MultipleInstances IgnoreNew `
             -ExecutionTimeLimit (New-TimeSpan -Hours 4) -StartWhenAvailable
Register-ScheduledTask -TaskName 'Tolarix run-due' -Action $action `
  -Trigger $trigger -Settings $settings -Description 'Run due Tolarix flows'

Start-ScheduledTask -TaskName 'Tolarix run-due'
Get-ScheduledTaskInfo -TaskName 'Tolarix run-due'   # LastTaskResult = exit code
```

On older PowerShell versions without an indefinite repetition default, add
`-RepetitionDuration ([TimeSpan]::MaxValue)` to the trigger.

## Docker

The container already runs the built-in scheduler, so you usually need nothing
else. To have the host's scheduler decide instead, turn off the built-in schedule
for those flows (see below) and call `run-due` inside the container:

```cron
*/5 * * * * docker exec tolarix /tolarix run-due >> /var/log/tolarix-run-due.log 2>&1
```

The container name comes from `docker ps` (with compose: `docker compose -f
/path/to/docker-compose.yml exec -T tolarix /tolarix run-due`). Use `-T` / no `-it`:
cron has no terminal.

A one-off container against the same volume also works, for example when the
server container is not running:

```sh
docker run --rm -v tolarix-data:/data ghcr.io/davidemaggi/tolarix:1 run-due
```

Do not run a second `serve` container on the same volume: two servers would share
one database and both run the built-in scheduler. The lock prevents double runs,
but other state, such as sessions and the scheduler cap, is per process.

## Kubernetes CronJob

When Tolarix runs as a Deployment with a PersistentVolumeClaim, `kubectl exec`
from a CronJob is fragile. Instead, run `run-due` in a short-lived pod that mounts
the same volume. With a ReadWriteOnce volume, the pod must land on the same node
as the server (or use ReadWriteMany storage).

```yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: tolarix-run-due
spec:
  schedule: "*/5 * * * *"
  concurrencyPolicy: Forbid          # never two run-due pods at once
  successfulJobsHistoryLimit: 3
  failedJobsHistoryLimit: 5
  jobTemplate:
    spec:
      backoffLimit: 0                # the next schedule tries again
      template:
        spec:
          restartPolicy: Never
          securityContext:
            runAsUser: 65532
            runAsNonRoot: true
            fsGroup: 65532
          containers:
            - name: run-due
              image: ghcr.io/davidemaggi/tolarix:1
              args: ["run-due", "--log-format", "json"]
              env:
                - name: TOLARIX_DATA_DIR
                  value: /data
                - name: TOLARIX_SECRET_KEY        # only if the server uses it too
                  valueFrom:
                    secretKeyRef: { name: tolarix, key: secret-key }
              volumeMounts:
                - { name: data, mountPath: /data }
          volumes:
            - name: data
              persistentVolumeClaim:
                claimName: tolarix-data
```

If no Tolarix server runs at all, this CronJob alone is the scheduler: give
the volume to the CronJob only, and manage flows with `tolarix flow import` from
a YAML file in a ConfigMap (secrets through `secretEnv`).

## How overlaps are prevented

Every run, whoever starts it, first takes the flow's **lock**: a row in the
database with the holder's name and a heartbeat refreshed while the run is
going. That covers the built-in scheduler, *Run now* in the UI, `flow run` and
`run-due`.

- If the lock is held, the new start is refused: the UI shows who holds it,
  and the command exits with code **4**. A dry-run takes the lock too.
- A run that crashed (power loss, `kill -9`) stops refreshing its heartbeat; its
  lock counts as stale after **2 minutes** and the next run takes over.
- Different flows run in parallel. The server runs at most two scheduled flows
  at once; `run-due` runs its flows one after the other.

On top of the lock, each **scheduled time** is claimed in the database before it
runs. The server and any number of `run-due` processes can see the same flow due
at the same time, but only the first to claim the time runs it; the others skip
it. A time that is skipped because the flow is still running (exit code 4) is
consumed, not retried: the flow runs at its next time.

So it is safe to leave the built-in scheduler on and also call `run-due` (for
example as a fallback while the server is down): each scheduled time runs once.

## Turning off the built-in schedule for a flow

To let only the external scheduler run a flow at its scheduled times:

- **Web interface**: edit the flow → **Schedule** → turn off **Run with the
  built-in scheduler**. The preview then says *Runs only via "tolarix run-due"*.
  The next run time is still shown, because `run-due` uses it.
- **YAML import**: `builtInSchedule: false` on the flow:

  ```yaml
  flows:
    - name: Handbook
      schedule: { kind: weekly, days: [mon, tue, wed, thu, fri], time: "07:30" }
      timezone: Europe/Rome
      builtInSchedule: false
  ```

- **API**: `scheduleEnabled: false` on the flow.

| Flow | Built-in scheduler | `run-due` | `flow run` / Run now |
|---|---|---|---|
| enabled, schedule set, built-in on | runs it | runs it (shared claim, no doubles) | runs it |
| enabled, schedule set, built-in off | skips it | runs it | runs it |
| enabled, *Only when I run it manually* | skips it | skips it | runs it |
| disabled | skips it | skips it | runs it (manual runs always work) |

## Exit codes and alerts

| Code | `run-due` / `flow run` |
|---|---|
| 0 | success, or nothing was due |
| 1 | a flow failed |
| 3 | a flow finished but some pages failed |
| 4 | a flow was already running (its time was skipped) |
| 5 | flow not found (`flow run`) |
| 6 | data folder, database or secret key problem |

With several flows, the most serious code wins (4, then 1, then 3, then 0).
Use the code for alerts: cron's `MAILTO`, systemd's `OnFailure=`, the Task
Scheduler *Last Run Result*, or a Kubernetes failed-job alert. For details,
`tolarix runs list` and `tolarix runs show <id>` print the reports, and
`--json` makes them easy to parse.
