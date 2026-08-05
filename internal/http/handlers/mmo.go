package handlers

// mmo.go — S75-02/03/04/05 + S129-05: DragonsNShit MMO API handlers.
//
// Routes (all require M2M or user JWT unless noted):
//   POST   /api/v1/characters                         — create character
//   GET    /api/v1/characters/:id                     — fetch character
//   GET    /api/v1/characters/by-player/:player_id     — resolve a WOTAN player_id to its character (2026-07-31)
//   PATCH  /api/v1/characters/:id/position            — update scene+pos (game server M2M)
//   PATCH  /api/v1/characters/:id/gold                — deduct gold (409 if insufficient)
//   PATCH  /api/v1/characters/:id/gold/credit          — credit gold, bounded per call (2026-07-31)
//   GET    /api/v1/characters/:id/inventory           — bag inventory (S129-05)
//   GET    /api/v1/characters/:id/equipment           — equipped slots (S129-05)
//   POST   /api/v1/items                              — craft item; provenance_chain[0] set
//   POST   /api/v1/items/:id/transfer                 — transfer item; append provenance
//   DELETE /api/v1/items/:id                          — soft-delete item
//   GET    /api/v1/items/:id                          — fetch item + full provenance
//   POST   /api/v1/guilds                             — found guild
//   GET    /api/v1/guilds/:id                         — fetch guild + members
//   POST   /api/v1/guilds/:id/members                 — join guild
//   PATCH  /api/v1/guilds/:id/members/:character_id   — role change
//   DELETE /api/v1/guilds/:id                         — disband guild (soft)
//   POST   /api/v1/world-events                       — open world event
//   PATCH  /api/v1/world-events/:id                   — phase transition
//   POST   /api/v1/world-events/:id/resolve           — resolve + Apple

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"iduna/internal/http/middleware"
)

// ── MMOHandler ────────────────────────────────────────────────────────────────

// MMOHandler serves all DragonsNShit MMO endpoints.
type MMOHandler struct {
	DB *sql.DB
}

func (h *MMOHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	switch {
	// Characters
	case strings.HasPrefix(path, "/api/v1/characters"):
		h.routeCharacters(w, r, path)
	// Items
	case strings.HasPrefix(path, "/api/v1/items"):
		h.routeItems(w, r, path)
	// Guilds
	case strings.HasPrefix(path, "/api/v1/guilds"):
		h.routeGuilds(w, r, path)
	// World events
	case strings.HasPrefix(path, "/api/v1/world-events"):
		h.routeWorldEvents(w, r, path)
	// Field office district overlay (S127-05)
	case strings.HasPrefix(path, "/api/v1/fieldoffices"):
		h.routeFieldOffices(w, r, path)
	default:
		http.NotFound(w, r)
	}
}

// ── Characters (S75-02) ───────────────────────────────────────────────────────

type createCharacterRequest struct {
	PlayerID string `json:"player_id"`
	Name     string `json:"name"`
	JobMain  string `json:"job_main"` // default "WAR"
}

type characterResponse struct {
	CharacterID string  `json:"character_id"`
	PlayerID    string  `json:"player_id"`
	Name        string  `json:"name"`
	SceneID     int     `json:"scene_id"`
	PosX        float64 `json:"pos_x"`
	PosY        float64 `json:"pos_y"`
	PosZ        float64 `json:"pos_z"`
	GoldBalance int     `json:"gold_balance"`
	Level       int     `json:"level"`
	CurrentXP   int     `json:"current_xp"`
	JobMain     string  `json:"job_main"`
	JobSub      string  `json:"job_sub"`
	HomeSceneID int     `json:"home_scene_id"`
	HomePosX    float64 `json:"home_pos_x"`
	HomePosY    float64 `json:"home_pos_y"`
	HomePosZ    float64 `json:"home_pos_z"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

func (h *MMOHandler) routeCharacters(w http.ResponseWriter, r *http.Request, path string) {
	// PATCH /api/v1/characters/:id/position
	if r.Method == http.MethodPatch && strings.HasSuffix(path, "/position") {
		id := extractSegment(path, "/api/v1/characters/", "/position")
		h.handleUpdatePosition(w, r, id)
		return
	}
	// PATCH /api/v1/characters/:id/home
	if r.Method == http.MethodPatch && strings.HasSuffix(path, "/home") {
		id := extractSegment(path, "/api/v1/characters/", "/home")
		h.handleUpdateHome(w, r, id)
		return
	}
	// PATCH /api/v1/characters/:id/level (2026-08-02, real bug found live building
	// GoblinFoxDragon's Town combat: idunaclient.UpdateCharacterLevel has always called plain
	// PATCH /api/v1/characters/:id -- a route that has never existed here, only ever falling
	// through to the 404 at the bottom of this function. Every caller (apps2/mud's real telnet
	// disconnect-time level/XP sync, and now headless sessions) has been silently failing this
	// entire time, masked by "best-effort" error handling at every call site. Real route added.
	if r.Method == http.MethodPatch && strings.HasSuffix(path, "/level") {
		id := extractSegment(path, "/api/v1/characters/", "/level")
		h.handleUpdateLevel(w, r, id)
		return
	}
	// PATCH /api/v1/characters/:id/job
	if r.Method == http.MethodPatch && strings.HasSuffix(path, "/job") {
		id := extractSegment(path, "/api/v1/characters/", "/job")
		h.handleUpdateJob(w, r, id)
		return
	}
	// PATCH /api/v1/characters/:id/gold
	if r.Method == http.MethodPatch && strings.HasSuffix(path, "/gold") {
		id := extractSegment(path, "/api/v1/characters/", "/gold")
		h.handleDeductGold(w, r, id)
		return
	}
	// PATCH /api/v1/characters/:id/gold/credit (2026-07-31, GoblinFoxDragon
	// EMILY/BACKLOG.md "unify the backends" follow-up): the deduct-only endpoint above has no
	// symmetric way to grant gold, which is why neither apps2/mud's nor apps2/server-go's own
	// gold-earning paths (quest rewards, item sales, future Battleground reward-crediting per
	// GoblinFoxDragon/docs2/REDGARDEN_GUI_NORTHSTAR.md's own Milestone 4) can ever persist an
	// increase.
	if r.Method == http.MethodPatch && strings.HasSuffix(path, "/gold/credit") {
		id := extractSegment(path, "/api/v1/characters/", "/gold/credit")
		h.handleCreditGold(w, r, id)
		return
	}
	// GET /api/v1/characters/:id/items
	if r.Method == http.MethodGet && strings.HasSuffix(path, "/items") {
		id := extractSegment(path, "/api/v1/characters/", "/items")
		h.handleListCharacterItems(w, r, id)
		return
	}
	// PATCH /api/v1/characters/:id/skills
	if r.Method == http.MethodPatch && strings.HasSuffix(path, "/skills") {
		id := extractSegment(path, "/api/v1/characters/", "/skills")
		h.handleIncrementSkill(w, r, id)
		return
	}
	// GET /api/v1/characters/:id/skills
	if r.Method == http.MethodGet && strings.HasSuffix(path, "/skills") {
		id := extractSegment(path, "/api/v1/characters/", "/skills")
		h.handleGetSkills(w, r, id)
		return
	}
	// GET /api/v1/characters/:id/inventory
	if r.Method == http.MethodGet && strings.HasSuffix(path, "/inventory") {
		id := extractSegment(path, "/api/v1/characters/", "/inventory")
		h.handleGetInventory(w, r, id)
		return
	}
	// GET /api/v1/characters/:id/equipment
	if r.Method == http.MethodGet && strings.HasSuffix(path, "/equipment") {
		id := extractSegment(path, "/api/v1/characters/", "/equipment")
		h.handleGetEquipment(w, r, id)
		return
	}
	// GET /api/v1/characters/by-player/:player_id (2026-07-31, REDGARDEN_GUI_NORTHSTAR.md
	// Milestone 4): resolves a WOTAN player_id to its DragonsNShit character, if it has one --
	// apps/arena_server's report_match_result already knows a match's players' real player_ids
	// (from their connect ticket) but has no way to find the character_id its own gold-credit
	// call needs. Must be checked before the generic GET /:id fallback just below, which would
	// otherwise treat "by-player" as a character_id.
	if r.Method == http.MethodGet && strings.HasPrefix(path, "/api/v1/characters/by-player/") {
		playerID := strings.TrimPrefix(path, "/api/v1/characters/by-player/")
		h.handleGetCharacterByPlayer(w, r, playerID)
		return
	}
	// GET /api/v1/characters/:id
	if r.Method == http.MethodGet && len(path) > len("/api/v1/characters/") {
		id := strings.TrimPrefix(path, "/api/v1/characters/")
		h.handleGetCharacter(w, r, id)
		return
	}
	// POST /api/v1/characters
	if r.Method == http.MethodPost && (path == "/api/v1/characters" || path == "/api/v1/characters/") {
		h.handleCreateCharacter(w, r)
		return
	}
	http.NotFound(w, r)
}

func (h *MMOHandler) handleCreateCharacter(w http.ResponseWriter, r *http.Request) {
	var req createCharacterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.PlayerID == "" || req.Name == "" {
		mmoWriteError(w, http.StatusBadRequest, "player_id and name required")
		return
	}
	if req.JobMain == "" {
		req.JobMain = "WAR"
	}
	charID := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := h.DB.ExecContext(r.Context(),
		`INSERT INTO characters (character_id, player_id, name, job_main, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		charID, req.PlayerID, req.Name, req.JobMain, now, now,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			mmoWriteError(w, http.StatusConflict, "character name already taken")
			return
		}
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"character_id": charID})
}

func (h *MMOHandler) handleGetCharacter(w http.ResponseWriter, r *http.Request, id string) {
	row := h.DB.QueryRowContext(r.Context(),
		`SELECT character_id, player_id, name, scene_id, pos_x, pos_y, pos_z,
		        gold_balance, level, current_xp, job_main, job_sub,
		        home_scene_id, home_pos_x, home_pos_y, home_pos_z, created_at, updated_at
		 FROM characters WHERE character_id = ?`, id)
	var c characterResponse
	if err := row.Scan(&c.CharacterID, &c.PlayerID, &c.Name, &c.SceneID,
		&c.PosX, &c.PosY, &c.PosZ, &c.GoldBalance, &c.Level, &c.CurrentXP,
		&c.JobMain, &c.JobSub, &c.HomeSceneID, &c.HomePosX, &c.HomePosY, &c.HomePosZ,
		&c.CreatedAt, &c.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			mmoWriteError(w, http.StatusNotFound, "character not found")
			return
		}
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// handleGetCharacterByPlayer (REDGARDEN_GUI_NORTHSTAR.md Milestone 4): same shape as
// handleGetCharacter, keyed by player_id instead of character_id. A player_id can have at most
// one DragonsNShit character (createCharacterRequest's own INSERT has no uniqueness constraint
// on player_id in the schema, but every real caller -- apps2/mud's fetch-or-create -- only ever
// creates one per player_id in practice); LIMIT 1 makes that assumption explicit rather than
// silently depending on row order if it's ever violated.
func (h *MMOHandler) handleGetCharacterByPlayer(w http.ResponseWriter, r *http.Request, playerID string) {
	row := h.DB.QueryRowContext(r.Context(),
		`SELECT character_id, player_id, name, scene_id, pos_x, pos_y, pos_z,
		        gold_balance, level, current_xp, job_main, job_sub,
		        home_scene_id, home_pos_x, home_pos_y, home_pos_z, created_at, updated_at
		 FROM characters WHERE player_id = ? LIMIT 1`, playerID)
	var c characterResponse
	if err := row.Scan(&c.CharacterID, &c.PlayerID, &c.Name, &c.SceneID,
		&c.PosX, &c.PosY, &c.PosZ, &c.GoldBalance, &c.Level, &c.CurrentXP,
		&c.JobMain, &c.JobSub, &c.HomeSceneID, &c.HomePosX, &c.HomePosY, &c.HomePosZ,
		&c.CreatedAt, &c.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			mmoWriteError(w, http.StatusNotFound, "no DragonsNShit character for this player_id")
			return
		}
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, c)
}

type updatePositionRequest struct {
	SceneID int     `json:"scene_id"`
	PosX    float64 `json:"pos_x"`
	PosY    float64 `json:"pos_y"`
	PosZ    float64 `json:"pos_z"`
}

func (h *MMOHandler) handleUpdatePosition(w http.ResponseWriter, r *http.Request, id string) {
	var req updatePositionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	// Ownership check (2026-08-02, GoblinFoxDragon "unify the whole bitch" -- Town's own GUI
	// client now calls this endpoint directly with a real PLAYER's own JWT, the first caller of
	// this handler that isn't a trusted M2M backend (apps2/mud's own agent JWT, still exempted
	// below via the agent_name claim only agent tokens carry, see AgentAuthHandler). Before this,
	// RequireAuth's "any valid JWT" was enough to move ANY character -- harmless while only a
	// trusted backend service ever reached this handler, not harmless once a compiled client
	// running on a player's own machine can reach it too.
	if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
		if _, isAgent := claims["agent_name"]; !isAgent {
			sub, _ := claims["sub"].(string)
			var ownerPlayerID string
			if err := h.DB.QueryRowContext(r.Context(),
				`SELECT player_id FROM characters WHERE character_id=?`, id,
			).Scan(&ownerPlayerID); err != nil {
				mmoWriteError(w, http.StatusNotFound, "character not found")
				return
			}
			if sub == "" || sub != ownerPlayerID {
				mmoWriteError(w, http.StatusForbidden, "not your character")
				return
			}
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	res, err := h.DB.ExecContext(r.Context(),
		`UPDATE characters SET scene_id=?, pos_x=?, pos_y=?, pos_z=?, updated_at=?
		 WHERE character_id=?`,
		req.SceneID, req.PosX, req.PosY, req.PosZ, now, id,
	)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		mmoWriteError(w, http.StatusNotFound, "character not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type updateHomeRequest struct {
	SceneID int     `json:"scene_id"`
	PosX    float64 `json:"pos_x"`
	PosY    float64 `json:"pos_y"`
	PosZ    float64 `json:"pos_z"`
}

// handleUpdateHome persists a character's real Home Point (2026-08-04, founder: iterate --
// following up on a real gap found live earlier the same day: `sethome` only ever mutated the
// in-memory homePoint struct in apps2/mud, never IDUNA, so a custom Home Point silently reverted
// to unset on every fresh session -- idle eviction or a service restart. Same shape as
// handleUpdatePosition, no ownership check needed beyond RequireAuth since only apps2/mud's own
// M2M agent JWT ever calls this (mirrors UpdatePosition's own real caller, not a player-facing
// client route).
func (h *MMOHandler) handleUpdateHome(w http.ResponseWriter, r *http.Request, id string) {
	var req updateHomeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := h.DB.ExecContext(r.Context(),
		`UPDATE characters SET home_scene_id=?, home_pos_x=?, home_pos_y=?, home_pos_z=?, updated_at=?
		 WHERE character_id=?`,
		req.SceneID, req.PosX, req.PosY, req.PosZ, now, id,
	)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		mmoWriteError(w, http.StatusNotFound, "character not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type updateLevelRequest struct {
	Level     int `json:"level"`
	CurrentXP int `json:"current_xp"`
}

type updateJobRequest struct {
	JobMain string `json:"job_main"`
	JobSub  string `json:"job_sub"`
}

// handleUpdateLevel persists a character's level/current_xp -- the real route
// idunaclient.UpdateCharacterLevel has always called (see this route's own registration comment
// in routeCharacters for the bug this closes). Agent-only, unlike handleUpdatePosition's
// player-self-update allowance: a player's own client legitimately needs to report its own
// position, but self-reporting your own level/XP is a cheat vector no client should ever be
// trusted with -- only apps2/mud's own agent JWT (the sole real authority on how much XP a
// character actually earned) may call this.
func (h *MMOHandler) handleUpdateLevel(w http.ResponseWriter, r *http.Request, id string) {
	if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
		if _, isAgent := claims["agent_name"]; !isAgent {
			mmoWriteError(w, http.StatusForbidden, "level updates are agent-only")
			return
		}
	}

	var req updateLevelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Level < 1 {
		mmoWriteError(w, http.StatusBadRequest, "level must be >= 1")
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	res, err := h.DB.ExecContext(r.Context(),
		`UPDATE characters SET level=?, current_xp=?, updated_at=? WHERE character_id=?`,
		req.Level, req.CurrentXP, now, id,
	)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		mmoWriteError(w, http.StatusNotFound, "character not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleUpdateJob persists a character's real job_main/job_sub -- real gap found live 2026-08-05
// diagnosing "1/2/3 ability hotkeys don't match my real spells in Meadow": apps2/mud's cmdSetJob
// only ever mutated the in-memory session's p.jobID, never wrote job_main back here, so
// town_fetch_character (battlegrounds_gui, called once per Town entry) kept reading whatever job
// the character was created with -- any relaunch or reconnect silently reverted the ability bar to
// the character's original job. Agent-only, same reasoning as handleUpdateLevel: a client
// self-reporting its own job would let a WAR client claim BLM's spell access. JobSub optional
// (empty string clears it, matching cmdSetSubJob's own "NONE" semantics) since both live on one
// row and this closes the same persistence gap for both in one endpoint rather than two.
func (h *MMOHandler) handleUpdateJob(w http.ResponseWriter, r *http.Request, id string) {
	if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
		if _, isAgent := claims["agent_name"]; !isAgent {
			mmoWriteError(w, http.StatusForbidden, "job updates are agent-only")
			return
		}
	}

	var req updateJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.JobMain == "" {
		mmoWriteError(w, http.StatusBadRequest, "job_main required")
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	res, err := h.DB.ExecContext(r.Context(),
		`UPDATE characters SET job_main=?, job_sub=?, updated_at=? WHERE character_id=?`,
		req.JobMain, req.JobSub, now, id,
	)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		mmoWriteError(w, http.StatusNotFound, "character not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeductGold atomically deducts gold from a character's balance.
// Payload: {"deduct": N}. Returns 409 Conflict if insufficient gold.
func (h *MMOHandler) handleDeductGold(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		Deduct int `json:"deduct"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Deduct <= 0 {
		mmoWriteError(w, http.StatusBadRequest, "deduct must be positive")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	// Atomic conditional update: only succeeds if gold_balance >= deduct
	res, err := h.DB.ExecContext(r.Context(),
		`UPDATE characters SET gold_balance = gold_balance - ?, updated_at = ?
		 WHERE character_id = ? AND gold_balance >= ?`,
		req.Deduct, now, id, req.Deduct,
	)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// Could be not found OR insufficient gold; check existence
		var exists int
		h.DB.QueryRowContext(r.Context(), `SELECT 1 FROM characters WHERE character_id=?`, id).Scan(&exists)
		if exists == 0 {
			mmoWriteError(w, http.StatusNotFound, "character not found")
		} else {
			mmoWriteError(w, http.StatusConflict, "insufficient gold balance")
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// maxGoldCreditPerCall bounds a single credit-gold call. Unlike handleDeductGold (bounded
// naturally by the character's own existing balance -- you can never spend more than you have),
// a credit endpoint has no such natural ceiling, so a single malformed or malicious call could
// otherwise mint an unbounded amount. 10,000 is a soft, generous sanity bound (20x a fresh
// character's starting 500-gold balance per apps2/mud's own real starting value) -- real
// economy-wide rate limiting or per-source auditing is a separate, larger concern not solved by
// this one constant.
const maxGoldCreditPerCall = 10000

// handleCreditGold atomically adds gold to a character's balance -- the symmetric counterpart
// handleDeductGold above never had. Payload: {"credit": N}.
func (h *MMOHandler) handleCreditGold(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		Credit int `json:"credit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Credit <= 0 {
		mmoWriteError(w, http.StatusBadRequest, "credit must be positive")
		return
	}
	if req.Credit > maxGoldCreditPerCall {
		mmoWriteError(w, http.StatusBadRequest, "credit exceeds per-call maximum")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := h.DB.ExecContext(r.Context(),
		`UPDATE characters SET gold_balance = gold_balance + ?, updated_at = ?
		 WHERE character_id = ?`,
		req.Credit, now, id,
	)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		mmoWriteError(w, http.StatusNotFound, "character not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleIncrementSkill patches a character's skill value by delta (PATCH /api/v1/characters/:id/skills).
// Payload: {"skill_name":"...","delta":N}. Capped at 110.0 (SkillCap).
func (h *MMOHandler) handleIncrementSkill(w http.ResponseWriter, r *http.Request, characterID string) {
	var req struct {
		SkillName string  `json:"skill_name"`
		Delta     float64 `json:"delta"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.SkillName == "" || req.Delta <= 0 {
		mmoWriteError(w, http.StatusBadRequest, "skill_name and positive delta required")
		return
	}
	const skillCap = 110.0
	now := time.Now().UTC().Format(time.RFC3339)
	// UPSERT: insert or add delta, capped at skillCap.
	_, err := h.DB.ExecContext(r.Context(), `
		INSERT INTO character_skills (character_id, skill_name, value, updated_at)
		VALUES (?, ?, MIN(?, ?), ?)
		ON CONFLICT(character_id, skill_name) DO UPDATE SET
			value = MIN(character_skills.value + excluded.value, ?),
			updated_at = excluded.updated_at`,
		characterID, req.SkillName, req.Delta, skillCap, now, skillCap)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleGetSkills returns all skills for a character (GET /api/v1/characters/:id/skills).
func (h *MMOHandler) handleGetSkills(w http.ResponseWriter, r *http.Request, characterID string) {
	rows, err := h.DB.QueryContext(r.Context(),
		`SELECT skill_name, value, updated_at FROM character_skills WHERE character_id=?`, characterID)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	type skillRow struct {
		SkillName string  `json:"skill_name"`
		Value     float64 `json:"value"`
		UpdatedAt string  `json:"updated_at"`
	}
	var skills []skillRow
	for rows.Next() {
		var s skillRow
		rows.Scan(&s.SkillName, &s.Value, &s.UpdatedAt)
		skills = append(skills, s)
	}
	if skills == nil {
		skills = []skillRow{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"skills": skills})
}

// handleListCharacterItems returns all non-destroyed items owned by character_id.
func (h *MMOHandler) handleListCharacterItems(w http.ResponseWriter, r *http.Request, characterID string) {
	rows, err := h.DB.QueryContext(r.Context(),
		`SELECT item_id, item_type, name, item_level, quantity, provenance_chain, created_at
		 FROM items WHERE owner_character_id=? AND destroyed_at IS NULL
		 ORDER BY created_at ASC`, characterID)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	type itemRow struct {
		ItemID          string          `json:"item_id"`
		ItemType        string          `json:"item_type"`
		Name            string          `json:"name"`
		ItemLevel       int             `json:"item_level"`
		Quantity        int             `json:"quantity"`
		ProvenanceChain json.RawMessage `json:"provenance_chain"`
		CreatedAt       string          `json:"created_at"`
	}
	var items []itemRow
	for rows.Next() {
		var it itemRow
		var chainStr string
		if err := rows.Scan(&it.ItemID, &it.ItemType, &it.Name, &it.ItemLevel, &it.Quantity, &chainStr, &it.CreatedAt); err != nil {
			continue
		}
		it.ProvenanceChain = json.RawMessage(chainStr)
		items = append(items, it)
	}
	if items == nil {
		items = []itemRow{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"items": items})
}

// handleGetInventory returns all bag slots for a character (GET /api/v1/characters/:id/inventory).
// Response: {"bags": {"inventory": [...], "storage": [...], "temporary": [...]}, "capacity": {"inventory": N, ...}}
func (h *MMOHandler) handleGetInventory(w http.ResponseWriter, r *http.Request, characterID string) {
	rows, err := h.DB.QueryContext(r.Context(),
		`SELECT bag, slot_index, item_id, def_id, quantity
		 FROM character_inventory WHERE character_id=? ORDER BY bag, slot_index`, characterID)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	type invSlot struct {
		SlotIndex int    `json:"slot_index"`
		ItemID    string `json:"item_id"`
		DefID     int    `json:"def_id"`
		Quantity  int    `json:"quantity"`
	}
	bags := map[string][]invSlot{
		"inventory": {},
		"storage":   {},
		"temporary": {},
	}
	for rows.Next() {
		var bag, itemID string
		var slotIndex, defID, qty int
		if err := rows.Scan(&bag, &slotIndex, &itemID, &defID, &qty); err != nil {
			continue
		}
		bags[bag] = append(bags[bag], invSlot{slotIndex, itemID, defID, qty})
	}

	capRows, err := h.DB.QueryContext(r.Context(),
		`SELECT bag, capacity FROM character_bag_capacity WHERE character_id=?`, characterID)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer capRows.Close()
	capacity := map[string]int{"inventory": 30, "storage": 0, "temporary": 0}
	for capRows.Next() {
		var bag string
		var cap int
		capRows.Scan(&bag, &cap)
		capacity[bag] = cap
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"bags": bags, "capacity": capacity})
}

// handleGetEquipment returns all equipped slots for a character (GET /api/v1/characters/:id/equipment).
// Response: {"equipment": [{"slot": "...", "item_id": "..."|null}, ...]}
func (h *MMOHandler) handleGetEquipment(w http.ResponseWriter, r *http.Request, characterID string) {
	rows, err := h.DB.QueryContext(r.Context(),
		`SELECT slot, COALESCE(item_id, '') FROM character_equipment WHERE character_id=? ORDER BY slot`,
		characterID)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	type eqSlot struct {
		Slot   string  `json:"slot"`
		ItemID *string `json:"item_id"`
	}
	var slots []eqSlot
	for rows.Next() {
		var slot, itemID string
		rows.Scan(&slot, &itemID)
		var itemPtr *string
		if itemID != "" {
			itemPtr = &itemID
		}
		slots = append(slots, eqSlot{slot, itemPtr})
	}
	if slots == nil {
		slots = []eqSlot{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"equipment": slots})
}

// ── Items (S75-03) ────────────────────────────────────────────────────────────

type createItemRequest struct {
	OwnerCharacterID string `json:"owner_character_id"`
	ItemType         string `json:"item_type"`
	Name             string `json:"name"`
	ItemLevel        int    `json:"item_level"`
	Quantity         int    `json:"quantity"`
	CrafterID        string `json:"crafter_id"` // actor for provenance[0]
}

type transferRequest struct {
	ToCharacterID string `json:"to_character_id"`
	ActorID       string `json:"actor_id"`
}

type provenanceEntry struct {
	ActorID string `json:"actor_id"`
	Action  string `json:"action"`
	At      string `json:"at"`
}

func (h *MMOHandler) routeItems(w http.ResponseWriter, r *http.Request, path string) {
	base := "/api/v1/items"
	tail := strings.TrimPrefix(path, base)
	tail = strings.Trim(tail, "/")
	parts := strings.SplitN(tail, "/", 2)

	if tail == "" || tail == "/" {
		if r.Method == http.MethodPost {
			h.handleCreateItem(w, r)
		} else {
			http.NotFound(w, r)
		}
		return
	}
	itemID := parts[0]
	if len(parts) == 2 {
		switch parts[1] {
		case "transfer":
			if r.Method == http.MethodPost {
				h.handleTransferItem(w, r, itemID)
			} else {
				http.NotFound(w, r)
			}
			return
		}
	}
	switch r.Method {
	case http.MethodGet:
		h.handleGetItem(w, r, itemID)
	case http.MethodDelete:
		h.handleDestroyItem(w, r, itemID)
	default:
		http.NotFound(w, r)
	}
}

func (h *MMOHandler) handleCreateItem(w http.ResponseWriter, r *http.Request) {
	var req createItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.OwnerCharacterID == "" || req.ItemType == "" || req.Name == "" {
		mmoWriteError(w, http.StatusBadRequest, "owner_character_id, item_type, name required")
		return
	}
	if req.Quantity < 1 {
		req.Quantity = 1
	}
	if req.CrafterID == "" {
		req.CrafterID = req.OwnerCharacterID
	}
	itemID := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)
	chain := []provenanceEntry{{ActorID: req.CrafterID, Action: "crafted", At: now}}
	chainJSON, _ := json.Marshal(chain)
	_, err := h.DB.ExecContext(r.Context(),
		`INSERT INTO items (item_id, owner_character_id, item_type, name, item_level, quantity, provenance_chain, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		itemID, req.OwnerCharacterID, req.ItemType, req.Name, req.ItemLevel, req.Quantity, string(chainJSON), now, now,
	)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"item_id": itemID})
}

func (h *MMOHandler) handleGetItem(w http.ResponseWriter, r *http.Request, id string) {
	row := h.DB.QueryRowContext(r.Context(),
		`SELECT item_id, owner_character_id, item_type, name, item_level, quantity,
		        provenance_chain, created_at, updated_at, COALESCE(destroyed_at,'')
		 FROM items WHERE item_id=?`, id)
	var itemID, ownerID, itemType, name, chain, createdAt, updatedAt, destroyedAt string
	var il, qty int
	if err := row.Scan(&itemID, &ownerID, &itemType, &name, &il, &qty, &chain, &createdAt, &updatedAt, &destroyedAt); err != nil {
		if err == sql.ErrNoRows {
			mmoWriteError(w, http.StatusNotFound, "item not found")
			return
		}
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := map[string]interface{}{
		"item_id": itemID, "owner_character_id": ownerID, "item_type": itemType,
		"name": name, "item_level": il, "quantity": qty,
		"provenance_chain": json.RawMessage(chain),
		"created_at":       createdAt, "updated_at": updatedAt,
	}
	if destroyedAt != "" {
		resp["destroyed_at"] = destroyedAt
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *MMOHandler) handleTransferItem(w http.ResponseWriter, r *http.Request, id string) {
	var req transferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	// Load existing provenance chain
	var chainJSON string
	err := h.DB.QueryRowContext(r.Context(), `SELECT provenance_chain FROM items WHERE item_id=? AND destroyed_at IS NULL`, id).Scan(&chainJSON)
	if err == sql.ErrNoRows {
		mmoWriteError(w, http.StatusNotFound, "item not found or destroyed")
		return
	}
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var chain []provenanceEntry
	json.Unmarshal([]byte(chainJSON), &chain)
	now := time.Now().UTC().Format(time.RFC3339)
	chain = append(chain, provenanceEntry{ActorID: req.ActorID, Action: "transferred", At: now})
	newChain, _ := json.Marshal(chain)
	_, err = h.DB.ExecContext(r.Context(),
		`UPDATE items SET owner_character_id=?, provenance_chain=?, updated_at=? WHERE item_id=?`,
		req.ToCharacterID, string(newChain), now, id)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *MMOHandler) handleDestroyItem(w http.ResponseWriter, r *http.Request, id string) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := h.DB.ExecContext(r.Context(),
		`UPDATE items SET destroyed_at=?, updated_at=? WHERE item_id=? AND destroyed_at IS NULL`,
		now, now, id)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		mmoWriteError(w, http.StatusNotFound, "item not found or already destroyed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Guilds (S75-04) ───────────────────────────────────────────────────────────

type createGuildRequest struct {
	Name      string `json:"name"`
	Tag       string `json:"tag"`
	FounderID string `json:"founder_id"` // character_id
}

type joinGuildRequest struct {
	CharacterID string `json:"character_id"`
}

type changeRoleRequest struct {
	Role string `json:"role"` // "leader","officer","member"
}

func (h *MMOHandler) routeGuilds(w http.ResponseWriter, r *http.Request, path string) {
	base := "/api/v1/guilds"
	tail := strings.Trim(strings.TrimPrefix(path, base), "/")
	parts := strings.SplitN(tail, "/", 3)

	if tail == "" {
		if r.Method == http.MethodPost {
			h.handleCreateGuild(w, r)
		} else {
			http.NotFound(w, r)
		}
		return
	}
	guildID := parts[0]
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			h.handleGetGuild(w, r, guildID)
		case http.MethodDelete:
			h.handleDisbandGuild(w, r, guildID)
		default:
			http.NotFound(w, r)
		}
		return
	}
	if parts[1] == "members" {
		if len(parts) == 2 {
			if r.Method == http.MethodPost {
				h.handleJoinGuild(w, r, guildID)
			} else {
				http.NotFound(w, r)
			}
			return
		}
		charID := parts[2]
		if r.Method == http.MethodPatch {
			h.handleChangeRole(w, r, guildID, charID)
		} else {
			http.NotFound(w, r)
		}
		return
	}
	http.NotFound(w, r)
}

func (h *MMOHandler) handleCreateGuild(w http.ResponseWriter, r *http.Request) {
	var req createGuildRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Name == "" || req.Tag == "" || req.FounderID == "" {
		mmoWriteError(w, http.StatusBadRequest, "name, tag, founder_id required")
		return
	}
	guildID := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)
	tx, _ := h.DB.BeginTx(r.Context(), nil)
	_, err := tx.ExecContext(r.Context(),
		`INSERT INTO guilds (guild_id, name, tag, founder_id, created_at) VALUES (?,?,?,?,?)`,
		guildID, req.Name, req.Tag, req.FounderID, now)
	if err != nil {
		tx.Rollback()
		if strings.Contains(err.Error(), "UNIQUE") {
			mmoWriteError(w, http.StatusConflict, "guild name or tag already taken")
			return
		}
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Founder is the leader member
	_, err = tx.ExecContext(r.Context(),
		`INSERT INTO guild_memberships (guild_id, character_id, role, joined_at) VALUES (?,?,?,?)`,
		guildID, req.FounderID, "leader", now)
	if err != nil {
		tx.Rollback()
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	tx.Commit()
	writeJSON(w, http.StatusCreated, map[string]string{"guild_id": guildID})
}

func (h *MMOHandler) handleGetGuild(w http.ResponseWriter, r *http.Request, id string) {
	row := h.DB.QueryRowContext(r.Context(),
		`SELECT guild_id, name, tag, founder_id, created_at, COALESCE(disbanded_at,'')
		 FROM guilds WHERE guild_id=?`, id)
	var guildID, name, tag, founderID, createdAt, disbandedAt string
	if err := row.Scan(&guildID, &name, &tag, &founderID, &createdAt, &disbandedAt); err != nil {
		if err == sql.ErrNoRows {
			mmoWriteError(w, http.StatusNotFound, "guild not found")
			return
		}
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rows, err := h.DB.QueryContext(r.Context(),
		`SELECT character_id, role, joined_at FROM guild_memberships WHERE guild_id=? AND left_at IS NULL`, id)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	members := []map[string]string{}
	for rows.Next() {
		var cid, role, joinedAt string
		rows.Scan(&cid, &role, &joinedAt)
		members = append(members, map[string]string{"character_id": cid, "role": role, "joined_at": joinedAt})
	}
	resp := map[string]interface{}{
		"guild_id": guildID, "name": name, "tag": tag, "founder_id": founderID,
		"created_at": createdAt, "members": members,
	}
	if disbandedAt != "" {
		resp["disbanded_at"] = disbandedAt
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *MMOHandler) handleJoinGuild(w http.ResponseWriter, r *http.Request, guildID string) {
	var req joinGuildRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.CharacterID == "" {
		mmoWriteError(w, http.StatusBadRequest, "character_id required")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := h.DB.ExecContext(r.Context(),
		`INSERT INTO guild_memberships (guild_id, character_id, role, joined_at) VALUES (?,?,'member',?)`,
		guildID, req.CharacterID, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			mmoWriteError(w, http.StatusConflict, "character already in guild")
			return
		}
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *MMOHandler) handleChangeRole(w http.ResponseWriter, r *http.Request, guildID, charID string) {
	var req changeRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	res, err := h.DB.ExecContext(r.Context(),
		`UPDATE guild_memberships SET role=? WHERE guild_id=? AND character_id=? AND left_at IS NULL`,
		req.Role, guildID, charID)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		mmoWriteError(w, http.StatusNotFound, "membership not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *MMOHandler) handleDisbandGuild(w http.ResponseWriter, r *http.Request, id string) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := h.DB.ExecContext(r.Context(),
		`UPDATE guilds SET disbanded_at=? WHERE guild_id=? AND disbanded_at IS NULL`, now, id)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		mmoWriteError(w, http.StatusNotFound, "guild not found or already disbanded")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── World Events (S75-05) ──────────────────────────────────────────────────────

type createWorldEventRequest struct {
	EventType string `json:"event_type"` // "world_crisis"
	SceneID   int    `json:"scene_id"`
}

type patchWorldEventRequest struct {
	Phase        string `json:"phase"`
	LeyIntegrity int    `json:"ley_integrity"`
}

type resolveWorldEventRequest struct {
	Outcome string `json:"outcome"`
}

func (h *MMOHandler) routeWorldEvents(w http.ResponseWriter, r *http.Request, path string) {
	base := "/api/v1/world-events"
	tail := strings.Trim(strings.TrimPrefix(path, base), "/")
	parts := strings.SplitN(tail, "/", 2)

	if tail == "" {
		if r.Method == http.MethodPost {
			h.handleCreateWorldEvent(w, r)
		} else {
			http.NotFound(w, r)
		}
		return
	}
	eventID := parts[0]
	if len(parts) == 2 && parts[1] == "resolve" {
		if r.Method == http.MethodPost {
			h.handleResolveWorldEvent(w, r, eventID)
		} else {
			http.NotFound(w, r)
		}
		return
	}
	if r.Method == http.MethodPatch {
		h.handlePatchWorldEvent(w, r, eventID)
	} else if r.Method == http.MethodGet {
		h.handleGetWorldEvent(w, r, eventID)
	} else {
		http.NotFound(w, r)
	}
}

func (h *MMOHandler) handleCreateWorldEvent(w http.ResponseWriter, r *http.Request) {
	var req createWorldEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.EventType == "" {
		mmoWriteError(w, http.StatusBadRequest, "event_type required")
		return
	}
	eventID := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := h.DB.ExecContext(r.Context(),
		`INSERT INTO world_events (event_id, event_type, scene_id, phase, ley_integrity, started_at)
		 VALUES (?,?,?,'opening',100,?)`,
		eventID, req.EventType, req.SceneID, now)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"event_id": eventID})
}

func (h *MMOHandler) handleGetWorldEvent(w http.ResponseWriter, r *http.Request, id string) {
	row := h.DB.QueryRowContext(r.Context(),
		`SELECT event_id, event_type, scene_id, phase, ley_integrity, started_at,
		        COALESCE(resolved_at,''), COALESCE(outcome,'')
		 FROM world_events WHERE event_id=?`, id)
	var eventID, eventType, phase, startedAt, resolvedAt, outcome string
	var sceneID, ley int
	if err := row.Scan(&eventID, &eventType, &sceneID, &phase, &ley, &startedAt, &resolvedAt, &outcome); err != nil {
		if err == sql.ErrNoRows {
			mmoWriteError(w, http.StatusNotFound, "world event not found")
			return
		}
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := map[string]interface{}{
		"event_id": eventID, "event_type": eventType, "scene_id": sceneID,
		"phase": phase, "ley_integrity": ley, "started_at": startedAt,
	}
	if resolvedAt != "" {
		resp["resolved_at"] = resolvedAt
		resp["outcome"] = outcome
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *MMOHandler) handlePatchWorldEvent(w http.ResponseWriter, r *http.Request, id string) {
	var req patchWorldEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := h.DB.ExecContext(r.Context(),
		`UPDATE world_events SET phase=?, ley_integrity=?, started_at=?
		 WHERE event_id=? AND resolved_at IS NULL`,
		req.Phase, req.LeyIntegrity, now, id)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		mmoWriteError(w, http.StatusNotFound, "world event not found or already resolved")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *MMOHandler) handleResolveWorldEvent(w http.ResponseWriter, r *http.Request, id string) {
	var req resolveWorldEventRequest
	json.NewDecoder(r.Body).Decode(&req)
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := h.DB.ExecContext(r.Context(),
		`UPDATE world_events SET resolved_at=?, outcome=? WHERE event_id=? AND resolved_at IS NULL`,
		now, req.Outcome, id)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		mmoWriteError(w, http.StatusNotFound, "world event not found or already resolved")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Field Office district overlay (S127-05) ───────────────────────────────────
//
// Routes:
//   GET   /api/v1/fieldoffices         — list all known FO snapshots
//   PATCH /api/v1/fieldoffices/:id     — game server pushes live state (M2M)
//
// State is ephemeral (in-memory); the game server patches on every Tick.

// FOSnapshot is a point-in-time view of one Field Office pushed by the game server.
type FOSnapshot struct {
	ID           string    `json:"id"`
	DistrictName string    `json:"district_name"`
	Phase        string    `json:"phase"`     // "Unclaimed"|"Held"|"Contested"|"Containment"
	HolderID     string    `json:"holder_id"` // crew ID or ""
	Alertness    float64   `json:"alertness"` // 0.0–1.0
	UpdatedAt    time.Time `json:"updated_at"`
}

// foStore is the shared in-memory map of FO snapshots, keyed by FO ID.
var foStore struct {
	mu  sync.RWMutex
	fos map[string]FOSnapshot
}

func init() {
	foStore.fos = make(map[string]FOSnapshot)
}

func (h *MMOHandler) routeFieldOffices(w http.ResponseWriter, r *http.Request, path string) {
	switch r.Method {
	case http.MethodGet:
		// List all FO snapshots.
		foStore.mu.RLock()
		out := make([]FOSnapshot, 0, len(foStore.fos))
		for _, fo := range foStore.fos {
			out = append(out, fo)
		}
		foStore.mu.RUnlock()
		writeJSON(w, http.StatusOK, map[string]interface{}{"fieldoffices": out})

	case http.MethodPatch:
		// Game server posts live FO state. Path: /api/v1/fieldoffices/:id
		foID := strings.TrimPrefix(path, "/api/v1/fieldoffices/")
		if foID == "" || foID == "fieldoffices" {
			mmoWriteError(w, http.StatusBadRequest, "missing FO id")
			return
		}
		var req FOSnapshot
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		req.ID = foID
		req.UpdatedAt = time.Now().UTC()
		foStore.mu.Lock()
		foStore.fos[foID] = req
		foStore.mu.Unlock()
		writeJSON(w, http.StatusOK, req)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func extractSegment(path, prefix, suffix string) string {
	s := strings.TrimPrefix(path, prefix)
	return strings.TrimSuffix(s, suffix)
}

func mmoWriteError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
