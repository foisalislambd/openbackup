# Operating a server

Day-to-day running of the server. The short version: it looks after itself, but
the volume is the only copy of your backups, so that part is on you.

## Back up the backup server

A backup server holding the only copy of your backups is still a single point of
failure. Everything that matters is in one volume: `openbackup.db` and `blobs/`.

The database is SQLite in WAL mode, so copy it with the tooling that understands
that rather than `cp` while it runs:

```bash
docker compose exec openbackup sh -c 'sqlite3 /data/openbackup.db ".backup /data/backup.db"'
```

The image has no shell, so in practice the simple route is a short stop:

```bash
cd /opt/openbackup
docker compose stop
tar -C /var/lib/docker/volumes/openbackup_openbackup-data/_data -czf /backup/openbackup-$(date +%F).tar.gz .
docker compose start
```

Blocks are immutable and content-addressed, so an incremental file-level copy of
`blobs/` (rsync, restic, a provider snapshot) is efficient and safe. The database
is the part that needs a consistent copy.

If you use the S3 backend, the blocks are already off the machine — but the
database is not, and without it the blocks are meaningless.

## Upgrading

```bash
cd /opt/openbackup
docker compose pull
docker compose up -d --wait
```

Schema migrations run at startup and are logged. **Upgrade the server before the
agents**: the protocol is additive within a major version, so an older agent keeps
working against a newer server, but not necessarily the reverse.

Agents upgrade by re-running the installer, which replaces the binary and restarts
the service:

```bash
curl -fsSL https://backup.example.com/install.sh | sh
```

Take a copy of the volume before a major version upgrade. Rolling back an image is
easy; rolling back a migrated database is not.

## What runs automatically

Once an hour by default (`OPENBACKUP_GC_INTERVAL`), a maintenance pass:

- **Fails stale snapshots.** One left "running" for six hours is assumed to belong
  to an agent that died, so it stops holding storage and stops looking like an
  in-progress backup.
- **Applies retention.** Snapshots past the account's window are deleted, along
  with the deltas depending on them. The newest complete snapshot is always kept,
  however old.
- **Collects garbage.** Blocks no longer referenced by any snapshot are deleted, in
  batches of 5000 so uploads are never blocked for long.
- **Prunes the activity log** to 30 days or 200,000 rows, whichever comes first.
- **Clears expired sessions and connection codes.**

It logs a summary of each pass: how many snapshots expired, how many blocks were
deleted, how much space came back.

## Checking integrity

```bash
docker compose exec openbackup openbackup-server check
docker compose exec openbackup openbackup-server check --fix
```

This compares the index against the block store both ways. It reports blocks that
are referenced but missing — the state that turns a backup into a lie — and names
every snapshot that would fail to restore, exiting non-zero when there are any.
It also reports orphaned objects, stored but referenced by nothing, which `--fix`
deletes to reclaim the space.

Worth running after a disk scare, after moving the volume, and occasionally for
its own sake.

## Accounts, devices and codes

```bash
docker compose exec openbackup openbackup-server user add --email someone@example.com
docker compose exec openbackup openbackup-server invite
```

Retention, quota, upload ceiling and required encryption are per account in the
dashboard (**Settings**); devices pick changes up on the next heartbeat, within a
minute.

Removing a device in the dashboard revokes its token immediately and deletes its
snapshots; the storage returns on the next maintenance pass.

## Storage planning

Deduplication and delta snapshots make history much cheaper than a naive estimate,
but the first backup is the honest number: roughly the size of the data, less
compression. Ninety days of a slowly changing home directory typically costs a
small fraction more than that.

The dashboard shows totals per device and the trend over time. To cap it, set a
quota — the agent is told the moment an upload is refused and reports it as a
quota error rather than failing silently.

Free space on the volume is shown in the dashboard too, because the failure mode
of a full disk is much less pleasant than a refused upload.

## Monitoring

- `GET /api/v1/health` for a liveness probe. `openbackup-server health` does the
  same from inside the container, which is what the image's healthcheck uses.
- The dashboard's **Overview** answers "is every device backing up", and
  **Activity** shows errors with the device that reported them.
- On a machine you care about, `openbackup doctor` exits non-zero when something is
  wrong, so it composes with whatever alerting you already have:

  ```bash
  openbackup doctor >/dev/null || notify-send "backups need attention"
  ```

- Logs are JSON by default (`OPENBACKUP_LOG_JSON`), so `docker compose logs` feeds
  a log collector without parsing tricks.

## Moving the server

1. Stop it.
2. Copy the volume to the new machine.
3. Start it there with the same `OPENBACKUP_PUBLIC_URL`.

Nothing on the agents changes, because they only know the URL. If the URL has to
change, the agents need reconnecting — so if you can, put a name you control in
front of it from the start.

## Removing everything

```bash
cd /opt/openbackup
docker compose down -v      # -v deletes the volume, and every backup in it
rm -rf /opt/openbackup
```

On each machine, `openbackup service uninstall` and delete the binary, the
configuration and the state directory. Nothing else was ever touched.
