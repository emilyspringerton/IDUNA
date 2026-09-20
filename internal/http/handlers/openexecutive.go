package handlers

import (
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
	store store.IAMStore
}

// NewOpenExecutiveHandler creates a new handler.
func NewOpenExecutiveHandler(st store.IAMStore) *OpenExecutiveHandler {
	return &OpenExecutiveHandler{store: st}
}

// ProvisionRequest is the request to provision OpenExecutive credentials.
type ProvisionRequest struct {
	Name        string   `json:"name"`        // e.g., "openexec-prod"
	Permissions []string `json:"permissions"` // e.g., ["openexec.read", "openexec.admin"]
}

// ProvisionResponse returns the provisioned secret (one-time, never retrievable again).
type ProvisionResponse struct {
	AgentID   string   `json:"agent_id"`
	AgentName string   `json:"agent_name"`
	Secret    string   `json:"secret"` // Plaintext, only shown once
	JWKSUrl   string   `json:"jwks_url"`
}

const (
	secretLenBytes = 32 // 64 hex chars when encoded
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

	if req.Name == "" {
		http.Error(w, "Name required", http.StatusBadRequest)
		return
	}

	operator := middleware.SubjectFromContext(r.Context())
	ctx := r.Context()

	// Create agent (PENDING status initially)
	agent, err := h.store.CreateAgent(ctx, systemUserID, req.Name, "service", operator)
	if err != nil {
		log.Printf("openexecutive provision error: %v", err)
		http.Error(w, "Failed to create agent", http.StatusInternalServerError)
		return
	}

	// Generate random secret
	secretBytes := make([]byte, secretLenBytes)
	if _, err := rand.Read(secretBytes); err != nil {
		log.Printf("openexecutive secret generation error: %v", err)
		http.Error(w, "Failed to generate secret", http.StatusInternalServerError)
		return
	}
	secret := hex.EncodeToString(secretBytes)

	// Store secret hash
	if err := h.store.SetAgentCredential(ctx, agent.ID, secret, operator); err != nil {
		log.Printf("openexecutive set credential error: %v", err)
		http.Error(w, "Failed to set credential", http.StatusInternalServerError)
		return
	}

	// Grant permissions
	for _, perm := range req.Permissions {
		if err := h.store.GrantAgentPermission(ctx, agent.ID, strings.TrimSpace(perm), operator); err != nil {
			log.Printf("openexecutive grant permission %s error: %v", perm, err)
			http.Error(w, "Failed to grant permissions", http.StatusInternalServerError)
			return
		}
	}

	resp := ProvisionResponse{
		AgentID:   agent.ID,
		AgentName: req.Name,
		Secret:    secret,
		JWKSUrl:   "http://localhost:8080/.well-known/jwks.json", // IDUNA's public JWKS endpoint
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
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
