# FAQ

## Is my data safe from the person running the server?

Not by default — the server can read your files, which is what makes restoring from
a browser possible. If you are not the operator, or you would rather not rely on
that, turn on [end-to-end encryption](encryption.md). The server then holds data it
cannot read, and you keep a recovery code that no one else has.

## What happens if I lose the recovery code?

The backup is gone. Not "contact support" gone — mathematically gone. That is the
property you asked for when you enabled encryption. Keep the code somewhere other
than the machine it protects.

## Will this slow my computer down?

It is built not to. The agent yields above 70% CPU, pauses while a full-screen app
is running, stays off metered connections, and idles at a few megabytes of RAM with
no measurable CPU. The desktop window, when open, costs about 10 MB.

If it ever does get in your way, that is a bug worth reporting — the whole point of
the resource governor is that you should not notice this software exists.

## How much storage will I need?

Roughly the size of the data for the first backup, less compression. History is far
cheaper: identical data is stored once, whether it repeats inside a file, across
your machines or across time, and each backup after the first records only changes.
Ninety days of a slowly changing home directory usually costs a small fraction more
than the first copy.

## Does it back up my whole system?

No, and deliberately. It backs up your files — Desktop, Documents, Pictures, Videos,
Music, Downloads and anything else you add. Windows, `/usr`, installed programs and
caches are excluded. This is not a disk imaging tool; it will not make a machine
bootable again, it will make your files reappear on a new one.

## Can I back up an external drive, or a network share?

An external drive, yes: `openbackup folders add /Volumes/Photos`. When it is
unplugged, the agent reports the folder as missing rather than treating the files as
deleted, so your backups are not silently reduced.

Network shares work when mounted as a path, but a watcher over the network is
unreliable, so changes are picked up on the twice-daily rescan instead of
immediately.

## Can several computers share one account?

Yes, and it is the intended setup. Devices deduplicate against each other, so the
second laptop with the same photo library costs almost nothing, and any device can
restore any other device's files — which is exactly what you need when the machine
you want to restore is the one that broke.

## Does it work on a phone?

Not yet. Android is planned; iOS is a much bigger problem because of the limits on
what a background app may do.

## Why not simply use restic, borg, Duplicati, or rsync?

Those are good tools, and if you already run a working backup with one, keep it. The
difference is where the effort goes. They give you a toolkit: you choose what to
include, write the schedule, and remember to test restores. This one ships opinions —
which folders, which exclusions, when to run, when to stop — so that the version you
get without making any decisions is already a correct backup, with a dashboard for
the family member who will not read a manual.

## Why HTTP and JSON instead of gRPC?

Because it has to work behind every corporate proxy, be debuggable with `curl`, and
be consumable by a browser without a translation layer. Chunk uploads are raw bytes,
so the format costs nothing where the volume actually is. See
[api.md](api.md).

## Why SQLite and not PostgreSQL?

The workload is tiny metadata writes from a handful of devices, which is what SQLite
in WAL mode is good at, and it means one container with one volume, no credentials to
manage and a database you can copy. The storage layer sits behind interfaces, so a
deployment that outgrows it has somewhere to go.

## Can I run the server on a Raspberry Pi or a NAS?

Yes. It is a single static binary with no CGO, and release builds cover 64-bit ARM.
Disk speed matters more than CPU.

## Does it protect me from ransomware?

It helps, and it is not a guarantee. Files encrypted in place arrive as new versions,
and older ones stay restorable within your retention window, which is the thing that
saves people. But an attacker with your dashboard credentials can delete backups, and
one with your machine can pause the agent. Long retention, and a copy of the server's
volume somewhere your workstation cannot reach, are what turn this into real
protection. See [security-model.md](security-model.md).

## What happens when I delete a file?

It stays in every backup taken before the deletion, and disappears from the ones
taken after, until those backups expire. Deleting a file is never propagated as a
deletion of your history.

## What if a backup is interrupted?

Nothing is lost and nothing is corrupted. Chunks already uploaded stay uploaded, so
the next attempt resumes rather than restarting. A snapshot left open is failed by
the server after six hours and its unreferenced blocks are collected.

## Can I change the server's address later?

The agents only know the URL, so a change means reconnecting each one. Put a domain
name you control in front of the server from the start and this never comes up.

## Is it really free?

Yes, MIT licensed, with no paid tier and nothing phoning home. You pay for the
server you chose to rent.

## How do I know it is actually working?

Restore something. Today, on a file you do not need — the dashboard makes it two
clicks. A backup nobody has restored is a hypothesis, and this is the one piece of
diligence worth insisting on with any backup tool, including this one.
