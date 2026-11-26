#!/bin/sh
# Mirrors DEADWEIGHT's append-only matches/decks ndjson into $DEST by resuming the download (curl -C -), so each
# pass only transfers newly appended bytes. If the source shrank (rotated/truncated) the range request fails
# with 416/33 and the local copy is dropped and re-fetched in full.
SRC="${SRC:-http://deadweight:8081}"; DEST="${DEST:-/app/var/dwlogs}"; EVERY="${EVERY:-30}"
mkdir -p "$DEST"
while :; do
  for f in matches.ndjson decks.ndjson; do
    curl -sS -f -C - -o "$DEST/$f" "$SRC/$f"; rc=$?
    if [ $rc -eq 33 ] || [ $rc -eq 22 -a -s "$DEST/$f" ]; then
      # 416 or similar: re-check whether source is simply unchanged vs shrunk
      r=$(curl -sI "$SRC/$f" | tr -d '\r' | awk 'tolower($1)=="content-length:"{print $2}'); l=$(wc -c < "$DEST/$f")
      if [ -n "$r" ] && [ "$r" -lt "$l" ]; then echo "logsync: $f shrank ($r < $l), refetching"; rm -f "$DEST/$f"; fi
    elif [ $rc -ne 0 ]; then echo "logsync: $f rc=$rc"; fi
  done
  sleep "$EVERY"
done
