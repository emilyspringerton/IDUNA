#ifndef PARENA_RUNTIME_MIN_H
#define PARENA_RUNTIME_MIN_H

/* parena_runtime.h — a REAL, minimal, portable subset of PARENA's own real runtime
 * (PARENA/runtime/parena_runtime.h), scoped exactly to what lz4_gen.c (compiled from
 * stdlib/compress/lz4.prn -- see this directory's own README) actually calls: Arena/Vec and
 * arena_init/arena_alloc/arena_free_all/vec_new/vec_push_/vec_get/vec_len/vec_box_i32.
 *
 * The REAL parena_runtime.h is ~135KB and pulls in a long, Linux/POSIX-specific include list
 * (pty.h, linux/spi/spidev.h, sys/mman.h, raw sockets, SDL2, ...) supporting PARENA's FULL
 * stdlib surface -- appropriate for a native PARENA program, wrong to drag into a game client
 * that also targets Windows (BRAWLPIT's own real, current platform target) or a Go server via
 * cgo. Every type/function signature here is copied VERBATIM from the real runtime (same struct
 * layout, same semantics) so the generated lz4_gen.c -- itself never hand-edited, "do not edit
 * by hand" per its own header -- compiles completely unmodified against this file instead.
 * Regenerate lz4_gen.c any time stdlib/compress/lz4.prn changes; this header only needs to
 * change if that regeneration ever calls a runtime function not already covered here. */

#include <stddef.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef struct ParenaArenaBlock {
    struct ParenaArenaBlock *next;
    size_t used;
    size_t capacity;
    unsigned char data[];
} ParenaArenaBlock;

typedef struct {
    ParenaArenaBlock *head;
} Arena;

void arena_init(Arena *a);
void *arena_alloc(Arena *a, size_t size);
void arena_free_all(Arena *a);

typedef struct {
    Arena *arena;
    void **items;
    size_t count;
    size_t capacity;
} Vec;

static inline Vec vec_new(Arena *dest) {
    Vec v;
    v.arena = dest;
    v.items = NULL;
    v.count = 0;
    v.capacity = 0;
    return v;
}

static inline void vec_push_(Vec *v, void *item) {
    if (v->count == v->capacity) {
        size_t new_cap = v->capacity == 0 ? 4 : v->capacity * 2;
        void **new_items = (void **)arena_alloc(v->arena, new_cap * sizeof(void *));
        for (size_t i = 0; i < v->count; i++) new_items[i] = v->items[i];
        v->items = new_items;
        v->capacity = new_cap;
    }
    v->items[v->count++] = item;
}

static inline void *vec_get(Vec *v, int idx) {
    if (idx < 0 || (size_t)idx >= v->count) return NULL;
    return v->items[idx];
}

static inline int vec_len(Vec *v) {
    return (int)v->count;
}

static inline void *vec_box_i32(Vec *v, int value) {
    int *cell = (int *)arena_alloc(v->arena, sizeof(int));
    *cell = value;
    return cell;
}

#endif
