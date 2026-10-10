/* tracker/host.c -- thin C host for the PARENA tracker. Owns ONLY: the file offset, one static
 * read buffer, the two native Arenas and the poll/sleep loop. All parsing and game logic is
 * PARENA (power_log.prn, game_state.prn).
 *
 * Memory rules ("avoid using the stack in C"):
 *   - no local arrays/VLAs/alloca; every buffer is file-scope static;
 *   - no malloc/free here; `scratch` arena is reset after every fold, `game` arena after every
 *     finished game (it holds the game's facts + names until the summary is printed);
 *   - bounded: a line longer than the buffer is dropped and counted.
 * Built with -Wframe-larger-than=256 so a stray stack buffer fails the build.
 *
 * usage: tracker <Power.log> [--once|--live]
 *   prints one JSON object per finished game. --once reads to EOF and exits; --live is --once plus
 *   one more object for a game still in progress at EOF ("complete":0).
 */
#ifndef PARENA_NO_GRAPHICS
#define PARENA_NO_GRAPHICS
#endif
#include "parena_runtime.h"
#include <stdio.h>
#include <string.h>
#include "tracker_gen.c"
#ifdef _WIN32
#include <windows.h>
#endif

#define BUF_CAP (1 << 20)

static Arena scratch;
static Arena game;
static GameState st;
static char buf[BUF_CAP];
static size_t have;          /* unconsumed bytes at the buffer front (a partial line) */
static long offset;          /* file read position */
static unsigned dropped;
static unsigned games;

static void sleep_ms(int ms) {
#ifdef _WIN32
    Sleep((DWORD)ms);
#else
    usleep((useconds_t)ms * 1000);
#endif
}

static void start_game(void) {
    arena_init(&game);
    st = new_state(&game);
}

static void emit_game(void) {
    arena_init(&scratch);
    printf("%s\n", summary_json(st, &scratch));
    fflush(stdout);
    arena_free_all(&scratch);
    arena_free_all(&game);
    games++;
    start_game();
}

/* feed buf[0..end) (NUL at end) to PARENA until everything complete is consumed */
static void fold_buffer(size_t end) {
    size_t pos = 0;
    while (pos < end) {
        int consumed;
        arena_init(&scratch);
        st = fold_text(buf + pos, st, &scratch, &game);
        arena_free_all(&scratch);
        consumed = st.consumed;
        if (st.complete > 0) {
            emit_game();               /* complete==2: the new CREATE_GAME line was left unconsumed */
            pos += (size_t)consumed;
            continue;
        }
        pos += (size_t)consumed;
        break;
    }
    have -= pos;
    memmove(buf, buf + pos, have);
}

static size_t poll_once(const char *path) {
    FILE *f = fopen(path, "rb");
    size_t n, i, last;
    char saved;
    if (!f) return 0;
    fseek(f, 0, SEEK_END);
    if (ftell(f) < offset) { offset = 0; have = 0; arena_free_all(&game); start_game(); } /* log rotated */
    fseek(f, offset, SEEK_SET);
    n = fread(buf + have, 1, BUF_CAP - 1 - have, f);
    fclose(f);
    offset += (long)n;
    have += n;
    if (n == 0) return 0;
    last = (size_t)-1;
    for (i = 0; i < have; i++) if (buf[i] == '\n') last = i;
    if (last == (size_t)-1) {
        if (have >= BUF_CAP - 1) { dropped++; have = 0; }
        return n;
    }
    saved = buf[last + 1];
    buf[last + 1] = '\0';
    {
        size_t end = last + 1;
        size_t tail = have - end;
        /* fold_buffer works on buf[0..end); the unconsumed remainder + the saved tail stay */
        size_t before = have;
        have = end;
        fold_buffer(end);
        buf[have] = saved;               /* restore the byte we NUL'd (now at new position) */
        have += tail;
        (void)before;
    }
    return n;
}

int main(int argc, char **argv) {
    int live = (argc > 2 && strcmp(argv[2], "--live") == 0);
    int once = live || (argc > 2 && strcmp(argv[2], "--once") == 0);
    if (argc < 2) { fprintf(stderr, "usage: %s <Power.log> [--once]\n", argv[0]); return 2; }
    start_game();
    for (;;) {
        size_t n = poll_once(argv[1]);
        if (once && n == 0) break;
        if (n == 0) sleep_ms(500);
    }
    if (live && st.started && st.complete == 0) emit_game();   /* game still running at EOF */
    if (dropped) fprintf(stderr, "tracker: %u oversize lines dropped\n", dropped);
    return 0;
}
