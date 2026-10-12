package handlers

// hs_live.go -- real-time Hearthstone deck tracking for WOTAN, private per player.
//
// Flow: the player's PC streams two log files' new lines to POST /live/lines using a short-lived,
// upload-scoped token minted for their own IDUNA identity (POST /live/token); the server keeps ONE
// session per IDUNA sub in memory, runs the HRIP PARENA tracker over the current game's lines, joins
// the queued deck (Decks.log "Finding Game With Deck" -> deck code -> hs_cards) and serves the result
// to that same sub only (GET /live/state). Nothing here is public: state is keyed by the verified
// token subject, so one player can never read or write another's session.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"iduna/internal/auth/jwt"
	"iduna/internal/hsdeck"
	"iduna/internal/hslz4"
	"iduna/internal/hstracker"
	"iduna/internal/http/middleware"
)

const (
	hsLiveUploadAud   = "hs-live-upload"
	hsLiveTokenTTL    = 12 * time.Hour
	hsLiveMaxBody     = 4 << 20
	hsLiveMaxInflated = 16 << 20 // decompressed ceiling (bomb guard)
	hsLiveMaxDecks    = 800
	hsLiveMaxLineLen  = 4000
)

// hsLiveMaxPower/hsLiveHeadPower are vars, not consts, so a test can shrink them to exercise the
// trim path against a real (but modest-sized) fixture instead of needing an actual 120,000-line
// capture -- see trimPowerLines's own comment for why the head must never be trimmed away.
var (
	hsLiveMaxPower  = 120000 // lines kept for the current game
	hsLiveHeadPower = 6000   // always-preserved prefix -- see trimPowerLines
)

var (
	hsLogPrefixRe = regexp.MustCompile(`^[A-Z] \d{2}:\d{2}:\d{2}\.\d+ `)
	hsDeckCodeRe  = regexp.MustCompile(`^AAE[A-Za-z0-9+/=]{8,}$`)
	hsLogClockRe  = regexp.MustCompile(`^[A-Z] (\d{2}):(\d{2}):(\d{2}\.\d+) `)
	// Real log, not assumed: PowerTaskList.DebugPrint*() is read on equal footing with GameState's,
	// not dropped as a presumed duplicate. CREATE_GAME itself, and whole nested TRIGGER/POWER
	// sub-blocks, were found with NO GameState copy at all on a real Hearthstone client build
	// (the live tracker never saw CREATE_GAME for that match, so it stayed "waiting for the game to
	// start" for the whole game -- see game_state.prn's handle-line for the matching parser fix).
	hsPowerLineRe = regexp.MustCompile(`^D \d{2}:\d{2}:\d{2}\.\d+ (?:GameState|PowerTaskList)\.DebugPrint(Power|Game)\(\) - `)
)

type liveSession struct {
	mu       sync.Mutex
	sub      string
	decks    []string
	power    []string
	createTS string // clock of the current game's own CREATE_GAME line (see the wipe check in liveLines)
	dirty    bool
	base     bool // a reset (full resend) has been received since this session was created
	client   liveClient
	computed time.Time
	state    liveState
	updated  time.Time
	synced   map[string]bool // deck-library syncs already done (deck id + code + game start)

	keyID      int64     // upload key the current uplink exchanged (0 = a normal sign-in token)
	keyTouched time.Time // last time keyID's last_used_at was written
}

// liveClient is what the uplink reports about the game's log-size limit (Hearthstone stops writing
// Power.log at ~10,000 KiB per session unless client.config has FileSizeLimit.Int=-1).
type liveClient struct {
	Known         bool
	CapFixed      bool
	RestartNeeded bool
	PowerBytes    int64
	FixError      string
}

const (
	hsLogCapBytes  = 10000 * 1024 // Hearthstone's default per-session Power.log limit
	hsLogNearBytes = 9000 * 1024
)

// logWarnings turns the uplink's report into plain-language warnings for the page.
func (c liveClient) logWarnings() []string {
	if !c.Known {
		return nil
	}
	var w []string
	switch {
	case c.CapFixed && c.RestartNeeded:
		w = append(w, "Hearthstone's log limit has been lifted in client.config. Restart Hearthstone once so it takes effect; until then the log still stops at about 10 MB.")
	case !c.CapFixed && c.PowerBytes >= hsLogCapBytes:
		w = append(w, "Hearthstone's log reached its 10 MB limit and has stopped recording, so tracking is frozen. Restart Hearthstone to continue (the uplink can lift the limit for good).")
	case !c.CapFixed && c.PowerBytes >= hsLogNearBytes:
		w = append(w, "Hearthstone's log is nearly full (limit about 10 MB). It will stop recording soon.")
	}
	if c.FixError != "" {
		w = append(w, "Could not lift the log limit automatically: "+c.FixError)
	}
	return w
}

type liveCard struct {
	CardID string `json:"card_id"`
	Name   string `json:"name"`
	Cost   int    `json:"cost"`
	Count  int    `json:"count"`
}

type liveDeck struct {
	Name  string `json:"name"`
	ID    string `json:"id"`
	Class string `json:"class"`
	Size  int    `json:"size"`
}

type liveState struct {
	Connected  bool       `json:"connected"` // has this player's uplink ever posted
	InGame     bool       `json:"in_game"`
	Turn       int        `json:"turn"`
	Deck       *liveDeck  `json:"deck,omitempty"`
	Left       []liveCard `json:"left"`
	LeftTotal  int        `json:"left_total"`
	Drawn      []liveCard `json:"drawn"`
	DrawnTotal int        `json:"drawn_total"`
	Opponent   struct {
		Name  string     `json:"name"`
		Cards []liveCard `json:"cards"`
	} `json:"opponent"`
	LibraryDeckID int64    `json:"library_deck_id,omitempty"` // set on the poll that synced this game's deck
	SyncTarget    string   `json:"sync_target,omitempty"`     // where finished games go right now (display only)
	SyncInvalid   bool     `json:"sync_target_invalid,omitempty"`
	Note          string   `json:"note,omitempty"`
	Warnings      []string `json:"warnings,omitempty"`
	LogBytes      int64    `json:"log_bytes,omitempty"`
	UpdatedAt     string   `json:"updated_at,omitempty"`
}

func (h *HSHandler) session(sub string, create bool) *liveSession {
	h.liveMu.Lock()
	defer h.liveMu.Unlock()
	if h.liveSess == nil {
		h.liveSess = map[string]*liveSession{}
	}
	s := h.liveSess[sub]
	if s == nil && create {
		s = &liveSession{sub: sub}
		h.liveSess[sub] = s
	}
	return s
}

// liveToken mints an upload-scoped token for the caller's own identity. It can only POST /live/lines.
func (h *HSHandler) liveToken(w http.ResponseWriter, v hsViewer) {
	if v.sub == "" {
		hsErr(w, http.StatusUnauthorized, "sign in with IDUNA first")
		return
	}
	tok, ok := h.mintUploadToken(v.sub, 0)
	if !ok {
		hsErr(w, http.StatusInternalServerError, "could not mint token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": tok, "expires_in": int(hsLiveTokenTTL.Seconds())})
}

// mintUploadToken signs the 12 h hs-live-upload JWT. keyID > 0 stamps the upload key it came from so ingest
// can attribute counters to it and refuse it the moment that key is revoked.
func (h *HSHandler) mintUploadToken(sub string, keyID int64) (string, bool) {
	now := time.Now().UTC()
	claims := map[string]any{
		"sub": sub, "aud": hsLiveUploadAud, "iss": "iduna",
		"iat": now.Unix(), "exp": now.Add(hsLiveTokenTTL).Unix(),
		"permissions": []string{"hs.live.upload"},
	}
	if keyID > 0 {
		claims["key_id"] = keyID
	}
	tok, err := jwt.Sign(h.Keys, claims)
	return tok, err == nil
}

type liveLinesReq struct {
	Reset  bool     `json:"reset"`
	Decks  []string `json:"decks"`
	Power  []string `json:"power"`
	Client *struct {
		CapFixed      bool   `json:"cap_fixed"`      // client.config has FileSizeLimit.Int=-1
		RestartNeeded bool   `json:"restart_needed"` // the fix was written after this Hearthstone process started
		PowerBytes    int64  `json:"power_bytes"`    // current size of the session's Power.log
		FixError      string `json:"fix_error"`      // why the uplink could not apply the fix, if it could not
	} `json:"client"`
}

// liveLines accepts new log lines. Auth: an upload-scoped token or a normal IDUNA token; either way the
// session is the token's own subject.
func (h *HSHandler) liveLines(w http.ResponseWriter, r *http.Request) {
	ah := r.Header.Get("Authorization")
	if !strings.HasPrefix(ah, "Bearer ") || h.Keys == nil {
		hsErr(w, http.StatusUnauthorized, "missing upload token")
		return
	}
	claims, err := jwt.Verify(h.Keys, strings.TrimPrefix(ah, "Bearer "))
	sub, _ := claims["sub"].(string)
	if aud, _ := claims["aud"].(string); err != nil || sub == "" || aud == clubTokenAud || aud == oauthStateAud {
		hsErr(w, http.StatusUnauthorized, "invalid or expired upload token")
		return
	}
	// A token minted from an upload key dies the instant that key is revoked, not at its 12 h expiry.
	keyID, keyOK := h.liveKeyOK(claims, sub)
	if !keyOK {
		h.dropLiveSession(sub, keyID)
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "upload key revoked", "code": "key_revoked"})
		return
	}
	// The uplink posts about once a second while a game is on, so it must not share the 40/min
	// deck-write limiter (that starved it after ~1 minute and dropped batches). Separate, generous
	// per-subject bucket; still bounded (and the body is capped) so it cannot be abused.
	if h.WriteLimiter != nil {
		h.liveMu.Lock()
		if h.liveLim == nil {
			h.liveLim = middleware.NewIPRateLimiter(900)
		}
		lim := h.liveLim
		h.liveMu.Unlock()
		if !lim.Allow("hslive:" + sub) {
			w.Header().Set("Retry-After", "5")
			hsErr(w, http.StatusTooManyRequests, "slow down")
			return
		}
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, hsLiveMaxBody))
	if err != nil {
		hsErr(w, http.StatusRequestEntityTooLarge, "batch too large")
		return
	}
	// LZ4 frames (X-Body-Encoding: lz4-frames) -- the uplink's default; plain JSON still accepted.
	// A custom header, not Content-Encoding, so no intermediary tries to "helpfully" decode it.
	if enc := r.Header.Get("X-Body-Encoding"); enc != "" {
		if enc != "lz4-frames" {
			hsErr(w, http.StatusUnsupportedMediaType, "unsupported body encoding")
			return
		}
		raw, err := hslz4.DecodeFrames(body, hsLiveMaxInflated)
		if err != nil {
			hsErr(w, http.StatusUnprocessableEntity, "bad lz4 body")
			return
		}
		body = raw
	}
	var req liveLinesReq
	if err := json.Unmarshal(body, &req); err != nil {
		hsErr(w, http.StatusUnprocessableEntity, "bad json")
		return
	}
	s := h.session(sub, true)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keyID = keyID
	h.touchKey(s)
	gameEnded := false
	if req.Reset {
		s.power = s.power[:0]
		s.base = true
	}
	for _, ln := range req.Decks {
		ln = strings.TrimRight(ln, "\r\n")
		if len(ln) > hsLiveMaxLineLen || !hsLogPrefixRe.MatchString(ln) && !strings.HasPrefix(ln, "###") && !strings.HasPrefix(ln, "# Deck ID") {
			continue
		}
		s.decks = append(s.decks, ln)
	}
	if len(s.decks) > hsLiveMaxDecks {
		s.decks = append([]string(nil), s.decks[len(s.decks)-hsLiveMaxDecks:]...)
	}
	for _, ln := range req.Power {
		ln = strings.TrimRight(ln, "\r\n")
		if len(ln) > hsLiveMaxLineLen || !hsPowerLineRe.MatchString(ln) {
			continue
		}
		// Hardened 2026-10-12 alongside the real bug this was meant to guard against (root-caused in
		// internal/hstracker's game_state.prn, against a real captured Power.log): Hearthstone logs
		// every CREATE_GAME twice -- once under GameState.DebugPrintPower(), once under
		// PowerTaskList.DebugPrintPower(), same instant, same event, with the PowerTaskList copy
		// indented a few extra spaces before CREATE_GAME. The old check here (HasSuffix against one
		// fixed-spacing string) never actually matched that real indented shape, so it happened to
		// never wipe on it -- not a deliberate guard, an accident of too-narrow a string match that
		// would have broken the moment the duplicate's exact spacing changed. This now genuinely
		// recognizes the real shape (marker text + CREATE_GAME suffix, independent of what's between
		// them) and distinguishes "the same game's own duplicate" from "a real new game" by clock
		// instead of relying on not matching at all: a genuinely new game's CREATE_GAME has a
		// materially later clock than the one already being tracked; the duplicate shares it exactly.
		if strings.Contains(ln, "DebugPrintPower() - ") && strings.HasSuffix(strings.TrimSpace(ln), "CREATE_GAME") {
			ts := hsLogClock(ln)
			if ts == "" || ts != s.createTS {
				s.power = s.power[:0] // a new game: only the current one is tracked live
				s.createTS = ts
			}
		}
		s.power = append(s.power, ln)
		if strings.Contains(ln, "value=COMPLETE") || strings.Contains(ln, "tag=PLAYSTATE") {
			gameEnded = true
		}
	}
	s.power = trimPowerLines(s.power, hsLiveMaxPower, hsLiveHeadPower)
	if req.Client != nil {
		s.client = liveClient{Known: true, CapFixed: req.Client.CapFixed, RestartNeeded: req.Client.RestartNeeded,
			PowerBytes: req.Client.PowerBytes, FixError: req.Client.FixError}
	}
	if len(req.Decks) > 0 || len(req.Power) > 0 || req.Reset {
		s.dirty = true
		s.updated = time.Now()
	}
	// A game just ended: compute now (not on the next page poll) so its deck is synced to the library
	// even when no browser tab is open.
	if gameEnded && s.dirty {
		s.state = h.computeLive(r.Context(), s)
		s.computed = time.Now()
		s.dirty = false
	}
	// After a server restart (every deploy wipes in-memory sessions) the uplink keeps sending only new
	// lines; without a base the tracker cannot know the deck or the game. Ask it to resend from the start.
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "power_lines": len(s.power), "resync": !s.base})
}

// trimPowerLines bounds the current game's line buffer to max lines, but always keeps the first
// head lines untouched rather than sliding the whole window forward.
//
// Regression (2026-10-12, founder real-time, mid-session: "it is still showing opponent but not
// me" / "we reached the end of our log or something"): the old trim kept only the newest max
// lines. Hearthstone declares every one of the local player's own deck cards (FULL_ENTITY + an
// indented tag=ZONE value=DECK) and both players' heroes in ONE burst right after CREATE_GAME --
// a one-time cost bounded by deck size (~30 cards), never repeated for the rest of the match: a
// card drawn on turn 20 gets only a TAG_CHANGE ZONE on the entity id that opening burst already
// gave a card id to, not a fresh FULL_ENTITY of its own. A single long or board-flood game (every
// minion buff/combat trigger logs verbosely) can push the total line count past the cap purely
// from mid-game text, and a tail-only trim silently discards that one-time opening burst along
// with it -- permanently blinding entities-json (and resolveMe's classOfPlayer, which reads the
// same early hero reveals) for the LOCAL player's own cards for the rest of the match, while the
// OPPONENT's revealed cards keep working fine (each reveal is logged fresh, right when they play
// it, always well within the trailing window -- never a one-time early burst). resolveMe still
// recovers `me` via known-plays (a live, recent signal), so the live page never falls back to the
// generic "waiting for your opening hand" note -- it looks fully alive, opponent panel correctly
// populated, while the player's own deck/drawn counts freeze at the full deck: exactly the
// symptom reported. The default head (hsLiveHeadPower) is ~7x the real opening burst's measured
// size (real_duplicate_create_game.log's own CREATE_GAME-through-mulligan window is under 900
// lines for a 30-card deck) and costs ~5% of the total cap.
func trimPowerLines(lines []string, max, head int) []string {
	if len(lines) <= max {
		return lines
	}
	if head > max {
		head = max
	}
	kept := make([]string, 0, max)
	kept = append(kept, lines[:head]...)
	kept = append(kept, lines[len(lines)-(max-head):]...)
	return kept
}

// liveState returns the caller's own tracker state.
func (h *HSHandler) liveState(w http.ResponseWriter, r *http.Request, v hsViewer) {
	if v.sub == "" {
		hsErr(w, http.StatusUnauthorized, "sign in with IDUNA first")
		return
	}
	s := h.session(v.sub, false)
	if s == nil {
		tgt := h.syncTargetOf(v.sub)
		writeJSON(w, http.StatusOK, liveState{Left: []liveCard{}, Drawn: []liveCard{}, Note: "Waiting for your PC. Start the uplink from this page.",
			SyncTarget: tgt.Label(), SyncInvalid: !tgt.Valid})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dirty { // recompute whenever new lines arrived (a tracker run is ~0.1s; polls are >=1s apart)
		s.state = h.computeLive(r.Context(), s)
		s.computed = time.Now()
		s.dirty = false
	}
	st := s.state
	st.Connected = true
	st.Warnings = s.client.logWarnings()
	st.LogBytes = s.client.PowerBytes
	if st.Left == nil {
		st.Left = []liveCard{}
	}
	if st.Drawn == nil {
		st.Drawn = []liveCard{}
	}
	st.UpdatedAt = s.updated.UTC().Format(time.RFC3339)
	tgt := h.syncTargetOf(v.sub)
	st.SyncTarget, st.SyncInvalid = tgt.Label(), !tgt.Valid
	writeJSON(w, http.StatusOK, st)
}

type queuedDeck struct{ name, id, code, ts string }

// lastQueuedDeck finds the most recent "Finding Game With Deck:" block in Decks.log lines.
func lastQueuedDeck(lines []string) (queuedDeck, bool) {
	var cur, last queuedDeck
	var found, in bool
	for _, raw := range lines {
		c := strings.TrimSpace(hsLogPrefixRe.ReplaceAllString(raw, ""))
		switch {
		case c == "Finding Game With Deck:":
			cur, in = queuedDeck{ts: hsLogClock(raw)}, true
		case in && strings.HasPrefix(c, "### "):
			cur.name = strings.TrimPrefix(c, "### ")
		case in && strings.HasPrefix(c, "# Deck ID:"):
			cur.id = strings.TrimSpace(strings.TrimPrefix(c, "# Deck ID:"))
		case in && hsDeckCodeRe.MatchString(c):
			cur.code = c
			last, found, in = cur, true, false
		}
	}
	return last, found
}

// hsLogClock returns seconds-of-day from a log line's "X HH:MM:SS.fffffff " prefix, or -1.
func hsLogClockSecs(line string) float64 {
	m := hsLogClockRe.FindStringSubmatch(line)
	if m == nil {
		return -1
	}
	h, _ := strconv.Atoi(m[1])
	mi, _ := strconv.Atoi(m[2])
	sec, _ := strconv.ParseFloat(m[3], 64)
	return float64(h*3600+mi*60) + sec
}

func hsLogClock(line string) string {
	if m := hsLogClockRe.FindStringSubmatch(line); m != nil {
		return m[1] + ":" + m[2] + ":" + m[3]
	}
	return ""
}

// gameStartedBeforeQueue reports whether the current game's CREATE_GAME predates the queued deck's
// "Finding Game With Deck" line, i.e. the record in the session belongs to the PREVIOUS game and the
// new deck's match has not started yet. Both lines come from the same machine clock; a wrap past
// midnight (queue far "later" than the game by clock) means the game is the newer one.
func gameStartedBeforeQueue(power []string, queueTS string) bool {
	if queueTS == "" || len(power) == 0 {
		return false
	}
	q := hsLogClockSecs("I " + queueTS + " ")
	if q < 0 {
		return false
	}
	g := -1.0
	for _, ln := range power {
		if strings.HasSuffix(strings.TrimSpace(ln), "DebugPrintPower() - CREATE_GAME") {
			g = hsLogClockSecs(ln)
			break
		}
	}
	if g < 0 {
		return false
	}
	d := q - g // >0: queued after the game started
	if d > 43200 {
		return false // clock wrapped past midnight: the game is the newer one
	}
	return d > 1
}

type hsCardRow struct {
	name   string
	cost   int
	cardID string
}

func (h *HSHandler) computeLive(ctx context.Context, s *liveSession) liveState {
	st := liveState{Connected: true}
	dq, ok := lastQueuedDeck(s.decks)
	if !ok {
		st.Note = "No queued deck seen yet. Queue a game in Hearthstone."
		return st
	}
	deck, err := hsdeck.Decode(dq.code)
	if err != nil {
		st.Note = "Could not read the queued deck code."
		return st
	}
	// dbf -> card (needs hs_cards.card_id loaded)
	byDBF := map[int]hsCardRow{}
	for _, c := range deck.DBFCards {
		var row hsCardRow
		if err := h.DB.QueryRow(`SELECT name,cost,card_id FROM hs_cards WHERE dbf_id=?`, c.DBF).Scan(&row.name, &row.cost, &row.cardID); err != nil || row.cardID == "" {
			st.Note = "Card table not loaded yet (dbf " + strconv.Itoa(c.DBF) + ")."
			return st
		}
		byDBF[c.DBF] = row
	}
	deckCount := map[string]int{}
	card := map[string]hsCardRow{}
	size := 0
	for _, c := range deck.DBFCards {
		r := byDBF[c.DBF]
		deckCount[r.cardID] += c.Count
		card[r.cardID] = r
		size += c.Count
	}
	st.Deck = &liveDeck{Name: dq.name, ID: dq.id, Class: deck.Class, Size: size}

	rt := h.Tracker
	if rt == nil {
		h.liveMu.Lock()
		if h.Tracker == nil {
			h.Tracker = &hstracker.Runner{}
		}
		rt = h.Tracker
		h.liveMu.Unlock()
	}
	var rec *hstracker.Record
	if len(s.power) > 0 && !gameStartedBeforeQueue(s.power, dq.ts) {
		recs, err := rt.Run(ctx, s.power)
		if err != nil {
			st.Note = "Tracker unavailable: " + err.Error()
			return st
		}
		if len(recs) > 0 {
			rec = &recs[len(recs)-1]
		}
	}
	if rec == nil {
		st.Note = "Deck queued. Waiting for the game to start."
		st.Left = cardsFrom(deckCount, card)
		st.LeftTotal = size
		return st
	}
	st.InGame = rec.Complete == 0
	st.Turn = rec.Turns
	me := h.resolveMe(rec, deck.Class)
	if rec.Complete == 1 && s.power != nil {
		key := dq.id + "|" + dq.code + "|" + hsLogClock(s.power[0])
		if s.synced == nil {
			s.synced = map[string]bool{}
		}
		if !s.synced[key] {
			s.synced[key] = true
			id := h.syncDeckToLibrary(s.sub, dq, deck)
			if id > 0 {
				st.LibraryDeckID = id
			}
			h.storeLiveGame(ctx, s.sub, rec, id, me, s.keyID) // the game counts even if the deck was removed from the library
		}
	}
	if me != 1 && me != 2 {
		st.Left = cardsFrom(deckCount, card)
		st.LeftTotal = size
		st.Note = "Game found; waiting for your opening hand to identify you."
		return st
	}
	zone := map[int]string{}
	for _, raw := range rec.Timeline {
		var ev []json.RawMessage
		if json.Unmarshal(raw, &ev) != nil || len(ev) < 6 {
			continue
		}
		var kind string
		var id int
		var z string
		if json.Unmarshal(ev[0], &kind) != nil || kind != "z" || json.Unmarshal(ev[2], &id) != nil || json.Unmarshal(ev[5], &z) != nil {
			continue
		}
		zone[id] = z
	}
	drawn := map[string]int{}
	oppSeen := map[string]int{}
	for _, e := range rec.Entities {
		if e.Card == "" || strings.HasPrefix(e.Card, "HERO_") || strings.HasPrefix(e.Card, "TIME_EVENT") {
			continue
		}
		if e.Player == me {
			if e.Zone0 == "DECK" {
				if z, ok := zone[e.ID]; ok && z != "DECK" {
					drawn[e.Card]++
				}
			}
		} else {
			oppSeen[e.Card]++
		}
	}
	left := map[string]int{}
	for id, n := range deckCount {
		if d := n - drawn[id]; d > 0 {
			left[id] = d
		}
	}
	for _, n := range left {
		st.LeftTotal += n
	}
	for _, n := range drawn {
		st.DrawnTotal += n
	}
	st.Left = cardsFrom(left, card)
	st.Drawn = h.cardsByID(drawn, card)
	st.Opponent.Name = rec.Player1
	if me == 1 {
		st.Opponent.Name = rec.Player2
	}
	st.Opponent.Cards = h.cardsByID(oppSeen, card)
	return st
}

// resolveMe cross-checks the tracker's reveal-count guess (rec.Me -- see game_state.prn's me-of)
// against the one piece of ground truth this handler already has: the class of the deck we just
// queued. Heroes are revealed for both players right at CREATE_GAME, so as soon as either side's
// HERO_* card is readable, class is a deterministic signal -- unlike the reveal-count heuristic,
// which can be tipped the wrong way (e.g. "The Coin" is non-secret and reveals its CardID on the
// opponent's side too, same as any of our own hand cards). Only overrides when exactly one side's
// resolved class matches ours; otherwise defers to the heuristic unchanged.
func (h *HSHandler) resolveMe(rec *hstracker.Record, deckClass string) int {
	want := hsdeck.NormalizeClass(deckClass)
	if want == "" {
		return rec.Me
	}
	mine, theirs := strings.EqualFold(h.classOfPlayer(rec, 1), want), strings.EqualFold(h.classOfPlayer(rec, 2), want)
	if mine && !theirs {
		return 1
	}
	if theirs && !mine {
		return 2
	}
	return rec.Me
}

// classOfPlayer resolves a player slot's class from the first HERO_* card revealed for it -- the
// same lookup hs_games.go's classOf does for finished games, inlined here for a live *hstracker.Record.
//
// Reads Card0 (the entity's FIRST revealed card id), never Card (its current one): a hero-change
// effect ("become a new hero," e.g. Galakrond-style or similar dragon/soul-themed transforms) emits
// another SHOW_ENTITY on the SAME hero entity, and game_state.prn's card-of always returns the latest
// one. Using Card there meant a mid-game hero transform silently defeated this whole cross-check --
// classOfPlayer would resolve to the TRANSFORMED hero's class (or none), resolveMe would find neither
// side matching our deck's class, and it would fall back to the same reveal-count heuristic this was
// built to correct. Card0 is always the original, pre-transform hero, since heroes are revealed via
// FULL_ENTITY at CREATE_GAME before any in-game effect can run.
func (h *HSHandler) classOfPlayer(rec *hstracker.Record, player int) string {
	for _, e := range rec.Entities {
		if e.Player != player || !strings.HasPrefix(e.Card0, "HERO_") {
			continue
		}
		var class string
		if err := h.DB.QueryRow(`SELECT class FROM hs_cards WHERE card_id=? AND class!='' LIMIT 1`, e.Card0).Scan(&class); err == nil {
			return class
		}
	}
	return ""
}

func cardsFrom(counts map[string]int, known map[string]hsCardRow) []liveCard {
	out := make([]liveCard, 0, len(counts))
	for id, n := range counts {
		r := known[id]
		name := r.name
		if name == "" {
			name = id
		}
		out = append(out, liveCard{CardID: id, Name: name, Cost: r.cost, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Cost != out[j].Cost {
			return out[i].Cost < out[j].Cost
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// cardsByID is cardsFrom plus a DB lookup for cards not in the deck (opponent / generated).
func (h *HSHandler) cardsByID(counts map[string]int, known map[string]hsCardRow) []liveCard {
	for id := range counts {
		if _, ok := known[id]; ok {
			continue
		}
		var r hsCardRow
		if err := h.DB.QueryRow(`SELECT name,cost,card_id FROM hs_cards WHERE card_id=? LIMIT 1`, id).Scan(&r.name, &r.cost, &r.cardID); err == nil {
			known[id] = r
		}
	}
	return cardsFrom(counts, known)
}

// liveUplink serves the PowerShell uplink. It contains no secret: the token is passed on the command line.
func (h *HSHandler) liveUplink(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, hsUplinkScript)
}

// liveInstaller serves a one-time installer: it writes a small launcher and a Desktop + Start Menu shortcut.
// The launcher fetches the current uplink on every start, so IDUNA deploys update every install automatically.
func (h *HSHandler) liveInstaller(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, hsInstallerScript)
}

const hsInstallerScript = `# WOTAN Hearthstone tracker installer. Creates a Desktop shortcut; no admin rights, nothing else is changed.
param([string]$Base = 'https://wotan.okemily.com')
$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
$dir = Join-Path $env:LOCALAPPDATA 'WOTAN'
New-Item -ItemType Directory -Force -Path $dir | Out-Null
$base = $Base.TrimEnd('/')
$launcher = Join-Path $dir 'launch.ps1'
$body = @'
# WOTAN Hearthstone tracker launcher (written by the installer). Fetches the latest uplink every start.
$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
$base = '@@BASE@@'
$cache = Join-Path $PSScriptRoot 'uplink.cached.ps1'
try {
  $src = Invoke-RestMethod ($base + '/api/v1/hs/live/uplink.ps1')
  Set-Content -Path $cache -Value $src -Encoding UTF8
} catch {
  if (Test-Path $cache) { Write-Host 'WOTAN unreachable, using the last downloaded tracker.'; $src = Get-Content -Raw $cache }
  else { Write-Host ('Could not reach WOTAN: ' + $_.Exception.Message); Read-Host 'Press Enter to close'; exit 1 }
}
$Host.UI.RawUI.WindowTitle = 'WOTAN Hearthstone Tracker'
& ([scriptblock]::Create($src)) -Base $base
Read-Host 'Tracker stopped. Press Enter to close'
'@
$body = $body.Replace('@@BASE@@', $base)
Set-Content -Path $launcher -Value $body -Encoding UTF8

$lnkArgs = '-NoProfile -ExecutionPolicy Bypass -File "' + $launcher + '"'
$ps = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
$sh = New-Object -ComObject WScript.Shell
$made = @()
foreach ($folder in @([Environment]::GetFolderPath('Desktop'), [Environment]::GetFolderPath('Programs'))) {
  if (-not $folder) { continue }
  $lnk = $sh.CreateShortcut((Join-Path $folder 'WOTAN Hearthstone Tracker.lnk'))
  $lnk.TargetPath = $ps
  $lnk.Arguments = $lnkArgs
  $lnk.WorkingDirectory = $dir
  $lnk.IconLocation = (Join-Path $env:SystemRoot 'System32\shell32.dll') + ',13'
  $lnk.Description = 'Live Hearthstone deck tracker for WOTAN'
  $lnk.Save()
  $made += $lnk.FullName
}
Write-Host 'Installed. Shortcuts created:'
$made | ForEach-Object { Write-Host ('  ' + $_) }
Write-Host 'Double-click "WOTAN Hearthstone Tracker" any time you play. Delete the shortcut and the folder' $dir 'to uninstall.'
`

const hsUplinkScript = `# WOTAN Hearthstone deck tracker uplink. Read-only: it tails your Hearthstone log files and sends the new
# lines to WOTAN with a token only valid for uploading your own tracker data. Close this window to stop.
param(
  [string]$Token = '',                                  # optional: omit to sign in with IAM in your browser
  [string]$Base = 'https://wotan.okemily.com',
  [string]$Iam = 'https://iam.okemily.com',
  [int]$Port = 51824,                                   # loopback port for the sign-in callback
  [string]$LogsDir = '',
  [switch]$NoConfigFix                                  # do not touch Hearthstone's client.config
)
$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
$script:Token = $Token
$url = $Base.TrimEnd('/') + '/api/v1/hs/live/lines'
if (-not $LogsDir) {
  foreach ($c in @("${env:ProgramFiles(x86)}\Hearthstone\Logs", "$env:ProgramFiles\Hearthstone\Logs", 'C:\Program Files (x86)\Hearthstone\Logs')) {
    if (Test-Path $c) { $LogsDir = $c; break }
  }
}
if (-not $LogsDir -or -not (Test-Path $LogsDir)) { Write-Host 'Could not find Hearthstone\Logs. Pass -LogsDir "<path>".'; exit 1 }
Write-Host "Watching $LogsDir"

# Compiled helper: PowerShell's pipeline is far too slow for tens of thousands of log lines.
Add-Type -TypeDefinition @'
using System; using System.IO; using System.Text; using System.Collections.Generic; using System.Text.RegularExpressions;
using System.Net; using System.Net.Sockets; using System.Threading;
public static class HsLogin {
  const string PAGE = "<!doctype html><html><body style=\"font-family:sans-serif;padding:40px\"><p id=m>Signing in...</p><script>var h=location.hash.substring(1);if(h.indexOf('sso_token=')!==-1){fetch('/complete?'+h).then(function(){document.getElementById('m').textContent='Signed in - you can close this window.';}).catch(function(){document.getElementById('m').textContent='Something went wrong - check the PowerShell window.';});}else{document.getElementById('m').textContent='No token received from IDUNA.';}</script></body></html>";
  // Loopback-only listener (127.0.0.1): returns the IAM token delivered by the sign-in redirect, or null on timeout.
  public static string WaitForToken(int port, int timeoutSec) {
    TcpListener l = new TcpListener(IPAddress.Loopback, port);
    l.Start();
    try {
      DateTime end = DateTime.UtcNow.AddSeconds(timeoutSec);
      while (DateTime.UtcNow < end) {
        if (!l.Pending()) { Thread.Sleep(100); continue; }
        using (TcpClient c = l.AcceptTcpClient()) {
          c.ReceiveTimeout = 3000;
          NetworkStream st = c.GetStream();
          byte[] buf = new byte[8192]; int n = 0;
          try { n = st.Read(buf, 0, buf.Length); } catch (Exception) { }
          string req = Encoding.ASCII.GetString(buf, 0, n);
          string[] first = req.Split('\n')[0].Split(' ');
          string path = first.Length > 1 ? first[1] : "";
          string token = null, body = PAGE, ct = "text/html; charset=utf-8";
          if (path.StartsWith("/complete") && path.Contains("sso_token=")) {
            int i = path.IndexOf("sso_token=") + 10; int j = path.IndexOf('&', i);
            string t = Uri.UnescapeDataString(j < 0 ? path.Substring(i) : path.Substring(i, j - i));
            if (t.Split('.').Length == 3) { token = t; body = "ok"; ct = "text/plain"; }
          }
          byte[] b = Encoding.UTF8.GetBytes(body);
          byte[] h = Encoding.ASCII.GetBytes("HTTP/1.1 200 OK\r\nContent-Type: " + ct + "\r\nContent-Length: " + b.Length + "\r\nConnection: close\r\n\r\n");
          st.Write(h, 0, h.Length); st.Write(b, 0, b.Length);
          if (token != null) return token;
        }
      }
      return null;
    } finally { l.Stop(); }
  }
}
public static class HsTail {
  public static string[] ReadNew(string path, ref long offset, string pattern) {
    if (!File.Exists(path)) return new string[0];
    byte[] buf; int got = 0;
    using (FileStream fs = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.ReadWrite)) {
      if (fs.Length < offset) offset = 0;
      long n = fs.Length - offset;
      if (n <= 0) return new string[0];
      fs.Seek(offset, SeekOrigin.Begin);
      buf = new byte[n];
      while (got < n) { int r = fs.Read(buf, got, (int)n - got); if (r <= 0) break; got += r; }
    }
    int last = Array.LastIndexOf(buf, (byte)10, got - 1);
    if (last < 0) return new string[0];
    offset += last + 1;
    string text = Encoding.UTF8.GetString(buf, 0, last);
    Regex re = pattern == null ? null : new Regex(pattern);
    List<string> res = new List<string>();
    foreach (string raw in text.Split('\n')) { string ln = raw.TrimEnd('\r'); if (re == null || re.IsMatch(ln)) res.Add(ln); }
    return res.ToArray();
  }
  // LZ4 block compressor (standard LZ4 block format, greedy, 4096-slot hash) + the frame wrapper
  // [u32 rawLen][u32 compLen][block] the server expects (X-Body-Encoding: lz4-frames).
  static void PutLen(MemoryStream o, int n) { while (n >= 255) { o.WriteByte(255); n -= 255; } o.WriteByte((byte)n); }
  static byte[] Block(byte[] s, int off, int len) {
    MemoryStream o = new MemoryStream();
    int[] table = new int[4096];
    int anchor = off, ip = off, end = off + len;
    int mflimit = end - 12, matchlimit = end - 5;
    while (ip <= mflimit) {
      uint seq = (uint)(s[ip] | (s[ip + 1] << 8) | (s[ip + 2] << 16) | (s[ip + 3] << 24));
      int h = (int)((seq * 2654435761u) >> 20);
      int refp = table[h] - 1;
      table[h] = ip + 1;
      if (refp >= off && ip - refp <= 65535 &&
          s[refp] == s[ip] && s[refp + 1] == s[ip + 1] && s[refp + 2] == s[ip + 2] && s[refp + 3] == s[ip + 3]) {
        int ml = 4;
        while (ip + ml < matchlimit && s[refp + ml] == s[ip + ml]) ml++;
        int lit = ip - anchor;
        int tok = (Math.Min(lit, 15) << 4) | Math.Min(ml - 4, 15);
        o.WriteByte((byte)tok);
        if (lit >= 15) PutLen(o, lit - 15);
        o.Write(s, anchor, lit);
        int d = ip - refp;
        o.WriteByte((byte)(d & 255)); o.WriteByte((byte)(d >> 8));
        if (ml - 4 >= 15) PutLen(o, ml - 4 - 15);
        ip += ml; anchor = ip;
      } else ip++;
    }
    int rem = end - anchor;
    o.WriteByte((byte)(Math.Min(rem, 15) << 4));
    if (rem >= 15) PutLen(o, rem - 15);
    o.Write(s, anchor, rem);
    return o.ToArray();
  }
  public static byte[] Frames(byte[] data) {
    MemoryStream o = new MemoryStream();
    for (int pos = 0; pos < data.Length || (pos == 0 && data.Length == 0); pos += 60000) {
      int n = Math.Min(60000, data.Length - pos);
      byte[] blk = Block(data, pos, n);
      o.Write(BitConverter.GetBytes((uint)n), 0, 4);
      o.Write(BitConverter.GetBytes((uint)blk.Length), 0, 4);
      o.Write(blk, 0, blk.Length);
      if (data.Length == 0) break;
    }
    return o.ToArray();
  }
  public static string JsonArr(string[] a, int from, int count) {
    StringBuilder sb = new StringBuilder("[");
    for (int i = 0; i < count; i++) {
      if (i > 0) sb.Append(',');
      sb.Append('"');
      foreach (char c in a[from + i]) {
        if (c == '\\') sb.Append("\\\\"); else if (c == '"') sb.Append("\\\""); else if (c < ' ') sb.Append(' '); else sb.Append(c);
      }
      sb.Append('"');
    }
    return sb.Append(']').ToString();
  }
}
'@

function Get-IamToken {
  $redirect = "http://127.0.0.1:$Port/callback"
  $loginUrl = "$Iam/api/v1/auth/sso/login?redirect_uri=" + [Uri]::EscapeDataString($redirect)
  Write-Host "Opening your browser to sign in with IAM ($Iam) ..."
  Start-Process $loginUrl
  $t = [HsLogin]::WaitForToken($Port, 300)
  if (-not $t) { throw 'Sign-in timed out. Close this window and run the command again.' }
  Write-Host 'Signed in.'
  return $t
}
$script:authNeeded = (-not $script:Token)

# Hearthstone stops writing Power.log at ~10,000 KiB per session (about 2-3 games), which silently freezes
# every log-based tracker. client.config next to Hearthstone.exe takes FileSizeLimit.Int=-1 to lift it
# (HDT and Firestone write the same key). Done once, only if missing, and reported.
$script:capFixed = $false; $script:restartNeeded = $false; $script:fixError = ''
function Test-CapFixed([string]$cfg) {
  if (-not (Test-Path -LiteralPath $cfg)) { return $false }
  return [bool]([IO.File]::ReadAllText($cfg) -match '(?im)^\s*FileSizeLimit\.Int\s*=\s*-1\s*$')
}
function Ensure-LogCap {
  $cfg = Join-Path (Split-Path $LogsDir -Parent) 'client.config'
  # Regression (2026-10-12 founder report): this used to return here the moment the file ALREADY
  # had the fix line, skipping the restart-needed check below entirely -- so a config fixed in an
  # earlier session, with Hearthstone running continuously since before that fix existed, reported
  # cap_fixed:true/restart_needed:false forever, even though THIS running Hearthstone process never
  # actually read the lifted limit and was still silently capping Power.log at ~10 MB. The check now
  # always runs, whichever branch got the file into a fixed state.
  if (-not (Test-CapFixed $cfg)) {
    if ($NoConfigFix) { $script:fixError = 'automatic fix disabled (-NoConfigFix)' }
    else {
      $nl = [string][char]13 + [string][char]10
      $text = ''
      if (Test-Path -LiteralPath $cfg) { $text = [IO.File]::ReadAllText($cfg) }
      if ($text -match '(?im)^\s*FileSizeLimit\.Int\s*=') {
        $new = [regex]::Replace($text, '(?im)^[ \t]*FileSizeLimit\.Int[ \t]*=[^\r\n]*', 'FileSizeLimit.Int=-1')
      } else {
        $new = 'FileSizeLimit.Int=-1' + $nl + $text
      }
      $enc = New-Object Text.UTF8Encoding($false)
      Write-Host "Hearthstone stops logging at ~10 MB (about 2-3 games). Lifting that in: $cfg"
      try {
        [IO.File]::WriteAllText($cfg, $new, $enc)
      } catch {
        # Program Files is protected: ask Windows for permission (UAC prompt); the user can decline.
        try {
          $tmp = Join-Path $env:TEMP 'wotan_client.config.new'
          [IO.File]::WriteAllText($tmp, $new, $enc)
          $cmd = "Copy-Item -LiteralPath '$tmp' -Destination '$cfg' -Force"
          Write-Host 'Windows will ask permission to write that one file...'
          Start-Process -FilePath powershell.exe -Verb RunAs -Wait -WindowStyle Hidden -ArgumentList @('-NoProfile', '-Command', $cmd)
          Remove-Item -LiteralPath $tmp -Force -ErrorAction SilentlyContinue
        } catch { $script:fixError = 'permission denied; add the line FileSizeLimit.Int=-1 to ' + $cfg + ' yourself' }
      }
    }
  }
  if (Test-CapFixed $cfg) {
    $script:capFixed = $true; $script:fixError = ''
    $p = Get-Process Hearthstone -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($p -and $p.StartTime -lt (Get-Item -LiteralPath $cfg).LastWriteTime) {
      $script:restartNeeded = $true
      Write-Host 'Hearthstone must be restarted once for the lifted log limit to take effect (it is still capped at ~10 MB until then).'
    }
  } elseif (-not $script:fixError) {
    $script:fixError = 'could not write client.config; add the line FileSizeLimit.Int=-1 to ' + $cfg + ' yourself'
  }
  if ($script:fixError) { Write-Host "Could not lift the limit automatically: $($script:fixError)" }
}
function ClientJson([long]$powerBytes) {
  $e = ($script:fixError -replace '[\\"]', ' ')
  return '{"cap_fixed":' + $(if ($script:capFixed) { 'true' } else { 'false' }) + ',"restart_needed":' +
    $(if ($script:restartNeeded) { 'true' } else { 'false' }) + ',"power_bytes":' + $powerBytes + ',"fix_error":"' + $e + '"}'
}
$script:powerBytes = 0

# Pending buffers: a batch is only dropped after the server accepted it, so a 429/network blip never
# loses log lines (the tracker needs every line of the game).
$pendD = New-Object 'System.Collections.Generic.List[string]'
$pendP = New-Object 'System.Collections.Generic.List[string]'
$script:pendReset = $false

function Flush-Pending {
  # only the current game matters: start at the last CREATE_GAME still pending
  for ($k = $pendP.Count - 1; $k -ge 1; $k--) {
    if ($pendP[$k].EndsWith('DebugPrintPower() - CREATE_GAME')) { $pendP.RemoveRange(0, $k); $script:pendReset = $true; break }
  }
  do {
    $cnt = [Math]::Min(4000, $pendP.Count)
    $arr = $pendP.GetRange(0, $cnt).ToArray()
    $dArr = $pendD.ToArray()
    $body = '{"reset":' + $(if ($script:pendReset) { 'true' } else { 'false' }) + ',"decks":' + [HsTail]::JsonArr($dArr, 0, $dArr.Length) + ',"power":' + [HsTail]::JsonArr($arr, 0, $arr.Length) + ',"client":' + (ClientJson $script:powerBytes) + '}'
    try {
      $z = [HsTail]::Frames([Text.Encoding]::UTF8.GetBytes($body))
      $resp = Invoke-RestMethod -Uri $url -Method Post -Headers @{ Authorization = "Bearer $($script:Token)"; 'X-Body-Encoding' = 'lz4-frames' } -ContentType 'application/json' -Body $z -TimeoutSec 30
    } catch {
      $code = 0; try { $code = [int]$_.Exception.Response.StatusCode } catch {}
      if ($code -eq 401) { Write-Host 'Session expired or not valid - signing in again.'; $script:authNeeded = $true; return }
      Write-Host "Upload failed (will retry): $($_.Exception.Message)"
      return
    }
    $pendP.RemoveRange(0, $cnt); $pendD.Clear(); $script:pendReset = $false
    if ($cnt -gt 0) { Write-Host ("sent {0} lines" -f $cnt) }
    if ($resp -and $resp.resync) {
      # the server lost its session (restart/deploy): resend this game from the start of the logs
      Write-Host 'Server asked for a resync - resending the current game.'
      $script:resync = $true
      $pendP.Clear(); $pendD.Clear()
      return
    }
  } while ($pendP.Count -gt 0)
}

# Display only: the server decides where finished games go (this script never names a club).
$script:syncShown = $false
function Show-SyncTarget {
  try {
    $st = Invoke-RestMethod -Uri ($Base.TrimEnd('/') + '/api/v1/hs/live/sync-target') -Headers @{ Authorization = "Bearer $($script:Token)" } -TimeoutSec 15
    $label = $st.label
    if (-not $label) { $label = $st.kind }
    Write-Host "Syncing to: $label"
    if ($st.sync_target_invalid) { Write-Host 'Your club sync target is no longer valid - games are kept private until you pick one on WOTAN.' }
  } catch {}
}
$ErrorActionPreference = 'Continue'   # a transient file/IO error must not kill the tracker
$folder = ''; [long]$pOff = 0; [long]$dOff = 0
$script:resync = $false
$lastBeat = Get-Date
try { Ensure-LogCap } catch { $script:fixError = $_.Exception.Message; Write-Host "Config check failed: $($script:fixError)" }
$pat = '(?:GameState|PowerTaskList)\.DebugPrint(Power|Game)\(\) - '
while ($true) {
  try {
    if ($script:authNeeded) {
      try { $script:Token = Get-IamToken; $script:authNeeded = $false; $script:syncShown = $false }
      catch { Write-Host $_.Exception.Message; Start-Sleep -Seconds 30; continue }
    }
    if (-not $script:syncShown) { Show-SyncTarget; $script:syncShown = $true }
    if ($script:resync) { $pOff = 0; $dOff = 0; $script:pendReset = $true; $script:resync = $false }
    $newest = Get-ChildItem $LogsDir -Directory -Filter 'Hearthstone_*' -ErrorAction Stop | Sort-Object Name -Descending | Select-Object -First 1
    if ($newest) {
      if ($newest.FullName -ne $folder) {
        $folder = $newest.FullName; $pOff = 0; $dOff = 0; $script:pendReset = $true
        $pendD.Clear(); $pendP.Clear()
        Write-Host "Session: $($newest.Name)"
      }
      $pf = Join-Path $folder 'Power.log'
      if (Test-Path -LiteralPath $pf) { $script:powerBytes = (Get-Item -LiteralPath $pf).Length }
      $d = [HsTail]::ReadNew((Join-Path $folder 'Decks.log'), [ref]$dOff, $null)
      $p = [HsTail]::ReadNew((Join-Path $folder 'Power.log'), [ref]$pOff, $pat)
      if ($d.Length -gt 0) { $pendD.AddRange($d) }
      if ($p.Length -gt 0) { $pendP.AddRange($p) }
      $beat = ((Get-Date) - $lastBeat).TotalSeconds -gt 10
      if ($pendD.Count -gt 0 -or $pendP.Count -gt 0 -or $script:pendReset -or $beat) { Flush-Pending; $lastBeat = Get-Date }
    }
  } catch {
    Write-Host "Error (continuing): $($_.Exception.Message)"
  }
  Start-Sleep -Milliseconds 1000
}
`

// storeLiveGame saves a finished live game for its owner, linked to the library deck that was played.
// me is the caller's already-cross-checked resolveMe result, not rec.Me directly -- rec.Me alone is
// just the bare reveal-count heuristic (game_state.prn's me-of), and using it here would bake the
// exact wrong-player mixup resolveMe exists to correct permanently into the saved game/deck stats,
// even on a poll where the live page itself showed the corrected result.
func (h *HSHandler) storeLiveGame(ctx context.Context, sub string, rec *hstracker.Record, deckID int64, me int, keyID int64) {
	if me != 1 && me != 2 {
		return
	}
	resolved := *rec
	resolved.Me = me
	// Identity binding (IDENTITY_NORTHSTAR 4.3): the uploader's own PlayerName must be one of their linked
	// BattleTags. An unlinked BattleTag-shaped name is dropped (and counted on the key); a generic or absent
	// name is stored but forced private.
	myName := rec.Player1
	if me == 2 {
		myName = rec.Player2
	}
	bind, drop := h.bindLiveGame(ctx, sub, myName)
	if drop {
		h.bumpRejected(keyID)
		return
	}
	body, err := json.Marshal(&resolved)
	if err != nil {
		return
	}
	var gr hsGameRecord
	if json.Unmarshal(body, &gr) != nil || len(gr.Timeline) == 0 {
		return
	}
	if _, dup, _, _, _, _, err := h.storeGameBound(sub, &gr, body, deckID, bind); err == nil && !dup {
		h.bumpUploadOK(keyID)
	}
}
