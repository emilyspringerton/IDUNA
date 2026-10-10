package handlers

// hs_games.go -- Hearthstone game records from the HRIP deck tracker: ingest, list, replay fetch and
// class-vs-class matchup stats. The record is the tracker's own JSON (header + revealed entities +
// ordered replay timeline); the full body is stored so the replay viewer reads exactly what was sent.

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

const hsGameMaxBody = 2 << 20

type hsGameRecord struct {
	GameType string `json:"game_type"`
	Format   string `json:"format"`
	Player1  string `json:"player1"`
	Player2  string `json:"player2"`
	Result1  int    `json:"result1"`
	Result2  int    `json:"result2"`
	Turns    int    `json:"turns"`
	Complete int    `json:"complete"`
	Me       int    `json:"me"`
	Entities []struct {
		ID     int    `json:"id"`
		Player int    `json:"player"`
		Card   string `json:"card"`
		Zone0  string `json:"zone0"`
	} `json:"entities"`
	Timeline json.RawMessage `json:"timeline"`
}

// classOf resolves a player's class from the first HERO_* card revealed for that slot.
func (h *HSHandler) classOf(rec *hsGameRecord, player int) string {
	for _, e := range rec.Entities {
		if e.Player != player || !strings.HasPrefix(e.Card, "HERO_") {
			continue
		}
		var class string
		if err := h.DB.QueryRow(`SELECT class FROM hs_cards WHERE card_id=? AND class!='' LIMIT 1`, e.Card).Scan(&class); err == nil {
			return class
		}
	}
	return ""
}

func (h *HSHandler) createGame(w http.ResponseWriter, r *http.Request, v hsViewer) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, hsGameMaxBody))
	if err != nil {
		hsErr(w, http.StatusRequestEntityTooLarge, "game record too large")
		return
	}
	var rec hsGameRecord
	if err := json.Unmarshal(body, &rec); err != nil || len(rec.Timeline) == 0 {
		hsErr(w, http.StatusUnprocessableEntity, "not a tracker game record")
		return
	}
	if me := atoiDefault(r.URL.Query().Get("me"), 0); me == 1 || me == 2 {
		rec.Me = me
	}
	if rec.Me != 1 && rec.Me != 2 {
		hsErr(w, http.StatusUnprocessableEntity, "could not tell which player you are; resend with ?me=1 or ?me=2")
		return
	}
	id, dup, mine, theirs, result, err := h.storeGame(v.sub, &rec, body, 0)
	if err != nil {
		hsErr(w, http.StatusInternalServerError, "could not store game")
		return
	}
	if dup {
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "duplicate": true})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "my_class": mine, "opp_class": theirs, "result": result})
}

// storeGame saves one finished game for its owner (deduplicated by content fingerprint). deckID links it to
// a library deck when the live tracker knows which deck was played.
func (h *HSHandler) storeGame(sub string, rec *hsGameRecord, body []byte, deckID int64) (id int64, dup bool, myClass, oppClass string, result int, err error) {
	sum := sha256.Sum256(body)
	fp := hex.EncodeToString(sum[:])
	if err = h.DB.QueryRow(`SELECT id FROM hs_games WHERE owner_sub=? AND fingerprint=?`, sub, fp).Scan(&id); err == nil {
		if deckID > 0 {
			var code string
			var priv int
			h.DB.QueryRow(`SELECT deckstring, private FROM hs_decks WHERE id=?`, deckID).Scan(&code, &priv)
			h.DB.Exec(`UPDATE hs_games SET deck_id=?, deck_code=?, deck_public=? WHERE id=? AND deck_id=0`, deckID, code, boolInt(priv == 0), id)
		}
		return id, true, "", "", 0, nil
	}
	opp := 3 - rec.Me
	result, myName, oppName := rec.Result1, rec.Player1, rec.Player2
	if rec.Me == 2 {
		result, myName, oppName = rec.Result2, rec.Player2, rec.Player1
	}
	seen := map[string]bool{}
	oppCards := []string{}
	for _, e := range rec.Entities {
		if e.Player == opp && e.Card != "" && !strings.HasPrefix(e.Card, "HERO_") && !seen[e.Card] {
			seen[e.Card] = true
			oppCards = append(oppCards, e.Card)
		}
	}
	oc, _ := json.Marshal(oppCards)
	myClass, oppClass = h.classOf(rec, rec.Me), h.classOf(rec, opp)
	// the win/loss pool: by deck contents, and public only if the player's own copy was public at game end
	var code string
	var priv int
	if deckID > 0 {
		h.DB.QueryRow(`SELECT deckstring, private FROM hs_decks WHERE id=?`, deckID).Scan(&code, &priv)
	}
	res, err := h.DB.Exec(`INSERT INTO hs_games (owner_sub,fingerprint,game_type,format,me,result,turns,complete,my_class,opp_class,my_name,opp_name,opp_cards,record_json,deck_id,deck_code,deck_public)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		sub, fp, rec.GameType, rec.Format, rec.Me, result, rec.Turns, rec.Complete,
		myClass, oppClass, myName, oppName, string(oc), string(body), deckID, code, boolInt(code != "" && priv == 0))
	if err != nil {
		return 0, false, "", "", 0, err
	}
	id, _ = res.LastInsertId()
	return id, false, myClass, oppClass, result, nil
}

func (h *HSHandler) listGames(w http.ResponseWriter, r *http.Request, v hsViewer) {
	if v.sub == "" {
		hsErr(w, http.StatusUnauthorized, "sign in with IDUNA first")
		return
	}
	limit, offset := hsPage(r, 25)
	rows, err := h.DB.Query(`SELECT id,game_type,format,me,result,turns,complete,my_class,opp_class,my_name,opp_name,created_at
		FROM hs_games WHERE owner_sub=? ORDER BY id DESC LIMIT ? OFFSET ?`, v.sub, limit, offset)
	if err != nil {
		hsErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var gt, fm, mc, oc, mn, on, ca string
		var me, res, turns, comp int
		if err := rows.Scan(&id, &gt, &fm, &me, &res, &turns, &comp, &mc, &oc, &mn, &on, &ca); err != nil {
			continue
		}
		out = append(out, map[string]any{"id": id, "game_type": gt, "format": fm, "me": me, "result": res, "turns": turns,
			"complete": comp, "my_class": mc, "opp_class": oc, "my_name": mn, "opp_name": on, "created_at": ca})
	}
	writeJSON(w, http.StatusOK, map[string]any{"games": out})
}

// getGame returns one game incl. the replay timeline; owner only (the record holds both BattleTags).
func (h *HSHandler) getGame(w http.ResponseWriter, v hsViewer, id int64) {
	if v.sub == "" {
		hsErr(w, http.StatusUnauthorized, "sign in with IDUNA first")
		return
	}
	var rec string
	var owner string
	err := h.DB.QueryRow(`SELECT owner_sub, record_json FROM hs_games WHERE id=?`, id).Scan(&owner, &rec)
	if err == sql.ErrNoRows || (err == nil && owner != v.sub && !v.admin) {
		hsErr(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		hsErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "record": json.RawMessage(rec)})
}

// matchups: class-vs-class results. Aggregate over everyone's finished games (no identities), or
// ?scope=mine for the signed-in user's own.
func (h *HSHandler) matchups(w http.ResponseWriter, r *http.Request, v hsViewer) {
	q := `SELECT my_class, opp_class, COUNT(*), SUM(result=1), SUM(result=2), SUM(result=3)
		FROM hs_games WHERE complete=1 AND result IN (1,2,3) AND my_class!='' AND opp_class!=''`
	args := []any{}
	if r.URL.Query().Get("scope") == "mine" {
		if v.sub == "" {
			hsErr(w, http.StatusUnauthorized, "sign in with IDUNA first")
			return
		}
		q += ` AND owner_sub=?`
		args = append(args, v.sub)
	}
	if f := r.URL.Query().Get("format"); f != "" {
		q += ` AND format=?`
		args = append(args, f)
	}
	q += ` GROUP BY my_class, opp_class ORDER BY COUNT(*) DESC`
	rows, err := h.DB.Query(q, args...)
	if err != nil {
		hsErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var mc, oc string
		var n, w1, l, t int
		if err := rows.Scan(&mc, &oc, &n, &w1, &l, &t); err != nil {
			continue
		}
		wr := 0.0
		if n > 0 {
			wr = float64(w1) / float64(n)
		}
		out = append(out, map[string]any{"my_class": mc, "opp_class": oc, "games": n, "wins": w1, "losses": l, "ties": t, "winrate": wr})
	}
	writeJSON(w, http.StatusOK, map[string]any{"matchups": out})
}
