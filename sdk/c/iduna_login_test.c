/* Loopback round-trip test: drives iduna_login_poll() with a scripted "browser" over real sockets.
 * Build: cc -Wall -Wextra -pedantic -Werror -o /tmp/ilt iduna_login_test.c && /tmp/ilt */
#include "iduna_login.h"
#include <assert.h>
#include <pthread.h>

static int g_port; static char g_state[64];
static char g_got_page[4096]; static int g_post_status = -1, g_bad_status = -1, g_host_status = -1;

static int http(const char *host, const char *req_fmt, const char *body, char *out, size_t cap) {
    int s = socket(AF_INET, SOCK_STREAM, 0);
    struct sockaddr_in a = {0}; a.sin_family = AF_INET; a.sin_port = htons((unsigned short)g_port);
    a.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
    if (connect(s, (struct sockaddr *)&a, sizeof a) != 0) { close(s); return -1; }
    char req[6000];
    int n = snprintf(req, sizeof req, req_fmt, host, (int)strlen(body), body);
    send(s, req, n, 0);
    size_t have = 0; int r;
    while (have + 1 < cap && (r = (int)recv(s, out + have, cap - 1 - have, 0)) > 0) have += (size_t)r;
    out[have] = 0; close(s);
    return atoi(out + 9);
}

static void *browser(void *arg) {
    (void)arg; usleep(100000);
    char host[64]; snprintf(host, sizeof host, "127.0.0.1:%d", g_port);
    char out[8192];
    char get[256]; snprintf(get, sizeof get, "GET /cb?state=%s HTTP/1.1\r\nHost: %%s\r\nX: %%d%%s\r\n\r\n", g_state);
    http(host, get, "", g_got_page, sizeof g_got_page);
    /* wrong host header (DNS-rebinding shape) */
    g_host_status = http("evil.example", "POST /token HTTP/1.1\r\nHost: %s\r\nContent-Length: %d\r\n\r\n%s", "state=x", out, sizeof out);
    /* wrong state */
    g_bad_status = http(host, "POST /token HTTP/1.1\r\nHost: %s\r\nContent-Length: %d\r\n\r\n%s", "state=nope&sso_token=a.b.c", out, sizeof out);
    /* correct */
    char body[512];
    snprintf(body, sizeof body, "state=%s&sso_token=eyJ.abc%%2Bdef.sig&player_id=42&display_name=Emily%%20S%%26%%C3%%A9", g_state);
    g_post_status = http(host, "POST /token HTTP/1.1\r\nHost: %s\r\nContent-Length: %d\r\n\r\n%s", body, out, sizeof out);
    return NULL;
}

int main(void) {
    IdunaLogin L;
    assert(iduna_login_begin_ex(&L, "http://evil.example", 0) < 0);       /* non-https, non-loopback base refused */
    assert(iduna_login_begin_ex(&L, "https://iam.okemily.com/'; rm", 0) < 0); /* shell metachar refused */
    assert(iduna_login_begin_ex(&L, NULL, 0) == 0);
    assert(strstr(L.login_url, "https://iam.okemily.com/api/v1/auth/sso/login?redirect_uri=http%3A%2F%2F127.0.0.1%3A"));
    assert(L.port > 0 && strlen(L.state) == 32);
    g_port = L.port; strcpy(g_state, L.state);
    pthread_t t; pthread_create(&t, NULL, browser, NULL);
    int r = 0;
    for (int i = 0; i < 500 && r == 0; i++) { r = iduna_login_poll(&L); usleep(10000); }
    pthread_join(t, NULL);
    assert(r == 1);
    assert(strstr(g_got_page, "location.hash"));
    assert(g_host_status == 403);
    assert(g_bad_status == 403);
    assert(g_post_status == 200);
    assert(strcmp(L.token, "eyJ.abc+def.sig") == 0);
    assert(strcmp(L.player_id, "42") == 0);
    assert(strcmp(L.display_name, "Emily S&\xc3\xa9") == 0);
    iduna_login_close(&L);
    puts("iduna_login_test: OK");
    return 0;
}
