# Getting help

## Try these first

- **Something is not being backed up:** `openbackup folders` shows what is
  included, `openbackup rules` explains why a path is skipped, and
  `openbackup doctor` checks the whole chain.
- **Nothing is happening at all:** `openbackup status`. The agent pauses on
  purpose — high CPU, a metered connection, a full-screen app, a low battery —
  and says which of those it is.
- **Anything else:** [docs/troubleshooting.md](docs/troubleshooting.md) covers
  the failures people actually hit, and [docs/faq.md](docs/faq.md) covers the
  questions.

## Where to ask

| What | Where |
| --- | --- |
| A question, or "am I doing this right" | [GitHub Discussions](https://github.com/foisalislambd/openbackup/discussions) |
| A bug, a wrong result, a crash | [Open an issue](https://github.com/foisalislambd/openbackup/issues/new/choose) |
| A security problem | Privately — see [SECURITY.md](SECURITY.md) |

This is a volunteer project, so answers come when someone has time. A question
with the output of `openbackup doctor` attached gets answered faster than one
without.

## What to include in a bug report

The issue template asks for these, and they are what actually shortens the
conversation:

1. What you expected, and what happened instead.
2. `openbackup doctor` output, and `openbackup-server version` if the server is
   involved.
3. Your platform, and whether the agent runs as a service or in the foreground.
4. Relevant log lines. The agent logs to its state directory:
   `%LocalAppData%\OpenBackup` on Windows, `~/Library/Caches/OpenBackup` on
   macOS, `~/.cache/OpenBackup` on Linux. Server logs are the container's:
   `docker compose logs`.

Redact paths and filenames you would rather not publish — the shape of the path
is usually enough.

## What this project will not do for you

- Recover a backup whose recovery code is lost. With end-to-end encryption on,
  the key never reaches the server, and there is no way around that by design.
- Act as a support desk for a fork or a modified build. Reproduce it on a release
  first.
