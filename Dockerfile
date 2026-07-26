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
RUN mkdir -p /data && go build -trimpath \
	-ldflags "-s -w \
	-X github.com/foisalislambd/openbackup/internal/version.Version=${VERSION} \
	-X github.com/foisalislambd/openbackup/internal/version.Commit=${COMMIT} \
	-X github.com/foisalislambd/openbackup/internal/version.Date=${DATE}" \
	-o /out/openbackup-server ./cmd/openbackup-server

# --- 3. Runtime -------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/openbackup-server /usr/local/bin/openbackup-server

# Distroless has no shell, so ownership has to be set at COPY time. Without a
# writable /data the process exits on startup and the published port never
# answers — which is exactly what the CI smoke test caught.
COPY --from=build --chown=nonroot:nonroot /data/ /data/

# The data directory holds the SQLite database and every blob, so it must be a
# volume: losing it loses the backups. On first mount Docker copies the image
# directory (including ownership) into an empty volume.
VOLUME /data
ENV OPENBACKUP_DATA_DIR=/data \
	OPENBACKUP_ADDR=:18200 \
	OPENBACKUP_LOG_JSON=true

EXPOSE 18200
USER nonroot:nonroot

# The server's own check subcommand verifies the database and blob store, which
# is a more useful signal than a TCP connect.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
	CMD ["/usr/local/bin/openbackup-server", "health"]

ENTRYPOINT ["/usr/local/bin/openbackup-server"]
CMD ["serve"]
