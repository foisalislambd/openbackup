# Restoring

A backup you have never restored is a hypothesis. Restore something today, while
nothing is wrong — it takes two minutes, and it is the only way to know the whole
chain works.

Restoring never overwrites a file that already exists unless you ask it to. The
default is to skip, because a restore must not be the thing that destroys the file
you still had.

## From the dashboard

Sign in, open **Backups**, pick a point in time, and browse it one folder at a
time. Download a single file, or a whole folder as a ZIP that is streamed rather
than assembled on the server first.

This is the fastest route when you are away from the machine, and it needs nothing
installed. It is unavailable for end-to-end-encrypted backups: the server holds
data it cannot read, so decryption has to happen on a device that has the key.

## From the desktop app

**Restore** browses backups, searches by name, and writes files back with a native
folder picker for the destination. Progress is shown per file, and a restore in
progress can be cancelled. See [desktop-app.md](desktop-app.md).

## From the command line

Find it first if you are not sure where it lived:

```bash
openbackup find "tax return"
```

Then restore. The default destination is a new `./restored-<date>` directory, so
the safe version of the command is also the short one:

```bash
# one file, into the current directory
openbackup restore --path "Documents/report.docx" --to .

# a whole folder from a specific backup
openbackup snapshots                       # pick an id
openbackup restore --snapshot snp_06fss... --path Documents --to ./recovered

# everything from the most recent backup
openbackup restore --to ./recovered
```

Flags that change how conflicts are handled:

| Flag | Effect |
| --- | --- |
| *(none)* | Existing files are skipped and counted |
| `--overwrite` | Replace files that already exist |
| `--keep-both` | Write the restored copy alongside, with a suffix |
| `--dry-run` | List what would be written, touch nothing |

`--snapshot` accepts `latest` (the default) or an id from `openbackup snapshots`.
Paths are as they appear in the backup — `openbackup find` prints the exact
string, and the ready-made command to restore it.

## Restoring onto a new computer

This is the case that matters, and it works without the old machine:

1. Install the agent on the new computer ([install-agent.md](install-agent.md)).
2. Connect it with a fresh code. Any device on the account can read the account's
   backups, so it can see the dead laptop's files.
3. If the backups are encrypted, enter the recovery code during `connect`, or
   afterwards with `openbackup encrypt --recovery-code <code>`. Without it the
   data cannot be decrypted by anyone, including us.
4. `openbackup snapshots` to find the last backup of the old machine, then
   restore into place.

Restore into a staging directory and move things across, rather than restoring
directly over a fresh profile. Restored files keep their modification times and
permissions; ownership follows the user doing the restore.

## What restoring cannot do

- **Reconstruct what was never backed up.** Excluded paths are excluded, and
  `openbackup rules` shows which ones. If it mattered, add it before you need it.
- **Recover an encrypted backup without the recovery code.** There is no escrow,
  no reset, no support route. That is the point of it.
- **Restore a system.** This backs up your files, not Windows, not installed
  programs, not a bootable image.

## Verifying a restore

For anything important, check the bytes rather than trusting a progress bar:

```bash
openbackup restore --path Documents/report.docx --to /tmp/check
cmp "/tmp/check/Documents/report.docx" ~/Documents/report.docx && echo identical
```

Every chunk is verified against its BLAKE3 hash as it is written, so a corrupted
transfer fails loudly rather than producing a plausible-looking file. On the
server side, `openbackup-server check` audits the stored blocks against the index;
see [operations.md](operations.md).
