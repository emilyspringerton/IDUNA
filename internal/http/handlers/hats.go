package handlers

// hats.go — WOTAN_HAT_STORE_NORTHSTAR.md Phase 1 ("real hat catalog + inventory data model").
// Real, decisive finding that changes that doc's own Phase 0 status: a real, external Flow
// balance-query + spend API already exists and did not need to be built here --
// GET /api/v1/characters/by-player/:player_id already returns gold_balance (GFD's own Flow,
// synced from apps2/mud/main.go's runHeadlessCommand via CreditGold/DeductGold), and the
// existing PATCH /api/v1/characters/:id/gold already spends it atomically (409 if insufficient).
// This file adds the genuinely new piece: the hat catalog itself, plus buy/equip endpoints that
// reuse that exact same atomic-conditional-UPDATE pattern handleDeductGold already established.
//
// Routes (all require M2M or user JWT, matching every other MMO endpoint in this package):
//   GET   /api/v1/hats                        — full catalog
//   GET   /api/v1/characters/:id/hats         — a character's owned hats
//   POST  /api/v1/characters/:id/hats/buy     — buy a hat by hat_id (body); deducts Flow atomically
//   PATCH /api/v1/characters/:id/hats/equip   — equip an owned hat by hat_id (body); un-equips any other

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"iduna/internal/http/middleware"
)

type hatResponse struct {
	HatID       string `json:"hat_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	FlowCost    int    `json:"flow_cost"`
	ImageAsset  string `json:"image_asset"`
}

type ownedHatResponse struct {
	HatID       string `json:"hat_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ImageAsset  string `json:"image_asset"`
	AcquiredAt  string `json:"acquired_at"`
	Equipped    bool   `json:"equipped"`
}

// routeHats handles the top-level /api/v1/hats prefix (the catalog only -- ownership/buy/equip
// live under /api/v1/characters/:id/hats..., handled by routeCharacters, matching how
// inventory/equipment already nest under characters rather than getting their own top-level
// prefix).
func (h *MMOHandler) routeHats(w http.ResponseWriter, r *http.Request, path string) {
	if r.Method == http.MethodGet && (path == "/api/v1/hats" || path == "/api/v1/hats/") {
		h.handleListHatCatalog(w, r)
		return
	}
	http.NotFound(w, r)
}

func (h *MMOHandler) handleListHatCatalog(w http.ResponseWriter, r *http.Request) {
	rows, err := h.DB.QueryContext(r.Context(),
		`SELECT hat_id, name, description, flow_cost, image_asset FROM hats ORDER BY flow_cost ASC`)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	out := []hatResponse{}
	for rows.Next() {
		var hat hatResponse
		if err := rows.Scan(&hat.HatID, &hat.Name, &hat.Description, &hat.FlowCost, &hat.ImageAsset); err != nil {
			mmoWriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, hat)
	}
	writeJSON(w, http.StatusOK, out)
}

// checkHatOwnership: real, found-live security gap fix (2026-09-04, WOTAN Phase 2 prep) --
// handleBuyHat/handleEquipHat had NO ownership check at all despite living behind the exact
// same "any valid player JWT" RequireAuth every other MMO route uses, unlike
// handleUpdatePosition's own already-established "not your character" check just above this
// file's sibling in mmo.go. Before this fix, ANY authenticated player (any real JWT from
// /api/v1/auth/email/login) could POST .../hats/buy against a completely different player's own
// character_id and spend THEIR Flow, or equip a hat on someone else's character -- silent
// because nothing in this repo's test suite or WOTAN's own not-yet-built Phase 2 page had ever
// driven this endpoint with a real player (non-agent) JWT before. Matches
// handleUpdatePosition's own pattern exactly: M2M agent tokens (carry an `agent_name` claim)
// stay exempt, since apps2/mud's own real GFD-side Flow sync legitimately acts on any
// character's behalf.
func checkHatOwnership(h *MMOHandler, r *http.Request, characterID string) (ok bool, status int, msg string) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		return true, 0, ""
	}
	if _, isAgent := claims["agent_name"]; isAgent {
		return true, 0, ""
	}
	sub, _ := claims["sub"].(string)
	var ownerPlayerID string
	if err := h.DB.QueryRowContext(r.Context(),
		`SELECT player_id FROM characters WHERE character_id=?`, characterID,
	).Scan(&ownerPlayerID); err != nil {
		return false, http.StatusNotFound, "character not found"
	}
	if sub == "" || sub != ownerPlayerID {
		return false, http.StatusForbidden, "not your character"
	}
	return true, 0, ""
}

func (h *MMOHandler) handleListCharacterHats(w http.ResponseWriter, r *http.Request, characterID string) {
	rows, err := h.DB.QueryContext(r.Context(),
		`SELECT ch.hat_id, hats.name, hats.description, hats.image_asset, ch.acquired_at, ch.equipped
		 FROM character_hats ch JOIN hats ON hats.hat_id = ch.hat_id
		 WHERE ch.character_id = ? ORDER BY ch.acquired_at ASC`, characterID)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	out := []ownedHatResponse{}
	for rows.Next() {
		var oh ownedHatResponse
		var equipped int
		if err := rows.Scan(&oh.HatID, &oh.Name, &oh.Description, &oh.ImageAsset, &oh.AcquiredAt, &equipped); err != nil {
			mmoWriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		oh.Equipped = equipped != 0
		out = append(out, oh)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleBuyHat: real, atomic buy -- deducts Flow via the exact same conditional-UPDATE pattern
// handleDeductGold already uses (only succeeds if gold_balance >= flow_cost), then records
// ownership, both inside one real DB transaction so a crash between the two steps can never
// leave Flow spent with no hat granted (or vice versa).
func (h *MMOHandler) handleBuyHat(w http.ResponseWriter, r *http.Request, characterID string) {
	if ok, status, msg := checkHatOwnership(h, r, characterID); !ok {
		mmoWriteError(w, status, msg)
		return
	}

	var req struct {
		HatID string `json:"hat_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.HatID == "" {
		mmoWriteError(w, http.StatusBadRequest, "hat_id required")
		return
	}

	var flowCost int
	if err := h.DB.QueryRowContext(r.Context(),
		`SELECT flow_cost FROM hats WHERE hat_id = ?`, req.HatID).Scan(&flowCost); err != nil {
		if err == sql.ErrNoRows {
			mmoWriteError(w, http.StatusNotFound, "hat not found")
			return
		}
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)
	res, err := tx.ExecContext(r.Context(),
		`UPDATE characters SET gold_balance = gold_balance - ?, updated_at = ?
		 WHERE character_id = ? AND gold_balance >= ?`,
		flowCost, now, characterID, flowCost,
	)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		var exists int
		// Real bug found live (test-driven, mirroring the same class of gotcha
		// handleDeductGold itself never had to face, since it has no transaction to stay on):
		// this existence-check must query through `tx`, not `h.DB` -- a plain pool query can
		// land on a different underlying connection than the one the open transaction pinned,
		// which for a `:memory:` SQLite test DB means a completely separate, empty database.
		tx.QueryRowContext(r.Context(), `SELECT 1 FROM characters WHERE character_id=?`, characterID).Scan(&exists)
		if exists == 0 {
			mmoWriteError(w, http.StatusNotFound, "character not found")
		} else {
			mmoWriteError(w, http.StatusConflict, "insufficient Flow balance")
		}
		return
	}

	if _, err := tx.ExecContext(r.Context(),
		`INSERT INTO character_hats (character_id, hat_id, acquired_at, equipped) VALUES (?, ?, ?, 0)`,
		characterID, req.HatID, now,
	); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "PRIMARY") {
			mmoWriteError(w, http.StatusConflict, "hat already owned")
			return
		}
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := tx.Commit(); err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleEquipHat: sets exactly one hat equipped per character (real, deliberate v0 -- no
// multi-hat layering, see the migration's own doc comment). Un-equips every other owned hat
// first, inside one transaction, so a crash mid-request can't leave two hats equipped at once.
func (h *MMOHandler) handleEquipHat(w http.ResponseWriter, r *http.Request, characterID string) {
	if ok, status, msg := checkHatOwnership(h, r, characterID); !ok {
		mmoWriteError(w, status, msg)
		return
	}

	var req struct {
		HatID string `json:"hat_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.HatID == "" {
		mmoWriteError(w, http.StatusBadRequest, "hat_id required")
		return
	}

	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(r.Context(),
		`UPDATE character_hats SET equipped = 0 WHERE character_id = ?`, characterID,
	); err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	res, err := tx.ExecContext(r.Context(),
		`UPDATE character_hats SET equipped = 1 WHERE character_id = ? AND hat_id = ?`,
		characterID, req.HatID,
	)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		mmoWriteError(w, http.StatusNotFound, "character does not own this hat")
		return
	}
	if err := tx.Commit(); err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
