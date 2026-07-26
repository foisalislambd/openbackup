# HTTP API

Everything is HTTP with JSON bodies, except chunk uploads, which are raw bytes.
There is no separate API server: the same process serves the agent protocol, the
dashboard's API and the dashboard itself.

This is documented so you can debug with `curl` and script against it. It is not a
stability promise — the protocol is additive within a major version, but the
dashboard endpoints follow whatever the dashboard needs.

Base path is `/api/v1`. `Content-Type: application/json` unless noted.

## Two audiences, two auth schemes

| | Agents | Dashboard |
| --- | --- | --- |
| Paths | `/api/v1/agent/…` | `/api/v1/ui/…` |
| Credential | `Authorization: Bearer <device token>` | `openbackup_session` cookie, HttpOnly |
| Obtained by | `POST /agent/enroll` with a connection code | `POST /ui/login` |

Device tokens do not expire; a device is revoked by deleting it in the dashboard,
which invalidates the token immediately. Sessions are opaque and stored
server-side, so signing out takes effect at once rather than when a token would
have expired. They last 30 days by default and carry the `Secure` flag when the
server knows it is behind https.

Errors are uniform:

```json
{ "error": "quota exceeded", "code": "quota_exceeded" }
```

The `code` is what clients branch on. `quota_exceeded` and
`encryption_required` are the two an agent handles specially; the rest are
reported to the user as they are.

## Health

```
GET /api/v1/health
```

No authentication. Returns `200` with a small JSON body when the server is
serving. This is what the container healthcheck and `openbackup-server health`
probe.

## Agent protocol

| Method and path | Purpose |
| --- | --- |
| `POST /api/v1/agent/enroll` | Exchange a connection code for a device id and token |
| `GET /api/v1/agent/device` | This device as the server sees it, plus the account policy |
| `POST /api/v1/agent/heartbeat` | Report state and receive commands and policy |
| `POST /api/v1/agent/chunks/missing` | Send digests, get back the ones to upload |
| `PUT /api/v1/agent/chunks/{digest}` | Upload one chunk, body is raw bytes |
| `GET /api/v1/agent/chunks/{digest}` | Download one chunk, for restore |
| `POST /api/v1/agent/snapshots` | Start a snapshot (full, or a delta with a parent) |
| `GET /api/v1/agent/snapshots` | List the account's snapshots |
| `POST /api/v1/agent/snapshots/{id}/entries` | Add a batch of entries and deletions |
| `GET /api/v1/agent/snapshots/{id}/entries` | List a snapshot's entries |
| `POST /api/v1/agent/snapshots/{id}/complete` | Finish the snapshot |
| `PUT` / `GET /api/v1/agent/key` | Store and read the encryption key's public identifier |
| `POST /api/v1/agent/events` | Report activity for the dashboard's log |

### A backup, in five requests

```bash
TOKEN=...   # from enrolment
S=https://backup.example.com

# 1. which of these chunks does the server not have?
curl -s -X POST "$S/api/v1/agent/chunks/missing" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"digests":["a1b2…","c3d4…"]}'

# 2. upload the ones it named, one PUT each, raw body
curl -s -X PUT "$S/api/v1/agent/chunks/a1b2…" \
  -H "Authorization: Bearer $TOKEN" \
  --data-binary @chunk.bin

# 3. open a snapshot
curl -s -X POST "$S/api/v1/agent/snapshots" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"kind":"full"}'

# 4. add entries (batched; the agent sends 500 at a time)
curl -s -X POST "$S/api/v1/agent/snapshots/$ID/entries" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"entries":[{"path":"Documents/a.txt","type":"file","size":18,"digest":"…","chunks":["a1b2…"]}]}'

# 5. complete it
curl -s -X POST "$S/api/v1/agent/snapshots/$ID/complete" \
  -H "Authorization: Bearer $TOKEN" -d '{}'
```

A snapshot that is opened and never completed is failed by the server after six
hours, and its unreferenced chunks are collected. Nothing an interrupted agent
leaves behind is permanent.

### Heartbeats carry both directions

The agent posts its state — idle, scanning, uploading, paused with a reason, plus
queue sizes and resource use — and the response carries the account policy
(quota, retention, upload ceiling, whether encryption is required) and any queued
commands: back up now, pause, resume, reload configuration, forget this device.
That is how a change made in the dashboard reaches a machine you are not sitting
at, within a minute.

## Dashboard API

| Method and path | Purpose |
| --- | --- |
| `GET /api/v1/ui/bootstrap` | Whether setup is needed and whether this browser is signed in |
| `POST /api/v1/ui/setup` | Create the first account |
| `POST /api/v1/ui/login`, `POST /api/v1/ui/logout` | Session in, session out |
| `GET /api/v1/ui/me`, `POST /api/v1/ui/password` | The current account |
| `GET /api/v1/ui/devices` | Devices with their status |
| `PATCH` / `DELETE /api/v1/ui/devices/{id}` | Rename, or remove with its snapshots |
| `POST /api/v1/ui/devices/{id}/commands` | Queue a command for that device |
| `GET /api/v1/ui/join-tokens`, `POST …` | List and create connection codes |
| `GET /api/v1/ui/usage`, `GET /api/v1/ui/history` | Storage totals and the trend |
| `GET /api/v1/ui/snapshots`, `GET …/{id}`, `DELETE …/{id}` | Browse and remove backups |
| `GET /api/v1/ui/snapshots/{id}/browse` | List a snapshot; `children=1` for one folder at a time |
| `GET /api/v1/ui/snapshots/{id}/download?path=…` | Download one file |
| `GET /api/v1/ui/snapshots/{id}/archive?prefix=…` | Download a folder as a streamed ZIP |
| `GET /api/v1/ui/events` | The activity log |
| `GET` / `PUT /api/v1/ui/settings` | Account policy: retention, quota, upload ceiling, required encryption |
| `GET /api/v1/ui/ignore-rules` | The default rules and project markers, with reasons |

### Listing: subtree or one level

`browse` and the agent's entry listing take the same options: `prefix`, `cursor`,
`limit`, and `children`.

Without `children=1` you get the prefix and everything beneath it, which is what a
restore needs. With `children=1` you get only the immediate children, with folders
synthesised where a client stored files without their parent directories — which is
what a file browser needs. Paginate by passing back the `next_cursor` from the
previous response.

```bash
curl -s -b cookies.txt \
  "$S/api/v1/ui/snapshots/$ID/browse?children=1&prefix=home/Documents"
```

### Downloads and encryption

`download` and `archive` fail with `encryption_required` for end-to-end-encrypted
snapshots: the server cannot decrypt them. The check happens before any response
headers are written, so you get a clean JSON error rather than a truncated ZIP.

## Rate limiting

Login and enrolment attempts are rate limited per address — the two places where
guessing gets you something. Nothing else is: an enrolled device is already
trusted, and the endpoints it uses are bounded by the request size limits (16 MiB
per chunk, 32 MiB per JSON body by default) rather than by a counter.

Behind a proxy, set `OPENBACKUP_TRUST_PROXY=true` so the limiter sees real client
addresses rather than the proxy's.

## Reading the source instead

[`internal/api/types.go`](../internal/api/types.go) is the authoritative shape of
every request and response, and [`internal/api/client.go`](../internal/api/client.go)
is a working client for the agent half — including the retry behaviour, which is
worth copying if you write your own.
