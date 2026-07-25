# OpenBackup server image.
#
# Three stages: build the dashboard with Node, build the binary with Go, ship
# neither toolchain. The result is a static binary plus CA certificates, so the
# runtime image has no shell, no package manager and nothing to patch.

# --- 1. Dashboard -----------------------------------------------------------
FROM node:22-alpine AS web

WORKDIR /web
# Dependencies are copied on their own so a change to the app does not reinstall
# them on every build.
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund

COPY web/ ./
RUN npm run build

# --- 2. Server --------------------------------------------------------------
FROM golang:1.26-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# The export lands where //go:embed expects it, so the binary carries the UI.
COPY --from=web /web/out/ ./internal/server/web/dist/

ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown

# CGO stays off: the SQLite driver is pure Go, which is what lets this run on a
# scratch image and cross-compile without a C toolchain.
ENV CGO_ENABLED=0
RUN go build -trimpath \
	-ldflags "-s -w \
	-X github.com/openbackup/openbackup/internal/version.Version=${VERSION} \
	-X github.com/openbackup/openbackup/internal/version.Commit=${COMMIT} \
	-X github.com/openbackup/openbackup/internal/version.Date=${DATE}" \
	-o /out/openbackup-server ./cmd/openbackup-server

# --- 3. Runtime -------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/openbackup-server /usr/local/bin/openbackup-server

# The data directory holds the SQLite database and every blob, so it must be a
# volume: losing it loses the backups.
VOLUME /data
ENV OPENBACKUP_DATA_DIR=/data \
	OPENBACKUP_ADDR=:8080 \
	OPENBACKUP_LOG_JSON=true

EXPOSE 8080
USER nonroot:nonroot

# The server's own check subcommand verifies the database and blob store, which
# is a more useful signal than a TCP connect.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
	CMD ["/usr/local/bin/openbackup-server", "health"]

ENTRYPOINT ["/usr/local/bin/openbackup-server"]
CMD ["serve"]
