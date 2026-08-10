package handlers

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"iduna/internal/http/middleware"
)

// CarePyreContactHandler serves the public "Contact Us" form on
// carepyre.org — CORS-scoped, rate-limited, no auth (the visitor has no
// IDUNA identity). Modeled on MailingListHandler's public subscribe
// endpoint but simpler: no vault, no Mailchimp sync, plain storage like
// every other operational table (see the migration's own doc comment).
// Founder real-time, 2026-08-10: "can we make the contact us a form that
// dumps to iduna?" -> "remove hello@carepyre.org and use the real contact
// form" — replaces the mailto: link that was on the page before.
type CarePyreContactHandler struct {
	DB          *sql.DB
	AllowOrigin []string // exact-match allowlist, e.g. "https://carepyre.org"
	Limiter     *middleware.IPRateLimiter
}

func (h *CarePyreContactHandler) Register(mux *http.ServeMux) {
	submit := http.HandlerFunc(h.submit)
	if h.Limiter != nil {
		mux.Handle("POST /api/v1/carepyre/contact", middleware.AuthRateLimit(h.Limiter)(submit))
	} else {
		mux.Handle("POST /api/v1/carepyre/contact", submit)
	}
	mux.HandleFunc("OPTIONS /api/v1/carepyre/contact", h.preflight)
}

func (h *CarePyreContactHandler) corsOrigin(r *http.Request) string {
	origin := r.Header.Get("Origin")
	for _, allowed := range h.AllowOrigin {
		if origin == allowed {
			return origin
		}
	}
	return ""
}

func (h *CarePyreContactHandler) preflight(w http.ResponseWriter, r *http.Request) {
	if origin := h.corsOrigin(r); origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	}
	w.WriteHeader(http.StatusNoContent)
}

type carepyreContactRequest struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Message string `json:"message"`
}

func (h *CarePyreContactHandler) submit(w http.ResponseWriter, r *http.Request) {
	if origin := h.corsOrigin(r); origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
	}

	if h.DB == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "contact form unavailable"})
		return
	}

	var req carepyreContactRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}

	name := strings.TrimSpace(req.Name)
	email := strings.TrimSpace(req.Email)
	message := strings.TrimSpace(req.Message)

	if name == "" || len(name) > 120 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name is required"})
		return
	}
	if !emailRe.MatchString(email) || len(email) > 254 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "a valid email address is required"})
		return
	}
	if message == "" || len(message) > 4000 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "message is required (max 4000 characters)"})
		return
	}

	if _, err := h.DB.Exec(
		`INSERT INTO carepyre_contact_submissions (name, email, message) VALUES (?, ?, ?)`,
		name, email, message,
	); err != nil {
		log.Printf("[carepyre_contact] insert failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}
