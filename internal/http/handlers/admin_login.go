package handlers

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"iduna/internal/auth/jwt"
	"iduna/internal/store"
)

// AdminLoginHandler serves /admin/login (GET + POST) and /admin/logout.
// These routes are public (no auth middleware) so the browser can reach them.
type AdminLoginHandler struct {
	Store  store.IAMStore
	Keys   *jwt.Keys
	Issuer string
}

func (h *AdminLoginHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/admin/logout":
		h.logout(w, r)
	default:
		h.login(w, r)
	}
}

func (h *AdminLoginHandler) login(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		renderLoginPage(w, map[string]any{"Next": r.URL.Query().Get("next")})
		return
	}
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	agentName := strings.TrimSpace(r.FormValue("agent_name"))
	agentSecret := r.FormValue("agent_secret")
	next := r.FormValue("next")
	if next == "" || !strings.HasPrefix(next, "/admin") {
		next = "/admin"
	}

	agent, err := h.Store.AuthenticateAgent(r.Context(), agentName, agentSecret)
	if err != nil {
		renderLoginPage(w, map[string]any{
			"Error": "Invalid agent name or secret.",
			"Next":  next,
		})
		return
	}

	hasAdmin := false
	for _, p := range agent.Permissions {
		if p == "iduna.admin" {
			hasAdmin = true
			break
		}
	}
	if !hasAdmin {
		renderLoginPage(w, map[string]any{
			"Error": fmt.Sprintf("Agent %q does not have the iduna.admin permission.", agentName),
			"Next":  next,
		})
		return
	}

	issuer := h.Issuer
	if issuer == "" {
		issuer = "https://iam.farthq.internal"
	}
	exp := time.Now().UTC().Add(8 * time.Hour)
	claims := map[string]any{
		"sub":         agent.ID,
		"agent_name":  agent.Name,
		"agent_type":  agent.Type,
		"permissions": agent.Permissions,
		"iss":         issuer,
		"aud":         "farthq-ecosystem",
		"exp":         exp.Unix(),
	}
	token, err := jwt.Sign(h.Keys, claims)
	if err != nil {
		http.Error(w, "failed to issue session token", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "iduna_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   8 * 3600,
	})
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (h *AdminLoginHandler) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:   "iduna_session",
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

func renderLoginPage(w http.ResponseWriter, data map[string]any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := adminLoginPageTmpl.Execute(w, data); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
	}
}

var adminLoginPageTmpl = template.Must(template.New("admin-login").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Admin Login — IDUNA Back Office</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:"Georgia","Times New Roman",serif;background:#f5f2ee;color:#1a1a1a;display:flex;min-height:100vh;align-items:center;justify-content:center;font-size:14px}
.frame{width:min(420px,100%);padding:2rem 2.5rem;border:1px solid #c0b8b0;background:#fff}
h1{font-size:20px;font-weight:normal;margin-bottom:4px;letter-spacing:.02em}
.sub{font-size:12px;color:#888;margin-bottom:24px;line-height:1.5}
label{display:block;font-size:11px;letter-spacing:.05em;text-transform:uppercase;color:#666;margin-bottom:5px;margin-top:18px}
input[type=text],input[type=password]{width:100%;padding:8px 10px;border:1px solid #bbb;font-size:13px;font-family:inherit;background:#fafafa}
input[type=text]:focus,input[type=password]:focus{outline:1px solid #999;background:#fff}
input[type=submit]{margin-top:22px;width:100%;padding:10px;background:#1a1a1a;color:#fff;border:none;font-size:13px;cursor:pointer;font-family:inherit;letter-spacing:.05em;text-transform:uppercase}
input[type=submit]:hover{background:#333}
.err{background:#f8d7da;color:#7a1a1a;padding:10px;font-size:12px;margin-bottom:16px;border-left:3px solid #c0392b}
</style>
</head>
<body>
<div class="frame">
  <h1>IDUNA Back Office</h1>
  <p class="sub">Sign in with an agent account that has the <code>iduna.admin</code> permission.</p>
  {{if .Error}}<div class="err">{{.Error}}</div>{{end}}
  <form method="POST" action="/admin/login">
    <input type="hidden" name="next" value="{{.Next}}">
    <label for="an">Agent Name</label>
    <input type="text" id="an" name="agent_name" placeholder="EMILY" autocomplete="off" required>
    <label for="as">Agent Secret</label>
    <input type="password" id="as" name="agent_secret" required>
    <input type="submit" value="Sign In">
  </form>
</div>
</body>
</html>`))
