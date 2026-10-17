package empire

import (
	"context"
	"encoding/binary"
	"sync"
	"time"
)

// Conn is one browser connection.
type Conn struct {
	hub *Hub
	t   Transport
	out chan []byte

	mu       sync.Mutex
	ident    Identity
	authed   bool
	closed   bool
	room     *Room
	seat     int
	watching bool
	tok      int
	tokAt    time.Time
	authTmr  *time.Timer
	plays    int
	playsAt  time.Time
}

// Connect registers a browser connection. The first frame must be AUTH within AuthTimeout.
func (h *Hub) Connect(t Transport) *Conn {
	c := &Conn{hub: h, t: t, out: make(chan []byte, 512)}
	h.mu.Lock()
	h.conns[c] = struct{}{}
	h.mu.Unlock()
	go c.writer()
	c.authTmr = time.AfterFunc(h.cfg.AuthTimeout, func() {
		c.mu.Lock()
		ok := c.authed
		c.mu.Unlock()
		if !ok {
			c.send(errorFrame(ErrBadHello, "no sign-in"))
			c.Close()
		}
	})
	return c
}

// writer is the only goroutine that touches the transport, so a slow browser stalls only itself.
func (c *Conn) writer() {
	for b := range c.out {
		if c.t == nil {
			continue
		}
		if err := c.t.Send(b); err != nil {
			c.Close()
			return
		}
	}
}

// send queues a frame; a browser that cannot keep up is dropped rather than allowed to hold a match back.
func (c *Conn) send(b []byte) {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return
	}
	select {
	case c.out <- b:
	default:
		go c.Close()
	}
}

// Close drops the connection. Leaving a match is not an instant loss: the seat has ReconnectGrace to come back.
func (c *Conn) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	room := c.room
	id := c.ident.ID
	c.mu.Unlock()
	if c.authTmr != nil {
		c.authTmr.Stop()
	}
	c.hub.dequeue(c)
	if room != nil {
		room.do(func() { room.detach(c) })
	}
	c.hub.mu.Lock()
	delete(c.hub.conns, c)
	if id != "" && c.hub.byPlayer[id] == c {
		delete(c.hub.byPlayer, id)
	}
	c.hub.mu.Unlock()
	close(c.out)
	if c.t != nil {
		c.t.Close()
	}
}

func (c *Conn) allow() bool {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if now.Sub(c.tokAt) >= time.Second {
		c.tokAt = now
		c.tok = 0
	}
	c.tok++
	return c.tok <= c.hub.cfg.MaxFramesPerS
}

// Handle processes one frame from the browser.
func (c *Conn) Handle(msg []byte) {
	if len(msg) == 0 || len(msg) > 2048 {
		return
	}
	if msg[0] == CAuth {
		c.onAuth(msg)
		return
	}
	c.mu.Lock()
	authed := c.authed
	c.mu.Unlock()
	// looking in on a match needs no account; everything else does
	if !authed && msg[0] != CWatch && msg[0] != CPing {
		c.send(errorFrame(ErrBadHello, "sign in first"))
		return
	}
	want, known := clientLen[msg[0]]
	if !known || len(msg)-1 != want {
		return // anything but a player's own orders is dropped
	}
	if !c.allow() {
		return
	}
	switch msg[0] {
	case CPing:
		c.send([]byte{GPong, msg[1], msg[2], msg[3], msg[4]})
	case CQueue:
		c.onQueue(msg)
	case CLeave:
		c.hub.dequeue(c)
	case CWatch:
		id := binary.LittleEndian.Uint32(msg[1:])
		if err := c.hub.watch(c, id); err != nil {
			c.send(errorFrame(ErrNoRoom, "that match has finished"))
			return
		}
		if c.authTmr != nil {
			c.authTmr.Stop() // a watcher is welcome without signing in
		}
	case CPlay, CEmote, CResync, CResign:
		c.mu.Lock()
		room := c.room
		c.mu.Unlock()
		if room == nil {
			return
		}
		m := roomMsg{c: c, kind: msg[0]}
		if len(msg) > 1 {
			m.a = msg[1]
		}
		if len(msg) > 2 {
			m.b = msg[2]
		}
		if len(msg) > 3 {
			m.d = msg[3]
		}
		select {
		case room.in <- m:
		default:
		}
	}
}

func (c *Conn) onAuth(msg []byte) {
	c.mu.Lock()
	already := c.authed
	c.mu.Unlock()
	if already {
		return
	}
	if len(msg) < 3 {
		c.send(errorFrame(ErrAuth, "no token"))
		return
	}
	n := int(binary.LittleEndian.Uint16(msg[1:]))
	if n == 0 || n > 1900 || len(msg) != 3+n {
		c.send(errorFrame(ErrAuth, "bad token frame"))
		return
	}
	token := string(msg[3:])
	ctx, cancel := context.WithTimeout(c.hub.ctx, 8*time.Second)
	defer cancel()
	id, err := c.hub.resolve(ctx, token)
	if err != nil || id.ID == "" {
		c.send(errorFrame(ErrAuth, "your sign-in was not accepted: "+firstOf(err)))
		return
	}
	var old *Conn
	var room *Room
	c.hub.mu.Lock()
	if o := c.hub.byPlayer[id.ID]; o != nil && o != c {
		old = o
	}
	c.hub.byPlayer[id.ID] = c
	room = c.hub.inRoom[id.ID]
	c.hub.mu.Unlock()
	c.mu.Lock()
	c.ident = id
	c.authed = true
	c.mu.Unlock()
	if old != nil {
		old.send(errorFrame(ErrTaken, "you opened EMPIRE somewhere else"))
		old.Close()
	}
	c.send(identFrame(id.Guest, id.Name, id.ID))
	if room != nil {
		room.do(func() { room.reattach(id.ID, c) })
	}
}

func firstOf(err error) string {
	if err == nil {
		return "unknown"
	}
	s := err.Error()
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

func (c *Conn) onQueue(msg []byte) {
	version := binary.LittleEndian.Uint32(msg[1:])
	deck := binary.LittleEndian.Uint32(msg[5:])
	fac, want := int(msg[9]), int(msg[10])
	if version != c.hub.Version() {
		c.send(errorFrame(ErrVersion, "EMPIRE was updated while this page was open: refresh the page"))
		return
	}
	if !deckValid(deck) || fac > 2 || want > 8 {
		c.send(errorFrame(ErrBadDeck, "a deck is eight different cards and a faction"))
		return
	}
	c.mu.Lock()
	busy := c.room != nil
	ident := c.ident
	c.mu.Unlock()
	if busy {
		return
	}
	rating := 1500.0
	ctx, cancel := context.WithTimeout(c.hub.ctx, 5*time.Second)
	if r, err := c.hub.rec.Rating(ctx, ident.ID); err == nil {
		rating = r
	}
	cancel()
	c.hub.enqueue(&queued{c: c, since: time.Now(), deck: deck, faction: fac, want: want, rating: rating})
}
