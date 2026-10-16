package handlers

// shankpit_edit_sessions.go -- live map-editing sessions shared by the NOCK web editor and the native
// SHANKPIT client (kanban #518/#519/#520, plan cards EDIT-1..4; founder real-time: "shankpit map editor needs to
// sync up live to NOCK tools so we can join a live editing session from either the shankpit side or the nock
// side", "you move the spawner around in nock and it moves your character in shankpit -- move your character in
// shankpit it moves the spawner", "a toggle between Spawning on spawner OR spawning where the characters
// crosshairs is").
//
// Shape: a session is a small append-only event log plus a materialised "latest state". Either side creates a
// session (POST), the other joins by its id, both POST events and long-poll GET ?since=<seq>. The session id is
// a 128-bit random capability token, which is the only credential -- the same trade shankpit_levels_public.go's
// snapshot endpoint makes (SHANKPIT has no player login to attach yet), bounded by a per-IP create rate limit,
// body and event caps, a session count cap and a TTL. Nothing here writes a level: edits travel as events and the
// NOCK editor (admin-gated) is what saves them.
//
// In memory on purpose: a session is a live editing room, not a record. A server restart ends every room and the
// clients simply create a new one.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"iduna/internal/http/middleware"
	"iduna/internal/shankpit"
)

const (
	editSessionTTL        = 6 * time.Hour
	editSessionMaxCount   = 200
	editSessionMaxEvents  = 2000
	editSessionMaxBody    = 8 * 1024
	editSessionMaxWaitSec = 25
	editSessionMaxDraft   = 4 << 20 // an unsaved level draft (walls/objects/...), far larger than an event
)

// editEventKinds are the events a session carries. Anything else is rejected so a room cannot be used as a
// general-purpose message bus.
//
//	spawner     {x,y,z,yaw}            the spawner marker moved (either side)
//	avatar      {x,y,z,yaw,pitch}      where the SHANKPIT character is (SHANKPIT -> NOCK; also drives the spawner)
//	spawn_mode  {mode:"spawner"|"crosshair"}  where a "spawn/teleport" puts the character
//	teleport    {x,y,z,yaw}            put the character here now (NOCK -> SHANKPIT; crosshair hits come from NOCK)
//	box_add     {id,x,y,z,sx,sy,sz,r,g,b}  a wall box was added
//	box_move    {id,x,y,z}             a wall box moved
//	box_size    {id,sx,sy,sz}          a wall box was resized
//	box_del     {id}                   a wall box was deleted
//	save        {}                     SHANKPIT asked for the working level to be saved (NOCK performs the save)
//	hello       {client:"nock"|"shankpit"}  presence
//	level       {rev}                  the NOCK editor's working (possibly unsaved) level changed; fetch GET {id}/level
//
// The working level itself travels out-of-band: PUT {id}/level (NOCK, full draft) and GET {id}/level (SHANKPIT,
// the native export shape of that draft). Falls back to the saved level when no draft has been pushed.
var editEventKinds = map[string]bool{
	"spawner": true, "avatar": true, "spawn_mode": true, "teleport": true,
	"box_add": true, "box_move": true, "box_size": true, "box_del": true, "hello": true,
	"level": true, "save": true,
}

type editEvent struct {
	Seq    int             `json:"seq"`
	Source string          `json:"source"`
	Kind   string          `json:"kind"`
	Data   json.RawMessage `json:"data,omitempty"`
	At     time.Time       `json:"at"`
}

type editSession struct {
	id        string
	levelID   int64
	levelName string
	created   time.Time
	touched   time.Time
	events    []editEvent
	nextSeq   int
	// latest materialised state, so a late joiner does not have to replay the log
	spawner   json.RawMessage
	avatar    json.RawMessage
	spawnMode string
	// draft is the NOCK editor's latest working level (unsaved edits included), raw updateShankpitLevelReq-shaped JSON.
	draft    json.RawMessage
	draftRev int
	// wake is closed and replaced whenever an event lands, so long-pollers wake without busy-waiting.
	wake chan struct{}
}

// ShankpitEditSessionsHandler serves /api/v1/shankpit-edit-sessions...
type ShankpitEditSessionsHandler struct {
	// CreateLimiter rate-limits session creation per client IP; nil disables the limit (tests).
	CreateLimiter *middleware.IPRateLimiter
	// Store builds the native export of a draft / the saved level for GET {id}/level; nil disables that route.
	Store *shankpit.LevelStore
	Now           func() time.Time

	mu       sync.Mutex
	sessions map[string]*editSession
}

func (h *ShankpitEditSessionsHandler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *ShankpitEditSessionsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	const prefix = "/api/v1/shankpit-edit-sessions"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, prefix), "/")
	parts := []string{}
	if rest != "" {
		parts = strings.Split(rest, "/")
	}
	switch {
	case len(parts) == 0 && r.Method == http.MethodPost:
		h.create(w, r)
	case len(parts) == 1 && r.Method == http.MethodGet:
		h.poll(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "events" && r.Method == http.MethodPost:
		h.post(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "level" && r.Method == http.MethodPut:
		h.putLevel(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "level" && r.Method == http.MethodGet:
		h.getLevel(w, r, parts[0])
	default:
		mmoWriteError(w, http.StatusMethodNotAllowed, "POST /shankpit-edit-sessions, GET /shankpit-edit-sessions/{id}?since=N, POST /shankpit-edit-sessions/{id}/events")
	}
}

func (h *ShankpitEditSessionsHandler) gcLocked(now time.Time) {
	for id, s := range h.sessions {
		if now.Sub(s.touched) > editSessionTTL {
			delete(h.sessions, id)
		}
	}
}

func (h *ShankpitEditSessionsHandler) create(w http.ResponseWriter, r *http.Request) {
	if h.CreateLimiter != nil && !h.CreateLimiter.Allow(clientIPFor(r)) {
		w.Header().Set("Retry-After", "60")
		mmoWriteError(w, http.StatusTooManyRequests, "too many edit sessions; try again shortly")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, editSessionMaxBody)
	var req struct {
		LevelID   int64  `json:"level_id"`
		LevelName string `json:"level_name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req) // an empty body is fine: a session for an unsaved/local map
	if len(req.LevelName) > 120 {
		req.LevelName = req.LevelName[:120]
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "no entropy")
		return
	}
	now := h.now()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessions == nil {
		h.sessions = map[string]*editSession{}
	}
	h.gcLocked(now)
	if len(h.sessions) >= editSessionMaxCount {
		mmoWriteError(w, http.StatusServiceUnavailable, "too many live edit sessions")
		return
	}
	s := &editSession{id: hex.EncodeToString(raw[:]), levelID: req.LevelID, levelName: req.LevelName,
		created: now, touched: now, nextSeq: 1, spawnMode: "spawner", wake: make(chan struct{})}
	h.sessions[s.id] = s
	writeJSON(w, http.StatusCreated, map[string]any{"id": s.id, "level_id": s.levelID, "level_name": s.levelName, "seq": 0})
}

func (h *ShankpitEditSessionsHandler) lookup(id string) *editSession {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.sessions[id]
	if s == nil {
		return nil
	}
	if h.now().Sub(s.touched) > editSessionTTL {
		delete(h.sessions, id)
		return nil
	}
	return s
}

// snapshotLocked returns the events after `since` plus the materialised state. Caller holds h.mu.
func (s *editSession) snapshotLocked(since int) map[string]any {
	var out []editEvent
	for _, e := range s.events {
		if e.Seq > since {
			out = append(out, e)
		}
	}
	if out == nil {
		out = []editEvent{}
	}
	return map[string]any{
		"id": s.id, "level_id": s.levelID, "level_name": s.levelName,
		"seq": s.nextSeq - 1, "events": out, "level_rev": s.draftRev,
		"state": map[string]any{"spawner": s.spawner, "avatar": s.avatar, "spawn_mode": s.spawnMode},
	}
}

// poll returns events after ?since=N. With ?wait=<seconds> and nothing new it holds the request open
// (long-poll, capped) until an event arrives -- SHANKPIT's curl-based client and the browser both just loop on it.
func (h *ShankpitEditSessionsHandler) poll(w http.ResponseWriter, r *http.Request, id string) {
	if h.lookup(id) == nil {
		mmoWriteError(w, http.StatusNotFound, "no such edit session (it may have expired or the server restarted)")
		return
	}
	since, _ := strconv.Atoi(r.URL.Query().Get("since"))
	wait, _ := strconv.Atoi(r.URL.Query().Get("wait"))
	if wait > editSessionMaxWaitSec {
		wait = editSessionMaxWaitSec
	}
	deadline := time.NewTimer(time.Duration(wait) * time.Second)
	defer deadline.Stop()
	for {
		h.mu.Lock()
		s := h.sessions[id]
		if s == nil {
			h.mu.Unlock()
			mmoWriteError(w, http.StatusNotFound, "edit session ended")
			return
		}
		s.touched = h.now()
		if s.nextSeq-1 > since || wait == 0 {
			snap := s.snapshotLocked(since)
			h.mu.Unlock()
			writeJSON(w, http.StatusOK, snap)
			return
		}
		wake := s.wake
		h.mu.Unlock()
		select {
		case <-wake:
		case <-deadline.C:
			h.mu.Lock()
			snap := s.snapshotLocked(since)
			h.mu.Unlock()
			writeJSON(w, http.StatusOK, snap)
			return
		case <-r.Context().Done():
			return
		}
	}
}

func (h *ShankpitEditSessionsHandler) post(w http.ResponseWriter, r *http.Request, id string) {
	r.Body = http.MaxBytesReader(w, r.Body, editSessionMaxBody)
	var req struct {
		Source string          `json:"source"`
		Kind   string          `json:"kind"`
		Data   json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if !editEventKinds[req.Kind] {
		mmoWriteError(w, http.StatusBadRequest, "unknown event kind "+strconv.Quote(req.Kind))
		return
	}
	if req.Source != "nock" && req.Source != "shankpit" {
		mmoWriteError(w, http.StatusBadRequest, `source must be "nock" or "shankpit"`)
		return
	}
	if len(req.Data) > 0 && !json.Valid(req.Data) {
		mmoWriteError(w, http.StatusBadRequest, "data must be JSON")
		return
	}
	if req.Kind == "spawn_mode" {
		var d struct {
			Mode string `json:"mode"`
		}
		if json.Unmarshal(req.Data, &d) != nil || (d.Mode != "spawner" && d.Mode != "crosshair") {
			mmoWriteError(w, http.StatusBadRequest, `spawn_mode data must be {"mode":"spawner"|"crosshair"}`)
			return
		}
	}
	h.mu.Lock()
	s := h.sessions[id]
	if s == nil || h.now().Sub(s.touched) > editSessionTTL {
		h.mu.Unlock()
		mmoWriteError(w, http.StatusNotFound, "no such edit session")
		return
	}
	now := h.now()
	s.touched = now
	ev := editEvent{Seq: s.nextSeq, Source: req.Source, Kind: req.Kind, Data: req.Data, At: now}
	s.nextSeq++
	if req.Kind == "avatar" || req.Kind == "spawner" { // pose streams: keep only the latest of each (source,kind), the log stays about edits
		kept := s.events[:0]
		for _, old := range s.events {
			if !(old.Kind == req.Kind && old.Source == req.Source) {
				kept = append(kept, old)
			}
		}
		s.events = kept
	}
	s.events = append(s.events, ev)
	if len(s.events) > editSessionMaxEvents {
		s.events = append([]editEvent(nil), s.events[len(s.events)-editSessionMaxEvents:]...)
	}
	switch req.Kind {
	case "spawner":
		s.spawner = req.Data
	case "avatar":
		s.avatar = req.Data
	case "spawn_mode":
		var d struct {
			Mode string `json:"mode"`
		}
		_ = json.Unmarshal(req.Data, &d)
		s.spawnMode = d.Mode
	}
	close(s.wake)
	s.wake = make(chan struct{})
	h.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{"seq": ev.Seq})
}

// putLevel stores the editor's current working level and announces it with a "level" event. Full-draft PUT (the
// same body the editor's save sends), debounced client-side; last write wins.
func (h *ShankpitEditSessionsHandler) putLevel(w http.ResponseWriter, r *http.Request, id string) {
	r.Body = http.MaxBytesReader(w, r.Body, editSessionMaxDraft)
	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON (or draft over 4 MiB)")
		return
	}
	var probe updateShankpitLevelReq
	if err := json.Unmarshal(raw, &probe); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "draft must be a level body: "+err.Error())
		return
	}
	h.mu.Lock()
	s := h.sessions[id]
	if s == nil || h.now().Sub(s.touched) > editSessionTTL {
		h.mu.Unlock()
		mmoWriteError(w, http.StatusNotFound, "no such edit session")
		return
	}
	now := h.now()
	s.touched = now
	s.draft = raw
	s.draftRev++
	data, _ := json.Marshal(map[string]int{"rev": s.draftRev})
	kept := s.events[:0]
	for _, old := range s.events { // only the newest "level" notice matters
		if old.Kind != "level" {
			kept = append(kept, old)
		}
	}
	s.events = append(kept, editEvent{Seq: s.nextSeq, Source: "nock", Kind: "level", Data: data, At: now})
	s.nextSeq++
	rev := s.draftRev
	close(s.wake)
	s.wake = make(chan struct{})
	h.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"rev": rev})
}

// getLevel returns the native export (ExportDoc shape, what the SHANKPIT loader parses) of the session's working
// level: the pushed draft if any, else the saved level the session was created for.
func (h *ShankpitEditSessionsHandler) getLevel(w http.ResponseWriter, r *http.Request, id string) {
	if h.Store == nil {
		mmoWriteError(w, http.StatusNotImplemented, "level export not configured")
		return
	}
	s := h.lookup(id)
	if s == nil {
		mmoWriteError(w, http.StatusNotFound, "no such edit session")
		return
	}
	h.mu.Lock()
	draft, rev, levelID, name := s.draft, s.draftRev, s.levelID, s.levelName
	h.mu.Unlock()
	var doc *shankpit.ExportDoc
	var err error
	if len(draft) > 0 {
		var req updateShankpitLevelReq
		if err = json.Unmarshal(draft, &req); err == nil {
			lvl := &shankpit.Level{ID: levelID, Name: name, Width: req.Width, Height: req.Height, Depth: req.Depth,
				GroundPlaneEnabled: req.GroundPlaneEnabled, GroundPlaneSquares: req.GroundPlaneSquares,
				Walls: req.Walls, Objects: req.Objects, Spawners: req.Spawners, Doors: req.Doors,
				NavNodes: req.NavNodes, Characters: req.Characters, LevelExits: req.LevelExits, NextLevelID: req.NextLevelID}
			if levelID > 0 { // carry the saved level's own non-draft fields (enclosed, floor tint, ...)
				if saved, gerr := h.Store.GetLevel(r.Context(), levelID); gerr == nil {
					lvl.Enclosed, lvl.FloorTint, lvl.BrickDamage = saved.Enclosed, saved.FloorTint, saved.BrickDamage
				}
			}
			doc, err = h.Store.ExportLevel(r.Context(), lvl)
		}
	} else if levelID > 0 {
		doc, err = h.Store.Export(r.Context(), levelID)
	} else {
		mmoWriteError(w, http.StatusNotFound, "session has no level yet")
		return
	}
	if err != nil {
		mmoWriteError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	w.Header().Set("X-Level-Rev", strconv.Itoa(rev))
	writeJSON(w, http.StatusOK, doc)
}
