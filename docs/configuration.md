# Configuration

Everything has a working default. This page is for when a default is wrong for
you, not something to read before installing.

## Server

Configuration is environment variables only — the target deployment is one
Compose file on a VPS, where editing a config file inside a container is friction
nobody needs.

### Basics

| Variable | Default | Meaning |
| --- | --- | --- |
| `OPENBACKUP_ADDR` | `:18200` | Listen address |
| `OPENBACKUP_DATA_DIR` | `./data` | Holds `openbackup.db` and `blobs/` |
| `OPENBACKUP_PUBLIC_URL` | — | The externally reachable base URL. Used in enrolment instructions, and an `https://` value turns on `Secure` session cookies |
| `OPENBACKUP_TRUST_PROXY` | `false` | Honour `X-Forwarded-For`. Required behind a proxy, dangerous when directly exposed |

### First account

| Variable | Default | Meaning |
| --- | --- | --- |
| `OPENBACKUP_ADMIN_EMAIL` | — | Creates the first account on an empty database |
| `OPENBACKUP_ADMIN_PASSWORD` | — | Its password. Requires the email to be set too |
| `OPENBACKUP_ALLOW_SIGNUP` | `true` | Whether the very first account may be created through the web form. Sign-ups are refused once an account exists either way |

### Storage and limits

| Variable | Default | Meaning |
| --- | --- | --- |
| `OPENBACKUP_RETENTION_DAYS` | `30` | Default retention for new accounts; `0` keeps everything. Per-account in the dashboard |
| `OPENBACKUP_QUOTA_BYTES` | `0` | Default per-account quota, `0` for unlimited. Accepts units: `500GB`, `2TiB` |
| `OPENBACKUP_GC_INTERVAL` | `1h` | How often retention, garbage collection and integrity checks run |
| `OPENBACKUP_MAX_CHUNK_BYTES` | `16MiB` | Largest single chunk upload accepted |
| `OPENBACKUP_MAX_BODY_BYTES` | `32MiB` | Largest JSON request body accepted |

### Sessions and enrolment

| Variable | Default | Meaning |
| --- | --- | --- |
| `OPENBACKUP_SESSION_TTL` | `720h` (30 days) | How long a dashboard login lasts |
| `OPENBACKUP_JOIN_TOKEN_TTL` | `24h` | How long a connection code stays valid |
| `OPENBACKUP_SECURE_COOKIES` | derived | Forces the `Secure` cookie flag. Defaults to on when `OPENBACKUP_PUBLIC_URL` is https |

### Privacy and logging

| Variable | Default | Meaning |
| --- | --- | --- |
| `OPENBACKUP_REQUIRE_ENCRYPTION` | `false` | Refuse unencrypted chunks server-wide |
| `OPENBACKUP_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `OPENBACKUP_LOG_JSON` | `true` | Structured logs, which is what you want in a container |

### S3-compatible blob storage

Set `OPENBACKUP_S3_ENDPOINT` and blocks go to object storage instead of the data
directory. The database stays local.

| Variable | Default | Meaning |
| --- | --- | --- |
| `OPENBACKUP_S3_ENDPOINT` | — | Host of the object store; enables this backend |
| `OPENBACKUP_S3_BUCKET` | — | Bucket name |
| `OPENBACKUP_S3_ACCESS_KEY` / `_SECRET_KEY` | — | Credentials |
| `OPENBACKUP_S3_REGION` | — | Region, where the provider needs one |
| `OPENBACKUP_S3_PREFIX` | — | Key prefix, to share a bucket |
| `OPENBACKUP_S3_USE_SSL` | `true` | Set `false` for a MinIO on a private network |

Durations accept Go syntax (`30m`, `24h`, `168h`). Sizes accept `K`, `M`, `G`,
`T`, `KB`, `MB`, `GB`, `TB`, `KiB`, `MiB`, `GiB`, `TiB`.

An invalid value fails at startup with a message naming the variable, rather than
being silently ignored.

## Agent

The agent keeps a JSON file. Most people never open it — the CLI, the desktop app
and the dashboard all write it — but it is documented so you can read it, and edit
it when scripting a fleet.

| | |
| --- | --- |
| Windows | `%AppData%\OpenBackup\config.json` |
| macOS | `~/Library/Application Support/OpenBackup/config.json` |
| Linux | `~/.config/OpenBackup/config.json` |

`OPENBACKUP_CONFIG` overrides the path, and `OPENBACKUP_STATE_DIR` overrides where
the local index and logs go. Setting both is how you run a throwaway second agent
for testing.

A complete file, with defaults shown:

```json
{
  "server_url": "https://backup.example.com",
  "device_id": "dev_...",
  "device_token": "...",
  "device_name": "Work laptop",

  "roots": [
    { "name": "documents", "path": "C:\\Users\\me\\Documents", "enabled": true, "detected": true },
    { "name": "projects", "path": "D:\\Projects", "enabled": true }
  ],
  "roots_chosen": true,

  "ignore": {
    "disabled_categories": [],
    "exclude": [],
    "include": [],
    "max_file_size_bytes": 0,
    "skip_hidden": false
  },

  "limits": {
    "upload_bytes_per_sec": 0,
    "max_cpu_percent": 70,
    "pause_on_metered": true,
    "pause_on_battery": false,
    "min_battery_percent": 20,
    "pause_while_fullscreen": true,
    "upload_concurrency": 3
  },

  "schedule": {
    "full_scan_interval": "12h",
    "debounce": "15s",
    "heartbeat_interval": "1m",
    "max_delta_chain_length": 24
  },

  "encryption": {
    "enabled": false,
    "key_id": "",
    "recovery_code": ""
  },

  "paused": false,
  "log_level": "info"
}
```

### What the less obvious fields mean

- **`roots[].name`** is a stable identifier used in snapshot paths, so renaming a
  folder on disk does not fork its backup history. **`enabled: false`** pauses one
  folder while keeping its backups. **`detected`** marks folders the agent found
  itself, so a future version can re-detect them without touching your additions.
- **`roots_chosen`** records that the folder list is yours. Without it, removing
  the last folder would look like a fresh install and every detected folder would
  come back on the next start.
- **`ignore`** is explained in [ignore-rules.md](ignore-rules.md).
  `max_file_size_bytes: 0` means the 8 GiB default; `-1` disables the limit.
- **`limits`** is what keeps the agent something people keep installed. Zero means
  unlimited for `upload_bytes_per_sec`. `pause_on_battery` is off by default
  because many laptops are never plugged in, and refusing to back one up would be
  a silent failure; `min_battery_percent` is the floor that applies regardless.
- **`schedule.debounce`** is the wait after a file stops changing.
  `full_scan_interval` is the safety net for changes the watcher missed.
  `max_delta_chain_length` forces a full backup after that many deltas, so a
  restore never walks an unbounded chain.
- **`encryption.recovery_code`** is the key in transcribable form, which is why the
  file is written owner-readable only. See [encryption.md](encryption.md).

### Editing it by hand

Changes are picked up on the next reload. Any `openbackup folders` command reloads
as a side effect, or restart the service:

```bash
openbackup service restart
```

Changing `encryption.key_id` needs a restart rather than a reload, and the agent
says so instead of quietly encrypting with two keys.

## Per-account settings on the server

These live in the dashboard (**Settings**) rather than in either config, because
they belong to the account and apply to every device on it: retention, quota,
upload ceiling and whether encryption is required. A device picks them up on its
next heartbeat, within a minute.
