package handlers_test

// shankpit-460 web's matchmaker over a real WebSocket: HELLO with a real IDUNA token resolves to the shankpit460
// player row (ssoGamePlayer), a second human searching puts both into a first-to-16 lobby, a bad token is a flagged
// guest, and the public status route counts them. The room rules themselves are tested in internal/shankpit460.

import (
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"

	"iduna/internal/http/handlers"
)

type spWS struct {
	t  *testing.T
	ws *websocket.Conn
}

func dialSP(t *testing.T, srv *httptest.Server) *spWS {
	t.Helper()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/shankpit460/play"
	ws, err := websocket.Dial(url, "", srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.Close() })
	return &spWS{t: t, ws: ws}
}

func (c *spWS) send(b []byte) {
	c.t.Helper()
	if err := websocket.Message.Send(c.ws, b); err != nil {
		c.t.Fatal(err)
	}
}

func (c *spWS) hello(token, name string) {
	b := []byte{0x01, 1, 0, 0}
	binary.LittleEndian.PutUint16(b[2:], uint16(len(token)))
	b = append(b, token...)
	b = append(b, byte(len(name)))
	b = append(b, name...)
	c.send(b)
}

// next reads frames until one of the wanted type arrives (or two seconds pass)
func (c *spWS) next(typ byte) []byte {
	c.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_ = c.ws.SetReadDeadline(deadline)
		var f []byte
		if err := websocket.Message.Receive(c.ws, &f); err != nil {
			c.t.Fatalf("waiting for frame 0x%02x: %v", typ, err)
		}
		if len(f) > 0 && f[0] == typ {
			return f
		}
	}
	c.t.Fatalf("no frame 0x%02x", typ)
	return nil
}

func TestShankpit460Play_IdentityPairingAndStatus(t *testing.T) {
	e := newGameEnv(t)
	play, err := handlers.NewShankpit460Play(e.h)
	if err != nil {
		t.Fatal(err)
	}
	defer play.Hub.Close()
	srv := httptest.NewServer(play)
	defer srv.Close()

	pid, tok := e.ssoIdentity(t, "wren@example.com")
	var want string
	if err := e.db.QueryRow(`SELECT display_name FROM players WHERE player_id = ?`, pid).Scan(&want); err != nil {
		t.Fatal(err)
	}

	a := dialSP(t, srv)
	a.hello(tok, "ignored")
	w := a.next(0x81)
	if w[2] != 1 {
		t.Fatalf("a real SSO token should be a signed-in player, welcome = %v", w)
	}
	if got := string(w[4 : 4+int(w[3])]); got != want {
		t.Fatalf("lobby name %q, the player row says %q", got, want)
	}
	// the token resolved the player through the game registry: the identity is now claimed for shankpit460
	var game string
	if err := e.db.QueryRow(`SELECT game FROM players WHERE player_id = ?`, pid).Scan(&game); err != nil || game != "shankpit460" {
		t.Fatalf("player row game = %q (%v), want shankpit460", game, err)
	}

	bad := dialSP(t, srv)
	bad.hello("not-a-token", "Pip")
	if w := bad.next(0x81); w[2] != 2 {
		t.Fatalf("a refused token is a flagged guest, got %v", w)
	}

	b := dialSP(t, srv)
	b.hello("", "Moss")
	if w := b.next(0x81); w[2] != 0 || string(w[4:4+int(w[3])]) != "Moss" {
		t.Fatalf("guest welcome = %v", w)
	}

	a.send([]byte{0x02})
	if st := a.next(0x82); st[1] != 1 {
		t.Fatalf("a lone searcher is searching, status %v", st)
	}
	b.send([]byte{0x02})
	ma, mb := a.next(0x83), b.next(0x83)
	if binary.LittleEndian.Uint32(ma[1:]) != binary.LittleEndian.Uint32(mb[1:]) || ma[5] == mb[5] {
		t.Fatalf("one room, two slots expected: %v %v", ma[:6], mb[:6])
	}
	if ma[6] != 16 || binary.LittleEndian.Uint16(ma[7:]) != 360 {
		t.Fatalf("format: kill limit %d, %d s; want 16 and 360", ma[6], binary.LittleEndian.Uint16(ma[7:]))
	}

	resp, err := http.Get(srv.URL + "/api/v1/shankpit460/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var st struct {
		Online    int              `json:"online"`
		Playing   int              `json:"playing"`
		KillLimit int              `json:"kill_limit"`
		Rooms     []map[string]any `json:"rooms"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	if st.Playing != 2 || len(st.Rooms) != 1 || st.KillLimit != 16 {
		t.Fatalf("status = %+v", st)
	}
}
