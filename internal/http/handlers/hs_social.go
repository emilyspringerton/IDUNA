package handlers

// hs_social.go -- WOTAN's Hearthstone deck section (WOTAN/HEARTHSTONE_NORTHSTAR.md): deck sharing via deck
// code plus deliberately small socials (handle profile, own-wall posts, likes, comments, follows, feed,
// reports). Identity is the generic IDUNA SSO JWT's "sub" (same shape store.html uses), one HS profile per
// IDUNA account. Reads are public (a token, if sent, only adds liked_by_me/can_delete); writes need a token.
// All user text is stored raw and escaped by the clients; this file only bounds length and strips control chars.

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"iduna/internal/auth/jwt"
	"iduna/internal/hsdeck"
	"iduna/internal/hstracker"
	"iduna/internal/http/middleware"
)

const hsPrefix = "/api/v1/hs/"

type HSHandler struct {
	DB           *sql.DB
	Keys         *jwt.Keys
	ReadLimiter  *middleware.IPRateLimiter // per IP, nil = unlimited (tests)
	WriteLimiter *middleware.IPRateLimiter // per subject, nil = unlimited (tests)
	Tracker      *hstracker.Runner         // live deck tracker (hs_live.go); nil = default runner

	liveMu   sync.Mutex
	liveSess map[string]*liveSession
	liveLim  *middleware.IPRateLimiter // uplink batches (about 1/s while playing); separate from WriteLimiter
}

var hsHandleRe = regexp.MustCompile(`^[a-z0-9_]{3,20}$`)
var hsReserved = map[string]bool{"admin": true, "iduna": true, "wotan": true, "emily": true, "support": true, "system": true, "moderator": true, "mod": true, "root": true, "null": true, "anonymous": true, "hearthstone": true, "blizzard": true}

type hsViewer struct {
	sub   string
	admin bool
}

func (h *HSHandler) viewer(r *http.Request) hsViewer {
	ah := r.Header.Get("Authorization")
	if !strings.HasPrefix(ah, "Bearer ") || h.Keys == nil {
		return hsViewer{}
	}
	claims, err := jwt.Verify(h.Keys, strings.TrimPrefix(ah, "Bearer "))
	if err != nil {
		return hsViewer{}
	}
	if aud, _ := claims["aud"].(string); aud == hsLiveUploadAud {
		return hsViewer{} // upload tokens are good for POST /live/lines only
	}
	sub, _ := claims["sub"].(string)
	v := hsViewer{sub: sub}
	if perms, ok := claims["permissions"].([]any); ok {
		for _, p := range perms {
			if s, _ := p.(string); s == "iduna.admin" {
				v.admin = true
			}
		}
	}
	return v
}

func hsErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}

func hsList(w http.ResponseWriter, total, limit, offset int, items any) {
	writeJSON(w, http.StatusOK, map[string]any{"total": total, "limit": limit, "offset": offset, "items": items})
}

func hsTime(s string) string {
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	return s
}

// hsText trims, strips control characters (except newline) and bounds length in runes.
func hsText(s string, min, max int) (string, bool) {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || r >= 0x20 && r != 0x7f {
			return r
		}
		return -1
	}, s))
	n := utf8.RuneCountInString(s)
	return s, n >= min && n <= max
}

func (h *HSHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.ReadLimiter != nil && !h.ReadLimiter.Allow(clientIP(r)) {
		hsErr(w, http.StatusTooManyRequests, "slow down")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	v := h.viewer(r)
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, hsPrefix), "/")
	p := strings.Split(rest, "/")
	m := r.Method
	if m == http.MethodPost && rest == "live/lines" {
		h.liveLines(w, r)
		return
	}
	isWrite := m != http.MethodGet && m != http.MethodHead
	if isWrite && !(m == http.MethodPost && rest == "decks/parse") {
		if v.sub == "" {
			hsErr(w, http.StatusUnauthorized, "sign in with IDUNA first")
			return
		}
		if h.WriteLimiter != nil && !h.WriteLimiter.Allow("hs:"+v.sub) {
			hsErr(w, http.StatusTooManyRequests, "you're doing that too fast")
			return
		}
	}
	id := func(i int) int64 {
		if i >= len(p) {
			return 0
		}
		n, _ := strconv.ParseInt(p[i], 10, 64)
		return n
	}
	switch {
	case rest == "classes" && m == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"classes": hsdeck.Classes, "formats": []string{"Standard", "Wild", "Classic", "Twist"}})
	case rest == "decks" && m == http.MethodGet:
		h.listDecks(w, r, v)
	case rest == "decks" && m == http.MethodPost:
		h.createDeck(w, r, v)
	case rest == "decks/parse" && m == http.MethodPost:
		h.parseDeck(w, r)
	case len(p) == 2 && p[0] == "decks" && m == http.MethodGet:
		h.getDeck(w, r, v, id(1))
	case len(p) == 2 && p[0] == "decks" && m == http.MethodPatch:
		h.patchDeck(w, r, v, id(1))
	case len(p) == 2 && p[0] == "decks" && m == http.MethodDelete:
		h.softDelete(w, v, "hs_decks", "owner_sub", id(1))
	case len(p) == 3 && p[0] == "decks" && (p[2] == "like" || p[2] == "comments") && !h.deckVisible(v, id(1)):
		hsErr(w, http.StatusNotFound, "not found")
	case len(p) == 3 && p[0] == "decks" && p[2] == "share" && (m == http.MethodPut || m == http.MethodDelete):
		h.shareStats(w, r, v, id(1), m == http.MethodPut)
	case len(p) == 3 && p[0] == "decks" && p[2] == "like" && (m == http.MethodPut || m == http.MethodDelete):
		h.toggleLike(w, v, "hs_decks", "hs_deck_likes", "deck_id", id(1), m == http.MethodPut)
	case len(p) == 3 && p[0] == "decks" && p[2] == "comments" && m == http.MethodGet:
		h.listComments(w, r, v, "hs_deck_comments", "deck_id", id(1))
	case len(p) == 3 && p[0] == "decks" && p[2] == "comments" && m == http.MethodPost:
		h.addComment(w, r, v, "hs_decks", "hs_deck_comments", "deck_id", id(1))
	case len(p) == 2 && p[0] == "comments" && m == http.MethodDelete:
		h.deleteComment(w, v, "hs_deck_comments", "hs_decks", "deck_id", id(1))
	case len(p) == 2 && p[0] == "post-comments" && m == http.MethodDelete:
		h.deleteComment(w, v, "hs_post_comments", "hs_wall_posts", "post_id", id(1))
	case rest == "settings" && m == http.MethodGet:
		h.getSettings(w, v)
	case rest == "settings" && m == http.MethodPut:
		h.putSettings(w, r, v)
	case rest == "me" && m == http.MethodGet:
		h.getMe(w, r, v)
	case rest == "me" && m == http.MethodPut:
		h.putMe(w, r, v)
	case rest == "users" && m == http.MethodGet:
		h.listUsers(w, r, v)
	case len(p) == 2 && p[0] == "users" && m == http.MethodGet:
		h.getUser(w, r, v, p[1])
	case len(p) == 3 && p[0] == "users" && p[2] == "decks" && m == http.MethodGet:
		h.userDecks(w, r, v, p[1])
	case len(p) == 3 && p[0] == "users" && p[2] == "wall" && m == http.MethodGet:
		h.userWall(w, r, v, p[1])
	case len(p) == 3 && p[0] == "users" && p[2] == "follow" && (m == http.MethodPut || m == http.MethodDelete):
		h.follow(w, v, p[1], m == http.MethodPut)
	case rest == "feed" && m == http.MethodGet:
		h.feed(w, r, v)
	case rest == "wall" && m == http.MethodPost:
		h.createPost(w, r, v)
	case len(p) == 2 && p[0] == "wall" && m == http.MethodDelete:
		h.softDelete(w, v, "hs_wall_posts", "owner_sub", id(1))
	case len(p) == 3 && p[0] == "wall" && p[2] == "like" && (m == http.MethodPut || m == http.MethodDelete):
		h.toggleLike(w, v, "hs_wall_posts", "hs_post_likes", "post_id", id(1), m == http.MethodPut)
	case len(p) == 3 && p[0] == "wall" && p[2] == "comments" && m == http.MethodGet:
		h.listComments(w, r, v, "hs_post_comments", "post_id", id(1))
	case len(p) == 3 && p[0] == "wall" && p[2] == "comments" && m == http.MethodPost:
		h.addComment(w, r, v, "hs_wall_posts", "hs_post_comments", "post_id", id(1))
	case rest == "live/token" && m == http.MethodPost:
		h.liveToken(w, v)
	case rest == "live/state" && m == http.MethodGet:
		h.liveState(w, r, v)
	case rest == "live/uplink.ps1" && m == http.MethodGet:
		h.liveUplink(w)
	case rest == "live/install.ps1" && m == http.MethodGet:
		h.liveInstaller(w)
	case rest == "games" && m == http.MethodPost:
		h.createGame(w, r, v)
	case rest == "games" && m == http.MethodGet:
		h.listGames(w, r, v)
	case len(p) == 2 && p[0] == "games" && m == http.MethodGet:
		h.getGame(w, v, id(1))
	case rest == "stats/matchups" && m == http.MethodGet:
		h.matchups(w, r, v)
	case rest == "reports" && m == http.MethodPost:
		h.report(w, r, v)
	default:
		hsErr(w, http.StatusNotFound, "not found")
	}
}

// ---- helpers ------------------------------------------------------------------------------------------

func hsPage(r *http.Request, defLimit int) (limit, offset int) {
	limit = atoiDefault(r.URL.Query().Get("limit"), defLimit)
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}
	offset = atoiDefault(r.URL.Query().Get("offset"), 0)
	if offset < 0 {
		offset = 0
	}
	return
}

func hsDecode(r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 32<<10)
	return json.NewDecoder(r.Body).Decode(dst)
}

func hsLike(q string) string {
	q = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
	return "%" + q + "%"
}

func (h *HSHandler) profileOf(sub string) (handle string, ok bool) {
	err := h.DB.QueryRow(`SELECT handle FROM hs_profiles WHERE sub=?`, sub).Scan(&handle)
	return handle, err == nil
}

func (h *HSHandler) subOfHandle(handle string) (string, bool) {
	var sub string
	err := h.DB.QueryRow(`SELECT sub FROM hs_profiles WHERE handle=?`, strings.ToLower(handle)).Scan(&sub)
	return sub, err == nil
}

func (h *HSHandler) needHandle(w http.ResponseWriter, v hsViewer) (string, bool) {
	hd, ok := h.profileOf(v.sub)
	if !ok {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "claim a handle first", "code": "handle_required"})
		return "", false
	}
	return hd, true
}

// ---- decks --------------------------------------------------------------------------------------------

type hsDeckOut struct {
	ID            int64             `json:"id"`
	Title         string            `json:"title"`
	Description   string            `json:"description"`
	Class         string            `json:"class"`
	Format        string            `json:"format"`
	Year          string            `json:"year"`
	Deckstring    string            `json:"deckstring"`
	CardCount     int               `json:"card_count"`
	Cards         []json.RawMessage `json:"cards,omitempty"`
	DBFCards      json.RawMessage   `json:"dbf_cards,omitempty"`
	NamesResolved bool              `json:"names_resolved"`
	Author        map[string]string `json:"author"`
	Likes         int               `json:"likes"`
	Comments      int               `json:"comments"`
	LikedByMe     bool              `json:"liked_by_me"`
	CanEdit       bool              `json:"can_edit"`
	CreatedAt     string            `json:"created_at"`
	Private       bool              `json:"private"`
	Synced        bool              `json:"synced"` // came from the live tracker, not a paste
	Games         int               `json:"games"`  // tracked finished games with this deck
	Wins          int               `json:"wins"`
	Losses        int               `json:"losses"`
	Ties          int               `json:"ties"`
	Winrate       float64           `json:"winrate"` // the PUBLIC pool: players who shared their stats for this exact deck
	// Only on the owner's own private deck page:
	Shared    *bool     `json:"stats_shared,omitempty"` // is my record for this deck in the public pool
	Mine      *hsRecord `json:"mine,omitempty"`         // my own games with this exact deck
	Projected *hsRecord `json:"projected,omitempty"`    // public pool + every private holder's games
}

const hsDeckCols = `d.id, d.title, d.description, d.class, d.format, d.year, d.deckstring, d.card_count, d.cards_json, d.dbf_json,
 d.names_resolved, COALESCE(p.handle,''), d.likes, d.comments, d.created_at, d.owner_sub,
 EXISTS(SELECT 1 FROM hs_deck_likes l WHERE l.deck_id=d.id AND l.sub=?), d.private, d.source_deck_id,
 (SELECT COUNT(*) FROM hs_games g WHERE g.deck_code=d.deckstring AND g.complete=1 AND EXISTS(SELECT 1 FROM hs_stat_shares s WHERE s.owner_sub=g.owner_sub AND s.deck_code=g.deck_code) AND g.result IN (1,2,3)),
 (SELECT COUNT(*) FROM hs_games g WHERE g.deck_code=d.deckstring AND g.complete=1 AND EXISTS(SELECT 1 FROM hs_stat_shares s WHERE s.owner_sub=g.owner_sub AND s.deck_code=g.deck_code) AND g.result=1),
 (SELECT COUNT(*) FROM hs_games g WHERE g.deck_code=d.deckstring AND g.complete=1 AND EXISTS(SELECT 1 FROM hs_stat_shares s WHERE s.owner_sub=g.owner_sub AND s.deck_code=g.deck_code) AND g.result=2)`

func scanDeck(sc interface{ Scan(...any) error }, v hsViewer, withCards bool) (hsDeckOut, error) {
	var d hsDeckOut
	var cardsJSON, dbfJSON, handle, created, owner string
	var resolved, liked, priv int
	var source string
	if err := sc.Scan(&d.ID, &d.Title, &d.Description, &d.Class, &d.Format, &d.Year, &d.Deckstring, &d.CardCount, &cardsJSON, &dbfJSON,
		&resolved, &handle, &d.Likes, &d.Comments, &created, &owner, &liked, &priv, &source, &d.Games, &d.Wins, &d.Losses); err != nil {
		return d, err
	}
	d.Private = priv == 1
	d.Ties = d.Games - d.Wins - d.Losses
	if d.Games > 0 {
		d.Winrate = float64(d.Wins) / float64(d.Games)
	}
	d.Synced = source != ""
	d.NamesResolved = resolved == 1
	d.Author = map[string]string{"handle": handle}
	d.LikedByMe = liked == 1
	d.CanEdit = v.sub != "" && (v.sub == owner || v.admin)
	d.CreatedAt = hsTime(created)
	if withCards {
		var disp []json.RawMessage
		_ = json.Unmarshal([]byte(cardsJSON), &disp)
		d.DBFCards = json.RawMessage(dbfJSON)
		if d.NamesResolved {
			d.Cards = disp
		} else {
			_ = json.Unmarshal([]byte(dbfJSON), &d.Cards)
		}
	} else {
		// list rows still need the cost curve: send cards only when names are known (small).
		if d.NamesResolved {
			_ = json.Unmarshal([]byte(cardsJSON), &d.Cards)
		}
	}
	return d, nil
}

func (h *HSHandler) listDecks(w http.ResponseWriter, r *http.Request, v hsViewer) {
	h.queryDecks(w, r, v, "")
}

func (h *HSHandler) queryDecks(w http.ResponseWriter, r *http.Request, v hsViewer, onlyOwner string) {
	q := r.URL.Query()
	limit, offset := hsPage(r, 24)
	// The library is public decks only. ?private=1 is the signed-in player's own private decks (their tab).
	where := []string{"d.deleted=0", "d.private=0", "d.merged_into=0"}
	args := []any{}
	if q.Get("private") == "1" {
		if v.sub == "" {
			hsErr(w, http.StatusUnauthorized, "sign in with IDUNA first")
			return
		}
		where = []string{"d.deleted=0", "d.private=1", "d.owner_sub=?"}
		args = []any{v.sub}
	}
	if onlyOwner != "" {
		where = append(where, "d.owner_sub=?")
		args = append(args, onlyOwner)
	}
	if c := hsdeck.NormalizeClass(q.Get("class")); c != "" {
		where = append(where, "d.class=?")
		args = append(args, c)
	}
	if f := hsdeck.FormatID(q.Get("format")); f != 0 {
		where = append(where, "d.format=?")
		args = append(args, q.Get("format"))
	}
	if a := strings.TrimSpace(q.Get("author")); a != "" {
		where = append(where, "p.handle=? COLLATE NOCASE")
		args = append(args, a)
	}
	if s := strings.TrimSpace(q.Get("q")); s != "" {
		if utf8.RuneCountInString(s) > 60 {
			s = string([]rune(s)[:60])
		}
		where = append(where, `(d.title LIKE ? ESCAPE '\' OR d.cards_json LIKE ? ESCAPE '\' OR p.handle LIKE ? ESCAPE '\')`)
		args = append(args, hsLike(s), hsLike(s), hsLike(s))
	}
	order := "d.id DESC"
	switch q.Get("sort") {
	case "top":
		order = "d.likes DESC, d.id DESC"
	case "trending":
		where = append(where, "d.created_at >= datetime('now','-14 days')")
		order = "(d.likes*2 + d.comments) DESC, d.id DESC"
	}
	from := ` FROM hs_decks d LEFT JOIN hs_profiles p ON p.sub=d.owner_sub WHERE ` + strings.Join(where, " AND ")
	var total int
	if err := h.DB.QueryRow(`SELECT COUNT(*)`+from, args...).Scan(&total); err != nil {
		hsErr(w, 500, "database error")
		return
	}
	rows, err := h.DB.Query(`SELECT `+hsDeckCols+from+` ORDER BY `+order+` LIMIT ? OFFSET ?`, append(append([]any{v.sub}, args...), limit, offset)...)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	defer rows.Close()
	items := []hsDeckOut{}
	for rows.Next() {
		d, err := scanDeck(rows, v, false)
		if err != nil {
			hsErr(w, 500, "database error")
			return
		}
		items = append(items, d)
	}
	hsList(w, total, limit, offset, items)
}

func (h *HSHandler) getDeck(w http.ResponseWriter, r *http.Request, v hsViewer, id int64) {
	var into int64
	if h.DB.QueryRow(`SELECT merged_into FROM hs_decks WHERE id=?`, id).Scan(&into) == nil && into > 0 {
		id = into // this copy was merged into the deck that was published first
	}
	row := h.DB.QueryRow(`SELECT `+hsDeckCols+` FROM hs_decks d LEFT JOIN hs_profiles p ON p.sub=d.owner_sub WHERE d.id=? AND d.deleted=0 AND (d.private=0 OR d.owner_sub=?)`, v.sub, id, v.sub)
	d, err := scanDeck(row, v, true)
	if err != nil {
		hsErr(w, http.StatusNotFound, "deck not found")
		return
	}
	if v.sub != "" {
		var owner string
		var priv int
		h.DB.QueryRow(`SELECT owner_sub, private FROM hs_decks WHERE id=?`, id).Scan(&owner, &priv)
		if owner == v.sub && priv == 1 {
			sh := h.statsShared(owner, d.Deckstring)
			mine, proj := h.recordOf(d.Deckstring, owner), h.recordOf(d.Deckstring, "")
			d.Shared, d.Mine, d.Projected = &sh, &mine, &proj
		}
	}
	writeJSON(w, http.StatusOK, d)
}

func (h *HSHandler) parseDeck(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text string `json:"text"`
	}
	if err := hsDecode(r, &in); err != nil {
		hsErr(w, 400, "bad request body")
		return
	}
	d, err := hsdeck.Parse(in.Text)
	if err != nil {
		hsErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	h.resolveNames(d)
	writeJSON(w, http.StatusOK, hsParsedOut(d))
}

// resolveNames fills the display list from hs_cards when the deck code carried no pasted names. It only
// upgrades a deck whose every dbf_id is in the table, so a partial card table never yields a half-named
// deck (same all-or-nothing rule hs-cards-import's backfill uses).
func (h *HSHandler) resolveNames(d *hsdeck.Deck) {
	if d.NamesResolved || len(d.DBFCards) == 0 {
		return
	}
	named := make([]hsdeck.NamedCard, 0, len(d.DBFCards))
	for _, c := range d.DBFCards {
		var name string
		var cost int
		if err := h.DB.QueryRow(`SELECT name,cost FROM hs_cards WHERE dbf_id=?`, c.DBF).Scan(&name, &cost); err != nil {
			return
		}
		named = append(named, hsdeck.NamedCard{Name: name, Cost: cost, Count: c.Count})
	}
	sort.SliceStable(named, func(i, j int) bool {
		if named[i].Cost != named[j].Cost {
			return named[i].Cost < named[j].Cost
		}
		return named[i].Name < named[j].Name
	})
	d.Cards = named
	d.NamesResolved = true
}

func hsParsedOut(d *hsdeck.Deck) map[string]any {
	var cards any = d.Cards
	if !d.NamesResolved {
		cards = d.DBFCards
	}
	return map[string]any{"title": d.Title, "class": d.Class, "format": d.Format, "year": d.Year, "deckstring": d.Deckstring,
		"card_count": d.CardCount, "cards": cards, "dbf_cards": d.DBFCards, "names_resolved": d.NamesResolved, "warnings": d.Warnings}
}

func (h *HSHandler) createDeck(w http.ResponseWriter, r *http.Request, v hsViewer) {
	handle, ok := h.needHandle(w, v)
	if !ok {
		return
	}
	var in struct{ Title, Description, Text string }
	if err := hsDecode(r, &in); err != nil {
		hsErr(w, 400, "bad request body")
		return
	}
	d, err := hsdeck.Parse(in.Text)
	if err != nil {
		hsErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	h.resolveNames(d)
	title := in.Title
	if strings.TrimSpace(title) == "" {
		title = d.Title
	}
	title, okT := hsText(title, 1, 80)
	desc, okD := hsText(in.Description, 0, 1000)
	if !okT {
		hsErr(w, 422, "give the deck a title (1-80 characters)")
		return
	}
	if !okD {
		hsErr(w, 422, "description is too long (max 1000 characters)")
		return
	}
	var recent int
	_ = h.DB.QueryRow(`SELECT COUNT(*) FROM hs_decks WHERE owner_sub=? AND created_at >= datetime('now','-1 hour')`, v.sub).Scan(&recent)
	if recent >= 10 {
		hsErr(w, http.StatusTooManyRequests, "you've published a lot of decks this hour -- try again later")
		return
	}
	var existing int64
	if err := h.DB.QueryRow(`SELECT id FROM hs_decks WHERE owner_sub=? AND deckstring=? AND deleted=0`, v.sub, d.Deckstring).Scan(&existing); err == nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "you've already published this deck", "deck_id": existing})
		return
	}
	if canon := h.publicCanonical(d.Deckstring); canon > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "this exact deck is already in the library", "deck_id": canon})
		return
	}
	cards, _ := json.Marshal(d.Cards)
	dbf, _ := json.Marshal(d.DBFCards)
	if d.Cards == nil {
		cards = []byte("[]")
	}
	// A previously soft-deleted copy blocks the UNIQUE key: revive it instead of failing.
	res, err := h.DB.Exec(`INSERT INTO hs_decks (owner_sub,title,description,class,format,year,hero_dbf,deckstring,cards_json,dbf_json,card_count,names_resolved)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(owner_sub,deckstring) DO UPDATE SET deleted=0,title=excluded.title,description=excluded.description,cards_json=excluded.cards_json,
		 names_resolved=excluded.names_resolved,updated_at=datetime('now'),created_at=datetime('now') WHERE hs_decks.deleted=1`,
		v.sub, title, desc, d.Class, d.Format, d.Year, d.HeroDBF, d.Deckstring, string(cards), string(dbf), d.CardCount, boolInt(d.NamesResolved))
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	did, _ := res.LastInsertId()
	if n, _ := res.RowsAffected(); n > 0 {
		_ = h.DB.QueryRow(`SELECT id FROM hs_decks WHERE owner_sub=? AND deckstring=?`, v.sub, d.Deckstring).Scan(&did)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": did, "author": handle})
}

type hsRecord struct {
	Games   int     `json:"games"`
	Wins    int     `json:"wins"`
	Losses  int     `json:"losses"`
	Ties    int     `json:"ties"`
	Winrate float64 `json:"winrate"`
}

// recordOf is one player's finished games with exact deck contents (owner != ""), or every player's
// games, shared or not (owner == "") -- the projected global record shown on private deck pages.
func (h *HSHandler) recordOf(deckstring, owner string) hsRecord {
	q := `SELECT COUNT(*), COALESCE(SUM(result=1),0), COALESCE(SUM(result=2),0) FROM hs_games WHERE deck_code=? AND complete=1 AND result IN (1,2,3)`
	args := []any{deckstring}
	if owner != "" {
		q += ` AND owner_sub=?`
		args = append(args, owner)
	}
	var r hsRecord
	h.DB.QueryRow(q, args...).Scan(&r.Games, &r.Wins, &r.Losses)
	r.Ties = r.Games - r.Wins - r.Losses
	if r.Games > 0 {
		r.Winrate = float64(r.Wins) / float64(r.Games)
	}
	return r
}

func (h *HSHandler) statsShared(owner, deckstring string) bool {
	var one int
	return h.DB.QueryRow(`SELECT 1 FROM hs_stat_shares WHERE owner_sub=? AND deck_code=?`, owner, deckstring).Scan(&one) == nil
}

func (h *HSHandler) setShare(owner, deckstring string, on bool) {
	if on {
		h.DB.Exec(`INSERT OR IGNORE INTO hs_stat_shares (owner_sub,deck_code) VALUES (?,?)`, owner, deckstring)
	} else {
		h.DB.Exec(`DELETE FROM hs_stat_shares WHERE owner_sub=? AND deck_code=?`, owner, deckstring)
	}
}

// shareStats: PUT/DELETE /decks/{id}/share -- include (or withdraw) my record with this deck in its public
// win rate without publishing the deck itself.
func (h *HSHandler) shareStats(w http.ResponseWriter, r *http.Request, v hsViewer, id int64, on bool) {
	var owner, code string
	var priv, merged int
	if err := h.DB.QueryRow(`SELECT owner_sub, deckstring, private, merged_into FROM hs_decks WHERE id=? AND deleted=0`, id).Scan(&owner, &code, &priv, &merged); err != nil || owner != v.sub {
		hsErr(w, http.StatusNotFound, "deck not found")
		return
	}
	if priv == 0 {
		hsErr(w, http.StatusConflict, "this deck is public, so its stats are shared; make it private to withdraw them")
		return
	}
	h.setShare(owner, code, on)
	h.getDeck(w, r, v, id)
}

// publicCanonical is the one public deck for these contents: the first to publish (0 if none).
func (h *HSHandler) publicCanonical(deckstring string) int64 {
	var id int64
	h.DB.QueryRow(`SELECT id FROM hs_decks WHERE deckstring=? AND private=0 AND deleted=0 AND merged_into=0
		ORDER BY CASE WHEN published_at='' THEN created_at ELSE published_at END, id LIMIT 1`, deckstring).Scan(&id)
	return id
}

// makePrivate takes a deck out of the public library. If other players were merged into it, the earliest
// of them becomes the public deck (and keeps the name its first publisher chose is lost with this deck).
func (h *HSHandler) makePrivate(id int64) {
	var heir int64
	h.DB.QueryRow(`SELECT id FROM hs_decks WHERE merged_into=? AND deleted=0 ORDER BY published_at, id LIMIT 1`, id).Scan(&heir)
	if heir > 0 {
		h.DB.Exec(`UPDATE hs_decks SET merged_into=0 WHERE id=?`, heir)
		h.DB.Exec(`UPDATE hs_decks SET merged_into=? WHERE merged_into=? AND id!=?`, heir, id, heir)
	}
	h.DB.Exec(`UPDATE hs_decks SET private=1,merged_into=0,updated_at=datetime('now') WHERE id=?`, id)
}

// deckVisible: a private (tracker-synced) deck is visible to its owner only.
func (h *HSHandler) deckVisible(v hsViewer, id int64) bool {
	var one int
	return h.DB.QueryRow(`SELECT 1 FROM hs_decks WHERE id=? AND deleted=0 AND (private=0 OR owner_sub=?)`, id, v.sub).Scan(&one) == nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (h *HSHandler) patchDeck(w http.ResponseWriter, r *http.Request, v hsViewer, id int64) {
	var owner string
	if err := h.DB.QueryRow(`SELECT owner_sub FROM hs_decks WHERE id=? AND deleted=0`, id).Scan(&owner); err != nil {
		hsErr(w, 404, "deck not found")
		return
	}
	if owner != v.sub && !v.admin {
		hsErr(w, 403, "not your deck")
		return
	}
	var in struct {
		Title, Description *string
		Private            *bool
	}
	if err := hsDecode(r, &in); err != nil {
		hsErr(w, 400, "bad request body")
		return
	}
	// Publishing needs a handle (the public deck page shows its author). Private decks never do.
	if in.Private != nil && !*in.Private {
		if _, has := h.profileOf(owner); !has {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "claim a handle before publishing a deck", "code": "handle_required"})
			return
		}
	}
	if in.Title != nil {
		t, ok := hsText(*in.Title, 1, 80)
		if !ok {
			hsErr(w, 422, "title must be 1-80 characters")
			return
		}
		h.DB.Exec(`UPDATE hs_decks SET title=?,updated_at=datetime('now') WHERE id=?`, t, id)
	}
	if in.Private != nil {
		var deckstring string
		var wasPrivate int
		h.DB.QueryRow(`SELECT deckstring, private FROM hs_decks WHERE id=?`, id).Scan(&deckstring, &wasPrivate)
		switch {
		case !*in.Private && wasPrivate == 1:
			// Identical public decks are one deck: the first to publish picked the name, later ones merge in.
			if canon := h.publicCanonical(deckstring); canon > 0 && canon != id {
				h.DB.Exec(`UPDATE hs_decks SET private=0,merged_into=?,published_at=datetime('now'),updated_at=datetime('now') WHERE id=?`, canon, id)
				h.setShare(owner, deckstring, true)
				writeJSON(w, http.StatusOK, map[string]any{"id": canon, "merged_into": canon,
					"message": "This exact deck was already published, so yours now counts toward it."})
				return
			}
			h.DB.Exec(`UPDATE hs_decks SET private=0,published_at=datetime('now'),updated_at=datetime('now') WHERE id=?`, id)
			h.setShare(owner, deckstring, true)
		case *in.Private && wasPrivate == 0:
			h.makePrivate(id)
			h.setShare(owner, deckstring, false) // back to private: the record leaves the public pool too
		}
	}
	if in.Description != nil {
		d, ok := hsText(*in.Description, 0, 1000)
		if !ok {
			hsErr(w, 422, "description is too long (max 1000 characters)")
			return
		}
		h.DB.Exec(`UPDATE hs_decks SET description=?,updated_at=datetime('now') WHERE id=?`, d, id)
	}
	h.getDeck(w, r, v, id)
}

// softDelete handles DELETE for decks and wall posts (owner or admin).
func (h *HSHandler) softDelete(w http.ResponseWriter, v hsViewer, table, ownerCol string, id int64) {
	var owner string
	if err := h.DB.QueryRow(`SELECT `+ownerCol+` FROM `+table+` WHERE id=? AND deleted=0`, id).Scan(&owner); err != nil {
		hsErr(w, 404, "not found")
		return
	}
	if owner != v.sub && !v.admin {
		hsErr(w, 403, "not yours to delete")
		return
	}
	h.DB.Exec(`UPDATE `+table+` SET deleted=1 WHERE id=?`, id)
	w.WriteHeader(http.StatusNoContent)
}

// toggleLike: table/likeTable/col are fixed internal literals, never user input.
func (h *HSHandler) toggleLike(w http.ResponseWriter, v hsViewer, table, likeTable, col string, id int64, on bool) {
	var one int
	if err := h.DB.QueryRow(`SELECT 1 FROM `+table+` WHERE id=? AND deleted=0`, id).Scan(&one); err != nil {
		hsErr(w, 404, "not found")
		return
	}
	if on {
		h.DB.Exec(`INSERT OR IGNORE INTO `+likeTable+` (`+col+`,sub) VALUES (?,?)`, id, v.sub)
	} else {
		h.DB.Exec(`DELETE FROM `+likeTable+` WHERE `+col+`=? AND sub=?`, id, v.sub)
	}
	var n int
	h.DB.QueryRow(`SELECT COUNT(*) FROM `+likeTable+` WHERE `+col+`=?`, id).Scan(&n)
	h.DB.Exec(`UPDATE `+table+` SET likes=? WHERE id=?`, n, id)
	writeJSON(w, http.StatusOK, map[string]any{"likes": n, "liked_by_me": on})
}

// ---- comments (decks and wall posts share one shape) ---------------------------------------------------

func (h *HSHandler) listComments(w http.ResponseWriter, r *http.Request, v hsViewer, table, col string, parent int64) {
	limit, offset := hsPage(r, 50)
	var total int
	h.DB.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE `+col+`=? AND deleted=0`, parent).Scan(&total)
	rows, err := h.DB.Query(`SELECT c.id, c.body, COALESCE(p.handle,''), c.sub, c.created_at FROM `+table+` c LEFT JOIN hs_profiles p ON p.sub=c.sub
		WHERE c.`+col+`=? AND c.deleted=0 ORDER BY c.id ASC LIMIT ? OFFSET ?`, parent, limit, offset)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int64
		var body, handle, sub, created string
		rows.Scan(&id, &body, &handle, &sub, &created)
		items = append(items, map[string]any{"id": id, "body": body, "author": map[string]string{"handle": handle},
			"created_at": hsTime(created), "can_delete": v.sub != "" && (v.sub == sub || v.admin)})
	}
	hsList(w, total, limit, offset, items)
}

func (h *HSHandler) addComment(w http.ResponseWriter, r *http.Request, v hsViewer, parentTable, table, col string, parent int64) {
	handle, ok := h.needHandle(w, v)
	if !ok {
		return
	}
	var in struct{ Body string }
	if err := hsDecode(r, &in); err != nil {
		hsErr(w, 400, "bad request body")
		return
	}
	body, okB := hsText(in.Body, 1, 500)
	if !okB {
		hsErr(w, 422, "comments are 1-500 characters")
		return
	}
	var one int
	if err := h.DB.QueryRow(`SELECT 1 FROM `+parentTable+` WHERE id=? AND deleted=0`, parent).Scan(&one); err != nil {
		hsErr(w, 404, "not found")
		return
	}
	res, err := h.DB.Exec(`INSERT INTO `+table+` (`+col+`,sub,body) VALUES (?,?,?)`, parent, v.sub, body)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	h.DB.Exec(`UPDATE `+parentTable+` SET comments=(SELECT COUNT(*) FROM `+table+` WHERE `+col+`=? AND deleted=0) WHERE id=?`, parent, parent)
	cid, _ := res.LastInsertId()
	writeJSON(w, http.StatusCreated, map[string]any{"id": cid, "body": body, "author": map[string]string{"handle": handle}, "can_delete": true,
		"created_at": time.Now().UTC().Format(time.RFC3339)})
}

func (h *HSHandler) deleteComment(w http.ResponseWriter, v hsViewer, table, parentTable, col string, id int64) {
	var sub string
	var parent int64
	if err := h.DB.QueryRow(`SELECT sub,`+col+` FROM `+table+` WHERE id=? AND deleted=0`, id).Scan(&sub, &parent); err != nil {
		hsErr(w, 404, "not found")
		return
	}
	if sub != v.sub && !v.admin {
		hsErr(w, 403, "not yours to delete")
		return
	}
	h.DB.Exec(`UPDATE `+table+` SET deleted=1 WHERE id=?`, id)
	h.DB.Exec(`UPDATE `+parentTable+` SET comments=(SELECT COUNT(*) FROM `+table+` WHERE `+col+`=? AND deleted=0) WHERE id=?`, parent, parent)
	w.WriteHeader(http.StatusNoContent)
}

// ---- profiles -----------------------------------------------------------------------------------------

func (h *HSHandler) getMe(w http.ResponseWriter, r *http.Request, v hsViewer) {
	if v.sub == "" {
		hsErr(w, http.StatusUnauthorized, "sign in with IDUNA first")
		return
	}
	var handle, bio, created string
	err := h.DB.QueryRow(`SELECT handle,bio,created_at FROM hs_profiles WHERE sub=?`, v.sub).Scan(&handle, &bio, &created)
	if err != nil {
		var dn string
		h.DB.QueryRow(`SELECT display_name FROM players WHERE player_id=?`, v.sub).Scan(&dn)
		writeJSON(w, http.StatusOK, map[string]any{"handle": nil, "suggested_handle": hsSuggest(dn)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"handle": handle, "bio": bio, "created_at": hsTime(created)})
}

func hsSuggest(display string) string {
	s := strings.ToLower(display)
	s = regexp.MustCompile(`[^a-z0-9_]+`).ReplaceAllString(s, "_")
	s = strings.Trim(s, "_")
	if len(s) > 20 {
		s = s[:20]
	}
	if !hsHandleRe.MatchString(s) {
		return ""
	}
	return s
}

func (h *HSHandler) putMe(w http.ResponseWriter, r *http.Request, v hsViewer) {
	var in struct {
		Handle *string `json:"handle"`
		Bio    *string `json:"bio"`
	}
	if err := hsDecode(r, &in); err != nil {
		hsErr(w, 400, "bad request body")
		return
	}
	cur, has := h.profileOf(v.sub)
	if !has {
		if in.Handle == nil {
			hsErr(w, 422, "choose a handle")
			return
		}
		hd := strings.ToLower(strings.TrimSpace(*in.Handle))
		if !hsHandleRe.MatchString(hd) {
			hsErr(w, 422, "handles are 3-20 characters: letters, numbers and underscores")
			return
		}
		if hsReserved[hd] {
			hsErr(w, 422, "that handle is reserved")
			return
		}
		bio := ""
		if in.Bio != nil {
			b, ok := hsText(*in.Bio, 0, 160)
			if !ok {
				hsErr(w, 422, "bio is max 160 characters")
				return
			}
			bio = b
		}
		if _, err := h.DB.Exec(`INSERT INTO hs_profiles (sub,handle,bio) VALUES (?,?,?)`, v.sub, hd, bio); err != nil {
			hsErr(w, http.StatusConflict, "that handle is taken")
			return
		}
		h.getMe(w, r, v)
		return
	}
	if in.Handle != nil && !strings.EqualFold(strings.TrimSpace(*in.Handle), cur) {
		hsErr(w, 422, "handles can't be changed once chosen")
		return
	}
	if in.Bio != nil {
		b, ok := hsText(*in.Bio, 0, 160)
		if !ok {
			hsErr(w, 422, "bio is max 160 characters")
			return
		}
		h.DB.Exec(`UPDATE hs_profiles SET bio=? WHERE sub=?`, b, v.sub)
	}
	h.getMe(w, r, v)
}

const hsUserCols = `p.sub, p.handle, p.bio, p.created_at,
 (SELECT COUNT(*) FROM hs_decks d WHERE d.owner_sub=p.sub AND d.deleted=0 AND d.private=0 AND d.merged_into=0),
 (SELECT COUNT(*) FROM hs_wall_posts w WHERE w.owner_sub=p.sub AND w.deleted=0),
 (SELECT COUNT(*) FROM hs_follows f WHERE f.followee_sub=p.sub),
 (SELECT COUNT(*) FROM hs_follows f WHERE f.follower_sub=p.sub),
 EXISTS(SELECT 1 FROM hs_follows f WHERE f.follower_sub=? AND f.followee_sub=p.sub)`

func scanUser(sc interface{ Scan(...any) error }, v hsViewer) (map[string]any, error) {
	var sub, handle, bio, created string
	var decks, posts, followers, following, fby int
	if err := sc.Scan(&sub, &handle, &bio, &created, &decks, &posts, &followers, &following, &fby); err != nil {
		return nil, err
	}
	return map[string]any{"handle": handle, "bio": bio, "created_at": hsTime(created), "decks": decks, "posts": posts,
		"followers": followers, "following": following, "followed_by_me": fby == 1, "is_me": v.sub != "" && v.sub == sub}, nil
}

func (h *HSHandler) getUser(w http.ResponseWriter, r *http.Request, v hsViewer, handle string) {
	u, err := scanUser(h.DB.QueryRow(`SELECT `+hsUserCols+` FROM hs_profiles p WHERE p.handle=?`, v.sub, strings.ToLower(handle)), v)
	if err != nil {
		hsErr(w, 404, "no such player")
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (h *HSHandler) listUsers(w http.ResponseWriter, r *http.Request, v hsViewer) {
	limit, offset := hsPage(r, 30)
	where, args := "1=1", []any{}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		where = `p.handle LIKE ? ESCAPE '\'`
		args = append(args, hsLike(strings.ToLower(q)))
	}
	order := "p.rowid DESC"
	if r.URL.Query().Get("sort") == "followed" {
		order = "(SELECT COUNT(*) FROM hs_follows f WHERE f.followee_sub=p.sub) DESC, p.rowid DESC"
	}
	var total int
	h.DB.QueryRow(`SELECT COUNT(*) FROM hs_profiles p WHERE `+where, args...).Scan(&total)
	rows, err := h.DB.Query(`SELECT `+hsUserCols+` FROM hs_profiles p WHERE `+where+` ORDER BY `+order+` LIMIT ? OFFSET ?`,
		append(append([]any{v.sub}, args...), limit, offset)...)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		if u, err := scanUser(rows, v); err == nil {
			items = append(items, u)
		}
	}
	hsList(w, total, limit, offset, items)
}

func (h *HSHandler) userDecks(w http.ResponseWriter, r *http.Request, v hsViewer, handle string) {
	sub, ok := h.subOfHandle(handle)
	if !ok {
		hsErr(w, 404, "no such player")
		return
	}
	h.queryDecks(w, r, v, sub)
}

func (h *HSHandler) follow(w http.ResponseWriter, v hsViewer, handle string, on bool) {
	if _, ok := h.needHandle(w, v); !ok {
		return
	}
	target, ok := h.subOfHandle(handle)
	if !ok {
		hsErr(w, 404, "no such player")
		return
	}
	if target == v.sub {
		hsErr(w, 422, "you can't follow yourself")
		return
	}
	if on {
		h.DB.Exec(`INSERT OR IGNORE INTO hs_follows (follower_sub,followee_sub) VALUES (?,?)`, v.sub, target)
	} else {
		h.DB.Exec(`DELETE FROM hs_follows WHERE follower_sub=? AND followee_sub=?`, v.sub, target)
	}
	var n int
	h.DB.QueryRow(`SELECT COUNT(*) FROM hs_follows WHERE followee_sub=?`, target).Scan(&n)
	writeJSON(w, http.StatusOK, map[string]any{"followed_by_me": on, "followers": n})
}

// ---- wall + feed --------------------------------------------------------------------------------------

const hsPostCols = `w.id, w.body, COALESCE(p.handle,''), w.owner_sub, w.likes, w.comments, w.created_at,
 EXISTS(SELECT 1 FROM hs_post_likes l WHERE l.post_id=w.id AND l.sub=?)`

func (h *HSHandler) queryPosts(w http.ResponseWriter, r *http.Request, v hsViewer, where string, args []any) {
	limit, _ := hsPage(r, 20)
	if b := atoiDefault(r.URL.Query().Get("before"), 0); b > 0 {
		where += " AND w.id < ?"
		args = append(args, b)
	}
	rows, err := h.DB.Query(`SELECT `+hsPostCols+` FROM hs_wall_posts w LEFT JOIN hs_profiles p ON p.sub=w.owner_sub WHERE w.deleted=0 AND `+where+
		` ORDER BY w.id DESC LIMIT ?`, append(append([]any{v.sub}, args...), limit+1)...)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int64
		var body, handle, owner, created string
		var likes, comments, liked int
		if rows.Scan(&id, &body, &handle, &owner, &likes, &comments, &created, &liked) != nil {
			continue
		}
		items = append(items, map[string]any{"id": id, "body": body, "author": map[string]string{"handle": handle}, "likes": likes,
			"comments": comments, "liked_by_me": liked == 1, "can_delete": v.sub != "" && (v.sub == owner || v.admin), "created_at": hsTime(created)})
	}
	more := len(items) > limit
	if more {
		items = items[:limit]
	}
	var next any
	if more && len(items) > 0 {
		next = items[len(items)-1]["id"]
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_before": next})
}

func (h *HSHandler) userWall(w http.ResponseWriter, r *http.Request, v hsViewer, handle string) {
	sub, ok := h.subOfHandle(handle)
	if !ok {
		hsErr(w, 404, "no such player")
		return
	}
	h.queryPosts(w, r, v, "w.owner_sub=?", []any{sub})
}

func (h *HSHandler) feed(w http.ResponseWriter, r *http.Request, v hsViewer) {
	if r.URL.Query().Get("scope") == "following" {
		if v.sub == "" {
			hsErr(w, http.StatusUnauthorized, "sign in with IDUNA first")
			return
		}
		h.queryPosts(w, r, v, "(w.owner_sub IN (SELECT followee_sub FROM hs_follows WHERE follower_sub=?) OR w.owner_sub=?)", []any{v.sub, v.sub})
		return
	}
	h.queryPosts(w, r, v, "1=1", nil)
}

func (h *HSHandler) createPost(w http.ResponseWriter, r *http.Request, v hsViewer) {
	handle, ok := h.needHandle(w, v)
	if !ok {
		return
	}
	var in struct{ Body string }
	if err := hsDecode(r, &in); err != nil {
		hsErr(w, 400, "bad request body")
		return
	}
	body, okB := hsText(in.Body, 1, 500)
	if !okB {
		hsErr(w, 422, "posts are 1-500 characters")
		return
	}
	res, err := h.DB.Exec(`INSERT INTO hs_wall_posts (owner_sub,body) VALUES (?,?)`, v.sub, body)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "body": body, "author": map[string]string{"handle": handle}, "likes": 0,
		"comments": 0, "liked_by_me": false, "can_delete": true, "created_at": time.Now().UTC().Format(time.RFC3339)})
}

func (h *HSHandler) report(w http.ResponseWriter, r *http.Request, v hsViewer) {
	var in struct {
		Kind   string `json:"kind"`
		ID     string `json:"id"`
		Reason string `json:"reason"`
	}
	if err := hsDecode(r, &in); err != nil {
		hsErr(w, 400, "bad request body")
		return
	}
	switch in.Kind {
	case "deck", "comment", "post", "user":
	default:
		hsErr(w, 422, "unknown report kind")
		return
	}
	reason, _ := hsText(in.Reason, 0, 300)
	tid, ok := hsText(in.ID, 1, 40)
	if !ok {
		hsErr(w, 422, "missing target")
		return
	}
	h.DB.Exec(`INSERT INTO hs_reports (reporter_sub,kind,target_id,reason) VALUES (?,?,?,?)`, v.sub, in.Kind, tid, reason)
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

// ---- per-player settings ---------------------------------------------------------------------------------

func (h *HSHandler) autoPublish(sub string) bool {
	var n int
	h.DB.QueryRow(`SELECT auto_publish FROM hs_settings WHERE sub=?`, sub).Scan(&n)
	return n == 1
}

func (h *HSHandler) getSettings(w http.ResponseWriter, v hsViewer) {
	if v.sub == "" {
		hsErr(w, http.StatusUnauthorized, "sign in with IDUNA first")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"auto_publish": h.autoPublish(v.sub)})
}

// putSettings: auto_publish=true publishes decks synced from the tracker immediately; the default (false)
// keeps them private until the owner publishes each one.
func (h *HSHandler) putSettings(w http.ResponseWriter, r *http.Request, v hsViewer) {
	if v.sub == "" {
		hsErr(w, http.StatusUnauthorized, "sign in with IDUNA first")
		return
	}
	var in struct {
		AutoPublish *bool `json:"auto_publish"`
	}
	if err := hsDecode(r, &in); err != nil {
		hsErr(w, 400, "bad request body")
		return
	}
	if in.AutoPublish != nil {
		h.DB.Exec(`INSERT INTO hs_settings (sub,auto_publish) VALUES (?,?) ON CONFLICT(sub) DO UPDATE SET auto_publish=excluded.auto_publish`, v.sub, boolInt(*in.AutoPublish))
	}
	h.getSettings(w, v)
}
