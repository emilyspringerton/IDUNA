# iduna image: the IAM/ledger/portal binary (pure-Go, modernc sqlite) plus the working-dir assets it serves
# (index.html, app.js, static/, migrations/, config/). State lives on the PVC mounted at /app/var.
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=1 GOWORK=off go build -o /out/iduna .
FROM debian:12-slim
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates git gcc libc6-dev imagemagick && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=build /out/iduna /app/iduna
COPY index.html app.js styles.css openapi.yaml /app/
COPY static /app/static
COPY migrations /app/migrations
# NOCK toolchain (same as the box: gcc + ImageMagick + parena). parena binary/stdlib are staged by scripts/build-image.sh (STOPGAP: prebuilt, not built here).
COPY internal/nock/parena_runtime /app/parena_runtime
COPY .parena/ /home/fatbaby/PARENA/
ENV NOCK_PARENA_RUNTIME_DIR=/app/parena_runtime
COPY config /app/config
CMD ["/app/iduna"]
