package handlers

// Secondary market for limited-edition hats (WOTAN hat store). Routes:
//
//   GET  /api/v1/hats/market                              -- active listings, cheapest first
//   POST /api/v1/characters/:id/hats/market/list          -- {hat_id, price}
//   POST /api/v1/characters/:id/hats/market/cancel        -- {listing_id}
//   POST /api/v1/characters/:id/hats/market/buy           -- {listing_id}
//
// Only limited-edition hats (hats.quantity_limit set) can be listed. A sale moves the
// character_hats row to the buyer; 8% of the price is a royalty to the hat's designer, 4% is
// burned, and the seller keeps the rest (88%).

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	// Resale split: fixed rates. Any future change to the burn is capped at HatMarketBurnCeilingPct.
	HatMarketRoyaltyPct     = 8
	HatMarketBurnPct        = 4
	HatMarketBurnCeilingPct = 25
)

// splitMarketPrice returns (seller, royalty, burn). Each share rounds down and the odd remainder
// is burned. With no designer to pay, the royalty share is burned too.
func splitMarketPrice(price int, hasCreator bool) (seller, royalty, burn int) {
	seller = price * (100 - HatMarketRoyaltyPct - HatMarketBurnPct) / 100
	if hasCreator {
		royalty = price * HatMarketRoyaltyPct / 100
	}
	return seller, royalty, price - seller - royalty
}

type marketListing struct {
	ListingID         string `json:"listing_id"`
	HatID             string `json:"hat_id"`
	Name              string `json:"name"`
	Description       string `json:"description"`
	ImageAsset        string `json:"image_asset"`
	ImageURL          string `json:"image_url,omitempty"`
	Creator           string `json:"creator,omitempty"`
	QuantityLimit     int    `json:"quantity_limit"`
	SellerCharacterID string `json:"seller_character_id"`
	SellerName        string `json:"seller_name"`
	Price             int    `json:"price"`
	CreatedAt         string `json:"created_at"`
}

func (h *MMOHandler) handleHatMarket(w http.ResponseWriter, r *http.Request) {
	rows, err := h.DB.QueryContext(r.Context(),
		`SELECT l.listing_id, l.hat_id, hats.name, hats.description, hats.image_asset,
		        CASE WHEN hats.png_data IS NULL THEN 0 ELSE 1 END, COALESCE(cr.name, ''),
		        COALESCE(hats.quantity_limit, 0), l.seller_character_id, COALESCE(s.name, ''), l.price, l.created_at
		 FROM hat_listings l
		 JOIN hats ON hats.hat_id = l.hat_id
		 LEFT JOIN characters s ON s.character_id = l.seller_character_id
		 LEFT JOIN characters cr ON cr.character_id = hats.generated_by_character_id
		 WHERE l.status = 'active' AND hats.status <> 'removed'
		 ORDER BY l.price ASC, l.created_at ASC`)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []marketListing{}
	for rows.Next() {
		var m marketListing
		var hasPNG int
		if err := rows.Scan(&m.ListingID, &m.HatID, &m.Name, &m.Description, &m.ImageAsset, &hasPNG, &m.Creator,
			&m.QuantityLimit, &m.SellerCharacterID, &m.SellerName, &m.Price, &m.CreatedAt); err != nil {
			mmoWriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if hasPNG != 0 {
			m.ImageURL = "/api/v1/hats/" + m.HatID + "/image"
		} else {
			m.Creator = ""
		}
		out = append(out, m)
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *MMOHandler) handleMarketList(w http.ResponseWriter, r *http.Request, characterID string) {
	if ok, status, msg := checkHatOwnership(h, r, characterID); !ok {
		mmoWriteError(w, status, msg)
		return
	}
	var req struct {
		HatID string `json:"hat_id"`
		Price int    `json:"price"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.HatID == "" {
		mmoWriteError(w, http.StatusBadRequest, "hat_id and price required")
		return
	}
	if req.Price < hatMinPrice || req.Price > hatMaxPrice {
		mmoWriteError(w, http.StatusBadRequest, "price must be between 10 and 100000 Flow")
		return
	}
	var limit sql.NullInt64
	if err := h.DB.QueryRowContext(r.Context(), `SELECT quantity_limit FROM hats WHERE hat_id=?`, req.HatID).Scan(&limit); err != nil {
		mmoWriteError(w, http.StatusNotFound, "hat not found")
		return
	}
	if !limit.Valid {
		mmoWriteError(w, http.StatusConflict, "only limited-edition hats can be sold on the secondary market")
		return
	}
	var one int
	if err := h.DB.QueryRowContext(r.Context(),
		`SELECT 1 FROM character_hats WHERE character_id=? AND hat_id=?`, characterID, req.HatID).Scan(&one); err != nil {
		mmoWriteError(w, http.StatusNotFound, "character does not own this hat")
		return
	}
	if err := h.DB.QueryRowContext(r.Context(),
		`SELECT 1 FROM hat_listings WHERE seller_character_id=? AND hat_id=? AND status='active'`, characterID, req.HatID).Scan(&one); err == nil {
		mmoWriteError(w, http.StatusConflict, "you already have this hat listed -- cancel it to change the price")
		return
	}
	id := uuid.New().String()
	if _, err := h.DB.ExecContext(r.Context(),
		`INSERT INTO hat_listings (listing_id, hat_id, seller_character_id, price, status, created_at) VALUES (?, ?, ?, ?, 'active', ?)`,
		id, req.HatID, characterID, req.Price, time.Now().UTC().Format(time.RFC3339)); err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"listing_id": id, "hat_id": req.HatID, "price": req.Price})
}

func (h *MMOHandler) handleMarketCancel(w http.ResponseWriter, r *http.Request, characterID string) {
	if ok, status, msg := checkHatOwnership(h, r, characterID); !ok {
		mmoWriteError(w, status, msg)
		return
	}
	var req struct {
		ListingID string `json:"listing_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ListingID == "" {
		mmoWriteError(w, http.StatusBadRequest, "listing_id required")
		return
	}
	res, err := h.DB.ExecContext(r.Context(),
		`UPDATE hat_listings SET status='cancelled' WHERE listing_id=? AND seller_character_id=? AND status='active'`,
		req.ListingID, characterID)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		mmoWriteError(w, http.StatusNotFound, "no active listing of yours with that id")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *MMOHandler) handleMarketBuy(w http.ResponseWriter, r *http.Request, characterID string) {
	if ok, status, msg := checkHatOwnership(h, r, characterID); !ok {
		mmoWriteError(w, status, msg)
		return
	}
	var req struct {
		ListingID string `json:"listing_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ListingID == "" {
		mmoWriteError(w, http.StatusBadRequest, "listing_id required")
		return
	}
	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback()

	var hatID, sellerID string
	var price int
	if err := tx.QueryRowContext(r.Context(),
		`SELECT hat_id, seller_character_id, price FROM hat_listings WHERE listing_id=? AND status='active'`,
		req.ListingID).Scan(&hatID, &sellerID, &price); err != nil {
		mmoWriteError(w, http.StatusNotFound, "listing not found or no longer available")
		return
	}
	if sellerID == characterID {
		mmoWriteError(w, http.StatusConflict, "you cannot buy your own listing")
		return
	}
	var one int
	if err := tx.QueryRowContext(r.Context(), `SELECT 1 FROM character_hats WHERE character_id=? AND hat_id=?`, characterID, hatID).Scan(&one); err == nil {
		mmoWriteError(w, http.StatusConflict, "hat already owned")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	// Claim the listing first: only one buyer can flip it from active to sold.
	res, err := tx.ExecContext(r.Context(),
		`UPDATE hat_listings SET status='sold', buyer_character_id=?, sold_at=? WHERE listing_id=? AND status='active'`,
		characterID, now, req.ListingID)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		mmoWriteError(w, http.StatusNotFound, "listing not found or no longer available")
		return
	}
	// The seller must still own the hat (it is deleted from them below).
	res, err = tx.ExecContext(r.Context(), `DELETE FROM character_hats WHERE character_id=? AND hat_id=?`, sellerID, hatID)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		mmoWriteError(w, http.StatusGone, "seller no longer owns this hat")
		return
	}
	res, err = tx.ExecContext(r.Context(),
		`UPDATE characters SET gold_balance = gold_balance - ?, updated_at = ? WHERE character_id = ? AND gold_balance >= ?`,
		price, now, characterID, price)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		mmoWriteError(w, http.StatusConflict, "insufficient Flow balance")
		return
	}
	var creatorID sql.NullString
	tx.QueryRowContext(r.Context(), `SELECT generated_by_character_id FROM hats WHERE hat_id=? AND user_generated=1`, hatID).Scan(&creatorID)
	hasCreator := false
	if creatorID.Valid {
		if err := tx.QueryRowContext(r.Context(), `SELECT 1 FROM characters WHERE character_id=?`, creatorID.String).Scan(&one); err == nil {
			hasCreator = true
		}
	}
	sellerAmt, royalty, burn := splitMarketPrice(price, hasCreator)
	if hasCreator && royalty > 0 {
		// If the designer is the seller, both credits land on the same character: that is correct.
		if _, err := tx.ExecContext(r.Context(),
			`UPDATE characters SET gold_balance = gold_balance + ?, updated_at = ? WHERE character_id = ?`,
			royalty, now, creatorID.String); err != nil {
			mmoWriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if _, err := tx.ExecContext(r.Context(),
		`UPDATE characters SET gold_balance = gold_balance + ?, updated_at = ? WHERE character_id = ?`,
		sellerAmt, now, sellerID); err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := tx.ExecContext(r.Context(),
		`UPDATE hat_listings SET seller_amount=?, royalty_amount=?, burn_amount=? WHERE listing_id=?`, sellerAmt, royalty, burn, req.ListingID); err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := tx.ExecContext(r.Context(),
		`INSERT INTO character_hats (character_id, hat_id, acquired_at, equipped) VALUES (?, ?, ?, 0)`,
		characterID, hatID, now); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "PRIMARY") {
			mmoWriteError(w, http.StatusConflict, "hat already owned")
			return
		}
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Any other active listings of this hat by the seller are void now that they no longer own it.
	tx.ExecContext(r.Context(), `UPDATE hat_listings SET status='cancelled' WHERE seller_character_id=? AND hat_id=? AND status='active'`, sellerID, hatID) //nolint:errcheck
	if err := tx.Commit(); err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
