# Troubleshooting

Start here:

```bash
openbackup doctor
```

It checks the configuration file, the enrolment, whether the server answers,
whether the credentials are accepted, whether the folders exist, whether the local
index is there, and whether the service is running. Almost everything below is a
`doctor` line with an explanation.

## Nothing is being backed up

**`openbackup status` says it is paused.** That is usually the answer, and the
reason is printed. The agent pauses above 70% CPU, on a metered connection, while a
full-screen app runs, and below 20% battery. Adjust in
[configuration.md](configuration.md), or `openbackup resume` if it was a manual
pause.

**`doctor` says the agent is not running.** Install or start the service:

```bash
openbackup service install
openbackup service status
```

**`doctor` says it is not connected to a server.** The enrolment did not complete.
Get a fresh code from the dashboard and run `openbackup connect` again.

**Everything passes but nothing uploads.** Look at what it thinks it should do:

```bash
openbackup folders    # is anything enabled?
openbackup backup     # run one in the foreground and read the output
```

## A file or folder is missing from the backup

In order of likelihood:

1. **A rule excluded it.** `openbackup rules` prints every rule with its reason,
   and the dashboard's **Activity** log records skipped files with the rule that
   matched. See [ignore-rules.md](ignore-rules.md).
2. **It is outside a configured folder.** `openbackup folders` shows what is
   included; add it with `openbackup folders add <path>`.
3. **It is bigger than 8 GiB.** The default cut-off, aimed at VM disks. Raise or
   disable it in the configuration.
4. **The drive was not connected** during the last backup. `doctor` reports a
   configured folder that is not currently there.
5. **Another program held it locked.** Retried on the next pass; a file that a
   running database or mail client holds open may need the program closed.

If none of these explain it, that is a bug — please report it with `doctor` output.

## "Connection code is invalid or expired"

Codes are single-use and expire after 24 hours. Create a new one under **Devices**.
If a code fails immediately after creation, check that the agent is talking to the
same server as the dashboard: the URL in `openbackup doctor` should match the
address in your browser.

## The agent cannot reach the server

```bash
curl -fsS https://backup.example.com/api/v1/health
```

- **Fails from the machine but works elsewhere:** a firewall, a VPN, or DNS.
- **Fails everywhere:** check the server, `docker compose ps` and
  `docker compose logs`.
- **Works over `http` but not `https`:** the certificate. A self-signed one is not
  trusted by the agent any more than by a browser; use a real certificate, which
  Caddy will get for you.
- **Times out on large uploads only:** a proxy body limit below the chunk size.
  Allow at least 16 MiB, or raise `OPENBACKUP_MAX_CHUNK_BYTES` and match it.

## Backups are slow

The first one is a full upload of everything, and is bounded by your upload speed.
After that only changes move.

- `openbackup limit --upload 0` removes a ceiling you may have set.
- A busy machine yields deliberately: raise `max_cpu_percent` if you would rather
  it competed.
- Millions of small files are slow to *scan*, not to upload; the second scan is far
  faster because the index remembers what is unchanged.
- Deduplication means a big number in the dashboard may already be stored. Watch
  "uploaded" rather than "protected".

## The server ran out of space

The dashboard shows free space on the volume. To recover:

1. Lower retention (**Settings**), then wait for the next maintenance pass, or
   restart the server to run one immediately.
2. Delete backups you do not need, or a device you no longer own.
3. Set a quota so uploads are refused before the disk fills — a refused upload is a
   much better failure than a full disk.

Space comes back when the pass deletes blocks that no snapshot references any more,
which can be a moment after deleting a snapshot rather than instantly.

## Restore problems

**"This backup is encrypted"** in the dashboard: correct, and by design. The server
cannot decrypt it. Restore with the CLI or the desktop app on a device that has the
key ([encryption.md](encryption.md)).

**"Skipped: file exists".** The default; a restore does not overwrite. Use
`--overwrite` or `--keep-both`.

**Restore fails on a checksum.** The chunk did not match its digest. Run
`openbackup-server check` on the server; if a block really is gone, restore from an
older snapshot.

**Nothing restores and the snapshot list is empty.** Check you are looking at the
right account, and that the device was not deleted — deleting a device deletes its
snapshots.

## The desktop app

**It starts and closes immediately, or shows a build-tags error.** That dialog means
the binary was built with plain `go build`; Wails apps need `wails build`. Not a
concern for a release download.

**"Another OpenBackup window is already open".** It is in the notification area —
click the tray icon. If it is genuinely gone, delete `desktop.lock` from the state
directory.

**A blank window.** The WebView2 runtime is missing or broken on Windows. Install
Microsoft's Evergreen WebView2 runtime; `wails doctor` reports it if you are
building from source.

**It says the agent is not running.** The app is a client; the background service
does the work. Install it: **Diagnostics → start the service**, or
`openbackup service install`.

## Logs

Agent logs are in its state directory:

| | |
| --- | --- |
| Windows | `%LocalAppData%\OpenBackup` |
| macOS | `~/Library/Caches/OpenBackup` |
| Linux | `~/.cache/OpenBackup` |

For more detail, set `"log_level": "debug"` in the configuration and restart, or
run in the foreground:

```bash
openbackup service stop
openbackup run
```

Server logs are the container's: `docker compose logs -f`, JSON by default.

## Starting over on one machine

Safe, and does not touch what is already on the server:

```bash
openbackup service uninstall
# delete the state directory (the local index is a cache)
openbackup service install
openbackup backup
```

The next backup re-reads the files but uploads almost nothing, because the server
already has the blocks.

## Still stuck

[SUPPORT.md](../SUPPORT.md) says where to ask and what to include. `openbackup
doctor` output is the single most useful thing to attach.
