package handlers

import (
	"html/template"
	"net/http"
	"strings"
	"time"

	authjwt "iduna/internal/auth/jwt"
	"iduna/internal/http/middleware"
	"iduna/internal/userlog"

	"golang.org/x/crypto/bcrypt"
)

// PortalHandler serves the developer notebook portal -- a Linode-Cloud-
// Manager-style list view of links to internal dev tools (Jupyter, and
// eventually SARENA_NOTEBOOK), gated by a real IDUNA login rather than
// left open on the reverse proxy. Founder, real-time, after settling on
// this shape through a longer back-and-forth (bare Jupyter login form ->
// okemily.com/py -> IDUNA backoffice-only -> this): "we dont haev a login
// with google button on anywhere yet have it be on the notebook portal"
// + "yea we need a portal that will have a link to jupyter and sarena
// similar to the linode cloud manager linodes manager list view."
//
// Auth shape: GET /portal/login renders a real Sign-in-with-Google button
// (Google Identity Services, same client-side flow promptoverse's own
// authStripHTML/authStripScript already use -- see
// internal/promptoverse/render.go) that POSTs to the existing
// /api/v1/auth/google, which now also sets a real HttpOnly iduna_session
// cookie (see auth.go's GoogleAuthHandler). GET /portal itself is wired
// in main.go behind RequireCookieAuth + RequirePermission("devportal.access")
// -- a brand-new permission granted to nobody by default (see migration
// 202608250001_devportal_permissions.sql's own header for why that's
// deliberate, not an oversight).
type PortalHandler struct {
	GoogleClientID string
	// Proj/Keys/Issuer power PortalLocalLogin (2026-08-28, founder
	// real-time: "get the developer portal working with iduna login
	// instead of just the google oauth" -- Google sign-in stays
	// blocked on a human-only GCP Console step, so this is the real,
	// working near-term path, not a stopgap left half-built). Optional:
	// a nil Proj means the local-login form is simply never rendered
	// (Login below), matching GoogleClientID's own existing "sign-in
	// not yet configured" fallback pattern for the Google button.
	Proj   userlog.UserProjector
	Keys   *authjwt.Keys
	Issuer string
}

// Login renders the sign-in page. Unauthenticated only in practice --
// RequireCookieAuth redirects here on any failed/missing session, so a
// signed-in user only ever lands on this page by navigating to it
// directly, in which case the script below just bounces them straight to
// /portal.
func (h *PortalHandler) Login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	next := r.URL.Query().Get("next")
	if next == "" {
		next = "/portal"
	}
	renderHTML(w, portalLoginTmpl, map[string]any{
		"GoogleClientID": h.GoogleClientID,
		"Next":           next,
		"LocalLogin":     h.Proj != nil,
	})
}

// LocalLogin handles POST /portal/login -- real IDUNA (email +
// password) sign-in for the developer portal, added 2026-08-28
// alongside the pre-existing Google-button flow above. Founder real-
// time: "get the developer portal working with iduna login instead of
// just the google oauth" / "make the whole thing real we will fix
// oauth once we get some inertia on the portal with a regular iduna
// login" -- Google sign-in stays blocked on a human-only GCP Console
// step, so this is the real, working path, not a stopgap.
//
// Reuses the exact same real verification LocalAuthHandler's own
// ServeHTTP already establishes (bcrypt against local_users via the
// shared userlog.UserProjector, same "invalid credentials" message
// for both a missing user and a wrong password, no user-enumeration
// leak) -- the only real difference is the OUTPUT shape: a real,
// HttpOnly iduna_session COOKIE + redirect (matching AdminLoginHandler's
// own real cookie-setting flow for /admin/login) instead of a JSON
// token response, since this is a browser form post, not an API call.
func (h *PortalHandler) LocalLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(strings.ToLower(r.FormValue("email")))
	password := r.FormValue("password")
	next := r.FormValue("next")
	if next == "" {
		next = "/portal"
	}

	loginErr := "Invalid email or password."
	if email == "" || password == "" || h.Proj == nil {
		h.renderLoginError(w, next, loginErr)
		return
	}

	user, err := h.Proj.GetByEmail(r.Context(), email)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if user == nil || user.Status == "deleted" || user.Status == "suspended" {
		h.renderLoginError(w, next, loginErr)
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		h.renderLoginError(w, next, loginErr)
		return
	}

	issuer := h.Issuer
	if issuer == "" {
		issuer = "https://iam.farthq.internal"
	}
	exp := time.Now().UTC().Add(AdminSessionTTL)
	sub := "local:" + itoa(user.LocalUID)
	claims := map[string]any{
		"sub":          sub,
		"local_uid":    user.LocalUID,
		"email":        user.Email,
		"display_name": user.DisplayName,
		"permissions":  localUserPermissions(user),
		"iss":          issuer,
		"aud":          "farthq-ecosystem",
		"exp":          exp.Unix(),
	}
	token, err := authjwt.Sign(h.Keys, claims)
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
		MaxAge:   int(AdminSessionTTL.Seconds()),
	})
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (h *PortalHandler) renderLoginError(w http.ResponseWriter, next, msg string) {
	renderHTML(w, portalLoginTmpl, map[string]any{
		"GoogleClientID": h.GoogleClientID,
		"Next":           next,
		"LocalLogin":     h.Proj != nil,
		"Error":          msg,
	})
}

// Home renders the list view. Only reached once RequireCookieAuth +
// RequirePermission("devportal.access") both pass -- no auth logic here.
func (h *PortalHandler) Home(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	email, _ := claims["email"].(string)
	renderHTML(w, portalHomeTmpl, map[string]any{
		"Title": "Developer Portal",
		"Email": email,
	})
}

// Logout clears the iduna_session cookie and returns to the portal login
// page -- same clearing shape as admin_login.go's own logout, duplicated
// (not called directly) so a portal sign-out lands back on /portal/login
// rather than /admin/login.
func (h *PortalHandler) Logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:   "iduna_session",
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})
	http.Redirect(w, r, "/portal/login", http.StatusSeeOther)
}

// portalLoginTmpl/portalHomeTmpl are styled on the real IDUNA style guide
// (IDUNA/index.html + IDUNA/styles.css: cream/gold ceremony aesthetic,
// Cormorant Garamond headlines over Spectral body text, gold-bordered
// panels) -- not an invented dark-tech theme. Art is real Prompt-o-verse
// gallery output (fenrir-robot, fox-robot), served as real static files
// (see main.go's /portal/images/ routes), same "downsample + serve" move
// as OKEMILY's own wotan art. Designed via /design (canvas published
// 2026-08-25) then ported here so the live page matches, not just a
// preview -- founder pattern established earlier this session ("finish
// the wotan stuff first i want to see that online first").
var portalLoginTmpl = template.Must(template.New("portal_login").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sign in — Developer Portal</title>
<meta name="robots" content="noindex, nofollow">
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Cormorant+Garamond:wght@400;500;600&family=Spectral:wght@400;500&display=swap" rel="stylesheet">
<style>
  :root {
    --bg: #f4f1ea; --bg-soft: #ede7dc; --panel: #ebe4d8; --line-soft: #d2c7b8;
    --gold: #c6a75e; --gold-soft: #bfa062; --gold-highlight: #d6bc7a;
    --text-main: #3a352e; --text-muted: #7a7368; --text-faint: #a8a093;
  }
  * { box-sizing: border-box; }
  body {
    margin: 0; min-height: 100vh;
    background: radial-gradient(circle at top, color-mix(in srgb, var(--bg) 84%, #fff 16%), var(--bg-soft));
    color: var(--text-main); font-family: "Spectral", Georgia, serif; line-height: 1.45;
  }
  a { color: var(--gold-soft); }
  a:hover { color: var(--gold-highlight); }
  .shell { min-height: 100vh; display: grid; place-items: center; padding: 3.5rem 1.5rem; }
  .frame {
    width: min(460px, 100%);
    border: 1px solid color-mix(in srgb, var(--gold) 60%, var(--line-soft) 40%);
    border-radius: 8px; background: color-mix(in srgb, var(--panel) 92%, white 8%);
    overflow: hidden;
  }
  .art { position: relative; height: 230px; overflow: hidden; }
  .art img { width: 100%; height: 100%; object-fit: cover; display: block; }
  .art::after {
    content: ""; position: absolute; inset: 0;
    background: linear-gradient(to bottom, rgba(20,18,14,0.05) 0%, color-mix(in srgb, var(--panel) 92%, white 8%) 96%);
  }
  .body { padding: 0 2.1rem 2.3rem; text-align: center; }
  .label { letter-spacing: 0.35em; text-transform: uppercase; font-size: 0.66rem; color: var(--text-muted); margin-top: -1.4rem; position: relative; }
  h1 { margin: 0.7rem 0 0; font-family: "Cormorant Garamond", serif; font-weight: 500; font-size: 2.15rem; letter-spacing: 0.01em; }
  .sub { margin: 0.6rem 0 0; color: var(--text-muted); font-size: 0.92rem; }
  #g_id_signin { margin-top: 1.9rem; display: flex; justify-content: center; min-height: 44px; }
  .fallback { font-size: 0.85rem; color: var(--text-muted); }
  .footnote { margin-top: 1.9rem; font-size: 0.76rem; color: var(--text-faint); }
  .local-form { margin-top: 1.8rem; text-align: left; display: grid; gap: 0.85rem; }
  .local-form label { font-size: 0.74rem; letter-spacing: 0.06em; text-transform: uppercase; color: var(--text-muted); display: block; margin-bottom: 0.3rem; }
  .local-form input {
    width: 100%; padding: 0.6rem 0.75rem; font-family: "Spectral", Georgia, serif; font-size: 0.95rem;
    border: 1px solid color-mix(in srgb, var(--gold) 45%, var(--line-soft) 55%); border-radius: 5px;
    background: color-mix(in srgb, var(--panel) 97%, white 3%); color: var(--text-main);
  }
  .local-form input:focus { outline: none; border-color: var(--gold-highlight); }
  .local-form button {
    margin-top: 0.3rem; padding: 0.65rem 1rem; font-family: "Spectral", Georgia, serif; font-size: 0.92rem;
    border: 1px solid var(--gold); border-radius: 5px; background: color-mix(in srgb, var(--gold) 22%, var(--panel) 78%);
    color: var(--text-main); cursor: pointer; transition: background 160ms ease;
  }
  .local-form button:hover { background: color-mix(in srgb, var(--gold) 34%, var(--panel) 66%); }
  .error-msg { margin: 1.3rem 0 0; font-size: 0.84rem; color: #a8452f; }
  .divider { display: flex; align-items: center; gap: 0.8rem; margin: 1.9rem 0 0; color: var(--text-faint); font-size: 0.74rem; letter-spacing: 0.05em; text-transform: uppercase; }
  .divider::before, .divider::after { content: ""; flex: 1; height: 1px; background: var(--line-soft); }
</style>
</head>
<body>
  <div class="shell">
    <div class="frame">
      <div class="art"><img src="/portal/images/fenrir-robot.jpg" alt=""></div>
      <div class="body">
        <p class="label">EINHORN_INDUSTRIAL &middot; IDUNA</p>
        <h1>Developer Portal</h1>
        <p class="sub">Sign in with your IDUNA identity.</p>
        {{if .Error}}<p class="error-msg">{{.Error}}</p>{{end}}
        {{if .LocalLogin}}
        <form class="local-form" method="post" action="/portal/login">
          <input type="hidden" name="next" value="{{.Next}}">
          <div>
            <label for="email">Email</label>
            <input type="email" id="email" name="email" required autocomplete="username">
          </div>
          <div>
            <label for="password">Password</label>
            <input type="password" id="password" name="password" required autocomplete="current-password">
          </div>
          <button type="submit">Sign in</button>
        </form>
        {{if .GoogleClientID}}<div class="divider">or</div>{{end}}
        {{end}}
        <div id="g_id_signin" data-google-client-id="{{.GoogleClientID}}" data-next="{{.Next}}"></div>
        <p class="footnote">Access is granted per-account. Signing in does not automatically<br>grant portal access &mdash; ask an IDUNA administrator.</p>
      </div>
    </div>
  </div>
{{if .GoogleClientID}}<script src="https://accounts.google.com/gsi/client" async defer></script>{{end}}
<script>
(function () {
  var container = document.getElementById('g_id_signin');
  var clientId = container.getAttribute('data-google-client-id');
  var next = container.getAttribute('data-next') || '/portal';

  if (!clientId) {
    container.innerHTML = '<span class="fallback">Sign-in is not yet configured (GOOGLE_CLIENT_ID unset).</span>';
    return;
  }

  function handleCredential(response) {
    fetch('/api/v1/auth/google', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      // credentials: 'same-origin' is the fetch default, but stated
      // explicitly here -- the whole point of this call is to let the
      // browser store the Set-Cookie: iduna_session response header,
      // not to read the JSON body's access_token (unlike promptoverse's
      // own localStorage-based widget).
      credentials: 'same-origin',
      body: JSON.stringify({ id_token: response.credential })
    })
      .then(function (res) { return res.ok ? res.json() : Promise.reject(res); })
      .then(function () { window.location.href = next; })
      .catch(function () {
        container.innerHTML = '<span class="fallback">Sign-in failed -- your Google account may not be registered.</span>';
      });
  }
  window.__portalHandleCredential = handleCredential;

  var tryInit = function () {
    if (!window.google || !window.google.accounts) { setTimeout(tryInit, 200); return; }
    window.google.accounts.id.initialize({ client_id: clientId, callback: handleCredential });
    window.google.accounts.id.renderButton(container, { theme: 'outline', size: 'large' });
  };
  tryInit();
})();
</script>
</body>
</html>
`))

var portalHomeTmpl = template.Must(template.New("portal_home").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<meta name="robots" content="noindex, nofollow">
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Cormorant+Garamond:wght@400;500;600&family=Spectral:wght@400;500&display=swap" rel="stylesheet">
<style>
  :root {
    --bg: #f4f1ea; --bg-soft: #ede7dc; --panel: #ebe4d8; --line-soft: #d2c7b8;
    --gold: #c6a75e; --gold-soft: #bfa062; --gold-highlight: #d6bc7a;
    --text-main: #3a352e; --text-muted: #7a7368; --text-faint: #a8a093; --amber: #9c6b1f; --sage: #5c7a4f;
  }
  * { box-sizing: border-box; }
  body {
    margin: 0; min-height: 100vh;
    background: radial-gradient(circle at top, color-mix(in srgb, var(--bg) 84%, #fff 16%), var(--bg-soft));
    color: var(--text-main); font-family: "Spectral", Georgia, serif; line-height: 1.45;
  }
  a { color: var(--gold-soft); }
  a:hover { color: var(--gold-highlight); }
  header {
    display: flex; align-items: center; justify-content: space-between;
    padding: 1.6rem clamp(1.5rem, 5vw, 3.5rem) 1.2rem;
    border-bottom: 1px solid color-mix(in srgb, var(--gold) 35%, var(--line-soft) 65%);
  }
  .wordmark { letter-spacing: 0.3em; text-transform: uppercase; font-size: 0.68rem; color: var(--text-muted); }
  .wordmark strong { color: var(--text-main); }
  .session { display: flex; align-items: center; gap: 0.9rem; font-size: 0.82rem; color: var(--text-muted); }
  .session a { text-decoration: none; border-bottom: 1px solid color-mix(in srgb, var(--gold) 65%, transparent 35%); padding-bottom: 1px; }
  .hero { position: relative; height: 260px; overflow: hidden; }
  .hero img { width: 100%; height: 100%; object-fit: cover; display: block; }
  .hero::after {
    content: ""; position: absolute; inset: 0;
    background: linear-gradient(to bottom, rgba(20,18,14,0.12) 0%, color-mix(in srgb, var(--bg-soft) 96%, #fff 4%) 97%);
  }
  .hero-text { position: absolute; left: clamp(1.5rem, 5vw, 3.5rem); bottom: 2.2rem; z-index: 1; }
  .hero-text .label { letter-spacing: 0.32em; text-transform: uppercase; font-size: 0.66rem; color: rgba(244,241,234,0.85); }
  .hero-text h1 {
    margin: 0.5rem 0 0; font-family: "Cormorant Garamond", serif; font-weight: 500;
    font-size: clamp(2rem, 4vw, 2.7rem); color: #f4f1ea; text-shadow: 0 2px 18px rgba(0,0,0,0.45);
  }
  main { max-width: 760px; margin: 0 auto; padding: 2.4rem clamp(1.5rem, 5vw, 3.5rem) 4rem; }
  .section-label { font-size: 0.78rem; color: var(--text-muted); margin: 0 0 1rem; }
  .tool-list { display: grid; gap: 0.9rem; }
  .tool-row {
    display: flex; align-items: center; gap: 1.1rem; padding: 1.15rem 1.35rem;
    border: 1px solid color-mix(in srgb, var(--gold) 55%, var(--line-soft) 45%);
    border-radius: 6px; background: color-mix(in srgb, var(--panel) 94%, white 6%);
    text-decoration: none; color: var(--text-main); transition: border-color 160ms ease;
  }
  .tool-row:hover { border-color: var(--gold-highlight); }
  .tool-icon {
    flex: none; width: 44px; height: 44px; display: grid; place-items: center;
    border-radius: 6px; border: 1px solid color-mix(in srgb, var(--gold) 60%, var(--line-soft) 40%);
    background: color-mix(in srgb, var(--panel) 97%, white 3%);
  }
  .tool-main { flex: 1; min-width: 0; }
  .tool-name { font-family: "Cormorant Garamond", serif; font-weight: 600; font-size: 1.25rem; }
  .tool-desc { margin-top: 0.15rem; font-size: 0.85rem; color: var(--text-muted); }
  .tool-status {
    flex: none; font-size: 0.68rem; letter-spacing: 0.06em; text-transform: uppercase;
    padding: 0.28rem 0.6rem; border-radius: 999px; white-space: nowrap;
  }
  .status-pending { color: var(--amber); border: 1px solid color-mix(in srgb, var(--amber) 55%, var(--line-soft) 45%); }
  .status-live { color: var(--sage); border: 1px solid color-mix(in srgb, var(--sage) 55%, var(--line-soft) 45%); }
  .chevron { flex: none; color: var(--text-faint); }
  footer { max-width: 760px; margin: 0 auto; padding: 0 clamp(1.5rem, 5vw, 3.5rem) 2.5rem; font-size: 0.76rem; color: var(--text-faint); }
</style>
</head>
<body>
<header>
  <div class="wordmark">EINHORN_INDUSTRIAL &nbsp;/&nbsp; <strong>IDUNA</strong></div>
  <div class="session">
    {{if .Email}}<span>{{.Email}}</span>{{end}}
    <a href="/portal/logout">Sign out</a>
  </div>
</header>

<div class="hero">
  <img src="/portal/images/fox-robot.jpg" alt="">
  <div class="hero-text">
    <p class="label">Developer Portal</p>
    <h1>Internal Tools</h1>
  </div>
</div>

<main>
  <p class="section-label">Signed in &mdash; select a tool below.</p>
  <div class="tool-list">
    <a class="tool-row" href="https://okemily.com/jewel/lab" target="_blank" rel="noopener">
      <div class="tool-icon">
        <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="#7a7368" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
          <path d="M4 4h16v12H4z"/><path d="M8 20h8"/><path d="M12 16v4"/><path d="M8 9l2.5 2.5L8 14"/><path d="M13 14h3"/>
        </svg>
      </div>
      <div class="tool-main">
        <div class="tool-name">Jupyter (JEWEL)</div>
        <div class="tool-desc">PARENA Jupyter kernel &mdash; interactive notebook environment for compiled PARENA work. HTTP Basic Auth for now, pending Google OAuth.</div>
      </div>
      <span class="tool-status status-live">Live</span>
      <svg class="chevron" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M9 6l6 6-6 6"/></svg>
    </a>
    <a class="tool-row" href="https://okemily.com/sarena/" target="_blank" rel="noopener">
      <div class="tool-icon">
        <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="#7a7368" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
          <rect x="5" y="3" width="14" height="18" rx="1.5"/><path d="M9 3v18"/><path d="M13 8h3"/><path d="M13 12h3"/><path d="M13 16h3"/>
        </svg>
      </div>
      <div class="tool-main">
        <div class="tool-name">SARENA_NOTEBOOK</div>
        <div class="tool-desc">Native PARENA notebook GUI &mdash; TYLER-style title cards, built-in note rendering. HTML v0 shipped; libplot/SDL-native are real, later phases.</div>
      </div>
      <span class="tool-status status-live">Live</span>
      <svg class="chevron" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M9 6l6 6-6 6"/></svg>
    </a>
  </div>
</main>

<footer>Access requires the <code>devportal.access</code> permission, granted per-account by an IDUNA administrator.</footer>
</body>
</html>
`))
