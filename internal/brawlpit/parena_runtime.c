/* parena_runtime.c — real implementation of this directory's own minimal parena_runtime.h.
 * arena_init/arena_alloc/arena_free_all are copied verbatim from PARENA's own real
 * runtime/parena_runtime.c (identical bump-allocator mechanics), minus arena_strdup (unused by
 * lz4_gen.c). */
#include "parena_runtime.h"

#define PARENA_ARENA_BLOCK_MIN_CAPACITY (64 * 1024)

void arena_init(Arena *a) {
    a->head = NULL;
}

static size_t align_up(size_t n) {
    return (n + 7u) & ~(size_t)7u;
}

static ParenaArenaBlock *arena_new_block(size_t min_capacity) {
    size_t capacity = min_capacity > PARENA_ARENA_BLOCK_MIN_CAPACITY
                           ? min_capacity
                           : PARENA_ARENA_BLOCK_MIN_CAPACITY;
    ParenaArenaBlock *b = (ParenaArenaBlock *)malloc(sizeof(ParenaArenaBlock) + capacity);
    if (!b) {
        fprintf(stderr, "parena runtime (min): out of memory (arena block alloc failed)\n");
        exit(1);
    }
    b->next = NULL;
    b->used = 0;
    b->capacity = capacity;
    return b;
}

void *arena_alloc(Arena *a, size_t size) {
    size_t aligned = align_up(size);
    if (!a->head || a->head->used + aligned > a->head->capacity) {
        ParenaArenaBlock *b = arena_new_block(aligned);
        b->next = a->head;
        a->head = b;
    }
    void *ptr = a->head->data + a->head->used;
    a->head->used += aligned;
    return ptr;
}

void arena_free_all(Arena *a) {
    ParenaArenaBlock *b = a->head;
    while (b) {
        ParenaArenaBlock *next = b->next;
        free(b);
        b = next;
    }
    a->head = NULL;
}
