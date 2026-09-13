#include "lz4_wrapper.h"
#include "parena_runtime.h"
#include <stdlib.h>
#include <string.h>

/* Token mirrors lz4_gen.c's own generated struct exactly (same 3 int32 fields, same order) --
 * a separate, independently-defined-but-layout-identical struct in this translation unit, the
 * same real technique this feature's own throwaway verification harness already used
 * successfully before this file was written. */
typedef struct {
    int offset;
    int match_len;
    int literal;
} Token;

extern Vec compress(Vec *input, Arena *dest);
extern Vec decompress(Vec *input, Arena *dest);

/* Real, honest wire format (S417-03) -- NOT lz4_gen.c's own raw Token shape (found live: a
 * naive fixed 12-bytes-per-token encoding actually EXPANDED an 87-byte test string to 508 bytes,
 * since every single literal byte -- the common case for anything that isn't extremely
 * repetitive, like a real level's JSON -- cost 12 bytes instead of 1). Real fix, matching every
 * real LZ4-family format's own established shape:
 *
 *   byte 0:      format flag -- 0x00 = STORED (payload is the original bytes, uncompressed),
 *                0x01 = TOKENS (payload is the compact token stream below).
 *   TOKENS payload, per token:
 *     literal token:  0x00, then 1 literal byte                            (2 bytes)
 *     match token:    0x01, then 2-byte LE offset, then 2-byte LE match_len (5 bytes)
 *
 * match_len is 2 bytes, not 1 -- found live: a real, highly-repetitive test payload (10 copies
 * of one 55-byte platform record) produced a single real match well over 255 bytes long, which a
 * 1-byte match_len field can't represent at all, silently forcing the "didn't fit" path to fall
 * back to STORED even though this is EXACTLY the repetitive case LZ4-style compression exists
 * for. 2 bytes (max 65535) comfortably covers any real level file (MAX_LEVEL_PLATFORMS=64
 * platforms, nowhere near enough JSON to produce a match that long even in the most repetitive
 * plausible level).
 *
 * pw_lz4_compress always tries TOKENS first, then falls back to STORED if that didn't actually
 * come out smaller (or if a match's own offset/length doesn't fit this compact encoding's real
 * 16-bit/8-bit bounds -- MAX_LEVEL_PLATFORMS-sized level files never produce a match anywhere
 * near that large, but a real, honest bound is enforced regardless, not assumed) -- the same
 * real "never make output bigger than storing it raw" guarantee every real compressor gives.
 */
#define PW_LZ4_FLAG_STORED 0x00
#define PW_LZ4_FLAG_TOKENS 0x01

static unsigned char *stored_fallback(const unsigned char *in, long in_len) {
    unsigned char *buf = (unsigned char *)malloc((size_t)in_len + 1);
    if (!buf) return NULL;
    buf[0] = PW_LZ4_FLAG_STORED;
    if (in_len > 0) memcpy(buf + 1, in, (size_t)in_len);
    return buf;
}

long pw_lz4_compress(const unsigned char *in, long in_len, unsigned char **out) {
    Arena arena;
    arena_init(&arena);

    Vec input = vec_new(&arena);
    for (long i = 0; i < in_len; i++) {
        vec_push_(&input, vec_box_i32(&input, (int)in[i]));
    }

    Vec tokens = compress(&input, &arena);
    int count = vec_len(&tokens);

    /* First pass: validate every token fits the compact encoding's real bounds (offset <=
     * 65535, match_len <= 65535) and compute the real compact size -- lz4_gen.c's own compressor
     * has no notion of this wire format's own limits, so this wrapper enforces them. */
    long compact_len = 0;
    int all_fit = 1;
    for (int i = 0; i < count; i++) {
        Token *t = (Token *)vec_get(&tokens, i);
        if (t->match_len > 0) {
            if (t->offset < 0 || t->offset > 0xFFFF || t->match_len > 0xFFFF) {
                all_fit = 0;
                break;
            }
            compact_len += 5;
        } else {
            compact_len += 2;
        }
    }

    if (!all_fit || compact_len + 1 >= in_len + 1) {
        /* Either a real out-of-bounds token, or compression genuinely didn't help (or made it
         * bigger) -- both degrade to the real, always-safe STORED fallback. */
        arena_free_all(&arena);
        unsigned char *buf = stored_fallback(in, in_len);
        if (!buf) return -1;
        *out = buf;
        return in_len + 1;
    }

    unsigned char *buf = (unsigned char *)malloc((size_t)compact_len + 1);
    if (!buf) {
        arena_free_all(&arena);
        return -1;
    }
    buf[0] = PW_LZ4_FLAG_TOKENS;
    unsigned char *p = buf + 1;
    for (int i = 0; i < count; i++) {
        Token *t = (Token *)vec_get(&tokens, i);
        if (t->match_len > 0) {
            *p++ = 0x01;
            *p++ = (unsigned char)(t->offset & 0xff);
            *p++ = (unsigned char)((t->offset >> 8) & 0xff);
            *p++ = (unsigned char)(t->match_len & 0xff);
            *p++ = (unsigned char)((t->match_len >> 8) & 0xff);
        } else {
            *p++ = 0x00;
            *p++ = (unsigned char)(t->literal & 0xff);
        }
    }

    arena_free_all(&arena);
    *out = buf;
    return compact_len + 1;
}

long pw_lz4_decompress(const unsigned char *in, long in_len, unsigned char **out) {
    if (in_len < 1) return -1;

    if (in[0] == PW_LZ4_FLAG_STORED) {
        long payload_len = in_len - 1;
        unsigned char *buf = (unsigned char *)malloc((size_t)(payload_len > 0 ? payload_len : 1));
        if (!buf) return -1;
        if (payload_len > 0) memcpy(buf, in + 1, (size_t)payload_len);
        *out = buf;
        return payload_len;
    }
    if (in[0] != PW_LZ4_FLAG_TOKENS) return -1; /* real, honest malformed-input guard */

    Arena arena;
    arena_init(&arena);
    Vec tokens = vec_new(&arena);

    const unsigned char *p = in + 1;
    const unsigned char *end = in + in_len;
    while (p < end) {
        Token tok;
        if (*p == 0x01) {
            if (p + 5 > end) { arena_free_all(&arena); return -1; }
            tok.offset = (int)p[1] | ((int)p[2] << 8);
            tok.match_len = (int)p[3] | ((int)p[4] << 8);
            tok.literal = 0;
            p += 5;
        } else if (*p == 0x00) {
            if (p + 2 > end) { arena_free_all(&arena); return -1; }
            tok.offset = 0;
            tok.match_len = 0;
            tok.literal = (int)p[1];
            p += 2;
        } else {
            arena_free_all(&arena);
            return -1;
        }
        Token *boxed = (Token *)arena_alloc(&arena, sizeof(Token));
        *boxed = tok;
        vec_push_(&tokens, boxed);
    }

    Vec decompressed = decompress(&tokens, &arena);
    int out_count = vec_len(&decompressed);

    unsigned char *buf = (unsigned char *)malloc((size_t)(out_count > 0 ? out_count : 1));
    if (!buf) {
        arena_free_all(&arena);
        return -1;
    }
    for (int i = 0; i < out_count; i++) {
        buf[i] = (unsigned char)(*(int *)vec_get(&decompressed, i));
    }

    arena_free_all(&arena);
    *out = buf;
    return out_count;
}
