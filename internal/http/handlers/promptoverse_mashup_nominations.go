// internal/http/handlers/promptoverse_mashup_nominations.go — the social
// layer for Prompt-o-verse mashup discovery.
//
// Founder, real-time: "build out mashup nomination as a social tool" /
// "two subjects already in the system" / "on a subject page there will
// be the mashup creation widget which will encourage user account
// creations." Scoped via AskUserQuestion: a nomination is a request to
// GENERATE a new mashup (real spend), not a vote on what the LLM judge
// has already found (internal/promptoverse/mashups.go) -- so it sits
// pending until an EINHORN_INDUSTRIAL admin reviews it, matching the
// already-established rule ("all promotion approvals run through admins
// before hitting the gen pipeline until we have revenue from
// promptoverse"). Approving a nomination here does NOT itself trigger
// generation -- an admin still runs `emily promptoverse promote-subject`
// by hand once they've reviewed it, same as any other subject promotion.
//
// Auth: Google OAuth via the EXISTING /api/v1/auth/google (ID-token POST,
// GoogleAuthHandler) -- deliberately NOT the redirect-based web ceremony
// (/auth/google/start+callback), since that needs a dedicated
// gate.farthq.com DNS record that doesn't exist yet (SECTION 151,
// unstarted). The ID-token flow works from any page via Google Identity
// Services' client-side JS, no server-side callback URL needed -- the
// subject-page widget can use it directly. Nominating also requires the
// honor code accepted (checked here against the IAM store, since the JWT
// itself doesn't carry that claim).
package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"iduna/internal/http/middleware"
	"iduna/internal/promptoverse"
	"iduna/internal/store"
)

type MashupNominationsHandler struct {
	Store    *promptoverse.Store
	IAMStore store.IAMStore
}

func (h *MashupNominationsHandler) RegisterRoutes(mux *http.ServeMux, createProtected, reviewProtected http.Handler) {
	mux.Handle("POST /api/v1/promptoverse/mashup-nominations", createProtected)
	mux.HandleFunc("GET /api/v1/promptoverse/mashup-nominations", h.list)
	mux.Handle("PATCH /api/v1/promptoverse/mashup-nominations/{id}", reviewProtected)
}

type createNominationRequest struct {
	SubjectA string `json:"subject_a"`
	SubjectB string `json:"subject_b"`
}

// Create handles POST /api/v1/promptoverse/mashup-nominations -- exported
// so main.go can wrap it with RequireAuth (any logged-in user, no special
// permission needed; the honor-code + subject-existence checks below are
// this handler's own job, not middleware's).
func (h *MashupNominationsHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID := middleware.SubjectFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"code": "UNAUTHORIZED", "message": "valid Bearer token required"})
		return
	}

	user, err := h.IAMStore.GetUserByID(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"code": "UNAUTHORIZED", "message": "unknown user"})
		return
	}
	if !user.HonorAccepted {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"code": "HONOR_CODE_REQUIRED", "message": "accept the honor code before nominating a mashup",
		})
		return
	}

	var req createNominationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}
	req.SubjectA = strings.TrimSpace(req.SubjectA)
	req.SubjectB = strings.TrimSpace(req.SubjectB)
	if req.SubjectA == "" || req.SubjectB == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "subject_a and subject_b are required"})
		return
	}
	if strings.EqualFold(req.SubjectA, req.SubjectB) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "subject_a and subject_b must be different subjects"})
		return
	}

	subjects, err := h.Store.DistinctSubjects()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
		return
	}
	subjectA, okA := canonicalSubject(subjects, req.SubjectA)
	subjectB, okB := canonicalSubject(subjects, req.SubjectB)
	if !okA || !okB {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "both subject_a and subject_b must already exist as real subjects with their own page",
		})
		return
	}

	id, err := h.Store.CreateMashupNomination(subjectA, subjectB, userID)
	switch err {
	case nil:
		writeJSON(w, http.StatusOK, map[string]any{"status": "pending", "id": id, "subject_a": subjectA, "subject_b": subjectB})
	case promptoverse.ErrDuplicateNomination:
		writeJSON(w, http.StatusConflict, map[string]any{"error": "you already nominated this pair"})
	case promptoverse.ErrTooManyPendingNominations:
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "too many pending nominations -- wait for a review before nominating more"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
	}
}

// canonicalSubject does a case-insensitive match against the real subject
// list and returns the subject's real-cased form -- so a nomination is
// always stored with the exact casing the rest of the taxonomy uses.
func canonicalSubject(subjects []string, want string) (string, bool) {
	for _, s := range subjects {
		if strings.EqualFold(s, want) {
			return s, true
		}
	}
	return "", false
}

func (h *MashupNominationsHandler) list(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	nominations, err := h.Store.ListMashupNominations(status)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
		return
	}
	out := make([]map[string]any, len(nominations))
	for i, n := range nominations {
		out[i] = map[string]any{
			"id": n.ID, "subject_a": n.SubjectA, "subject_b": n.SubjectB,
			"status": n.Status, "created_at": n.CreatedAt,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"nominations": out})
}

type reviewNominationRequest struct {
	Status string `json:"status"` // "approved" | "rejected"
}

// Review handles PATCH /api/v1/promptoverse/mashup-nominations/{id} --
// exported so main.go can wrap it with RequirePermission("promptoverse.mashups.review").
func (h *MashupNominationsHandler) Review(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	var req reviewNominationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}
	if req.Status != "approved" && req.Status != "rejected" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "status must be 'approved' or 'rejected'"})
		return
	}
	reviewer := middleware.SubjectFromContext(r.Context())

	if err := h.Store.ReviewMashupNomination(id, req.Status, reviewer); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "nomination not found or already reviewed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": req.Status, "id": id})
}
