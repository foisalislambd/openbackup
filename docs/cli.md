# CLI reference

Two commands: `openbackup` on the machines being backed up, `openbackup-server`
on the server. Both print help without arguments, and neither has a subcommand
that needs a manual.

Anything that changes settings applies to the running background service
immediately, not on the next restart.

## openbackup

### Getting started

```bash
openbackup connect --server https://backup.example.com --code ABCD-EFGH-JKLM
```

| Flag | Meaning |
| --- | --- |
| `--server` | The dashboard's address |
| `--code` | A single-use connection code from **Devices** |
| `--name` | What this machine is called in the dashboard (default: its hostname) |
| `--encrypt` | Turn on end-to-end encryption and print a recovery code |
| `--recovery-code` | Use an existing recovery code, so this device deduplicates with your others |

```bash
openbackup service install     # run in the background from login onwards
```

### Everyday use

```bash
openbackup status              # is my data backed up, and if not, why not
openbackup backup              # back up now and wait for it
openbackup pause --for 2h      # stop for a while (no flag: until resumed)
openbackup resume
```

`status` names the reason whenever the agent is idle on purpose — high CPU, a
metered connection, a full-screen app, a low battery, a manual pause.

### Getting files back

```bash
openbackup snapshots                          # the backups on the server
openbackup find "tax return"                  # search the latest backup by name
                                              #   --snapshot <id>, --limit 25
openbackup restore --path Documents/report.docx --to .
openbackup restore --snapshot snp_06fss... --to ./recovered
```

| Flag | Default | Meaning |
| --- | --- | --- |
| `--snapshot` | `latest` | Which backup to restore from |
| `--path` | everything | A file or folder inside the backup |
| `--to` | `./restored-<date>` | Where to write |
| `--overwrite` | off | Replace files that already exist |
| `--keep-both` | off | Write restored copies alongside existing files |
| `--dry-run` | off | Show what would be restored, write nothing |

Without `--overwrite` or `--keep-both`, existing files are skipped: a restore must
not destroy the file you still had. More in [restoring.md](restoring.md).

### Folders

```bash
openbackup folders                   # what is backed up, and what is skipped
openbackup folders add ~/Projects    # include another folder, on any drive
openbackup folders remove ~/Projects # stop backing it up; its backups remain
openbackup folders off ~/Projects    # pause one folder
openbackup folders on ~/Projects
openbackup rules                     # every exclusion rule, with its reason
```

### Encryption

```bash
openbackup encrypt                                   # turn it on, print a recovery code
openbackup encrypt --recovery-code ABCD-EFGH-...     # adopt an existing key
```

See [encryption.md](encryption.md) — particularly the part about what happens if
you lose the code.

### Limits and diagnostics

```bash
openbackup limit --upload 5MB    # cap the upload speed (0 for no limit)
openbackup doctor                # check configuration, service, server, folders, backups
openbackup run                   # run in the foreground, for debugging
openbackup version
```

`doctor` is what to run when something looks wrong, and what to paste into an
issue.

### Service management

```bash
openbackup service install|uninstall|start|stop|restart|status|run
```

Uses the native mechanism on each platform: a Windows service, a systemd unit, or
a launchd agent. `uninstall` leaves nothing behind.

### Global flags

| Flag | Meaning |
| --- | --- |
| `--config <path>` | Use a different configuration file |

`OPENBACKUP_CONFIG` and `OPENBACKUP_STATE_DIR` do the same for the config file and
the state directory, which is how you run a second agent side by side.

## openbackup-server

```bash
openbackup-server                    # same as: serve
openbackup-server serve --addr :9000
```

| Command | What it does |
| --- | --- |
| `serve` | Run the server. The default when no command is given |
| `invite [--email]` | Create a device connection code and print the enrolment command |
| `user add --email <email> [--password <password>]` | Create an account; generates a password if none is given |
| `check [--fix]` | Verify that stored blocks match the index, and list snapshots that would fail to restore. `--fix` deletes stored objects the index no longer references |
| `health` | Probe a running server. Used by the container healthcheck |
| `version` | Print the build version |

Under Docker, prefix these with `docker compose exec openbackup`:

```bash
docker compose exec openbackup openbackup-server invite
docker compose exec openbackup openbackup-server check --fix
```

Configuration is environment variables; `openbackup-server --help` lists the
common ones and [configuration.md](configuration.md) has all of them.

## Exit codes

Both commands exit `0` on success and non-zero with a message on `stderr`
otherwise, so they compose in scripts. `openbackup doctor` exits non-zero when a
check fails, which makes it usable as a monitoring probe:

```bash
openbackup doctor >/dev/null || notify-send "backups need attention"
```
