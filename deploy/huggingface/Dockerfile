# IDUNA on a Hugging Face Space (custom Docker SDK).
#
# WHY THIS SHAPE:
#   * CGO IS ON, and not by preference. SQLite itself is pure Go here (modernc.org/sqlite, no gcc needed),
#     but internal/brawlpit/lz4.go is a deliberate cgo binding over PARENA's own stdlib/compress/lz4.prn
#     compiled to lz4_gen.c -- the same codec BRAWLPIT's native client links, so the wire format matches.
#     `CGO_ENABLED=0 go build .` fails with "undefined: brawlpit.CompressLZ4" (found by running it, not by
#     reading this file). Hand-porting that codec to Go to avoid gcc would undo S417-03's own decision, so
#     the image carries gcc in the build stage instead and the runtime stays gcc-free.
#   * Hugging Face routes traffic to the container's port 7860, so IDUNA_ADDR binds 0.0.0.0:7860 (main.go
#     reads IDUNA_ADDR; it defaults to :8080, which is wrong on a Space).
#   * Hugging Face runs the container as a non-root, arbitrary UID, so nothing here may write inside the
#     image. State goes to /data: SQLITE_PATH and KEY_FILE, and WORKDIR /data so the optional per-feature
#     SQLite files (blog/vault/kanban/...) that main.go opens with relative paths also land somewhere
#     writable instead of failing on a read-only /app.
#   * /data is EPHEMERAL on free hardware. Every restart is a fresh identity database and a fresh signing
#     key. That is stated in the Space README and in docs/HF_SPACE_DEPLOY.md, not hidden.
FROM golang:1.25-alpine AS build
RUN apk add --no-cache gcc musl-dev
WORKDIR /src
ENV CGO_ENABLED=1 GOFLAGS=-trimpath GOTOOLCHAIN=local
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -ldflags="-s -w" -o /out/iduna .

FROM alpine:3.21
RUN addgroup -g 1000 -S iduna \
 && adduser -u 1000 -S -G iduna iduna \
 && mkdir -p /data/var /app \
 && chown -R iduna:iduna /data
WORKDIR /data
COPY --from=build /out/iduna /app/iduna
COPY --from=build /src/migrations /app/migrations
USER iduna
ENV IDUNA_ADDR=0.0.0.0:7860 \
    IDUNA_ROOT=/app \
    SQLITE_PATH=/data/iduna.db \
    KEY_FILE=/data/iduna-key.json
EXPOSE 7860
# No HEALTHCHECK: Hugging Face's own proxy decides liveness from the port, and a failing healthcheck on a
# cold start reads as a broken Space rather than a slow one.
#
# The dirs are recreated at start, not trusted from build time: Hugging Face can mount an empty volume over
# /data, and IDUNA opens its optional stores by relative path (./var/mailinglist.db, vault, kanban, ...),
# which is fatal if the directory is missing. Found by actually booting this environment -- the failure was
# "mailinglist: failed to open store: migrate mailinglist db: unable to open database file (14)" and the
# process died before it ever bound the port.
ENTRYPOINT ["/bin/sh", "-c", "mkdir -p \"$PWD/var\" && exec /app/iduna"]
