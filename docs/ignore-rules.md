# What is not backed up

Most backup tools either upload everything, which wastes storage and bandwidth on
files that can be recreated by typing one command, or make you write exclusion
patterns, which means the first version of your backup is wrong. This one ships
opinions, explains each of them, and lets you override any of them.

```bash
openbackup rules        # every rule, with its reason
```

The dashboard shows the same list under **Settings → What is not backed up**.

## The categories

| Category | What it covers | Why |
| --- | --- | --- |
| `system` | `C:\Windows`, `Program Files`, `ProgramData`, `$Recycle.Bin`, `System Volume Information`, `/proc`, `/sys`, `/dev`, `/run`, `/tmp`, `/usr`, `/System`, `/Library` | Operating system and installed software. Reinstallable, and often not even readable |
| `junk` | `Thumbs.db`, `.DS_Store`, `desktop.ini`, `.Trash`, recycle bins | Regenerated automatically; noise in a restore |
| `cache` | browser caches, `~/.cache`, package manager caches, thumbnail caches | Large, constantly rewritten, worthless once restored |
| `developer` | `.git`, `node_modules`, `vendor`, `target`, `build`, `dist`, `.next`, `venv`, `__pycache__`, `bin`, `obj` and similar | Reproducible from the sources next to them (or from a git remote) — see the scoping rule below |
| `virtualisation` | `.vdi`, `.vmdk`, `.qcow2`, `.vhdx`, Docker images | Tens of gigabytes that change on every boot |
| `ephemeral` | `*.tmp`, `*.part`, `~$*.docx`, crash dumps, swap and hibernation files | Half-written files and things the OS recreates |

Beyond the categories, files larger than 8 GiB are skipped by default, and the
agent never follows a symbolic link out of a backed-up folder.

## Why `node_modules` is not simply banned

A pattern like `build` or `bin` is right inside a software project and wrong
everywhere else. Someone with `Pictures/Portraits/build/` — a folder they named
for a photo series — would lose it silently, which is exactly the kind of quiet
failure that makes backup software untrustworthy.

So developer rules are *scoped*. The agent looks for a marker file that identifies
a project — `package.json`, `go.mod`, `Cargo.toml`, `pom.xml`, `composer.json`,
`Gemfile`, `pubspec.yaml`, `*.csproj`, `*.xcodeproj` and 23 others, 32 in total —
and only applies that ecosystem's exclusions beneath the directory holding it. A
`node_modules` next to a `package.json` is dependency output; a folder with the
same name anywhere else is your data and gets backed up.

Source code always gets backed up. Lockfiles too, since they are what make the
dependencies reproducible.

The rules are per ecosystem rather than one big list, which is why a Rust project
loses `target/` but keeps `build/`, and a Unity project loses `Library/` — a name
that would be dangerous to exclude globally.

## Overriding

Add something the rules skipped:

```bash
openbackup folders add ~/Projects/client-site/dist
```

An explicitly added folder is backed up, rules or not: telling the agent to back
something up outranks a default.

For finer control, edit the agent configuration
([configuration.md](configuration.md)):

```json
{
  "ignore": {
    "disabled_categories": ["developer"],
    "exclude": ["*.iso", "Downloads/torrents"],
    "include": ["Projects/**/dist"],
    "max_file_size_bytes": -1,
    "skip_hidden": false
  }
}
```

- `disabled_categories` turns off a whole category — `developer` if you want
  dependencies backed up as well.
- `exclude` adds your own patterns, gitignore-style: `*` and `?` within a path
  segment, `**` across segments, a trailing `/` for directories only, a leading
  `/` to anchor at the backup root.
- `include` wins over any exclusion, including the defaults.
- `max_file_size_bytes`: `0` is the 8 GiB default, `-1` removes the limit.
- Hidden files are backed up by default; `skip_hidden` turns that off.

Restart or reload the agent after editing by hand — `openbackup service restart`,
or any `openbackup folders` command, which reloads as a side effect.

## Checking what a rule did

`openbackup rules` explains the defaults. To find out what happened to one path,
run a backup and look at what it reports as skipped, or check **Activity** in the
dashboard: skipped files are logged with the rule that matched, not silently
dropped.

If a file you wanted is missing and no rule explains it, that is a bug worth
reporting — see [SUPPORT.md](../SUPPORT.md).
