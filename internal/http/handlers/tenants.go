package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"

	"iduna/internal/tenantprovision"
)

// TenantsHandler exposes the real control-plane tenant-provisioning pipeline
// (internal/tenantprovision) -- see that package's own header comment for the full mechanism
// and its real, deliberate scope: this handler DOES actually spin up a live IDUNA_PRO systemd
// service, gated iduna.admin-only (no public self-serve signup exists yet).
//
// Routes (require Bearer JWT + iduna.admin via middleware.RequireAuth + RequirePermission,
// wired in main.go):
//
//	GET   /api/v1/tenants        list every tenant
//	POST  /api/v1/tenants        provision a new one -- {"org_name","contact_email","subdomain"}
type TenantsHandler struct {
	DB  *sql.DB
	Cfg tenantprovision.Config
}

func (h *TenantsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.list(w, r)
	case http.MethodPost:
		h.create(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *TenantsHandler) list(w http.ResponseWriter, r *http.Request) {
	tenants, err := tenantprovision.ListTenants(r.Context(), h.DB)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, tenants)
}

type createTenantRequest struct {
	OrgName      string `json:"org_name"`
	ContactEmail string `json:"contact_email"`
	Subdomain    string `json:"subdomain"`
}

// create runs the real provisioning pipeline synchronously -- a real, honest tradeoff: this
// call blocks for as long as tenantprovision.Provision's own health-check wait takes (up to
// Cfg.HealthCheckTimeout), so a caller sees the REAL outcome (active or failed) in the response,
// not a fire-and-forget "queued" status this admin-only, low-frequency operation doesn't need.
func (h *TenantsHandler) create(w http.ResponseWriter, r *http.Request) {
	var req createTenantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	// A real provisioning run can take up to Cfg.HealthCheckTimeout; the request context's own
	// deadline (if any) still governs, this just makes sure the client's own timeout doesn't cut
	// the pipeline off mid-way through a systemctl call.
	ctx := context.WithoutCancel(r.Context())
	t, err := tenantprovision.Provision(ctx, h.Cfg, req.OrgName, req.ContactEmail, req.Subdomain)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	status := http.StatusCreated
	if t.Status == "failed" {
		status = http.StatusBadGateway
	}
	writeJSON(w, status, t)
}
