# IDUNA on a Hugging Face Space (custom Docker SDK).
#
# THE LAYOUT IS DICTATED BY ONE FACT, FOUND BY RUNNING IT: IDUNA_ROOT is the base for BOTH
# `migrations/truestore` (read) AND `var/` -- the user event log, the Apples eventlog and
# `var/agent-secrets.env` (write). So IDUNA_ROOT must be WRITABLE. An earlier version of this file pointed
# IDUNA_ROOT at a read-only /app; as uid 1000 that dies on
#   userlog: write seq tmp: open /app/var/user-events/state/latest-seq.tmp: permission denied
# which no amount of reading the Dockerfile shows you.
#
#   /app   read-only: the binary, plus a PRISTINE copy of migrations (a source, never written)
#   /data  IDUNA_ROOT and cwd: iduna.db, iduna-key.json, var/, and the working migrations copy
#
# The entrypoint seeds /data from /app every start, because Hugging Face can mount an empty volume over
# /data and shadow whatever the image baked in.
#
# CGO IS ON, and not by preference. SQLite is pure Go here (modernc.org/sqlite, no gcc needed), but
# internal/brawlpit/lz4.go is a cgo binding over PARENA's own stdlib/compress/lz4.prn compiled to lz4_gen.c --
# the same codec BRAWLPIT's native client links, so the wire format matches. `CGO_ENABLED=0 go build .` fails
# with "undefined: brawlpit.CompressLZ4". Hand-porting that to Go to avoid gcc would undo S417-03's decision,
# so gcc lives in the build stage only and the runtime image stays gcc-free.
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
    IDUNA_ROOT=/data \
    SQLITE_PATH=/data/iduna.db \
    KEY_FILE=/data/iduna-key.json
EXPOSE 7860
# No HEALTHCHECK: Hugging Face's own proxy decides liveness from the port, and a failing healthcheck during a
# cold start reads as a broken Space rather than a slow one.
ENTRYPOINT ["/bin/sh", "-c", "mkdir -p \"$IDUNA_ROOT/var\" && cp -a /app/migrations \"$IDUNA_ROOT/\" && exec /app/iduna"]
