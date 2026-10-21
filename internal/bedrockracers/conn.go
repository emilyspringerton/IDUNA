package bedrockracers

import (
	"context"
	"encoding/binary"
	"sync"
	"time"
)

// Conn is one browser connection.
type Conn struct {
	hub  *Hub
	t    Transport
	out  chan []byte
	done chan struct{} // closed by Close: out itself is never closed, so a send can never race it

	mu      sync.Mutex
	ident   Identity
	hello   bool
	closed  bool
	room    *Room
	slot    int
	authTmr *time.Timer
	tok     int
	tokAt   time.Time
}

// Connect registers a browser connection. The first frame must be HELLO within AuthTimeout.
func (h *Hub) Connect(t Transport) *Conn {
	c := &Conn{hub: h, t: t, out: make(chan []byte, 1200), done: make(chan struct{})}
	h.mu.Lock()
	over := len(h.conns) >= h.cfg.MaxConns
	if !over {
		h.conns[c] = struct{}{}
	}
	h.mu.Unlock()
	if over {
		c.closed = true
		close(c.done)
		_ = t.Send(errorFrame(ErrBusy, "Bedrock Racers is full: try again in a minute"))
		t.Close()
		return c
	}
	go c.writer()
	c.authTmr = time.AfterFunc(h.cfg.AuthTimeout, func() {
		c.mu.Lock()
		ok := c.hello
		c.mu.Unlock()
		if !ok {
			c.sendNow(errorFrame(ErrBadFrame, "no HELLO"))
			c.Close()
		}
	})
	return c
}

func (c *Conn) identID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ident.ID
}

// writer is the only goroutine that touches the transport, so a slow browser stalls only itself.
func (c *Conn) writer() {
	for {
		select {
		case b := <-c.out:
			if c.t == nil {
				continue
			}
			if err := c.t.Send(b); err != nil {
				c.Close()
				return
			}
		case <-c.done:
			return
		}
	}
}

// sendNow writes a frame straight to the transport, for the last word before a Close (the writer would be stopped first).
func (c *Conn) sendNow(b []byte) {
	if c.t != nil {
		_ = c.t.Send(b)
	}
}

// send queues a frame; a browser that cannot keep up (about 19 seconds of ticks behind) is dropped rather than allowed to hold anything back.
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

// Close drops the connection. A person in a match becomes a bot at the next tick.
func (c *Conn) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	room := c.room
	slot := c.slot
	id := c.ident.ID
	c.mu.Unlock()
	if c.authTmr != nil {
		c.authTmr.Stop()
	}
	c.hub.dequeue(c)
	if room != nil {
		room.leave(c, slot)
	}
	c.hub.mu.Lock()
	delete(c.hub.conns, c)
	if id != "" && c.hub.byPlayer[id] == c {
		delete(c.hub.byPlayer, id)
	}
	c.hub.mu.Unlock()
	close(c.done)
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

// the length each client frame must have (type byte included); HELLO is variable
var clientLen = map[byte]int{CQueue: 3, CLeave: 1, CCmd: 9, CPing: 5, CHash: 9}

// Handle processes one frame from the browser.
func (c *Conn) Handle(msg []byte) {
	if len(msg) == 0 || len(msg) > 2048 {
		return
	}
	if msg[0] == CHello {
		c.onHello(msg)
		return
	}
	c.mu.Lock()
	hello := c.hello
	c.mu.Unlock()
	if !hello {
		c.send(errorFrame(ErrBadFrame, "say HELLO first"))
		return
	}
	want, known := clientLen[msg[0]]
	if !known || len(msg) != want {
		return
	}
	if !c.allow() {
		return
	}
	switch msg[0] {
	case CPing:
		c.send(pongFrame(binary.LittleEndian.Uint32(msg[1:])))
	case CQueue:
		c.onQueue(msg)
	case CLeave:
		c.onLeave()
	case CCmd, CHash:
		c.mu.Lock()
		room, slot := c.room, c.slot
		c.mu.Unlock()
		if room == nil {
			return
		}
		m := roomMsg{c: c, slot: slot, kind: msg[0]}
		if msg[0] == CCmd {
			copy(m.cmd[:], msg[1:9])
		} else {
			m.ticks = binary.LittleEndian.Uint32(msg[1:])
			m.hash = binary.LittleEndian.Uint32(msg[5:])
		}
		select {
		case room.in <- m:
		default:
		}
	}
}

func (c *Conn) onHello(msg []byte) {
	c.mu.Lock()
	already := c.hello
	c.mu.Unlock()
	if already {
		return
	}
	h, ok := parseHello(msg)
	if !ok {
		c.send(errorFrame(ErrBadFrame, "bad HELLO"))
		return
	}
	if h.Ver != Version || h.SimVer != c.hub.Version() {
		c.sendNow(errorFrame(ErrVersion, "Bedrock Racers was updated while this page was open: reload the page"))
		c.Close()
		return
	}
	id := Identity{Name: h.Name}
	auth := byte(0)
	if h.Token != "" {
		ctx, cancel := context.WithTimeout(c.hub.ctx, 8*time.Second)
		got, err := c.hub.resolve(ctx, h.Token)
		cancel()
		if err != nil || got.ID == "" {
			auth = 2 // not accepted: they play as a guest by name, nothing is recorded
		} else {
			id = got
			auth = 1
			if got.Guest {
				auth = 0
			}
		}
	}
	if id.Name == "" {
		id.Name = "Racer"
	}
	var old *Conn
	if id.ID != "" {
		c.hub.mu.Lock()
		if o := c.hub.byPlayer[id.ID]; o != nil && o != c {
			old = o
		}
		c.hub.byPlayer[id.ID] = c
		c.hub.mu.Unlock()
	}
	c.mu.Lock()
	c.ident = id
	c.hello = true
	c.mu.Unlock()
	if c.authTmr != nil {
		c.authTmr.Stop()
	}
	if old != nil {
		old.sendNow(errorFrame(ErrTaken, "you opened Bedrock Racers somewhere else"))
		old.Close()
	}
	c.hub.mu.Lock()
	online := c.hub.online()
	c.hub.mu.Unlock()
	c.send(welcomeFrame(auth, c.hub.Version(), id.Name, online, id.ID))
}

func (c *Conn) onQueue(msg []byte) {
	team, level := int(msg[1]), int(msg[2])
	if team > 3 {
		team = -1
	}
	if level < 1 || level > 3 {
		level = 2
	}
	c.mu.Lock()
	busy := c.room != nil
	c.mu.Unlock()
	if busy {
		return
	}
	c.hub.enqueue(&queued{c: c, since: time.Now(), team: team, level: level})
}

func (c *Conn) onLeave() {
	c.mu.Lock()
	room, slot := c.room, c.slot
	c.room = nil
	c.mu.Unlock()
	c.hub.dequeue(c)
	if room != nil {
		room.leave(c, slot)
	}
}
