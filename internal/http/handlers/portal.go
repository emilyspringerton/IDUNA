package handlers

import (
	"html/template"
	"net/http"
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
	})
}

// Home renders the list view. Only reached once RequireCookieAuth +
// RequirePermission("devportal.access") both pass -- no auth logic here.
func (h *PortalHandler) Home(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	renderHTML(w, portalHomeTmpl, map[string]any{
		"Title": "Developer Portal",
	})
}

var portalLoginTmpl = template.Must(template.New("portal_login").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sign in — Developer Portal</title>
<meta name="robots" content="noindex, nofollow">
<style>
  :root { --bg: #0b0d10; --panel: #14171c; --rule: #262b33; --text: #e7ebf0; --text-soft: #9aa4b2; --accent: #5b8def; }
  * { box-sizing: border-box; }
  body { margin: 0; min-height: 100vh; display: flex; align-items: center; justify-content: center; background: var(--bg); color: var(--text); font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif; }
  .card { width: 100%; max-width: 360px; padding: 2.5rem 2rem; background: var(--panel); border: 1px solid var(--rule); border-radius: 12px; text-align: center; }
  h1 { font-size: 1.1rem; font-weight: 600; margin: 0 0 0.35rem; }
  p { font-size: 0.86rem; color: var(--text-soft); margin: 0 0 1.75rem; }
  #g_id_signin { display: flex; justify-content: center; }
  .fallback { font-size: 0.8rem; color: var(--text-soft); opacity: 0.7; }
</style>
</head>
<body>
  <div class="card">
    <h1>Developer Portal</h1>
    <p>Sign in with the Google account tied to your IDUNA identity.</p>
    <div id="g_id_signin" data-google-client-id="{{.GoogleClientID}}" data-next="{{.Next}}"></div>
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
<style>
  :root { --bg: #0b0d10; --panel: #14171c; --rule: #262b33; --text: #e7ebf0; --text-soft: #9aa4b2; --accent: #5b8def; --amber: #d9a441; }
  * { box-sizing: border-box; }
  body { margin: 0; min-height: 100vh; background: var(--bg); color: var(--text); font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif; }
  header { padding: 1.5rem 2rem; border-bottom: 1px solid var(--rule); }
  header h1 { font-size: 1.15rem; font-weight: 600; margin: 0; }
  main { max-width: 880px; margin: 2rem auto; padding: 0 2rem; }
  ul.tool-list { list-style: none; margin: 0; padding: 0; border: 1px solid var(--rule); border-radius: 10px; overflow: hidden; }
  ul.tool-list li { display: flex; align-items: center; justify-content: space-between; gap: 1rem; padding: 1.1rem 1.4rem; border-bottom: 1px solid var(--rule); background: var(--panel); }
  ul.tool-list li:last-child { border-bottom: none; }
  .tool-name { font-weight: 600; font-size: 0.95rem; }
  .tool-desc { font-size: 0.8rem; color: var(--text-soft); margin-top: 0.15rem; }
  .tool-status { font-size: 0.72rem; padding: 0.25rem 0.65rem; border-radius: 999px; white-space: nowrap; }
  .status-pending { color: var(--amber); border: 1px solid var(--amber); }
  .status-link { color: var(--accent); border: 1px solid var(--accent); text-decoration: none; }
  .status-link:hover { background: var(--accent); color: var(--bg); }
</style>
</head>
<body>
  <header><h1>Developer Portal</h1></header>
  <main>
    <ul class="tool-list">
      <li>
        <div>
          <div class="tool-name">Jupyter</div>
          <div class="tool-desc">PARENA Jupyter kernel -- notebook environment for interactive PARENA/compiled work.</div>
        </div>
        <span class="tool-status status-pending">install pending</span>
      </li>
      <li>
        <div>
          <div class="tool-name">SARENA_NOTEBOOK</div>
          <div class="tool-desc">Native PARENA notebook GUI (TYLER-style title cards, built-in note rendering, libplot).</div>
        </div>
        <span class="tool-status status-pending">not yet built</span>
      </li>
    </ul>
  </main>
</body>
</html>
`))
