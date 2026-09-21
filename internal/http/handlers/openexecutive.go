package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"iduna/internal/http/middleware"
	"iduna/internal/store"
)

// OpenExecutiveHandler provisions and manages OpenExecutive M2M credentials.
type OpenExecutiveHandler struct {
	store   store.IAMStore
	baseURL string // e.g. "https://okemily.com" -- for the real, reachable JWKSUrl in ProvisionResponse
}

// NewOpenExecutiveHandler creates a new handler. baseURL is used to build the real, externally
// reachable JWKSUrl returned from a provision call -- a hardcoded "http://localhost:8080" here
// would be actively wrong for whoever actually deploys OpenExecutive against this IDUNA instance.
func NewOpenExecutiveHandler(st store.IAMStore, baseURL string) *OpenExecutiveHandler {
	return &OpenExecutiveHandler{store: st, baseURL: baseURL}
}

// ProvisionRequest is the request to provision OpenExecutive credentials.
type ProvisionRequest struct {
	Name        string   `json:"name"`        // e.g., "openexec-prod"
	Permissions []string `json:"permissions"` // e.g., ["openexec.read", "openexec.admin"]
}

// ProvisionResponse returns the provisioned secret (one-time, never retrievable again).
type ProvisionResponse struct {
	AgentID   string `json:"agent_id"`
	AgentName string `json:"agent_name"`
	Secret    string `json:"secret"` // Plaintext, only shown once
	JWKSUrl   string `json:"jwks_url"`
}

const (
	secretLenBytes = 32                                     // 64 hex chars when encoded
	systemUserID   = "00000000-0000-4000-8000-000000000001" // well-known system user
)

// Provision creates new OpenExecutive M2M credentials (admin only).
// POST /api/v1/openexecutive/provision
func (h *OpenExecutiveHandler) Provision(w http.ResponseWriter, r *http.Request) {
	// Require iduna.admin permission
	if !hasPermission(r, "iduna.admin") {
		http.Error(w, "Unauthorized", http.StatusForbidden)
		return
	}

	var req ProvisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	operator := middleware.SubjectFromContext(r.Context())
	resp, err := h.provisionCore(r.Context(), operator, req.Name, req.Permissions)
	if err != nil {
		http.Error(w, err.publicMessage, err.statusCode)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// provisionErr carries both the real (logged) error and the status/message safe to show a caller
// -- the admin HTML page (admin_openexecutive.go) and the JSON API share this exact logic and
// need to react to the same failure the same way, just render it differently.
type provisionErr struct {
	err           error
	statusCode    int
	publicMessage string
}

func (e *provisionErr) Error() string { return e.publicMessage }

// provisionCore is the real, shared logic behind both Provision (the JSON API) and the Back
// Office's own /admin/openexecutive/provision form -- one code path, two presentations, so a
// future change to how agents/credentials/permissions get provisioned can't drift between them.
func (h *OpenExecutiveHandler) provisionCore(ctx context.Context, operator, name string, permissions []string) (ProvisionResponse, *provisionErr) {
	if name == "" {
		return ProvisionResponse{}, &provisionErr{statusCode: http.StatusBadRequest, publicMessage: "Name required"}
	}

	// Create agent (PENDING status initially)
	agent, err := h.store.CreateAgent(ctx, systemUserID, name, "service", operator)
	if err != nil {
		log.Printf("openexecutive provision error: %v", err)
		return ProvisionResponse{}, &provisionErr{err: err, statusCode: http.StatusInternalServerError, publicMessage: "Failed to create agent"}
	}

	// Generate random secret
	secretBytes := make([]byte, secretLenBytes)
	if _, err := rand.Read(secretBytes); err != nil {
		log.Printf("openexecutive secret generation error: %v", err)
		return ProvisionResponse{}, &provisionErr{err: err, statusCode: http.StatusInternalServerError, publicMessage: "Failed to generate secret"}
	}
	secret := hex.EncodeToString(secretBytes)

	// Store secret hash
	if err := h.store.SetAgentCredential(ctx, agent.ID, secret, operator); err != nil {
		log.Printf("openexecutive set credential error: %v", err)
		return ProvisionResponse{}, &provisionErr{err: err, statusCode: http.StatusInternalServerError, publicMessage: "Failed to set credential"}
	}

	// Grant permissions
	for _, perm := range permissions {
		if err := h.store.GrantAgentPermission(ctx, agent.ID, strings.TrimSpace(perm), operator); err != nil {
			log.Printf("openexecutive grant permission %s error: %v", perm, err)
			return ProvisionResponse{}, &provisionErr{err: err, statusCode: http.StatusInternalServerError, publicMessage: "Failed to grant permissions"}
		}
	}

	return ProvisionResponse{
		AgentID:   agent.ID,
		AgentName: name,
		Secret:    secret,
		JWKSUrl:   h.baseURL + "/.well-known/jwks.json",
	}, nil
}

// HealthCheck returns OpenExecutive service status.
// GET /api/v1/openexecutive/health
func (h *OpenExecutiveHandler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	resp := map[string]string{
		"status": "ok",
		"name":   "openexecutive",
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
