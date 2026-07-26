# Installing the server

The server stores the backups and serves the dashboard. It is one container with
one volume: the database is SQLite inside that volume, and the dashboard is
embedded in the binary, so there is nothing else to run.

You need a machine with Docker that your computers can reach — a €4 VPS is
plenty — and enough disk for the data you intend to keep.

## The one-command install

```bash
curl -fsSL https://raw.githubusercontent.com/foisalislambd/openbackup/main/scripts/install-server.sh | sudo sh
```

It installs Docker and Compose if they are missing, writes a Compose file and an
`.env` to `/opt/openbackup`, generates an admin password, starts the server,
waits until it actually answers, and prints the dashboard address with the
credentials. Run it with `sudo` so those installs are allowed.

Piping a script from the internet into a shell is a reasonable thing to be wary
of. Read it first if you like:
[`scripts/install-server.sh`](../scripts/install-server.sh) — it is about 150
lines and does what the previous paragraph says.

Settings it accepts, all optional:

```bash
OPENBACKUP_PUBLIC_URL=https://backup.example.com \
OPENBACKUP_ADMIN_EMAIL=you@example.com \
OPENBACKUP_PORT=8080 \
OPENBACKUP_DIR=/opt/openbackup \
sh install-server.sh
```

Re-running it upgrades in place: the Compose file is rewritten, `.env` (which
holds your password) is left alone.

## By hand with Docker Compose

Take [`docker-compose.yml`](../docker-compose.yml) from the repository, or start
from this:

```yaml
services:
  openbackup:
    image: openbackup/server:latest
    container_name: openbackup
    restart: unless-stopped
    ports:
      # localhost only, because a reverse proxy on this host will front it
      - '127.0.0.1:8080:8080'
    volumes:
      - openbackup-data:/data
    environment:
      OPENBACKUP_PUBLIC_URL: https://backup.example.com
      OPENBACKUP_ADMIN_EMAIL: you@example.com
      OPENBACKUP_ADMIN_PASSWORD: use-a-long-one
      OPENBACKUP_TRUST_PROXY: 'true'
    security_opt: [no-new-privileges:true]
    read_only: true
    tmpfs: [/tmp]

volumes:
  openbackup-data:
```

```bash
docker compose up -d --wait
```

`--wait` works because the image declares its own healthcheck, which probes
`/api/v1/health` using the binary itself.

The admin variables create the first account on an empty database, so the install
finishes with a working login instead of a signup form on a public URL. They are
ignored once an account exists; remove them after your first sign-in.

Every other setting is in [configuration.md](configuration.md).

## Put TLS in front of it

Device tokens travel in an `Authorization` header, so plain HTTP is only
acceptable on a network you control. Anything reachable from the internet needs
TLS, and the easiest way is Caddy, which gets certificates by itself:

```
backup.example.com {
	reverse_proxy 127.0.0.1:8080
}
```

Then set `OPENBACKUP_PUBLIC_URL` to the `https://` address and restart. That does
two things beyond cosmetics: session cookies get the `Secure` flag, and the
enrolment instructions the dashboard prints use an address that works.

Also set `OPENBACKUP_TRUST_PROXY=true` when proxied, so rate limiting and logs
see the real client address instead of the proxy's. Do not set it while the
server is directly exposed — it would let a client spoof its own address.

With nginx, forward the usual headers and raise the body limit above the largest
chunk (16 MiB by default):

```nginx
location / {
	proxy_pass http://127.0.0.1:8080;
	proxy_set_header Host $host;
	proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
	proxy_set_header X-Forwarded-Proto $scheme;
	client_max_body_size 64m;
}
```

## Without Docker

The server is a single static binary with no dependencies. Download the release
for your platform, or build it (`make`), then:

```bash
OPENBACKUP_DATA_DIR=/var/lib/openbackup \
OPENBACKUP_PUBLIC_URL=https://backup.example.com \
./openbackup-server serve
```

Run it under systemd as an unprivileged user that owns the data directory. It
needs no root, no ports below 1024, and no shell.

## First sign-in

1. Open the dashboard and log in.
2. Change the generated password (**Settings**).
3. Go to **Devices**, create a connection code, and follow
   [install-agent.md](install-agent.md) on each computer.

New sign-ups are refused once the first account exists, so an exposed server is
not an open storage relay. To create further accounts:

```bash
docker compose exec openbackup openbackup-server user add --email someone@example.com
```

## Storing blobs in S3 instead of the volume

Useful when you would rather not grow a disk. The database stays local either
way; only the content-addressed blocks move.

```yaml
OPENBACKUP_S3_ENDPOINT: s3.eu-central-1.amazonaws.com
OPENBACKUP_S3_BUCKET: my-backups
OPENBACKUP_S3_ACCESS_KEY: ...
OPENBACKUP_S3_SECRET_KEY: ...
```

Works with AWS S3, MinIO, Backblaze B2 and anything else speaking the same
protocol. Add `OPENBACKUP_S3_REGION`, `OPENBACKUP_S3_PREFIX`, or
`OPENBACKUP_S3_USE_SSL=false` for a MinIO on your own network.

This is a one-way decision in practice: blocks already on local disk are not
migrated, and switching backends leaves those snapshots unrestorable until you
copy the blobs across yourself.

## Next

- [Install an agent](install-agent.md) on the computers you want backed up.
- [Operating a server](operations.md) — and in particular, back up the volume:
  a backup server holding the only copy of your backups is still a single point
  of failure.
