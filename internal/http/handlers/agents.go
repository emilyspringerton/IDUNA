package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"iduna/internal/auth"
	"iduna/internal/store"
)

// AgentsHandler serves GET /api/v1/agents and GET /api/v1/agents/{id}.
// Requires a valid JWT (any agent or local user). Returns JSON.
type AgentsHandler struct {
	Store store.IAMStore
}

func (h *AgentsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Route: /api/v1/agents/{id}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/agents")
	path = strings.Trim(path, "/")
	if path != "" {
		h.getOne(w, r, path)
		return
	}
	h.list(w, r)
}

func (h *AgentsHandler) list(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	agents, err := h.Store.ListAgents(ctx)
	if err != nil {
		http.Error(w, `{"error":"failed to list agents"}`, http.StatusInternalServerError)
		return
	}

	// Optional ?type= filter for emily_cluster queries.
	typeFilter := r.URL.Query().Get("type")
	if typeFilter != "" {
		filtered := agents[:0]
		for _, a := range agents {
			if a.Type == typeFilter {
				filtered = append(filtered, a)
			}
		}
		agents = filtered
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(agentListResponse{Agents: toAgentViews(agents)})
}

func (h *AgentsHandler) getOne(w http.ResponseWriter, r *http.Request, id string) {
	ctx := r.Context()
	agents, err := h.Store.ListAgents(ctx)
	if err != nil {
		http.Error(w, `{"error":"store error"}`, http.StatusInternalServerError)
		return
	}
	for _, a := range agents {
		if a.ID == id || a.Name == id {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(toAgentView(a))
			return
		}
	}
	http.Error(w, `{"error":"agent not found"}`, http.StatusNotFound)
}

type agentView struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Status      string   `json:"status"`
	Permissions []string `json:"permissions,omitempty"`
	CreatedAt   string   `json:"created_at"`
}

type agentListResponse struct {
	Agents []agentView `json:"agents"`
}

func toAgentView(a auth.Agent) agentView {
	return agentView{
		ID:          a.ID,
		Name:        a.Name,
		Type:        a.Type,
		Status:      a.Status,
		Permissions: a.Permissions,
		CreatedAt:   a.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

func toAgentViews(agents []auth.Agent) []agentView {
	out := make([]agentView, len(agents))
	for i, a := range agents {
		out[i] = toAgentView(a)
	}
	return out
}
