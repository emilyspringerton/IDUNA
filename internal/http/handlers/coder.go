// coder.go — coder agent API (chat sessions, work queue for the emily.cli worker, lessons).
// Every route has its own fine-grained permission (internal/coder/perms.go); coder.admin and
// iduna.admin are supersets. The same handler is mounted twice: /api/v1/coder/ behind bearer JWT
// auth (CLI, worker, M2M) and /admin/coder/api/ behind admin cookie auth (the chat page).
package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"iduna/internal/coder"
	"iduna/internal/http/middleware"
	"iduna/internal/store"
)

type CoderHandler struct {
	Store  *coder.Store
	IAM    store.IAMStore // optional: enables POST workers (agent provisioning)
	Prefix string         // e.g. "/api/v1/coder"
}

func (h *CoderHandler) can(r *http.Request, perm string) bool {
	return coder.Allows(middleware.PermissionsFromContext(r.Context()), perm)
}

func (h *CoderHandler) deny(w http.ResponseWriter, perm string) {
	writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden", "required_permission": perm})
}

func (h *CoderHandler) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, coder.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	case errors.Is(err, coder.ErrForbidden):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
}

func (h *CoderHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, h.Prefix), "/")
	seg := strings.Split(path, "/")
	sub := middleware.SubjectFromContext(r.Context())
	method := r.Method

	// require checks perm, writing 403 on failure.
	require := func(perm string) bool {
		if !h.can(r, perm) {
			h.deny(w, perm)
			return false
		}
		return true
	}

	switch {
	case path == "tools" && method == http.MethodGet:
		if !require(coder.PermToolsRead) {
			return
		}
		type tool struct {
			Name        string `json:"name"`
			Permission  string `json:"permission"`
			Description string `json:"description"`
		}
		out := []tool{}
		for n, d := range coder.Tools {
			out = append(out, tool{n, coder.ToolPerm(n), d})
		}
		writeJSON(w, 200, map[string]any{"tools": out})

	case path == "sessions" && method == http.MethodPost:
		if !require(coder.PermSessionCreate) {
			return
		}
		var in struct{ Title string }
		json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in)
		b := make([]byte, 8)
		rand.Read(b)
		s, err := h.Store.CreateSession(hex.EncodeToString(b), sub, in.Title)
		if err != nil {
			h.fail(w, err)
			return
		}
		writeJSON(w, 201, s)

	case path == "sessions" && method == http.MethodGet:
		owner := sub
		if h.can(r, coder.PermSessionReadAll) {
			owner = ""
		} else if !require(coder.PermSessionRead) {
			return
		}
		list, err := h.Store.ListSessions(owner)
		if err != nil {
			h.fail(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"sessions": list})

	case len(seg) == 3 && seg[0] == "sessions" && seg[2] == "messages":
		sess, err := h.Store.GetSession(seg[1])
		if err != nil {
			h.fail(w, err)
			return
		}
		mine := sess.Owner == sub
		switch method {
		case http.MethodGet:
			if !(mine && h.can(r, coder.PermSessionRead)) && !h.can(r, coder.PermSessionReadAll) {
				h.deny(w, coder.PermSessionRead)
				return
			}
			after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
			msgs, err := h.Store.Messages(sess.ID, after)
			if err != nil {
				h.fail(w, err)
				return
			}
			writeJSON(w, 200, map[string]any{"messages": msgs})
		case http.MethodPost:
			if !require(coder.PermChatSend) {
				return
			}
			if !mine && !h.can(r, coder.PermAdmin) {
				h.deny(w, "coder.admin (not your session)")
				return
			}
			var in struct{ Content string }
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&in); err != nil || strings.TrimSpace(in.Content) == "" {
				writeJSON(w, 400, map[string]string{"error": "content required"})
				return
			}
			m, err := h.Store.Send(sess.ID, sub, middleware.PermissionsFromContext(r.Context()), in.Content)
			if err != nil {
				h.fail(w, err)
				return
			}
			writeJSON(w, 202, m)
		default:
			http.NotFound(w, r)
		}

	case path == "lessons" && method == http.MethodGet:
		if !require(coder.PermLessonsRead) {
			return
		}
		ls, err := h.Store.Lessons()
		if err != nil {
			h.fail(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"lessons": ls})

	case path == "lessons" && method == http.MethodPost:
		if !require(coder.PermLessonsWrite) {
			return
		}
		var in struct{ Text string }
		json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in)
		l, err := h.Store.AddLesson(in.Text, sub)
		if err != nil {
			h.fail(w, err)
			return
		}
		writeJSON(w, 201, l)

	case len(seg) == 2 && seg[0] == "lessons" && method == http.MethodDelete:
		if !require(coder.PermLessonsWrite) {
			return
		}
		id, _ := strconv.ParseInt(seg[1], 10, 64)
		if err := h.Store.DeleteLesson(id); err != nil {
			h.fail(w, err)
			return
		}
		writeJSON(w, 200, map[string]string{"status": "deleted"})

	case path == "workers" && method == http.MethodPost:
		// Mints a worker agent holding exactly coder.worker.claim + coder.worker.report (no chat,
		// no admin, no tools): the least-privilege identity `emily coder work` runs as.
		if !require(coder.PermWorkerProvision) {
			return
		}
		if h.IAM == nil {
			writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "agent provisioning not configured"})
			return
		}
		nb := make([]byte, 3)
		rand.Read(nb)
		name := "CODER-WORKER-" + strings.ToUpper(hex.EncodeToString(nb))
		agent, err := h.IAM.CreateAgent(r.Context(), systemUserID, name, "service", sub)
		if err != nil {
			h.fail(w, err)
			return
		}
		sb := make([]byte, 32)
		rand.Read(sb)
		secret := hex.EncodeToString(sb)
		if err := h.IAM.SetAgentCredential(r.Context(), agent.ID, secret, sub); err != nil {
			h.fail(w, err)
			return
		}
		perms := []string{coder.PermWorkerClaim, coder.PermWorkerReport}
		for _, p := range perms {
			if err := h.IAM.GrantAgentPermission(r.Context(), agent.ID, p, sub); err != nil {
				h.fail(w, err)
				return
			}
		}
		writeJSON(w, 201, map[string]any{"agent_id": agent.ID, "agent_name": name, "agent_secret": secret,
			"permissions": perms, "note": "the secret is shown once"})

	case path == "work/claim" && method == http.MethodPost:
		if !require(coder.PermWorkerClaim) {
			return
		}
		wk, err := h.Store.ClaimNext(sub)
		if errors.Is(err, coder.ErrNotFound) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if err != nil {
			h.fail(w, err)
			return
		}
		writeJSON(w, 200, wk)

	case len(seg) == 3 && seg[0] == "work" && method == http.MethodPost:
		if !require(coder.PermWorkerReport) {
			return
		}
		id, _ := strconv.ParseInt(seg[1], 10, 64)
		switch seg[2] {
		case "events":
			var in struct{ Role, Tool, Content string }
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&in); err != nil {
				writeJSON(w, 400, map[string]string{"error": "bad json"})
				return
			}
			m, err := h.Store.Report(id, sub, in.Role, in.Tool, in.Content)
			if err != nil {
				h.fail(w, err)
				return
			}
			writeJSON(w, 201, m)
		case "finish":
			var in struct{ OK bool }
			json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in)
			if err := h.Store.Finish(id, sub, in.OK); err != nil {
				h.fail(w, err)
				return
			}
			writeJSON(w, 200, map[string]string{"status": "ok"})
		default:
			http.NotFound(w, r)
		}

	default:
		http.NotFound(w, r)
	}
}
