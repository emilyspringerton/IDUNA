package handlers

// identity.go -- WOTAN Identity Federation, chunk ID-A (WOTAN/IDENTITY_NORTHSTAR.md): the shared model and
// helpers every other identity file builds on.
//
// player_id is the master id (it equals the JWT sub). A player has N rows in player_identities (battlenet x N,
// google, iduna_local, twitch x 1). A (provider, provider_sub) pair belongs to at most one player; linking one
// that another player already owns is "identity_taken" (non-leaking), never an auto-merge.
//
// Files: this one (model, linking core, gate, audit), identity_oauth.go (Battle.net SSO + Twitch link),
// hs_identity.go (identities / prefs / upload-keys API, key exchange, ingest tag binding), admin_identities.go
// (Back Office, force-unlink, revoke key, operator-assisted merge).

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"iduna/internal/userlog"
)

const (
	idProvBattlenet = "battlenet"
	idProvGoogle    = "google"
	idProvTwitch    = "twitch"
	idProvLocal     = "iduna_local"

	identityGraceDays  = 30
	identityBypassPerm = "wotan.identity.bypass"
	identityEventSrc   = "iduna-wotan-identity"
)

// loginDoorProviders are the identities that can sign a player in. Twitch joined as a login door
// 2026-10-12 (IQ-2 resolved: founder confirmed Twitch-as-login) — still identity-only, no stream-key
// or email scope (I8 still holds).
var loginDoorProviders = map[string]bool{idProvBattlenet: true, idProvGoogle: true, idProvLocal: true, idProvTwitch: true}

// hsTagShapeRe is "looks like a BattleTag": a name with no spaces, a '#', and 1-8 digits.
var hsTagShapeRe = regexp.MustCompile(`^[^\s#]{1,24}#[0-9]{1,8}$`)

var (
	errIdentityTaken       = errors.New("identity_taken")
	errTwitchAlreadyLinked = errors.New("twitch_already_linked")
)

// playerIdentity is one player_identities row.
type playerIdentity struct {
	ID            int64
	PlayerID      string
	Provider      string
	Sub           string
	Display       string
	ExtraJSON     string
	Verified      bool
	LinkedAt      string
	LastLoginAt   string
	LastRefreshed string
	Status        string
}

const identityCols = `id, player_id, provider, provider_sub, display, extra_json, verified, linked_at,
	COALESCE(last_login_at,''), COALESCE(last_refreshed_at,''), status`

func scanIdentity(sc interface{ Scan(...any) error }) (playerIdentity, error) {
	var it playerIdentity
	var ver int
	err := sc.Scan(&it.ID, &it.PlayerID, &it.Provider, &it.Sub, &it.Display, &it.ExtraJSON, &ver,
		&it.LinkedAt, &it.LastLoginAt, &it.LastRefreshed, &it.Status)
	it.Verified = ver == 1
	return it, err
}

func sqlNowUTC() string { return time.Now().UTC().Format("2006-01-02 15:04:05") }

// identitiesOf lists a player's identities (active only unless all).
func identitiesOf(db dbq, playerID string, includeRevoked bool) []playerIdentity {
	q := `SELECT ` + identityCols + ` FROM player_identities WHERE player_id=?`
	if !includeRevoked {
		q += ` AND status='active'`
	}
	rows, err := db.Query(q+` ORDER BY id`, playerID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []playerIdentity
	for rows.Next() {
		if it, err := scanIdentity(rows); err == nil {
			out = append(out, it)
		}
	}
	return out
}

// ensureLocalIdentities lazily backfills identity rows for a player whose players row (google /
// iduna_local) pre-dates or bypassed the migration backfill, so the list and the last-door guard always
// see every login door the player really has. It also covers a password credential with no players-row
// identity yet.
func ensureLocalIdentities(db dbq, playerID string) {
	_, _ = db.Exec(`INSERT OR IGNORE INTO player_identities (player_id, provider, provider_sub, display, verified, linked_at, status)
		SELECT player_id, provider, provider_sub, COALESCE(NULLIF(email,''), display_name),
		       CASE provider WHEN 'google' THEN 1 ELSE 0 END, registered_at, 'active'
		  FROM players WHERE player_id=? AND provider IN ('google','iduna_local')`, playerID)
}

// activeBattleTags returns the player's active Battle.net identities (id -> BattleTag).
func activeBattleTags(db dbq, playerID string) []playerIdentity {
	var out []playerIdentity
	for _, it := range identitiesOf(db, playerID, false) {
		if it.Provider == idProvBattlenet {
			out = append(out, it)
		}
	}
	return out
}

// ---- gate (section 5) -------------------------------------------------------------------------------------

type identityGateState struct {
	BattlenetLinked bool
	Bypass          bool
	InGrace         bool
	GraceUntil      string // empty when there is no grace row
}

// Open reports whether a Battle.net-gated action (club create, upload-key minting) is allowed right now.
func (g identityGateState) Open() bool { return g.BattlenetLinked || g.Bypass || g.InGrace }

// identityGate evaluates the gate for sub. Bypass is read from player_permissions (the real per-account
// grant list, same one the JWT's permissions claim is built from) and also from jwtPerms when the caller has
// a token (so a token carrying the claim works even before a row exists).
func identityGate(db dbq, sub string, jwtPerms map[string]bool) identityGateState {
	var g identityGateState
	if sub == "" {
		return g
	}
	var one int
	g.BattlenetLinked = db.QueryRow(`SELECT 1 FROM player_identities WHERE player_id=? AND provider='battlenet' AND status='active' LIMIT 1`, sub).Scan(&one) == nil
	g.Bypass = jwtPerms[identityBypassPerm] ||
		db.QueryRow(`SELECT 1 FROM player_permissions WHERE player_id=? AND permission=? LIMIT 1`, sub, identityBypassPerm).Scan(&one) == nil
	var until string
	if db.QueryRow(`SELECT grace_until FROM player_identity_grace WHERE player_id=?`, sub).Scan(&until) == nil {
		g.GraceUntil = until
		g.InGrace = until > sqlNowUTC()
	}
	return g
}

func gateJSON(g identityGateState) map[string]any {
	out := map[string]any{"battlenet_linked": g.BattlenetLinked, "bypass": g.Bypass, "in_grace": g.InGrace, "open": g.Open()}
	if g.GraceUntil != "" {
		out["grace_until"] = hsTime(g.GraceUntil)
	}
	return out
}

// ---- audit --------------------------------------------------------------------------------------------------

// identityAudit appends an identity_audit_log row and mirrors it to the unified event log (nil-safe).
func identityAudit(ctx context.Context, db dbq, el userlog.EventLog, playerID, actor, action string, meta map[string]any, platform bool) {
	if meta == nil {
		meta = map[string]any{}
	}
	raw, _ := json.Marshal(meta)
	pa := 0
	if platform {
		pa = 1
	}
	_, _ = db.Exec(`INSERT INTO identity_audit_log (player_id, actor_sub, action, meta_json, platform_admin, at) VALUES (?,?,?,?,?,?)`,
		playerID, actor, action, string(raw), pa, sqlNowUTC())
	emitAuthEvent(ctx, el, "iduna:wotan.identity."+action, identityEventSrc,
		map[string]any{"player_id": playerID, "actor_sub": actor, "meta": meta, "platform_admin": platform})
}

// ---- linking core --------------------------------------------------------------------------------------------

// linkOutcome is what linkIdentity did.
type linkOutcome int

const (
	linkCreated   linkOutcome = iota // new row attached
	linkRefreshed                    // already this player's: display refreshed
)

// linkIdentity attaches (provider, sub) to playerID. An active row owned by another player yields
// errIdentityTaken; a second Twitch for the same player yields errTwitchAlreadyLinked. A revoked tombstone
// is re-activated for the caller (the OAuth round trip just proved they own it).
func linkIdentity(db *sql.DB, playerID, provider, sub, display, extra string, verified bool) (id int64, out linkOutcome, err error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	if extra == "" {
		extra = "{}"
	}
	now := sqlNowUTC()
	var curPlayer, status string
	err = tx.QueryRow(`SELECT id, player_id, status FROM player_identities WHERE provider=? AND provider_sub=?`, provider, sub).
		Scan(&id, &curPlayer, &status)
	switch {
	case err == nil && status == "active" && curPlayer != playerID:
		return id, 0, errIdentityTaken
	case err == nil && status == "active":
		_, err = tx.Exec(`UPDATE player_identities SET display=?, extra_json=?, last_refreshed_at=? WHERE id=?`, display, extra, now, id)
		if err != nil {
			return 0, 0, err
		}
		return id, linkRefreshed, tx.Commit()
	case err != nil && err != sql.ErrNoRows:
		return 0, 0, err
	}
	if provider == idProvTwitch {
		var n int
		_ = tx.QueryRow(`SELECT COUNT(*) FROM player_identities WHERE player_id=? AND provider='twitch' AND status='active'`, playerID).Scan(&n)
		if n > 0 {
			return 0, 0, errTwitchAlreadyLinked
		}
	}
	v := boolInt(verified)
	if err == nil { // revoked tombstone: revive for this player
		_, err = tx.Exec(`UPDATE player_identities SET player_id=?, display=?, extra_json=?, verified=?, status='active',
			linked_at=?, last_refreshed_at=? WHERE id=?`, playerID, display, extra, v, now, now, id)
	} else {
		var res sql.Result
		res, err = tx.Exec(`INSERT INTO player_identities (player_id, provider, provider_sub, display, extra_json, verified, linked_at, last_refreshed_at, status)
			VALUES (?,?,?,?,?,?,?,?, 'active')`, playerID, provider, sub, display, extra, v, now, now)
		if err == nil {
			id, _ = res.LastInsertId()
		}
	}
	if err != nil {
		return 0, 0, err
	}
	return id, linkCreated, tx.Commit()
}

// battletagName is the part before '#'.
func battletagName(tag string) string {
	if i := strings.Index(tag, "#"); i > 0 {
		return tag[:i]
	}
	return tag
}

// loginDoorCount counts the player's active login-capable identities.
func loginDoorCount(db dbq, playerID string) int {
	n := 0
	for _, it := range identitiesOf(db, playerID, false) {
		if loginDoorProviders[it.Provider] {
			n++
		}
	}
	return n
}

// unlinkChecks applies the section 2 guards for removing identity it as its owner (operators bypass them).
// Returns a machine code and message, or "" when allowed.
func unlinkChecks(db dbq, it playerIdentity) (code, msg string) {
	if it.Provider == idProvTwitch {
		return "", ""
	}
	if loginDoorProviders[it.Provider] && loginDoorCount(db, it.PlayerID) <= 1 {
		return "last_login_door", "this is your only way to sign in; link another login first"
	}
	if it.Provider == idProvBattlenet {
		remaining := 0
		for _, o := range activeBattleTags(db, it.PlayerID) {
			if o.ID != it.ID {
				remaining++
			}
		}
		if remaining == 0 {
			var keys int
			_ = db.QueryRow(`SELECT COUNT(*) FROM hs_upload_keys WHERE player_id=? AND revoked_at IS NULL`, it.PlayerID).Scan(&keys)
			if keys > 0 {
				return "has_active_keys", "revoke your tracker upload keys before unlinking your last Battle.net account"
			}
		}
	}
	return "", ""
}
