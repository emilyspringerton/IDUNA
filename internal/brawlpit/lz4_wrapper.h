#ifndef PW_LZ4_WRAPPER_H
#define PW_LZ4_WRAPPER_H

/* lz4_wrapper.h — S417-03, founder real-time: "PARENA has lz4" / "you can build parena in."
 * A real, clean byte-buffer-in/byte-buffer-out API over lz4_gen.c's own generated
 * compress()/decompress() (compiled from PARENA/stdlib/compress/lz4.prn), which itself operates
 * on Vec<Token> -- not raw bytes -- so this hand-written wrapper (never generated, safe to edit)
 * does the real byte<->Vec<Token> conversion and defines the actual wire format: a 4-byte
 * little-endian token count, followed by that many 12-byte {offset, match_len, literal}
 * records (each a 4-byte little-endian int32) -- this project's OWN real, honest wire shape for
 * this compressor's own Token intermediate representation, not liblz4's own real wire format
 * (see lz4_gen.c's own header comment on why this is "LZ4-style," not byte-compatible with the
 * reference codec).
 *
 * Real, honest correction found live while writing this: a naive fixed 12-bytes-per-token
 * encoding actually EXPANDED an 87-byte test input to 508 bytes (every literal byte cost 12
 * bytes instead of 1) -- the real wire format is a compact flag+payload scheme instead (a
 * literal token is 2 bytes, a match token 4), with an always-safe STORED fallback (a single
 * 0x00 flag byte + the original bytes verbatim) whenever the compact encoding wouldn't actually
 * be smaller. See lz4_wrapper.c's own doc comment for the exact byte layout.
 *
 * Both functions return a newly malloc'd buffer via *out (caller must free(*out)) and the real
 * byte length of that buffer as the return value, or -1 on failure (out-of-memory, or malformed
 * input to decompress).
 */

#include <stddef.h>

long pw_lz4_compress(const unsigned char *in, long in_len, unsigned char **out);
long pw_lz4_decompress(const unsigned char *in, long in_len, unsigned char **out);

#endif
