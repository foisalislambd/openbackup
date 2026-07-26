# OpenBackup documentation

Start at the top if you are setting this up for the first time; the guides below
that are reference material you can come back to.

## Setting it up

| | |
| --- | --- |
| [Install the server](install-server.md) | One command on a VPS, or Docker Compose by hand. TLS, reverse proxies, S3 storage. |
| [Install an agent](install-agent.md) | Windows, macOS and Linux. Enrolment, running as a service, the desktop app. |
| [The desktop app](desktop-app.md) | The window: what it does, and how it relates to the background service. |

## Using it

| | |
| --- | --- |
| [Backing up](backing-up.md) | What gets backed up and when, adding folders, pausing, limits. |
| [Restoring](restoring.md) | From the dashboard, from the CLI, from the app, and after losing a machine. |
| [What is not backed up](ignore-rules.md) | The default exclusions, project detection, and how to override them. |
| [End-to-end encryption](encryption.md) | Turning it on, the recovery code, and what you give up. |

## Reference

| | |
| --- | --- |
| [Configuration](configuration.md) | Every server environment variable and every agent setting. |
| [CLI reference](cli.md) | `openbackup` and `openbackup-server`, command by command. |
| [HTTP API](api.md) | The endpoints, the auth model, and the shape of the wire protocol. |
| [Architecture](architecture.md) | How chunking, snapshots, dedup and the store fit together. |
| [Security model](security-model.md) | What it protects against, what it does not, where the keys live. |

## Running and building

| | |
| --- | --- |
| [Operating a server](operations.md) | Upgrades, backing up the backup server, retention, GC, integrity checks, monitoring. |
| [Troubleshooting](troubleshooting.md) | The failures people actually hit, and what to do about them. |
| [FAQ](faq.md) | Short answers, including the awkward ones. |
| [Development](development.md) | Building from source, the test suite, the release process. |

## Elsewhere in the repository

- [README](../README.md) — the overview and the quickest path to a working setup
- [CONTRIBUTING](../CONTRIBUTING.md) — house style and what a good pull request looks like
- [SECURITY](../SECURITY.md) — how to report a vulnerability privately
- [SUPPORT](../SUPPORT.md) — where to ask questions
- [CHANGELOG](../CHANGELOG.md) — what changed
- [`web/`](../web/README.md) — the dashboard
- [`desktop/`](../desktop/README.md) — the desktop app's own notes
