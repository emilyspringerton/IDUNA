package handlers

// Founder real-time, 2026-09-29: "i dont have the password manager set up i need a page in iDUNA
// admin that lets me scan a QR code to log in ... additional qr code passkey functionality (make
// there a button to create a login code just like the nock video uploader)". IDUNA's admin login
// has no human password at all -- it's an agent_name + agent_secret (a long M2M credential, see
// admin_login.go), exactly the kind of thing a password manager exists to autofill, and exactly
// the kind of thing that's painful to type by hand on a machine that doesn't have one set up.
// This reuses the same real "mint a short-lived token, render it as a QR code, a phone visits a
// token-scoped URL" shape nock_videos.go's own phone-upload-link flow already established --
// applied here to a materially different action (approve a login, not upload a file).
//
// Real, deliberate security difference from the NOCK upload-link precedent: there, the token
// itself IS the bearer credential (fine -- uploading a video is low-stakes). Here it must NOT be:
// if scanning/photographing the QR code alone were enough to grant an iduna.admin session, this
// would be a strictly WORSE authentication mechanism than the agent_secret it replaces (a QR code
// on a screen is far easier to shoulder-surf than a typed secret). So /approve/{token} is gated
// behind the exact same RequireCookieAuth+iduna.admin chain as every other admin route -- only a
// request that already proves iduna.admin authority (an existing valid iduna_session cookie, or a
// fresh agent_name/agent_secret login redirecting back here) can approve a pending code. This
// mirrors internal/auth/device's own Confirm step (approving a device-link code also requires an
// existing authenticated identity) rather than inventing a new trust model. Once the waiting
// browser observes "approved" via /status, the code is immediately marked consumed -- one-time
// use, matching a normal login POST.
//
//	POST /admin/login/qr/api/codes                 (public) -> mint a pending code
//	GET  /admin/login/qr/api/codes/{token}/qr       (public) -> QR PNG of BaseURL+/admin/login/qr/approve/{token}
//	GET  /admin/login/qr/api/codes/{token}/status   (public, polled) -> {status}; the first poll to
//	     observe "approved" gets the real iduna_session cookie set on ITS OWN response and flips
//	     the row to consumed
//	GET  /admin/login/qr/approve/{token}            (iduna.admin required) -> confirmation page
//	POST /admin/login/qr/approve/{token}            (iduna.admin required) -> approve or deny

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"html"
	"net/http"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"iduna/internal/auth/jwt"
	"iduna/internal/http/middleware"
	"iduna/internal/util"
)

// AdminQRLoginTTL is how long a minted code stays valid if nobody approves or denies it.
const AdminQRLoginTTL = 5 * time.Minute

func generateQRLoginToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil // 48 hex chars, unguessable
}

// AdminQRLoginHandler serves the public half of the flow: mint a code, render its QR image, and
// let the browser that minted it poll for approval. Registered with NO auth middleware -- this IS
// part of the login path, the same way /admin/login itself is public.
type AdminQRLoginHandler struct {
	DB           *sql.DB
	BaseURL      string
	StartLimiter *util.WindowRateLimiter // guards POST .../codes -- an unauthenticated caller can mint DB rows
}

func (h *AdminQRLoginHandler) approveURL(token string) string {
	return strings.TrimRight(h.BaseURL, "/") + "/admin/login/qr/approve/" + token
}

func (h *AdminQRLoginHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.DB == nil {
		http.Error(w, "qr login not available", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "no-store")

	path := strings.TrimPrefix(r.URL.Path, "/admin/login/qr/api/codes")
	path = strings.Trim(path, "/")

	switch {
	case path == "" && r.Method == http.MethodPost:
		h.create(w, r)
	case strings.HasSuffix(path, "/qr") && r.Method == http.MethodGet:
		h.image(w, r, strings.TrimSuffix(path, "/qr"))
	case strings.HasSuffix(path, "/status") && r.Method == http.MethodGet:
		h.status(w, r, strings.TrimSuffix(path, "/status"))
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func (h *AdminQRLoginHandler) create(w http.ResponseWriter, r *http.Request) {
	if h.StartLimiter != nil && !h.StartLimiter.Allow(clientIP(r), time.Now().UTC()) {
		mmoWriteError(w, http.StatusTooManyRequests, "too many requests")
		return
	}
	token, err := generateQRLoginToken()
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	expiresAt := time.Now().UTC().Add(AdminQRLoginTTL)
	if _, err := h.DB.ExecContext(r.Context(),
		`INSERT INTO admin_qr_login_codes (token, status, expires_at) VALUES (?, 'pending', ?)`,
		token, expiresAt); err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"token":       token,
		"expires_at":  expiresAt.Format(time.RFC3339),
		"expires_in_seconds": int(AdminQRLoginTTL.Seconds()),
		"approve_url": h.approveURL(token),
		"qr_url":      "/admin/login/qr/api/codes/" + token + "/qr",
		"status_url":  "/admin/login/qr/api/codes/" + token + "/status",
	})
}

func (h *AdminQRLoginHandler) image(w http.ResponseWriter, r *http.Request, token string) {
	if token == "" {
		mmoWriteError(w, http.StatusBadRequest, "token required")
		return
	}
	var status string
	err := h.DB.QueryRowContext(r.Context(), `SELECT status FROM admin_qr_login_codes WHERE token = ?`, token).Scan(&status)
	if err == sql.ErrNoRows {
		mmoWriteError(w, http.StatusNotFound, "code not found")
		return
	} else if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "db error")
		return
	}
	png, err := qrcode.Encode(h.approveURL(token), qrcode.Medium, 384)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "could not render qr image")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(png)
}

func (h *AdminQRLoginHandler) status(w http.ResponseWriter, r *http.Request, token string) {
	if token == "" {
		mmoWriteError(w, http.StatusBadRequest, "token required")
		return
	}
	var status, sessionToken string
	var expiresAt time.Time
	err := h.DB.QueryRowContext(r.Context(),
		`SELECT status, session_token, expires_at FROM admin_qr_login_codes WHERE token = ?`, token,
	).Scan(&status, &sessionToken, &expiresAt)
	if err == sql.ErrNoRows {
		writeJSON(w, http.StatusOK, map[string]any{"status": "expired"})
		return
	} else if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "db error")
		return
	}
	if status == "pending" && time.Now().UTC().After(expiresAt) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "expired"})
		return
	}
	if status != "approved" {
		writeJSON(w, http.StatusOK, map[string]any{"status": status})
		return
	}

	// First observation of "approved" wins the cookie and consumes the row -- a second poll (or
	// anyone else who learns the token afterward) can never replay it into a second session.
	res, err := h.DB.ExecContext(r.Context(),
		`UPDATE admin_qr_login_codes SET status = 'consumed' WHERE token = ? AND status = 'approved'`, token)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "db error")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"status": "consumed"})
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "iduna_session",
		Value:    sessionToken,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(AdminSessionTTL.Seconds()),
	})
	writeJSON(w, http.StatusOK, map[string]any{"status": "approved"})
}

// AdminQRLoginApproveHandler serves /admin/login/qr/approve/{token} -- gated by the same
// RequireCookieAuth+iduna.admin chain as any other admin route (see the header comment above for
// why this gate is load-bearing, not incidental).
type AdminQRLoginApproveHandler struct {
	DB     *sql.DB
	Keys   *jwt.Keys
	Issuer string
}

func (h *AdminQRLoginApproveHandler) issuer() string {
	if h.Issuer != "" {
		return h.Issuer
	}
	return "https://iam.farthq.internal"
}

func (h *AdminQRLoginApproveHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := strings.Trim(strings.TrimPrefix(r.URL.Path, "/admin/login/qr/approve/"), "/")
	if token == "" || strings.Contains(token, "/") {
		http.NotFound(w, r)
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		mmoWriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	agentName, _ := claims["agent_name"].(string)

	switch r.Method {
	case http.MethodGet:
		h.page(w, r, token, agentName)
	case http.MethodPost:
		h.confirm(w, r, token, claims, agentName)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *AdminQRLoginApproveHandler) lookupStatus(r *http.Request, token string) (status string, expiresAt time.Time, err error) {
	err = h.DB.QueryRowContext(r.Context(), `SELECT status, expires_at FROM admin_qr_login_codes WHERE token = ?`, token).Scan(&status, &expiresAt)
	return
}

func (h *AdminQRLoginApproveHandler) page(w http.ResponseWriter, r *http.Request, token, agentName string) {
	status, expiresAt, err := h.lookupStatus(r, token)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err == sql.ErrNoRows {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(qrApprovePageHTML(agentName, "not_found")))
		return
	} else if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	if status == "pending" && time.Now().UTC().After(expiresAt) {
		status = "expired"
	}
	_, _ = w.Write([]byte(qrApprovePageHTML(agentName, status)))
}

func (h *AdminQRLoginApproveHandler) confirm(w http.ResponseWriter, r *http.Request, token string, claims map[string]any, agentName string) {
	status, expiresAt, err := h.lookupStatus(r, token)
	if err == sql.ErrNoRows {
		mmoWriteError(w, http.StatusNotFound, "code not found")
		return
	} else if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "db error")
		return
	}
	if status != "pending" {
		mmoWriteError(w, http.StatusConflict, "code is "+status)
		return
	}
	if time.Now().UTC().After(expiresAt) {
		mmoWriteError(w, http.StatusGone, "code expired")
		return
	}

	if r.FormValue("deny") == "1" {
		if _, err := h.DB.ExecContext(r.Context(), `UPDATE admin_qr_login_codes SET status = 'denied' WHERE token = ? AND status = 'pending'`, token); err != nil {
			mmoWriteError(w, http.StatusInternalServerError, "db error")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "denied"})
		return
	}

	// Mint a FRESH session for the waiting browser, copying the approving session's own claims --
	// same agent, same permissions, a brand new exp. This never touches or extends the approving
	// request's own cookie; it's a wholly separate token for the other device.
	newClaims := map[string]any{
		"sub":         claims["sub"],
		"agent_name":  claims["agent_name"],
		"agent_type":  claims["agent_type"],
		"permissions": claims["permissions"],
		"iss":         h.issuer(),
		"aud":         "farthq-ecosystem",
		"exp":         time.Now().UTC().Add(AdminSessionTTL).Unix(),
	}
	sessionToken, err := jwt.Sign(h.Keys, newClaims)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "failed to issue session")
		return
	}
	res, err := h.DB.ExecContext(r.Context(),
		`UPDATE admin_qr_login_codes SET status = 'approved', agent_name = ?, session_token = ?, approved_at = CURRENT_TIMESTAMP WHERE token = ? AND status = 'pending'`,
		agentName, sessionToken, token)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "db error")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Lost a race to a concurrent approve/deny on the same code.
		mmoWriteError(w, http.StatusConflict, "code was already resolved")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "approved"})
}

func qrApprovePageHTML(agentName, state string) string {
	safeAgent := html.EscapeString(agentName)
	var body string
	switch state {
	case "pending":
		body = `<p class="sub">Approve signing in to the IDUNA Back Office on your other device, as <strong>` + safeAgent + `</strong>?</p>
      <div class="row">
        <button class="submit" id="approve">Approve</button>
        <button class="deny" id="deny">Deny</button>
      </div>
      <p id="result" class="result"></p>
      <script>
        function post(body){
          return fetch(location.pathname, {method:'POST', headers:{'Content-Type':'application/x-www-form-urlencoded'}, body, credentials:'same-origin'})
            .then(r => r.json().then(j => ({ok:r.ok, j})));
        }
        document.getElementById('approve').addEventListener('click', function(e){
          e.target.disabled = true;
          post('').then(({ok,j}) => {
            document.getElementById('result').textContent = ok ? 'Approved -- your other device is now signed in.' : ('Failed: ' + (j.error||'unknown error'));
          });
        });
        document.getElementById('deny').addEventListener('click', function(){
          post('deny=1').then(({ok,j}) => {
            document.getElementById('result').textContent = ok ? 'Denied.' : ('Failed: ' + (j.error||'unknown error'));
          });
        });
      </script>`
	case "approved", "consumed":
		body = `<p class="sub">This code was already approved.</p>`
	case "denied":
		body = `<p class="sub">This code was denied.</p>`
	case "expired":
		body = `<p class="sub">This code expired. Go back to the other device and generate a new one.</p>`
	default:
		body = `<p class="sub">This login code was not found.</p>`
	}
	return `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Approve login — IDUNA Back Office</title>
<style>
  :root { --bg:#f4f1ea; --panel:#ebe4d8; --line:#d2c7b8; --gold:#c6a75e; --text:#3a352e; --muted:#7a7368; --rose:#b3261e; }
  body { margin:0; min-height:100vh; display:grid; place-items:center; background:var(--bg); color:var(--text); font-family:Georgia,serif; padding:2rem 1.2rem; }
  .frame { width:min(420px,100%); border:1px solid var(--gold); border-radius:8px; background:var(--panel); padding:1.6rem 1.8rem; text-align:center; }
  h1 { font-size:1.4rem; margin:0 0 .6rem; }
  .sub { color:var(--muted); font-size:.95rem; }
  .row { display:flex; gap:.6rem; justify-content:center; margin-top:1.2rem; }
  button { font:inherit; font-size:.9rem; padding:.6rem 1.1rem; border-radius:6px; cursor:pointer; }
  .submit { border:1px solid var(--gold); background:var(--gold); color:#2b2618; }
  .deny { border:1px solid var(--line); background:#e8e0d2; color:var(--rose); }
  .result { margin-top:1rem; font-size:.85rem; color:var(--muted); }
</style></head>
<body><div class="frame"><h1>IDUNA Back Office</h1>` + body + `</div></body></html>`
}
