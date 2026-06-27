// supply.go — S136-02/03 supply chain CRUD endpoints
//
// GET  /api/v1/supply/vendors?category=<cat>   — list vendors (optional category filter)
// POST /api/v1/supply/vendors                  — create vendor
// GET  /api/v1/supply/orders?status=<s>        — list orders (optional status filter)
// POST /api/v1/supply/orders                   — create pending order
// PATCH /api/v1/supply/orders/:id/status       — transition order status
//
// Auth: requires valid JWT (any authenticated agent or user).

package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type SupplyHandler struct {
	DB *sql.DB
}

func (h *SupplyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/supply")

	switch {
	case path == "/vendors" && r.Method == http.MethodGet:
		h.listVendors(w, r)
	case path == "/vendors" && r.Method == http.MethodPost:
		h.createVendor(w, r)
	case path == "/orders" && r.Method == http.MethodGet:
		h.listOrders(w, r)
	case path == "/orders" && r.Method == http.MethodPost:
		h.createOrder(w, r)
	case strings.HasPrefix(path, "/orders/") && strings.HasSuffix(path, "/status") && r.Method == http.MethodPatch:
		orderID := strings.TrimSuffix(strings.TrimPrefix(path, "/orders/"), "/status")
		h.patchOrderStatus(w, r, orderID)
	default:
		http.NotFound(w, r)
	}
}

func (h *SupplyHandler) listVendors(w http.ResponseWriter, r *http.Request) {
	category := r.URL.Query().Get("category")
	var (
		rows *sql.Rows
		err  error
	)
	if category != "" {
		rows, err = h.DB.QueryContext(r.Context(),
			`SELECT vendor_id, name, category, url, moq, unit_cost_cents, lead_days, quality_tier, status
			 FROM vendors WHERE category = ? AND status != 'disqualified' ORDER BY name`,
			category)
	} else {
		rows, err = h.DB.QueryContext(r.Context(),
			`SELECT vendor_id, name, category, url, moq, unit_cost_cents, lead_days, quality_tier, status
			 FROM vendors WHERE status != 'disqualified' ORDER BY category, name`)
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL"})
		return
	}
	defer rows.Close()

	type vendor struct {
		VendorID      string `json:"vendor_id"`
		Name          string `json:"name"`
		Category      string `json:"category"`
		URL           string `json:"url"`
		MOQ           int    `json:"moq"`
		UnitCostCents int    `json:"unit_cost_cents"`
		LeadDays      int    `json:"lead_days"`
		QualityTier   string `json:"quality_tier"`
		Status        string `json:"status"`
	}
	var vendors []vendor
	for rows.Next() {
		var v vendor
		if err := rows.Scan(&v.VendorID, &v.Name, &v.Category, &v.URL, &v.MOQ, &v.UnitCostCents, &v.LeadDays, &v.QualityTier, &v.Status); err != nil {
			continue
		}
		vendors = append(vendors, v)
	}
	if vendors == nil {
		vendors = []vendor{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"vendors": vendors})
}

func (h *SupplyHandler) createVendor(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name          string `json:"name"`
		Category      string `json:"category"`
		URL           string `json:"url"`
		MOQ           int    `json:"moq"`
		UnitCostCents int    `json:"unit_cost_cents"`
		LeadDays      int    `json:"lead_days"`
		QualityTier   string `json:"quality_tier"`
		Notes         string `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": "BAD_REQUEST", "detail": "invalid JSON"})
		return
	}
	if body.Name == "" || body.Category == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": "BAD_REQUEST", "detail": "name and category required"})
		return
	}
	if body.QualityTier == "" {
		body.QualityTier = "standard"
	}
	if body.MOQ == 0 {
		body.MOQ = 1
	}

	res, err := h.DB.ExecContext(r.Context(),
		`INSERT INTO vendors (name, category, url, moq, unit_cost_cents, lead_days, quality_tier, notes, last_evaluated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		body.Name, body.Category, body.URL, body.MOQ, body.UnitCostCents, body.LeadDays, body.QualityTier, body.Notes,
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL", "detail": err.Error()})
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, http.StatusCreated, map[string]any{"vendor_id": fmt.Sprintf("%d", id)})
}

func (h *SupplyHandler) listOrders(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	var (
		rows *sql.Rows
		err  error
	)
	if status != "" {
		rows, err = h.DB.QueryContext(r.Context(),
			`SELECT order_id, vendor_id, product, quantity, unit_cost_cents, total_cost_cents, status, ordered_at, received_at, notes
			 FROM supply_orders WHERE status = ? ORDER BY created_at DESC`, status)
	} else {
		rows, err = h.DB.QueryContext(r.Context(),
			`SELECT order_id, vendor_id, product, quantity, unit_cost_cents, total_cost_cents, status, ordered_at, received_at, notes
			 FROM supply_orders ORDER BY created_at DESC`)
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL"})
		return
	}
	defer rows.Close()

	type order struct {
		OrderID       string  `json:"order_id"`
		VendorID      string  `json:"vendor_id"`
		Product       string  `json:"product"`
		Quantity      int     `json:"quantity"`
		UnitCostCents int     `json:"unit_cost_cents"`
		TotalCents    int     `json:"total_cost_cents"`
		Status        string  `json:"status"`
		OrderedAt     *string `json:"ordered_at,omitempty"`
		ReceivedAt    *string `json:"received_at,omitempty"`
		Notes         string  `json:"notes"`
	}
	var orders []order
	for rows.Next() {
		var o order
		if err := rows.Scan(&o.OrderID, &o.VendorID, &o.Product, &o.Quantity, &o.UnitCostCents, &o.TotalCents, &o.Status, &o.OrderedAt, &o.ReceivedAt, &o.Notes); err != nil {
			continue
		}
		orders = append(orders, o)
	}
	if orders == nil {
		orders = []order{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"orders": orders})
}

func (h *SupplyHandler) createOrder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		VendorID      string `json:"vendor_id"`
		Product       string `json:"product"`
		Quantity      int    `json:"quantity"`
		UnitCostCents int    `json:"unit_cost_cents"`
		TotalCents    int    `json:"total_cost_cents"`
		Notes         string `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": "BAD_REQUEST", "detail": "invalid JSON"})
		return
	}
	if body.Product == "" || body.Quantity == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": "BAD_REQUEST", "detail": "product and quantity required"})
		return
	}
	if body.TotalCents == 0 {
		body.TotalCents = body.Quantity * body.UnitCostCents
	}

	res, err := h.DB.ExecContext(r.Context(),
		`INSERT INTO supply_orders (vendor_id, product, quantity, unit_cost_cents, total_cost_cents, status, notes)
		 VALUES (?, ?, ?, ?, ?, 'pending', ?)`,
		body.VendorID, body.Product, body.Quantity, body.UnitCostCents, body.TotalCents, body.Notes,
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL", "detail": err.Error()})
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, http.StatusCreated, map[string]any{"order_id": fmt.Sprintf("%d", id)})
}

func (h *SupplyHandler) patchOrderStatus(w http.ResponseWriter, r *http.Request, orderID string) {
	var body struct {
		Status string `json:"status"`
		Notes  string `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": "BAD_REQUEST", "detail": "invalid JSON"})
		return
	}
	valid := map[string]bool{"pending": true, "ordered": true, "shipped": true, "received": true, "qc_pass": true, "qc_fail": true}
	if !valid[body.Status] {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": "BAD_REQUEST", "detail": "invalid status"})
		return
	}

	var extra string
	switch body.Status {
	case "ordered":
		extra = ", ordered_at = CURRENT_TIMESTAMP"
	case "received", "qc_pass", "qc_fail":
		extra = ", received_at = CURRENT_TIMESTAMP"
	}

	q := fmt.Sprintf(
		`UPDATE supply_orders SET status = ?, notes = CASE WHEN ? != '' THEN ? ELSE notes END%s WHERE order_id = ?`,
		extra)
	_, err := h.DB.ExecContext(r.Context(), q, body.Status, body.Notes, body.Notes, orderID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": "INTERNAL", "detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
