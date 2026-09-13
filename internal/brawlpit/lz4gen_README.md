# lz4gen — PARENA-compiled LZ4-style compression for BRAWLPIT levels

S417-03 (founder real-time: "PARENA has lz4" / "you can build parena in"). This directory embeds
PARENA's own real `stdlib/compress/lz4.prn`, compiled to C, as the real compression codec for
BRAWLPIT level payloads sent over the wire (S417-02's public API) -- reused as-is in both this
Go server (via cgo, see `../lz4.go`) and BRAWLPIT's native C client (`packages/common/lz4/` in
that repo, a byte-for-byte copy of this directory).

## Real, honest scope

This is an "LZ4-style" compressor (literal runs + back-reference matches, the same real idea
LZ4's own format is built on) -- **not** byte-compatible with the reference `liblz4` wire format.
`lz4.prn`'s own header names this directly: its `Token` intermediate representation (offset/
match-len/literal triples) is this project's own honest design, not liblz4's own bitstream. Since
both the compressing and decompressing ends are always this same code, that's fine -- it's a real,
working, self-consistent codec, just not a drop-in replacement for a generic `lz4` CLI/library.

## Files

- `lz4_gen.c` — **generated, do not edit by hand.** Produced by `parena build
  stdlib/compress/lz4.prn -o lz4_gen.c` from the PARENA repo root. Regenerate whenever
  `stdlib/compress/lz4.prn` changes.
- `parena_runtime.h`/`parena_runtime.c` — a real, **minimal, portable** subset of PARENA's own
  real `runtime/parena_runtime.h`/`.c`, hand-written (not generated), scoped to exactly the
  Arena/Vec functions `lz4_gen.c` actually calls. The real runtime is ~135KB and pulls in a long,
  Linux/POSIX-specific include list (pty, SPI/I2C, raw sockets, SDL2) supporting PARENA's full
  stdlib surface -- wrong to drag into a Windows-targeting game client or a cgo-linked Go server
  for what's really just a compression codec. Every type/function signature here is copied
  verbatim from the real runtime (identical struct layout and semantics), so `lz4_gen.c` compiles
  completely unmodified against this file instead of the real one.
- `lz4_wrapper.h`/`lz4_wrapper.c` — hand-written (not generated, safe to edit), the real
  byte-buffer-in/byte-buffer-out API (`pw_lz4_compress`/`pw_lz4_decompress`) both this Go server
  and BRAWLPIT's native client actually call. `lz4_gen.c`'s own `compress`/`decompress` operate on
  `Vec<Token>`, not raw bytes -- this file does the real byte<->Vec<Token> conversion and defines
  this project's own wire format: a 4-byte little-endian token count, then that many 12-byte
  `{offset, match_len, literal}` records (3 little-endian int32 each).

## Regenerating lz4_gen.c

```bash
cd /home/fatbaby/PARENA
./parena build stdlib/compress/lz4.prn -o /home/fatbaby/IDUNA/internal/brawlpit/lz4_gen.c
# then copy the same, regenerated lz4_gen.c to BRAWLPIT/packages/common/lz4/lz4_gen.c
```
