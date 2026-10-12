package handlers

// hs_identity.go -- WOTAN Identity Federation, chunk ID-A: the signed-in player's identities / prefs /
// upload-key API under /api/v1/hs/me/..., the upload-key -> 12 h upload-JWT exchange, and the ingest tag
// binding that attributes (or refuses) finished live games (IDENTITY_NORTHSTAR sections 2, 4.3, 5, 7).
//
//	GET    /api/v1/hs/me/identities                 list + gate + prefs + provider status
//	DELETE /api/v1/hs/me/identities/{id}            unlink (last-login-door and active-key guards)
//	GET|PUT /api/v1/hs/me/identities/prefs          {show_twitch_name, show_battletag}
//	GET|POST /api/v1/hs/me/identities/{battlenet|twitch}/start   begin a link flow (identity_oauth.go)
//	POST   /api/v1/hs/me/upload-keys                mint (shown once; needs the Battle.net gate open)
//	GET    /api/v1/hs/me/upload-keys                list (prefix, counters; never the key or hash)
//	DELETE /api/v1/hs/me/upload-keys/{id}           revoke (live session tombstoned)
//	POST   /api/v1/hs/live/exchange                 {key} -> 12 h hs-live-upload JWT (no bearer; the key is the credential)

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	uploadKeyPrefix  = "wt_"
	uploadKeyMax     = 5 // active keys per player
	twitchLiveTTL    = 60 * time.Second
	keyTouchEvery    = 30 * time.Second
	identityErrTaken = "that account is already linked to another WOTAN player"
)

func uploadKeyHash(key string) string {
	s := sha256.Sum256([]byte(key))
	return hex.EncodeToString(s[:])
}

func idErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": msg, "code": code})
}

// ---- identities ------------------------------------------------------------------------------------------------

func (h *HSHandler) identityPrefs(sub string) (showTwitch, showTag bool) {
	var a, b int
	_ = h.DB.QueryRow(`SELECT show_twitch_name, show_battletag FROM hs_settings WHERE sub=?`, sub).Scan(&a, &b)
	return a == 1, b == 1
}

func (h *HSHandler) listIdentities(w http.ResponseWriter, v hsViewer) {
	ensureLocalIdentities(h.DB, v.sub)
	its := identitiesOf(h.DB, v.sub, false)
	items := make([]map[string]any, 0, len(its))
	canUnlink := map[string]bool{}
	for _, it := range its {
		var extra map[string]any
		_ = json.Unmarshal([]byte(it.ExtraJSON), &extra)
		if extra == nil {
			extra = map[string]any{}
		}
		code, _ := unlinkChecks(h.DB, it)
		m := map[string]any{
			"id": it.ID, "provider": it.Provider, "display": it.Display, "verified": it.Verified,
			"linked_at": hsTime(it.LinkedAt), "last_login_at": hsTime(it.LastLoginAt), "login_door": loginDoorProviders[it.Provider],
			"extra": extra, "can_unlink": code == "", "status": it.Status,
		}
		canUnlink[fmt.Sprint(it.ID)] = code == ""
		if code != "" {
			m["unlink_blocked_by"] = code
		}
		items = append(items, m)
	}
	st, sb := h.identityPrefs(v.sub)
	var oauth map[string]any
	if h.OAuth != nil {
		oauth = h.OAuth.ProviderStatus()
	} else {
		oauth = (*IdentityOAuthHandler)(nil).ProviderStatus()
	}
	g := identityGate(h.DB, v.sub, nil)
	out := map[string]any{
		"identities":       items,
		"can_unlink":       canUnlink,
		"battlenet_linked": g.BattlenetLinked,
		"login_doors":      loginDoorCount(h.DB, v.sub),
		"gate":             gateJSON(g),
		"prefs":            map[string]any{"show_twitch_name": st, "show_battletag": sb},
		"providers":        oauth,
	}
	if g.GraceUntil != "" {
		out["grace_until"] = hsTime(g.GraceUntil)
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *HSHandler) unlinkIdentity(w http.ResponseWriter, r *http.Request, v hsViewer, id int64) {
	it, err := scanIdentity(h.DB.QueryRow(`SELECT `+identityCols+` FROM player_identities WHERE id=? AND player_id=? AND status='active'`, id, v.sub))
	if err != nil {
		idErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	if code, msg := unlinkChecks(h.DB, it); code != "" {
		idErr(w, http.StatusConflict, code, msg)
		return
	}
	if _, err := h.DB.Exec(`UPDATE player_identities SET status='revoked' WHERE id=? AND player_id=?`, id, v.sub); err != nil {
		idErr(w, http.StatusInternalServerError, "server_error", "could not unlink")
		return
	}
	identityAudit(r.Context(), h.DB, h.EventLog, v.sub, v.sub, "unlink", map[string]any{"provider": it.Provider, "identity_id": id}, false)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id, "provider": it.Provider})
}

func (h *HSHandler) getIdentityPrefs(w http.ResponseWriter, v hsViewer) {
	st, sb := h.identityPrefs(v.sub)
	writeJSON(w, http.StatusOK, map[string]any{"show_twitch_name": st, "show_battletag": sb})
}

func (h *HSHandler) putIdentityPrefs(w http.ResponseWriter, r *http.Request, v hsViewer) {
	var in struct {
		ShowTwitchName *bool `json:"show_twitch_name"`
		ShowBattletag  *bool `json:"show_battletag"`
	}
	if hsDecode(r, &in) != nil {
		hsErr(w, http.StatusBadRequest, "bad json")
		return
	}
	_, _ = h.DB.Exec(`INSERT OR IGNORE INTO hs_settings (sub) VALUES (?)`, v.sub)
	if in.ShowTwitchName != nil {
		_, _ = h.DB.Exec(`UPDATE hs_settings SET show_twitch_name=? WHERE sub=?`, boolInt(*in.ShowTwitchName), v.sub)
	}
	if in.ShowBattletag != nil {
		_, _ = h.DB.Exec(`UPDATE hs_settings SET show_battletag=? WHERE sub=?`, boolInt(*in.ShowBattletag), v.sub)
	}
	st, sb := h.identityPrefs(v.sub)
	writeJSON(w, http.StatusOK, map[string]any{"show_twitch_name": st, "show_battletag": sb})
}

// identityLinkStart is always link mode -- this route exists to attach a provider identity to the
// caller's own, already-authenticated player, never to sign a new session in (that's the public
// /api/v1/auth/sso/{prov}/start route instead). IdentityOAuthHandler.start infers link mode from a
// non-empty linkSub, which requires an authenticated caller here just as much as the pid lookup
// inside start itself does -- check it explicitly so an anonymous call 401s rather than silently
// falling through to a login-mode start.
func (h *HSHandler) identityLinkStart(w http.ResponseWriter, r *http.Request, v hsViewer, prov string) {
	if v.sub == "" {
		hsErr(w, http.StatusUnauthorized, "sign in with IDUNA first")
		return
	}
	if h.OAuth == nil || !h.OAuth.configured(prov) {
		notConfigured(w)
		return
	}
	h.OAuth.start(w, r, prov, v.sub)
}

// ---- upload keys -------------------------------------------------------------------------------------------------

func (h *HSHandler) createUploadKey(w http.ResponseWriter, r *http.Request, v hsViewer) {
	var in struct {
		Label string `json:"label"`
	}
	_ = hsDecode(r, &in) // an empty body is fine
	label, ok := hsText(in.Label, 0, 40)
	if !ok {
		hsErr(w, http.StatusUnprocessableEntity, "label must be at most 40 characters")
		return
	}
	g := identityGate(h.DB, v.sub, nil)
	if !g.Open() {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "link a Battle.net account to create a tracker upload key", "code": "battlenet_required", "gate": gateJSON(g)})
		return
	}
	var active int
	_ = h.DB.QueryRow(`SELECT COUNT(*) FROM hs_upload_keys WHERE player_id=? AND revoked_at IS NULL`, v.sub).Scan(&active)
	if active >= uploadKeyMax {
		idErr(w, http.StatusConflict, "key_limit", "revoke an unused upload key before creating another")
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		hsErr(w, http.StatusInternalServerError, "could not generate key")
		return
	}
	key := uploadKeyPrefix + base64.RawURLEncoding.EncodeToString(raw)
	prefix := key[:9]
	now := sqlNowUTC()
	res, err := h.DB.Exec(`INSERT INTO hs_upload_keys (player_id, key_prefix, key_hash, label, created_at) VALUES (?,?,?,?,?)`,
		v.sub, prefix, uploadKeyHash(key), label, now)
	if err != nil {
		hsErr(w, http.StatusInternalServerError, "could not save key")
		return
	}
	id, _ := res.LastInsertId()
	identityAudit(r.Context(), h.DB, h.EventLog, v.sub, v.sub, "uploadkey.create", map[string]any{"key_id": id, "prefix": prefix}, false)
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": id, "key": key, "prefix": prefix, "label": label, "created_at": hsTime(now),
		"note": "This key is shown once. Store it somewhere safe; it cannot be shown again.",
	})
}

func (h *HSHandler) listUploadKeys(w http.ResponseWriter, v hsViewer) {
	rows, err := h.DB.Query(`SELECT id, key_prefix, label, created_at, COALESCE(last_used_at,''), COALESCE(revoked_at,''),
		uploads_ok, rejected_tag_mismatch FROM hs_upload_keys WHERE player_id=? ORDER BY id DESC LIMIT 50`, v.sub)
	if err != nil {
		hsErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()
	keys := []map[string]any{}
	for rows.Next() {
		var id int64
		var prefix, label, created, used, revoked string
		var ok, rej int
		if rows.Scan(&id, &prefix, &label, &created, &used, &revoked, &ok, &rej) != nil {
			continue
		}
		keys = append(keys, map[string]any{
			"id": id, "prefix": prefix, "key_prefix": prefix, "label": label, "created_at": hsTime(created), "last_used_at": hsTime(used),
			"revoked_at": hsTime(revoked), "active": revoked == "", "uploads_ok": ok, "rejected_tag_mismatch": rej,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys, "items": keys, "gate": gateJSON(identityGate(h.DB, v.sub, nil))})
}

// dropLiveSession tombstones a player's in-memory live session (and so any state it held).
func (h *HSHandler) dropLiveSession(sub string, keyID int64) {
	h.liveMu.Lock()
	s := h.liveSess[sub]
	h.liveMu.Unlock()
	if s == nil {
		return
	}
	s.mu.Lock()
	match := keyID == 0 || s.keyID == keyID
	s.mu.Unlock()
	if match {
		h.liveMu.Lock()
		if h.liveSess[sub] == s {
			delete(h.liveSess, sub)
		}
		h.liveMu.Unlock()
	}
}

func (h *HSHandler) revokeUploadKey(w http.ResponseWriter, r *http.Request, v hsViewer, id int64) {
	res, err := h.DB.Exec(`UPDATE hs_upload_keys SET revoked_at=? WHERE id=? AND player_id=? AND revoked_at IS NULL`, sqlNowUTC(), id, v.sub)
	if err != nil {
		hsErr(w, http.StatusInternalServerError, "could not revoke")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		idErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	h.dropLiveSession(v.sub, id)
	identityAudit(r.Context(), h.DB, h.EventLog, v.sub, v.sub, "uploadkey.revoke", map[string]any{"key_id": id}, false)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

// liveExchange swaps an upload key for the existing 12 h upload JWT. Invalid and revoked keys are
// indistinguishable to the caller.
func (h *HSHandler) liveExchange(w http.ResponseWriter, r *http.Request) {
	if h.WriteLimiter != nil && !h.WriteLimiter.Allow("hsx:"+clientIP(r)) {
		w.Header().Set("Retry-After", "30")
		idErr(w, http.StatusTooManyRequests, "rate_limited", "slow down")
		return
	}
	var in struct {
		Key string `json:"key"`
	}
	if hsDecode(r, &in) != nil || !strings.HasPrefix(in.Key, uploadKeyPrefix) || len(in.Key) > 128 {
		idErr(w, http.StatusUnauthorized, "invalid_key", "invalid or revoked upload key")
		return
	}
	var id int64
	var pid string
	var revoked sql.NullString
	err := h.DB.QueryRow(`SELECT id, player_id, revoked_at FROM hs_upload_keys WHERE key_hash=?`, uploadKeyHash(in.Key)).Scan(&id, &pid, &revoked)
	if err != nil || revoked.Valid {
		idErr(w, http.StatusUnauthorized, "invalid_key", "invalid or revoked upload key")
		return
	}
	var disabled sql.NullString
	if h.DB.QueryRow(`SELECT disabled_at FROM players WHERE player_id=?`, pid).Scan(&disabled) == nil && disabled.Valid {
		idErr(w, http.StatusUnauthorized, "invalid_key", "invalid or revoked upload key")
		return
	}
	if g := identityGate(h.DB, pid, nil); !g.Open() {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "this key's account no longer has a linked Battle.net identity", "code": "battlenet_required"})
		return
	}
	tok, ok := h.mintUploadToken(pid, id)
	if !ok {
		hsErr(w, http.StatusInternalServerError, "could not mint token")
		return
	}
	_, _ = h.DB.Exec(`UPDATE hs_upload_keys SET last_used_at=? WHERE id=?`, sqlNowUTC(), id)
	tgt := h.syncTargetOf(pid)
	writeJSON(w, http.StatusOK, map[string]any{
		"token": tok, "expires_in": int(hsLiveTokenTTL.Seconds()),
		"tags_linked": len(activeBattleTags(h.DB, pid)), "sync_target": tgt.Label(),
	})
}

// liveKeyOK validates the key_id claim (when present) of an upload JWT against the live key row.
// Tokens without key_id (minted by POST /live/token from a normal sign-in) are unaffected.
func (h *HSHandler) liveKeyOK(claims map[string]any, sub string) (keyID int64, ok bool) {
	f, has := claims["key_id"].(float64)
	if !has {
		return 0, true
	}
	keyID = int64(f)
	var revoked sql.NullString
	var owner string
	if h.DB.QueryRow(`SELECT player_id, revoked_at FROM hs_upload_keys WHERE id=?`, keyID).Scan(&owner, &revoked) != nil {
		return keyID, false
	}
	return keyID, owner == sub && !revoked.Valid
}

func (h *HSHandler) touchKey(s *liveSession) {
	if s.keyID == 0 || time.Since(s.keyTouched) < keyTouchEvery {
		return
	}
	s.keyTouched = time.Now()
	_, _ = h.DB.Exec(`UPDATE hs_upload_keys SET last_used_at=? WHERE id=?`, sqlNowUTC(), s.keyID)
}

// ---- ingest binding (section 4.3 step 4) -----------------------------------------------------------------------------

// gameBinding is how one finished game is attributed.
type gameBinding struct {
	battletagID int64 // player_identities.id, 0 = none
	unverified  bool  // generic/absent PlayerName: forced private, never routable
	firstLive   bool  // Twitch said the owner was live at ingest
}

// bindLiveGame applies the BattleTag rules to the uploader's own PlayerName.
//
//	name equals a linked BattleTag      -> attributed to that identity
//	generic / absent name               -> unverified_tag, forced private
//	BattleTag-shaped but not linked     -> drop (the caller bumps the key's rejected_tag_mismatch)
//
// A player with no linked Battle.net identity who is still inside the grace window (or holds bypass) keeps the
// pre-identity behavior unchanged, so grandfathered uploaders are not silently cut off.
func (h *HSHandler) bindLiveGame(ctx context.Context, sub, name string) (b gameBinding, drop bool) {
	tags := activeBattleTags(h.DB, sub)
	if len(tags) == 0 {
		if g := identityGate(h.DB, sub, nil); g.InGrace || g.Bypass {
			b.firstLive = h.twitchLiveNow(ctx, sub)
			return b, false
		}
	}
	name = strings.TrimSpace(name)
	for _, t := range tags {
		if name != "" && strings.EqualFold(name, t.Display) {
			b.battletagID = t.ID
			b.firstLive = h.twitchLiveNow(ctx, sub)
			return b, false
		}
	}
	if hsTagShapeRe.MatchString(name) {
		return b, true
	}
	b.unverified = true
	return b, false
}

// twitchLiveNow is the first_streamed_live stub: an injectable seam (HSHandler.TwitchLive), cached 60 s per
// Twitch user id, and off (false) when no seam is configured or the player has no Twitch identity.
func (h *HSHandler) twitchLiveNow(ctx context.Context, sub string) bool {
	if h.TwitchLive == nil {
		return false
	}
	var twID string
	if h.DB.QueryRow(`SELECT provider_sub FROM player_identities WHERE player_id=? AND provider='twitch' AND status='active' LIMIT 1`, sub).Scan(&twID) != nil {
		return false
	}
	h.liveMu.Lock()
	if e, ok := h.twitchCache[twID]; ok && time.Since(e.at) < twitchLiveTTL {
		h.liveMu.Unlock()
		return e.live
	}
	h.liveMu.Unlock()
	live, err := h.TwitchLive(ctx, twID)
	if err != nil {
		live = false
	}
	h.liveMu.Lock()
	if h.twitchCache == nil {
		h.twitchCache = map[string]twitchLiveEntry{}
	}
	h.twitchCache[twID] = twitchLiveEntry{live: live, at: time.Now()}
	h.liveMu.Unlock()
	return live
}

type twitchLiveEntry struct {
	live bool
	at   time.Time
}

func (h *HSHandler) bumpRejected(keyID int64) {
	if keyID > 0 {
		_, _ = h.DB.Exec(`UPDATE hs_upload_keys SET rejected_tag_mismatch=rejected_tag_mismatch+1 WHERE id=?`, keyID)
	}
}

func (h *HSHandler) bumpUploadOK(keyID int64) {
	if keyID > 0 {
		_, _ = h.DB.Exec(`UPDATE hs_upload_keys SET uploads_ok=uploads_ok+1 WHERE id=?`, keyID)
	}
}
