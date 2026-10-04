# iduna image: the IAM/ledger/portal binary (pure-Go, modernc sqlite) plus the working-dir assets it serves
# (index.html, app.js, static/, migrations/, config/). State lives on the PVC mounted at /app/var.
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=1 GOWORK=off go build -o /out/iduna .
FROM debian:12-slim
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates git && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=build /out/iduna /app/iduna
COPY index.html app.js styles.css openapi.yaml /app/
COPY static /app/static
COPY migrations /app/migrations
COPY config /app/config
CMD ["/app/iduna"]
