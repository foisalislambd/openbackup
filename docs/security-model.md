# Security model

What this protects against, what it does not, and where the keys live. Read this
before deciding whether the defaults suit you — and before reporting something as
a vulnerability, since several of the items below are deliberate.

To report an actual flaw, see [SECURITY.md](../SECURITY.md).

## What it defends against

- **Someone on the network between you and your server.** Everything travels over
  TLS, which you terminate. Device tokens and session cookies are useless to an
  observer who cannot break it.
- **A stranger finding your server.** After the first account exists, sign-ups are
  refused, so an exposed instance is not an open storage relay. Enrolment needs a
  single-use code that expires. Login attempts are rate limited per address, and
  passwords are hashed with Argon2id.
- **Another account on the same server.** Every query is scoped by account. A
  device can read its own account's backups and nothing else.
- **A stolen device token.** It grants that one account's backups, and revoking the
  device in the dashboard invalidates it immediately — there is no signed token
  that stays valid until it expires.
- **Corrupted or tampered data.** Every chunk is verified against its BLAKE3 digest
  on restore. With encryption on, the AEAD also fails on modified ciphertext, so a
  server that changed a block cannot make you restore its version silently.
- **A malicious snapshot during restore.** Paths from the server are untrusted
  input: absolute paths, `..` and symlink tricks are rejected, and a restore writes
  only inside its destination.
- **The backup itself doing damage.** The agent reads only configured folders and
  writes only to its own state directory and to restore destinations you name. It
  never needs root on the machine being backed up, and the recommended install is
  per-user.

## What it does not defend against

- **A server operator reading your files, when end-to-end encryption is off.**
  That is the default, because it enables browser restore. If the operator is
  someone other than you, turn on [encryption](encryption.md).
- **Metadata, always.** Even with encryption on, the server sees paths, file sizes,
  modification times and how much you back up. That is what it needs to serve a
  listing and expire old snapshots. Directory and file *names* are metadata.
- **Anyone with your user account on the machine being backed up.** They can read
  the agent's configuration, which holds the device token and — with encryption on
  — the recovery code. They can also read the files being backed up, so this adds
  no exposure. It does mean an unattended agent's key is only as protected as the
  machine and its disk encryption.
- **Root on the machine being backed up.** Same reasoning, without limits.
- **A compromised server serving a malicious binary.** `install.sh` is served by
  your own server and downloads a release from GitHub. If your server is
  compromised, so is anything it hands you.
- **Ransomware, on its own.** Version history helps a lot: encrypted-in-place files
  arrive as new versions, and older ones remain restorable within your retention
  window. But an attacker with your account credentials can delete backups from the
  dashboard, and one with your machine can pause the agent. Long retention and
  server backups outside the reach of your workstation are what turn this into real
  protection.
- **Someone who wants your recovery code, if you stored it badly.** A screenshot in
  your Documents folder defeats the whole feature — and gets backed up.

## Where key material lives

| | Where | Protection |
| --- | --- | --- |
| Encryption master key | The agent's config file, as a recovery code | Owner-readable only; never sent to the server |
| Its public identifier (`KeyID`) | Config, and each snapshot on the server | Not secret; used to tell you "wrong key" |
| Device token | The agent's config file | Owner-readable only; sent as a bearer header over TLS |
| Its hash | The server's database | BLAKE3; the plaintext token is not stored |
| Account password | Nowhere | Only an Argon2id hash is stored |
| Session id | An HttpOnly cookie, and the database as a hash | `Secure` when the server knows it is behind https |

The server never receives the encryption key. There is no escrow and no reset —
which is exactly why losing the recovery code means losing the backup.

## Choices worth knowing about

**Opaque sessions rather than JWTs.** Revocation that works beats statelessness we
do not need: a handful of devices per account produces no scaling problem, and
"sign out everywhere" has to mean it.

**A convergent nonce for encryption.** The nonce is derived from the chunk's content
hash rather than being random, so identical plaintext produces identical ciphertext
and deduplication still works. The trade-off is that the server can tell that two
chunks are identical — including across your own devices. With a fixed key and
content-derived nonces, a nonce is never reused with different plaintext, which is
the property XChaCha20-Poly1305 needs.

**Compress, then encrypt.** In that order because encrypted data does not compress.
Compressed sizes leak a little about content, as they do for TLS.

**The recovery code on disk.** An unattended service must be able to encrypt after
a reboot with nobody logged in. The alternative — prompting for a passphrase — means
a machine that stops backing up after every restart, which is a worse outcome than
this trade-off. Full-disk encryption is the right companion.

**Plaintext by default.** Most people running this are their own server operator,
and browser restore is the feature they use. The dashboard says clearly when
encryption is off, and an operator can require it globally with
`OPENBACKUP_REQUIRE_ENCRYPTION=true`.

## Hardening a deployment

- Terminate TLS, set `OPENBACKUP_PUBLIC_URL` to the https address, and set
  `OPENBACKUP_TRUST_PROXY=true` only while actually proxied.
- Keep the container as the Compose file ships it: `read_only`,
  `no-new-privileges`, no exposed port beyond localhost when a proxy fronts it.
- Bind to `127.0.0.1` and let the proxy reach it, so the server is never briefly
  exposed without TLS.
- Change the generated admin password after the first sign-in, and remove
  `OPENBACKUP_ADMIN_*` from the environment.
- Turn on encryption for devices whose files should not be readable by the server,
  and consider requiring it account-wide.
- Back up the server's volume somewhere the workstations cannot write; see
  [operations.md](operations.md).
