package handlers

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"iduna/internal/auth"
	"iduna/internal/http/middleware"
	"iduna/internal/store"
)

// AdminHandler serves the Back Office admin UI.
// All routes require iduna.admin permission (enforced at the mux level via middleware).
type AdminHandler struct {
	Store store.IAMStore
	mux   *http.ServeMux
}

// Init registers routes on the handler's internal mux. Call once after construction.
func (h *AdminHandler) Init() {
	h.mux = http.NewServeMux()
	h.mux.HandleFunc("/admin", h.overview)
	h.mux.HandleFunc("/admin/users", h.users)
	h.mux.HandleFunc("/admin/users/", h.userAction)
	h.mux.HandleFunc("/admin/agents", h.agents)
	h.mux.HandleFunc("/admin/agents/", h.agentAction)
	h.mux.HandleFunc("/admin/audit", h.audit)
	h.mux.HandleFunc("/admin/apples", h.apples)
	h.mux.HandleFunc("/admin/apples/", h.appleDetail)
}

// ServeHTTP dispatches admin routes. Mount at /admin and /admin/ with auth middleware.
func (h *AdminHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *AdminHandler) routeHandler() http.Handler { return h }

// --- overview ---

func (h *AdminHandler) overview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()
	users, _ := h.Store.ListUsers(ctx, 5)
	agents, _ := h.Store.ListAgents(ctx)
	events, _ := h.Store.ListIAMEvents(ctx, 5)

	renderHTML(w, adminOverviewTmpl, map[string]any{
		"Title":        "Back Office",
		"RecentUsers":  users,
		"AgentCount":   len(agents),
		"RecentEvents": events,
		"Now":          time.Now().UTC().Format("2006-01-02 15:04 UTC"),
	})
}

// --- users ---

func (h *AdminHandler) users(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	users, err := h.Store.ListUsers(ctx, 200)
	if err != nil {
		http.Error(w, "failed to list users: "+err.Error(), http.StatusInternalServerError)
		return
	}
	roles, _ := h.Store.ListRoles(ctx)
	renderHTML(w, adminUsersTmpl, map[string]any{
		"Title": "User Management",
		"Users": users,
		"Roles": roles,
	})
}

// userAction handles POST /admin/users/{id}/{action}
func (h *AdminHandler) userAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	// Path: /admin/users/{id}/{action}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/admin/users/"), "/")
	if len(parts) < 2 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	userID, action := parts[0], parts[1]
	if userID == "" || action == "" {
		http.Error(w, "missing user id or action", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	operatorID := middleware.SubjectFromContext(ctx)

	var opErr error
	switch action {
	case "suspend":
		opErr = h.Store.UpdateUserStatus(ctx, userID, "SUSPENDED", operatorID)
	case "activate":
		opErr = h.Store.UpdateUserStatus(ctx, userID, "ACTIVE", operatorID)
	case "roles":
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		roleID := r.FormValue("role_id")
		verb := r.FormValue("verb") // "assign" or "revoke"
		if roleID == "" {
			http.Error(w, "role_id required", http.StatusBadRequest)
			return
		}
		if verb == "revoke" {
			opErr = h.Store.RevokeRole(ctx, userID, roleID, operatorID)
		} else {
			opErr = h.Store.AssignRole(ctx, userID, roleID, operatorID)
		}
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}

	if opErr != nil {
		http.Error(w, "operation failed: "+opErr.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

// --- agents ---

func (h *AdminHandler) agents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		name := strings.TrimSpace(r.FormValue("name"))
		agentType := strings.TrimSpace(r.FormValue("type"))
		ownerID := strings.TrimSpace(r.FormValue("owner_user_id"))
		operatorID := middleware.SubjectFromContext(ctx)
		if name == "" || ownerID == "" {
			http.Error(w, "name and owner_user_id required", http.StatusBadRequest)
			return
		}
		if _, err := h.Store.CreateAgent(ctx, ownerID, name, agentType, operatorID); err != nil {
			http.Error(w, "failed to create agent: "+err.Error(), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/admin/agents", http.StatusSeeOther)
		return
	}

	agents, err := h.Store.ListAgents(ctx)
	if err != nil {
		http.Error(w, "failed to list agents: "+err.Error(), http.StatusInternalServerError)
		return
	}
	users, _ := h.Store.ListUsers(ctx, 200)
	renderHTML(w, adminAgentsTmpl, map[string]any{
		"Title":  "Agent Registry",
		"Agents": agents,
		"Users":  users,
	})
}

// agentAction handles POST /admin/agents/{id}/suspend
func (h *AdminHandler) agentAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/admin/agents/"), "/")
	if len(parts) < 2 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	agentID, action := parts[0], parts[1]
	ctx := r.Context()
	operatorID := middleware.SubjectFromContext(ctx)

	var status string
	switch action {
	case "suspend":
		status = "SUSPENDED"
	case "activate":
		status = "ACTIVE"
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	if err := h.Store.UpdateAgentStatus(ctx, agentID, status, operatorID); err != nil {
		http.Error(w, "operation failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/agents", http.StatusSeeOther)
}

// --- audit ---

func (h *AdminHandler) audit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	events, err := h.Store.ListIAMEvents(ctx, 200)
	if err != nil {
		http.Error(w, "failed to load audit log: "+err.Error(), http.StatusInternalServerError)
		return
	}
	renderHTML(w, adminAuditTmpl, map[string]any{
		"Title":  "Audit Log",
		"Events": events,
	})
}

// --- apples ---

func (h *AdminHandler) apples(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()

	q := r.URL.Query()
	agentID := q.Get("agent_id")
	sourceRepo := q.Get("source_repo")
	appleType := q.Get("apple_type")
	limit := 200

	apples, err := h.Store.ListApples(ctx, agentID, sourceRepo, appleType, limit)
	if err != nil {
		http.Error(w, "failed to list apples: "+err.Error(), http.StatusInternalServerError)
		return
	}
	renderHTML(w, adminApplesTmpl, map[string]any{
		"Title":      "Apples Ledger",
		"Apples":     apples,
		"AgentID":    agentID,
		"SourceRepo": sourceRepo,
		"AppleType":  appleType,
	})
}

func (h *AdminHandler) appleDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	idStr := strings.TrimPrefix(r.URL.Path, "/admin/apples/")
	idStr = strings.Trim(idStr, "/")
	if idStr == "" {
		http.Redirect(w, r, "/admin/apples", http.StatusSeeOther)
		return
	}
	id, err := parseInt64(idStr)
	if err != nil || id <= 0 {
		http.Error(w, "invalid apple id", http.StatusBadRequest)
		return
	}
	apple, err := h.Store.GetApple(r.Context(), id)
	if err != nil {
		http.Error(w, "apple not found", http.StatusNotFound)
		return
	}

	var metaStr string
	if len(apple.Metadata) > 0 && string(apple.Metadata) != "null" {
		var m any
		if err := json.Unmarshal(apple.Metadata, &m); err == nil {
			out, _ := json.MarshalIndent(m, "", "  ")
			metaStr = string(out)
		} else {
			metaStr = string(apple.Metadata)
		}
	}

	renderHTML(w, adminAppleDetailTmpl, map[string]any{
		"Title":    fmt.Sprintf("Apple #%d", apple.ID),
		"Apple":    apple,
		"Metadata": metaStr,
	})
}

func parseInt64(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}

// --- helpers ---

func renderHTML(w http.ResponseWriter, tmpl *template.Template, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, data); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
	}
}

// --- templates ---

var adminBase = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}} — IDUNA Back Office</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:"Georgia","Times New Roman",serif;background:#f5f2ee;color:#1a1a1a;font-size:14px;line-height:1.55}
a{color:#5a3e36;text-decoration:none}
a:hover{text-decoration:underline}
nav{background:#1a1a1a;color:#ccc;padding:10px 24px;display:flex;gap:24px;align-items:center}
nav .brand{color:#c4a882;font-weight:bold;letter-spacing:.04em;margin-right:12px}
nav a{color:#ccc;font-size:13px}
nav a:hover{color:#fff}
.container{max-width:1100px;margin:0 auto;padding:24px}
h1{font-size:22px;font-weight:normal;border-bottom:1px solid #c0b8b0;padding-bottom:8px;margin-bottom:20px;letter-spacing:.02em}
h2{font-size:16px;font-weight:normal;margin:20px 0 10px;color:#3a2a24}
table{width:100%;border-collapse:collapse;font-size:13px;margin-bottom:24px}
th{background:#e8e0d8;text-align:left;padding:7px 10px;font-weight:normal;letter-spacing:.03em;border-bottom:2px solid #c0b8b0}
td{padding:7px 10px;border-bottom:1px solid #ddd8d0;vertical-align:top}
tr:hover td{background:#f0ece6}
.badge{display:inline-block;padding:2px 7px;border-radius:2px;font-size:11px;letter-spacing:.03em;font-family:monospace}
.badge-active{background:#d4edda;color:#1a5c2a}
.badge-pending{background:#fff3cd;color:#7a5800}
.badge-suspended{background:#f8d7da;color:#7a1a1a}
.badge-decommissioned{background:#e2e3e5;color:#555}
button,input[type=submit]{font-size:12px;padding:4px 12px;cursor:pointer;border:1px solid #888;background:#f0ece6;font-family:inherit}
button:hover,input[type=submit]:hover{background:#e0d8d0}
button.danger,input[type=submit].danger{border-color:#b85c5c;color:#7a1a1a}
input[type=text],input[type=email],select{font-size:13px;padding:4px 8px;border:1px solid #bbb;background:#fff;font-family:inherit}
form.inline{display:inline}
.meta{font-size:11px;color:#888;font-family:monospace}
.section-card{background:#fff;border:1px solid #ddd8d0;padding:16px;margin-bottom:20px}
.empty{color:#888;font-style:italic;padding:12px}
pre{background:#1a1a1a;color:#d4d0c8;padding:12px;font-size:11px;overflow-x:auto;border-radius:2px}
</style>
</head>
<body>
<nav>
  <span class="brand">IDUNA</span>
  <span style="color:#666;font-size:11px;margin-right:16px">Back Office</span>
  <a href="/admin">Overview</a>
  <a href="/admin/users">Users</a>
  <a href="/admin/agents">Agents</a>
  <a href="/admin/audit">Audit Log</a>
  <a href="/admin/apples">Apples</a>
  <span style="flex:1"></span>
  <a href="/admin/logout" style="font-size:11px;color:#888">Sign out</a>
</nav>
<div class="container">
{{block "body" .}}{{end}}
</div>
</body>
</html>`

func mustParseTmpl(name, body string) *template.Template {
	return template.Must(template.New(name).Funcs(template.FuncMap{
		"statusBadge": func(s string) template.HTML {
			cls := "badge-" + strings.ToLower(s)
			return template.HTML(fmt.Sprintf(`<span class="badge %s">%s</span>`, cls, s))
		},
		"fmtTime": func(t time.Time) string {
			return t.UTC().Format("2006-01-02 15:04")
		},
		"fmtPayload": func(b []byte) string {
			if len(b) == 0 || string(b) == "null" {
				return "—"
			}
			var m map[string]any
			if err := json.Unmarshal(b, &m); err != nil {
				return string(b)
			}
			out, _ := json.MarshalIndent(m, "", "  ")
			return string(out)
		},
		"userRoles": func(u auth.User) string {
			if len(u.Roles) == 0 {
				return "—"
			}
			return strings.Join(u.Roles, ", ")
		},
	}).Parse(adminBase + body))
}

var adminOverviewTmpl = mustParseTmpl("overview", `
{{define "body"}}
<h1>Back Office Overview</h1>
<p class="meta">{{.Now}}</p>

<div style="display:grid;grid-template-columns:1fr 1fr;gap:20px;margin-top:20px">
<div class="section-card">
  <h2>Recent Users</h2>
  {{if .RecentUsers}}
  <table>
  <tr><th>Email</th><th>Status</th><th>Roles</th></tr>
  {{range .RecentUsers}}
  <tr>
    <td>{{.Email}}</td>
    <td>{{statusBadge .Status}}</td>
    <td class="meta">{{userRoles .}}</td>
  </tr>
  {{end}}
  </table>
  <a href="/admin/users">→ All users</a>
  {{else}}<p class="empty">No users yet.</p>{{end}}
</div>

<div class="section-card">
  <h2>Recent Audit Events</h2>
  {{if .RecentEvents}}
  <table>
  <tr><th>Event</th><th>Aggregate</th><th>Time</th></tr>
  {{range .RecentEvents}}
  <tr>
    <td class="meta">{{.EventType}}</td>
    <td class="meta">{{.AggregateType}}/{{.AggregateID}}</td>
    <td class="meta">{{fmtTime .RecordedAt}}</td>
  </tr>
  {{end}}
  </table>
  <a href="/admin/audit">→ Full audit log</a>
  {{else}}<p class="empty">No events yet.</p>{{end}}
</div>
</div>

<div class="section-card" style="margin-top:0">
  <h2>System</h2>
  <table>
  <tr><th>Component</th><th>Value</th></tr>
  <tr><td>Agent registry</td><td>{{.AgentCount}} agents registered — <a href="/admin/agents">manage</a></td></tr>
  </table>
</div>
{{end}}`)

var adminUsersTmpl = mustParseTmpl("users", `
{{define "body"}}
<h1>User Management</h1>
{{if .Users}}
<table>
<tr><th>Email</th><th>Handle</th><th>Status</th><th>Roles</th><th>Actions</th></tr>
{{range .Users}}
<tr>
  <td>{{.Email}}</td>
  <td class="meta">{{if .Handle}}{{.Handle}}{{else}}—{{end}}</td>
  <td>{{statusBadge .Status}}</td>
  <td class="meta">{{userRoles .}}</td>
  <td>
    {{if eq .Status "ACTIVE"}}
    <form class="inline" method="POST" action="/admin/users/{{.IDString}}/suspend">
      <button type="submit" class="danger" onclick="return confirm('Suspend this user?')">Suspend</button>
    </form>
    {{else if eq .Status "SUSPENDED"}}
    <form class="inline" method="POST" action="/admin/users/{{.IDString}}/activate">
      <button type="submit">Activate</button>
    </form>
    {{end}}
    &nbsp;
    {{if $.Roles}}
    <form class="inline" method="POST" action="/admin/users/{{.IDString}}/roles">
      <input type="hidden" name="verb" value="assign">
      <select name="role_id" style="font-size:11px;padding:2px">
        {{range $.Roles}}<option value="{{.ID}}">{{.Name}}</option>{{end}}
      </select>
      <input type="submit" value="+ Role">
    </form>
    {{end}}
  </td>
</tr>
{{end}}
</table>
{{else}}
<p class="empty">No users registered yet.</p>
{{end}}
{{end}}`)

var adminAgentsTmpl = mustParseTmpl("agents", `
{{define "body"}}
<h1>Agent Registry</h1>

<div class="section-card">
<h2>Register New Agent</h2>
<form method="POST" action="/admin/agents" style="display:flex;gap:10px;flex-wrap:wrap;align-items:flex-end">
  <div>
    <label style="font-size:12px;display:block;margin-bottom:3px">Name</label>
    <input type="text" name="name" placeholder="EMILY" required>
  </div>
  <div>
    <label style="font-size:12px;display:block;margin-bottom:3px">Type</label>
    <input type="text" name="type" placeholder="ops_agent">
  </div>
  <div>
    <label style="font-size:12px;display:block;margin-bottom:3px">Owner User ID</label>
    {{if .Users}}
    <select name="owner_user_id">
      {{range .Users}}<option value="{{.IDString}}">{{.Email}}</option>{{end}}
    </select>
    {{else}}
    <input type="text" name="owner_user_id" placeholder="UUID">
    {{end}}
  </div>
  <div><input type="submit" value="Register Agent"></div>
</form>
</div>

{{if .Agents}}
<table>
<tr><th>Name</th><th>Type</th><th>Status</th><th>Owner</th><th>Created</th><th>Actions</th></tr>
{{range .Agents}}
<tr>
  <td>{{.Name}}</td>
  <td class="meta">{{if .Type}}{{.Type}}{{else}}—{{end}}</td>
  <td>{{statusBadge .Status}}</td>
  <td class="meta">{{.OwnerUserID}}</td>
  <td class="meta">{{fmtTime .CreatedAt}}</td>
  <td>
    {{if eq .Status "ACTIVE"}}
    <form class="inline" method="POST" action="/admin/agents/{{.ID}}/suspend">
      <button type="submit" class="danger" onclick="return confirm('Suspend this agent?')">Suspend</button>
    </form>
    {{else if eq .Status "SUSPENDED"}}
    <form class="inline" method="POST" action="/admin/agents/{{.ID}}/activate">
      <button type="submit">Activate</button>
    </form>
    {{end}}
  </td>
</tr>
{{end}}
</table>
{{else}}
<p class="empty">No agents registered yet. Use the form above to register your first agent.</p>
{{end}}
{{end}}`)

var adminAuditTmpl = mustParseTmpl("audit", `
{{define "body"}}
<h1>Audit Log</h1>
<p class="meta" style="margin-bottom:16px">Most recent 200 IAM events. Every identity and governance state change is recorded here.</p>
{{if .Events}}
<table>
<tr><th>#</th><th>Event</th><th>Type</th><th>Aggregate</th><th>Operator</th><th>Payload</th><th>Time</th></tr>
{{range .Events}}
<tr>
  <td class="meta">{{.EventID}}</td>
  <td class="meta" style="font-weight:bold">{{.EventType}}</td>
  <td class="meta">{{.AggregateType}}</td>
  <td class="meta">{{.AggregateID}}</td>
  <td class="meta">{{if .OperatorID}}{{.OperatorID}}{{else}}system{{end}}</td>
  <td><pre style="font-size:10px;padding:4px;max-width:280px;white-space:pre-wrap">{{fmtPayload .Payload}}</pre></td>
  <td class="meta">{{fmtTime .RecordedAt}}</td>
</tr>
{{end}}
</table>
{{else}}
<p class="empty">No audit events yet.</p>
{{end}}
{{end}}`)

var adminApplesTmpl = mustParseTmpl("apples", `
{{define "body"}}
<h1>Apples Ledger</h1>
<p class="meta" style="margin-bottom:12px">Golden documentation records from recursive self-improvement runs. Append-only.</p>

<div class="section-card" style="margin-bottom:16px">
<form method="GET" action="/admin/apples" style="display:flex;gap:10px;flex-wrap:wrap;align-items:flex-end">
  <div>
    <label style="font-size:12px;display:block;margin-bottom:3px">Source Repo</label>
    <input type="text" name="source_repo" value="{{.SourceRepo}}" placeholder="prrject-fatbaby">
  </div>
  <div>
    <label style="font-size:12px;display:block;margin-bottom:3px">Agent ID</label>
    <input type="text" name="agent_id" value="{{.AgentID}}" placeholder="agent UUID">
  </div>
  <div>
    <label style="font-size:12px;display:block;margin-bottom:3px">Type</label>
    <select name="apple_type">
      <option value="">All</option>
      <option value="improvement"{{if eq .AppleType "improvement"}} selected{{end}}>improvement</option>
      <option value="observation"{{if eq .AppleType "observation"}} selected{{end}}>observation</option>
      <option value="incident"{{if eq .AppleType "incident"}} selected{{end}}>incident</option>
      <option value="release"{{if eq .AppleType "release"}} selected{{end}}>release</option>
      <option value="audit"{{if eq .AppleType "audit"}} selected{{end}}>audit</option>
    </select>
  </div>
  <div><input type="submit" value="Filter"></div>
  {{if or .SourceRepo .AgentID .AppleType}}<div><a href="/admin/apples" style="font-size:12px;line-height:26px">clear</a></div>{{end}}
</form>
</div>

{{if .Apples}}
<table>
<tr><th>#</th><th>Recorded</th><th>Repo</th><th>Agent</th><th>Type</th><th>Title</th></tr>
{{range .Apples}}
<tr>
  <td class="meta"><a href="/admin/apples/{{.ID}}">{{.ID}}</a></td>
  <td class="meta">{{fmtTime .RecordedAt}}</td>
  <td class="meta">{{.SourceRepo}}</td>
  <td class="meta" style="max-width:120px;overflow:hidden;text-overflow:ellipsis">{{.AgentID}}</td>
  <td><span class="badge badge-pending">{{.AppleType}}</span></td>
  <td><a href="/admin/apples/{{.ID}}">{{.Title}}</a></td>
</tr>
{{end}}
</table>
{{else}}
<p class="empty">No apples yet. Agents submit via POST /api/v1/apples.</p>
{{end}}
{{end}}`)

var adminAppleDetailTmpl = mustParseTmpl("apple-detail", `
{{define "body"}}
<h1>{{.Title}}</h1>
<p class="meta" style="margin-bottom:16px">
  <a href="/admin/apples">← Apples ledger</a>
</p>

<div class="section-card" style="margin-bottom:16px">
<table>
<tr><th style="width:140px">ID</th><td class="meta">{{.Apple.ID}}</td></tr>
<tr><th>Agent</th><td class="meta">{{.Apple.AgentID}}</td></tr>
<tr><th>Source Repo</th><td class="meta">{{.Apple.SourceRepo}}</td></tr>
<tr><th>Run ID</th><td class="meta">{{.Apple.RunID}}</td></tr>
<tr><th>Type</th><td><span class="badge badge-pending">{{.Apple.AppleType}}</span></td></tr>
<tr><th>Recorded</th><td class="meta">{{fmtTime .Apple.RecordedAt}}</td></tr>
</table>
</div>

<h2>Body</h2>
<pre style="white-space:pre-wrap;font-size:12px;line-height:1.6;margin-bottom:20px">{{.Apple.Body}}</pre>

{{if .Metadata}}
<h2>Metadata</h2>
<pre style="font-size:11px">{{.Metadata}}</pre>
{{end}}
{{end}}`)
