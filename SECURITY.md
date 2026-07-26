# Security policy

This software holds people's personal files. A vulnerability here is not an
inconvenience, so reports are welcome and taken seriously.

## Reporting a vulnerability

**Do not open a public issue.** Use GitHub's private vulnerability reporting:

> [Report a vulnerability](https://github.com/foisalislambd/openbackup/security/advisories/new)

That opens a private advisory visible only to you and the maintainers. If you
cannot use it, contact a maintainer through their GitHub profile and ask for a
private channel.

Please include, as far as you can:

- what an attacker gains, and what position they need to start from (network
  path? an enrolled device? a dashboard login? local access to the machine?)
- the affected component: server, agent, dashboard, desktop app, or an installer
- the version (`openbackup version`, `openbackup-server version`) and platform
- steps or a proof of concept, and any relevant log lines with paths redacted

Reports in any language are fine, and a rough report now is better than a polished
one next month.

### What to expect

There is no paid bounty programme and no service-level agreement — this is a
volunteer project, and promising a response time we cannot keep would be worse
than saying so. In practice:

- an acknowledgement as soon as a maintainer sees the report
- an assessment of severity, and whether it is in scope, in the same thread
- a fix released before public disclosure, with credit if you want it
- if a report is declined, an explanation of why

Please give us a reasonable chance to ship a fix before publishing. If a flaw is
being exploited, say so — that changes the order things happen in.

## Supported versions

Only the latest release is supported. Fixes go to `main` and into the next
release; there are no maintenance branches. If you are running from a checkout,
update before reporting.

## Scope

In scope, and interesting:

- reading, deleting or modifying another account's data or metadata
- authentication or session flaws: enrolment code reuse, token forgery, session
  fixation, privilege escalation to admin
- the agent writing outside its target directory — a restore that escapes the
  destination through `..`, a symlink or an absolute path in a snapshot
- the agent reading, modifying or deleting anything outside the folders it is
  configured to back up
- weaknesses in the encryption: key derivation, nonce handling, a plaintext leak
  in metadata, anything that lets the server recover content from an
  end-to-end-encrypted backup
- integrity failures: a restore that silently produces wrong bytes, a chunk
  accepted under a hash it does not match
- remote crashes or resource exhaustion reachable without credentials
- privilege escalation through the installers or the background service
  definitions (`scripts/install-server.sh`, the served `install.sh`,
  `openbackup service install`)

## Not vulnerabilities

These are known, documented properties of the design. Reporting them is not
useful, though arguing that a default is wrong is a fair feature discussion.

- **The server can read your files when end-to-end encryption is off.** That is
  the default, because it is what makes browser-based restore possible. Turn on
  `openbackup encrypt` if you do not want that; see
  [docs/encryption.md](docs/encryption.md).
- **The recovery code is stored on the device, owner-readable.** An unattended
  agent has to be able to encrypt after a reboot with nobody logged in. Anyone
  who can read your user's files can already read the files being backed up.
- **Plain HTTP exposes device tokens.** Documented in every deployment path: run
  the server behind TLS.
- **An enrolled device can read the account's other backups.** Restoring a dead
  laptop onto a new one depends on it.
- **The dashboard has no rate limit on browsing**, only on login.
- **The server trusts `X-Forwarded-For` when `OPENBACKUP_TRUST_PROXY=true`.**
  That is what the setting means; do not enable it while directly exposed.
- Missing hardening headers with no demonstrated impact, results from an
  automated scanner with no exploit path, or anything requiring the attacker to
  already be root on the machine being backed up.

## Threat model

[docs/security-model.md](docs/security-model.md) states plainly what the system
protects against, what it does not, and which key material lives where. Reading
it first will tell you whether a behaviour is a bug or a documented trade-off.
