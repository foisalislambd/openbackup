# Backing up

Once a device is connected there is nothing to schedule. This page explains what
the agent decides on its own, so you can tell whether an idle agent is broken or
behaving.

## What gets backed up

On a fresh install: Desktop, Documents, Pictures, Videos, Music and Downloads —
located by asking the operating system, so it works on a non-English system and
on a machine where those folders live on another drive.

```bash
openbackup folders                    # what is included now
openbackup folders add ~/Projects     # add anything else, on any drive
openbackup folders remove ~/Projects  # stop backing it up; existing backups stay
openbackup folders off ~/Projects     # pause one folder without removing it
openbackup folders on ~/Projects
```

Changes reach the running background service immediately rather than at the next
restart, so a folder you just added starts being watched at once. Add
`openbackup backup` if you would rather not wait for the next scan to upload it.

Inside those folders, system files, caches and build output are skipped.
`openbackup rules` prints every rule with the reason attached, and
[ignore-rules.md](ignore-rules.md) explains the design — including why
`node_modules` is skipped inside a project and kept in a folder that merely has
that name.

## When it runs

Three things trigger work:

- **A file changes.** The agent watches your folders and reacts. It waits 15
  seconds after a file stops changing before uploading it, so saving a document
  forty times does not upload it forty times.
- **Every 12 hours**, the whole tree is walked again. This catches what a watcher
  cannot: changes made while the machine was off, dropped events, an external
  drive that was unplugged at the time.
- **When you ask.** `openbackup backup` starts one now and waits for it.

The first backup is a full one. After that each backup records only what changed,
as a delta against the previous one, and a full backup happens again after 24
deltas so a restore never has to walk an unbounded chain.

## When it deliberately does not run

The agent yields to you. `openbackup status` names the reason whenever it is
paused, so "nothing is happening" is always answerable:

| Reason | Default |
| --- | --- |
| The machine is busier than 70% CPU | pauses until it settles |
| A full-screen app is running (a game, a presentation) | pauses |
| The connection is metered (a phone hotspot, a capped link) | pauses |
| Battery below 20% | pauses |
| On battery at all | keeps going — laptops are often never plugged in, and refusing to back one up would be a silent failure |

All of these are settings; see [configuration.md](configuration.md). You can also
stop it yourself:

```bash
openbackup pause --for 2h    # or with no flag, until you resume
openbackup resume
```

## Limiting how much of your connection it uses

```bash
openbackup limit --upload 5MB    # 5 MB/s ceiling
openbackup limit --upload 0      # no limit (the default)
```

The ceiling applies to the running service straight away. Three chunks upload
concurrently by default, which keeps a single slow request from stalling the
queue without saturating a home connection.

## What the dashboard shows

**Overview** is the answer to "am I backed up": last backup per device, how much
is protected, how much space it uses, and how many versions exist. **Activity**
is the log — every backup, every error, every skipped file with the reason.
**Devices** is where you create connection codes, rename a machine, and pause or
remove one remotely.

The desktop app shows the same thing for the machine it runs on, without a
browser. See [desktop-app.md](desktop-app.md).

## Version history and retention

Every backup is a restorable point in time. Retention defaults to 30 days and is
set per account in the dashboard (**Settings → keep backups for**); `0` keeps
everything.

Because identical data is stored once, history is far cheaper than it sounds:
ninety days of a home directory that changes slowly costs a small fraction of
ninety copies. When a snapshot expires, only the blocks no longer referenced by
any snapshot are deleted, and the newest complete backup is never removed even if
it is older than the retention window — an old backup is much better than none.

## Large files, and things you might expect to be included

- Files above 8 GiB are skipped by default. This is aimed at virtual machine
  disks, which are enormous, rewritten constantly, and reproducible. Raise or
  disable the limit in the configuration if you keep large video files.
- Symbolic links are stored as links, not followed, so a link into a system
  directory cannot drag it into the backup.
- Files locked by another program are retried on the next pass rather than
  failing the backup. The one that a running database or Outlook holds open may
  need the program closed to be captured cleanly.
- Nothing outside a configured folder is read. Ever. That is the boundary the
  whole design rests on.

## Next

- [Restoring](restoring.md) — try it now, on a file you do not need
- [End-to-end encryption](encryption.md) — if the server should not be able to
  read your files
- [Troubleshooting](troubleshooting.md) — if a backup is not doing what this page
  describes
