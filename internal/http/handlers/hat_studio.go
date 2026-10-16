package handlers

// hat_studio.go -- WOTAN_HAT_STORE_NORTHSTAR.md Phase 4: player-drawn hats.
//
// Founder real-time (2026-10-16): players draw hats, set their own price, and "should get 88% of
// the flow spent on the hat and 12% should just be burned to reduce inflation". Flow is the
// cross-world currency (characters.gold_balance, delta-synced from GFD's MUD), so a designer who
// grinds in the MUD can buy hats and a designer whose hat sells sees the Flow in the MUD too.
//
// Routes:
//   POST  /api/v1/characters/:id/hats/design                 -- publish a drawn hat (owner only)
//   GET   /api/v1/characters/:id/hats/designs                -- creator dashboard + earnings
//   PATCH /api/v1/characters/:id/hats/:hat_id/listing        -- re-price / unlist / relist (creator)
//   GET   /api/v1/hats/economy                               -- price guide + burn total (public)
//   GET   /api/v1/hats/:hat_id/image                         -- the hat's PNG (public)
//   POST  /api/v1/hats/:hat_id/report                        -- flag a hat for review
//   PATCH /api/v1/hats/:hat_id/status                        -- agent-only moderation

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"iduna/internal/http/middleware"
)

const (
	// HatCreatorSharePct / HatBurnPct: the founder-set split of every player-hat sale.
	HatCreatorSharePct = 88
	HatBurnPct         = 12

	HatCanvasSize     = 32
	hatMinPrice       = 10 // keeps the 12% burn >= 1 and the creator's cut >= 8
	hatMaxPrice       = 100000
	hatMaxPerDesigner = 12
	hatMaxPNGBytes    = 16 * 1024
	hatMinPixels      = 8
	hatReportHold     = 3 // distinct reporters before a hat is held for review
	hatMaxQuantity    = 1000000
)

// splitHatPrice returns (creator, burn). The creator's cut rounds down so any odd Flow is
// burned (deflationary). With no creator (a curated house hat) the whole price is burned.
func splitHatPrice(price int, hasCreator bool) (creator, burn int) {
	if !hasCreator {
		return 0, price
	}
	creator = price * HatCreatorSharePct / 100
	return creator, price - creator
}

// settleHatSale pays the creator (inside the buyer's transaction) and records the split.
func settleHatSale(ctx context.Context, tx *sql.Tx, hatID, buyerID, sellerID string, price int, now string) error {
	hasCreator := false
	if sellerID != "" && sellerID != buyerID {
		var one int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM characters WHERE character_id=?`, sellerID).Scan(&one); err == nil {
			hasCreator = true
		}
	}
	creator, burn := splitHatPrice(price, hasCreator)
	var seller interface{}
	if hasCreator {
		if _, err := tx.ExecContext(ctx,
			`UPDATE characters SET gold_balance = gold_balance + ?, updated_at = ? WHERE character_id = ?`,
			creator, now, sellerID); err != nil {
			return err
		}
		seller = sellerID
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO hat_sales (sale_id, hat_id, buyer_character_id, seller_character_id, price, creator_amount, burn_amount, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		uuid.New().String(), hatID, buyerID, seller, price, creator, burn, now); err != nil {
		return err
	}
	// Claim one unit of a limited edition atomically: the guard in the WHERE clause means two
	// buyers racing for the last copy cannot both succeed.
	res, err := tx.ExecContext(ctx,
		`UPDATE hats SET sales_count = sales_count + 1
		 WHERE hat_id = ? AND (quantity_limit IS NULL OR sales_count < quantity_limit)`, hatID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errHatSoldOut
	}
	return nil
}

var errHatSoldOut = errors.New("hat sold out")

// routeHatStudio handles the /api/v1/hats/... studio routes; false = not one of ours.
func (h *MMOHandler) routeHatStudio(w http.ResponseWriter, r *http.Request, path string) bool {
	rest := strings.Trim(strings.TrimPrefix(path, "/api/v1/hats"), "/")
	parts := strings.Split(rest, "/")
	switch {
	case r.Method == http.MethodGet && rest == "economy":
		h.handleHatEconomy(w, r)
		return true
	case r.Method == http.MethodGet && len(parts) == 2 && parts[1] == "image":
		h.handleHatImage(w, r, parts[0])
		return true
	case r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "report":
		h.handleHatReport(w, r, parts[0])
		return true
	case r.Method == http.MethodPatch && len(parts) == 2 && parts[1] == "status":
		h.handleHatStatus(w, r, parts[0])
		return true
	}
	return false
}

func hatValidName(s string) bool {
	n := utf8.RuneCountInString(s)
	if n < 2 || n > 32 {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// decodeHatPNG validates a base64 PNG and returns a freshly re-encoded copy (so no foreign
// chunks or metadata ever reach storage). Must be exactly 32x32 with at least a few pixels.
func decodeHatPNG(b64 string) ([]byte, string) {
	b64 = strings.TrimPrefix(b64, "data:image/png;base64,")
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, "png_base64 is not valid base64"
	}
	if len(raw) == 0 || len(raw) > hatMaxPNGBytes {
		return nil, "image must be a PNG of at most 16 KB"
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, "image is not a valid PNG"
	}
	if cfg.Width != HatCanvasSize || cfg.Height != HatCanvasSize {
		return nil, "hat image must be exactly 32x32"
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, "image is not a valid PNG"
	}
	nrgba := image.NewNRGBA(img.Bounds())
	visible := 0
	for y := 0; y < HatCanvasSize; y++ {
		for x := 0; x < HatCanvasSize; x++ {
			c := nrgbaAt(img, x, y)
			nrgba.SetNRGBA(x, y, c)
			if c.A > 0 {
				visible++
			}
		}
	}
	if visible < hatMinPixels {
		return nil, "draw a little more -- the hat is nearly empty"
	}
	var out bytes.Buffer
	if err := png.Encode(&out, nrgba); err != nil {
		return nil, "could not re-encode image"
	}
	return out.Bytes(), ""
}

func (h *MMOHandler) handleDesignHat(w http.ResponseWriter, r *http.Request, characterID string) {
	if ok, status, msg := checkHatOwnership(h, r, characterID); !ok {
		mmoWriteError(w, status, msg)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		FlowCost    int    `json:"flow_cost"`
		PNGBase64   string `json:"png_base64"`
		// QuantityLimit makes the hat a limited edition; omitted/null = unlimited.
		QuantityLimit *int `json:"quantity_limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Description = strings.TrimSpace(req.Description)
	if !hatValidName(req.Name) {
		mmoWriteError(w, http.StatusBadRequest, "name must be 2-32 characters")
		return
	}
	if utf8.RuneCountInString(req.Description) > 120 {
		mmoWriteError(w, http.StatusBadRequest, "description is at most 120 characters")
		return
	}
	if req.FlowCost < hatMinPrice || req.FlowCost > hatMaxPrice {
		mmoWriteError(w, http.StatusBadRequest, "price must be between 10 and 100000 Flow")
		return
	}
	if req.QuantityLimit != nil && (*req.QuantityLimit < 1 || *req.QuantityLimit > hatMaxQuantity) {
		mmoWriteError(w, http.StatusBadRequest, "quantity limit must be between 1 and 1,000,000")
		return
	}
	pngBytes, bad := decodeHatPNG(req.PNGBase64)
	if bad != "" {
		mmoWriteError(w, http.StatusBadRequest, bad)
		return
	}

	var exists int
	if err := h.DB.QueryRowContext(r.Context(), `SELECT 1 FROM characters WHERE character_id=?`, characterID).Scan(&exists); err != nil {
		mmoWriteError(w, http.StatusNotFound, "character not found")
		return
	}
	var n int
	h.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM hats WHERE user_generated=1 AND generated_by_character_id=? AND flow_cost>0 AND status IN ('listed','unlisted','review')`,
		characterID).Scan(&n)
	if n >= hatMaxPerDesigner {
		mmoWriteError(w, http.StatusConflict, "you have reached the limit of 12 designs -- unlist or remove one first")
		return
	}

	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback()
	hatID := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(r.Context(),
		`INSERT INTO hats (hat_id, name, description, flow_cost, image_asset, user_generated, generated_by_character_id, created_at, png_data, status, quantity_limit)
		 VALUES (?, ?, ?, ?, '', 1, ?, ?, ?, 'listed', ?)`,
		hatID, req.Name, req.Description, req.FlowCost, characterID, now, pngBytes, req.QuantityLimit); err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// The designer owns their own work from the start.
	if _, err := tx.ExecContext(r.Context(),
		`INSERT INTO character_hats (character_id, hat_id, acquired_at, equipped) VALUES (?, ?, ?, 0)`,
		characterID, hatID, now); err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := tx.Commit(); err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, hatResponse{
		HatID: hatID, Name: req.Name, Description: req.Description, FlowCost: req.FlowCost,
		QuantityLimit: req.QuantityLimit,
		ImageURL: "/api/v1/hats/" + hatID + "/image",
	})
}

type designResponse struct {
	HatID         string `json:"hat_id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	FlowCost      int    `json:"flow_cost"`
	Status        string `json:"status"`
	ImageURL      string `json:"image_url"`
	SalesCount    int    `json:"sales_count"`
	QuantityLimit *int   `json:"quantity_limit,omitempty"`
	SoldOut       bool   `json:"sold_out,omitempty"`
	FlowEarned    int    `json:"flow_earned"`
	FlowBurned    int    `json:"flow_burned"`
	ReportCount   int    `json:"report_count"`
}

func (h *MMOHandler) handleListDesigns(w http.ResponseWriter, r *http.Request, characterID string) {
	if ok, status, msg := checkHatOwnership(h, r, characterID); !ok {
		mmoWriteError(w, status, msg)
		return
	}
	rows, err := h.DB.QueryContext(r.Context(),
		`SELECT h.hat_id, h.name, h.description, h.flow_cost, h.status, h.sales_count, h.report_count, h.quantity_limit,
		        COALESCE((SELECT SUM(creator_amount) FROM hat_sales s WHERE s.hat_id=h.hat_id), 0),
		        COALESCE((SELECT SUM(burn_amount)    FROM hat_sales s WHERE s.hat_id=h.hat_id), 0)
		 FROM hats h
		 WHERE h.user_generated=1 AND h.flow_cost>0 AND h.generated_by_character_id=? AND h.png_data IS NOT NULL
		 ORDER BY h.created_at DESC`, characterID)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []designResponse{}
	totalEarned, totalBurned, totalSales := 0, 0, 0
	for rows.Next() {
		var d designResponse
		if err := rows.Scan(&d.HatID, &d.Name, &d.Description, &d.FlowCost, &d.Status,
			&d.SalesCount, &d.ReportCount, &d.QuantityLimit, &d.FlowEarned, &d.FlowBurned); err != nil {
			mmoWriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		d.SoldOut = d.QuantityLimit != nil && d.SalesCount >= *d.QuantityLimit
		d.ImageURL = "/api/v1/hats/" + d.HatID + "/image"
		totalEarned += d.FlowEarned
		totalBurned += d.FlowBurned
		totalSales += d.SalesCount
		out = append(out, d)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"designs": out, "total_sales": totalSales, "total_earned": totalEarned, "total_burned": totalBurned,
		"max_designs": hatMaxPerDesigner,
	})
}

// handleSetListing: the creator re-prices and/or lists/unlists their own hat. Already-owning
// players are unaffected; only future sales use the new price.
func (h *MMOHandler) handleSetListing(w http.ResponseWriter, r *http.Request, characterID, hatID string) {
	if ok, status, msg := checkHatOwnership(h, r, characterID); !ok {
		mmoWriteError(w, status, msg)
		return
	}
	var req struct {
		FlowCost *int  `json:"flow_cost"`
		Listed   *bool `json:"listed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || (req.FlowCost == nil && req.Listed == nil) {
		mmoWriteError(w, http.StatusBadRequest, "flow_cost and/or listed required")
		return
	}
	var creator sql.NullString
	var status string
	if err := h.DB.QueryRowContext(r.Context(),
		`SELECT generated_by_character_id, status FROM hats WHERE hat_id=? AND user_generated=1 AND flow_cost>0 AND png_data IS NOT NULL`,
		hatID).Scan(&creator, &status); err != nil {
		mmoWriteError(w, http.StatusNotFound, "design not found")
		return
	}
	if !creator.Valid || creator.String != characterID {
		mmoWriteError(w, http.StatusForbidden, "not your design")
		return
	}
	if status == "removed" || status == "review" {
		mmoWriteError(w, http.StatusConflict, "this hat is under review and cannot be changed")
		return
	}
	if req.FlowCost != nil {
		if *req.FlowCost < hatMinPrice || *req.FlowCost > hatMaxPrice {
			mmoWriteError(w, http.StatusBadRequest, "price must be between 10 and 100000 Flow")
			return
		}
		if _, err := h.DB.ExecContext(r.Context(), `UPDATE hats SET flow_cost=? WHERE hat_id=?`, *req.FlowCost, hatID); err != nil {
			mmoWriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if req.Listed != nil {
		st := "unlisted"
		if *req.Listed {
			st = "listed"
		}
		if _, err := h.DB.ExecContext(r.Context(), `UPDATE hats SET status=? WHERE hat_id=?`, st, hatID); err != nil {
			mmoWriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *MMOHandler) handleHatImage(w http.ResponseWriter, r *http.Request, hatID string) {
	var data []byte
	if err := h.DB.QueryRowContext(r.Context(),
		`SELECT png_data FROM hats WHERE hat_id=? AND png_data IS NOT NULL AND status <> 'removed'`, hatID).Scan(&data); err != nil || len(data) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write(data) //nolint:errcheck
}

// handleHatEconomy: the price guide a designer sees while pricing, plus the running burn total.
func (h *MMOHandler) handleHatEconomy(w http.ResponseWriter, r *http.Request) {
	rows, err := h.DB.QueryContext(r.Context(),
		`SELECT flow_cost FROM hats WHERE status='listed' AND flow_cost>0 ORDER BY flow_cost ASC`)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	prices := []int{}
	for rows.Next() {
		var p int
		rows.Scan(&p) //nolint:errcheck
		prices = append(prices, p)
	}
	sort.Ints(prices)
	pct := func(q float64) int {
		if len(prices) == 0 {
			return 0
		}
		return prices[int(q*float64(len(prices)-1)+0.5)]
	}
	var burned, paid, sales int
	h.DB.QueryRowContext(r.Context(),
		`SELECT COALESCE(SUM(burn_amount),0), COALESCE(SUM(creator_amount),0), COUNT(*) FROM hat_sales`).Scan(&burned, &paid, &sales)
	writeJSON(w, http.StatusOK, map[string]any{
		"creator_share_pct": HatCreatorSharePct, "burn_pct": HatBurnPct,
		"min_price": hatMinPrice, "max_price": hatMaxPrice,
		"listed": len(prices), "p25": pct(0.25), "median": pct(0.5), "p75": pct(0.75),
		"total_burned": burned, "total_paid_to_creators": paid, "total_sales": sales,
	})
}

func (h *MMOHandler) handleHatReport(w http.ResponseWriter, r *http.Request, hatID string) {
	var req struct {
		CharacterID string `json:"character_id"`
	}
	json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
	if req.CharacterID == "" {
		mmoWriteError(w, http.StatusBadRequest, "character_id required")
		return
	}
	if ok, status, msg := checkHatOwnership(h, r, req.CharacterID); !ok {
		mmoWriteError(w, status, msg)
		return
	}
	var exists int
	if err := h.DB.QueryRowContext(r.Context(),
		`SELECT 1 FROM hats WHERE hat_id=? AND user_generated=1`, hatID).Scan(&exists); err != nil {
		mmoWriteError(w, http.StatusNotFound, "hat not found")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := h.DB.ExecContext(r.Context(),
		`INSERT INTO hat_reports (hat_id, character_id, created_at) VALUES (?, ?, ?)`, hatID, req.CharacterID, now)
	if err != nil {
		// Already reported by this character: idempotent, not an error.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	_, _ = res.RowsAffected()
	h.DB.ExecContext(r.Context(), `UPDATE hats SET report_count = report_count + 1 WHERE hat_id=?`, hatID) //nolint:errcheck
	h.DB.ExecContext(r.Context(),
		`UPDATE hats SET status='review' WHERE hat_id=? AND status='listed' AND report_count >= ?`, hatID, hatReportHold) //nolint:errcheck
	w.WriteHeader(http.StatusNoContent)
}

// handleHatStatus: agent-only moderation (listed / unlisted / review / removed).
func (h *MMOHandler) handleHatStatus(w http.ResponseWriter, r *http.Request, hatID string) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		mmoWriteError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if _, isAgent := claims["agent_name"]; !isAgent {
		mmoWriteError(w, http.StatusForbidden, "moderation is agent-only")
		return
	}
	var req struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "status required")
		return
	}
	switch req.Status {
	case "listed", "unlisted", "review", "removed":
	default:
		mmoWriteError(w, http.StatusBadRequest, "status must be listed, unlisted, review or removed")
		return
	}
	res, err := h.DB.ExecContext(r.Context(), `UPDATE hats SET status=? WHERE hat_id=?`, req.Status, hatID)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		mmoWriteError(w, http.StatusNotFound, "hat not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func nrgbaAt(img image.Image, x, y int) color.NRGBA {
	return color.NRGBAModel.Convert(img.At(img.Bounds().Min.X+x, img.Bounds().Min.Y+y)).(color.NRGBA)
}
