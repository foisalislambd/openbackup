# End-to-end encryption

By default your files are compressed and sent over TLS, and the server can read
them. That is a deliberate default: it is what lets you restore a file from a
browser on someone else's computer, and on a server you own it is often exactly
what you want.

Turn on end-to-end encryption and that changes: chunks are encrypted on the device
before upload, the key never leaves your machines, and the server stores data it
cannot read — including your file *contents*. Paths, sizes and modification times
remain visible to the server, because it needs them to serve a listing and to
expire old snapshots.

## Turning it on

```bash
openbackup encrypt
```

It prints a **recovery code**. That code is the key, in a form you can write down.

```
Write this down and keep it somewhere safe:

    ABCD-EFGH-JKLM-NPQR-STUV-WXYZ-2345-6789

Without it, an encrypted backup cannot be recovered by anyone, including the
person running the server.
```

Store it outside the machine it protects — a password manager, a piece of paper in
a drawer. A copy on the desktop of the laptop you are backing up is not a copy.

You can also enable it during enrolment, which is the cleanest moment because
nothing has been uploaded yet:

```bash
openbackup connect --server URL --code CODE --encrypt
```

## Using the same key on your other computers

Deduplication works across devices only if they encrypt identically, so give the
other machines the same code:

```bash
openbackup encrypt --recovery-code ABCD-EFGH-JKLM-NPQR-STUV-WXYZ-2345-6789
```

Same on a replacement machine when restoring after a loss. Without the code, the
backups are unreadable — there is no escrow and no reset.

## Turning it on later, on a device that already has backups

You cannot mix the two on one device. Existing unencrypted snapshots stay readable
by the server, and a device that switched would leave a backup history where some
parts can be restored in a browser and others cannot. Rather than half-encrypt,
the agent refuses.

To switch: remove the device in the dashboard, which drops its snapshots, then
connect it again with `--encrypt`. You are choosing to discard history for privacy;
if the history matters more, keep the device as it is and enable encryption on the
next machine you set up.

Turning encryption *off* is not supported at all. The data on the server cannot be
decrypted by the server, so there is nothing to convert.

## What you give up

- **Browser restore.** The dashboard can list an encrypted backup but not download
  from it. Restore with the CLI or the desktop app on a device that has the key.
- **Server-side ZIP downloads**, for the same reason.
- **Recovery if you lose the code.** This is not a limitation to work around; it
  is the property you asked for.

## Requiring it

An operator who never wants readable data on the disk they administer can refuse
plaintext uploads server-wide:

```yaml
OPENBACKUP_REQUIRE_ENCRYPTION: 'true'
```

Or per account, in the dashboard under **Settings**. An account that already has
unencrypted backups cannot enable this until those are removed, so the setting
cannot create a half-protected state.

## How it works

- The master key is 32 bytes from the operating system's random source. The
  recovery code is that key in a transcribable alphabet with a checksum, so a
  mistyped code is rejected rather than silently producing wrong data.
- Per-chunk keys are derived with **Argon2id**, which makes a guess at the key
  expensive rather than instant.
- Chunks are encrypted with **XChaCha20-Poly1305**: authenticated, so tampering is
  detected on restore rather than producing garbage.
- The nonce is derived from the chunk's content hash rather than being random.
  That is what keeps deduplication working — the same block encrypts to the same
  ciphertext, so it is still stored once — and it is safe here because the key is
  fixed and the content determines the nonce, so a nonce is never reused with
  different plaintext.
- `KeyID` is a public identifier for the key. It is stored with each snapshot so a
  restore can tell you "this backup needs a different key" instead of failing with
  a decryption error.
- The recovery code is written into the agent's configuration file, owner-readable
  only. An unattended service has to be able to encrypt after a reboot with nobody
  logged in, and anyone who can read that file can already read the files being
  backed up. [security-model.md](security-model.md) is explicit about what this
  does and does not defend against.

The format details are in [`internal/codec`](../internal/codec), and the
compatibility rule is in [architecture.md](architecture.md).
