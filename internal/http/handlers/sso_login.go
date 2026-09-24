package handlers

import (
	"html/template"
	"net/http"
	"net/url"
	"strings"
)

// SSOLoginHandler serves GET /api/v1/auth/sso/login — the one place a
// password is typed for IDUNA email/password auth. Founder real-time,
// 2026-09-24: "instead of putting your password into page on wotan iduna
// needs to become the SSO" -- WOTAN's store.html used to have its own
// email/password/confirm-password <input> fields and its own doAuth() JS,
// duplicating credential-collection UI in a site repo that isn't IDUNA's.
// Founder follow-up, same thread: "iam.okemily.com make that the actual
// sso page just the login a nice modern SSO login that has like the 2
// panes" + "classic IDUNA style guide" -- this page is now meant to be
// reached at a real, dedicated domain (iam.okemily.com, DNS + nginx vhost
// + cert queued separately, see ops/nginx-iam-okemily.conf and
// sudo-queue/) so the browser's own address bar genuinely shows IDUNA's
// identity while a password is typed, not just a same-origin proxy trick
// -- a real cross-domain redirect, the way the rest of this monorepo
// doesn't do auth yet (every other site reaches IDUNA same-origin via its
// own /api/ proxy: WOTAN/ops/nginx-wotan.conf, OKEMILY/ops/
// nginx-okemily.conf, IDUNA/ops/nginx/console-okemily.conf). It's also
// still reachable the old same-origin-proxy way from any caller's own
// /api/v1/auth/sso/login path -- the handler doesn't care which domain
// serves it, only iam.okemily.com's nginx additionally rewrites `/` to
// this path so the dedicated domain's root IS the login page. Two-pane
// layout (form left, brand right, collapses to one pane under 760px) in
// IDUNA's own classic style -- Cormorant Garamond/Spectral, warm gold/
// cream palette, the same identity internal/http/handlers/portal.go's
// own login page (`portalLoginTmpl`) and styles.css (the VS0 honor-code
// ceremony) already use, deliberately NOT WOTAN's neon-brutalist palette
// -- this page has to look like IDUNA, not like whichever site linked to
// it. The page itself has no server-side auth logic of its own -- its JS
// calls the existing, already-tested POST /api/v1/auth/email/login and
// /register endpoints (PlayerEmailAuthHandler) exactly like store.html's
// old inline form did, then hands the returned JWT back to the caller via
// a URL fragment on redirect_uri (never a query string, so it's never
// logged server-side or leaked via Referer).
//
// redirect_uri is validated against an allowlist of hosts BEFORE anything
// is rendered -- an unrecognized redirect_uri gets a plain 400, not a
// rendered login form that would bounce a real password to an arbitrary
// target. This is the one real security boundary of this handler: without
// it, this becomes an open-redirect-fronted phishing page for IDUNA
// credentials.
type SSOLoginHandler struct {
	// AllowedHosts is the set of hostnames (no scheme, no port) redirect_uri
	// is allowed to target. Configured via SSO_ALLOWED_REDIRECT_HOSTS in
	// main.go; always includes localhost/127.0.0.1 for local dev.
	AllowedHosts []string
}

func (h *SSOLoginHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	raw := r.URL.Query().Get("redirect_uri")
	if raw == "" {
		http.Error(w, "redirect_uri is required", http.StatusBadRequest)
		return
	}
	target, err := url.Parse(raw)
	if err != nil || !target.IsAbs() {
		http.Error(w, "redirect_uri must be an absolute URL", http.StatusBadRequest)
		return
	}
	if !h.hostAllowed(target.Hostname()) {
		http.Error(w, "redirect_uri host is not allowed", http.StatusBadRequest)
		return
	}

	signup := r.URL.Query().Get("signup") == "1"

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = ssoLoginTemplate.Execute(w, ssoLoginPageData{
		RedirectURI: raw,
		Signup:      signup,
	})
}

func (h *SSOLoginHandler) hostAllowed(host string) bool {
	host = strings.ToLower(host)
	for _, allowed := range h.AllowedHosts {
		allowed = strings.ToLower(strings.TrimSpace(allowed))
		if allowed != "" && host == allowed {
			return true
		}
	}
	return false
}

type ssoLoginPageData struct {
	RedirectURI string
	Signup      bool
}

// html/template contextually auto-escapes RedirectURI both as HTML (the
// hidden readout) and as a JS string literal inside <script> -- this is
// the actual XSS defense for a value that came straight from the query
// string, not the allowlist check above (that check is about WHERE the
// browser ends up, not what's safe to print).
var ssoLoginTemplate = template.Must(template.New("sso_login").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Sign in &mdash; IDUNA</title>
<meta name="robots" content="noindex, nofollow">
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Cormorant+Garamond:wght@400;500;600&family=Spectral:wght@400;500&display=swap" rel="stylesheet">
<style>
  :root {
    --bg: #f4f1ea; --bg-soft: #ede7dc; --panel: #ebe4d8; --line-soft: #d2c7b8;
    --gold: #c6a75e; --gold-soft: #bfa062; --gold-highlight: #d6bc7a;
    --text-main: #3a352e; --text-muted: #7a7368; --text-faint: #a8a093;
    --error: #a8452f;
  }
  * { box-sizing: border-box; }
  body {
    margin: 0; min-height: 100vh;
    color: var(--text-main); font-family: "Spectral", Georgia, serif; line-height: 1.45;
  }
  .sso-shell { min-height: 100vh; display: flex; }

  /* Left pane -- the actual form. Always visible, full width under 760px. */
  .form-pane {
    flex: 1 1 480px; display: flex; align-items: center; justify-content: center;
    padding: 2.5rem 1.5rem; background: var(--bg);
  }
  .form-inner { width: min(380px, 100%); }
  .label { letter-spacing: 0.3em; text-transform: uppercase; font-size: 0.66rem; color: var(--text-muted); }
  h1 { margin: 0.6rem 0 0; font-family: "Cormorant Garamond", serif; font-weight: 500; font-size: 2.3rem; letter-spacing: 0.01em; }
  .sub { margin: 0.5rem 0 0; color: var(--text-muted); font-size: 0.92rem; }
  form { margin-top: 1.9rem; display: grid; gap: 0.85rem; }
  label.field { font-size: 0.74rem; letter-spacing: 0.06em; text-transform: uppercase; color: var(--text-muted); display: block; margin-bottom: 0.3rem; }
  input {
    width: 100%; padding: 0.65rem 0.8rem; font-family: "Spectral", Georgia, serif; font-size: 0.98rem;
    border: 1px solid color-mix(in srgb, var(--gold) 45%, var(--line-soft) 55%); border-radius: 5px;
    background: color-mix(in srgb, var(--panel) 97%, white 3%); color: var(--text-main);
  }
  input:focus { outline: none; border-color: var(--gold-highlight); }
  #confirm-row { display: none; }
  #confirm-row.show { display: block; }
  button {
    margin-top: 0.4rem; width: 100%; padding: 0.7rem 1rem; font-family: "Spectral", Georgia, serif; font-size: 0.95rem;
    border: 1px solid var(--gold); border-radius: 5px; background: color-mix(in srgb, var(--gold) 22%, var(--panel) 78%);
    color: var(--text-main); cursor: pointer; transition: background 160ms ease;
  }
  button:hover { background: color-mix(in srgb, var(--gold) 34%, var(--panel) 66%); }
  button.link {
    margin-top: 1.1rem; background: none; border: none; padding: 0; width: auto;
    color: var(--gold-soft); font-size: 0.85rem; text-decoration: underline;
  }
  button.link:hover { color: var(--gold-highlight); background: none; }
  .msg { margin-top: 1.1rem; font-size: 0.84rem; min-height: 1.1em; }
  .msg.error { color: var(--error); }
  .msg.ok { color: var(--gold-soft); }
  .footnote { margin-top: 2.2rem; font-size: 0.76rem; color: var(--text-faint); }

  /* Right pane -- brand, decorative only. Hidden on narrow viewports. */
  .visual-pane {
    flex: 1 1 55%; display: flex; align-items: center; justify-content: center;
    position: relative; overflow: hidden; padding: 3rem;
    background: radial-gradient(circle at 30% 20%, #6b4f28, #3a2f1f 70%);
  }
  .visual-pane::before {
    content: ""; position: absolute; inset: 0; opacity: 0.5;
    background: repeating-linear-gradient(135deg, transparent 0 42px, rgba(244,236,216,0.05) 42px 43px);
  }
  .visual-inner { position: relative; text-align: center; color: #f4ecd8; max-width: 360px; }
  .visual-inner .wordmark {
    margin: 0; font-family: "Cormorant Garamond", serif; font-weight: 500;
    font-size: 3rem; letter-spacing: 0.1em;
  }
  .visual-inner .tagline { margin-top: 1rem; font-size: 1.05rem; color: #e4d3ab; }
  .visual-inner .apps { margin-top: 2.2rem; font-size: 0.78rem; letter-spacing: 0.08em; text-transform: uppercase; color: color-mix(in srgb, #e4d3ab 70%, transparent 30%); }

  @media (max-width: 760px) {
    .visual-pane { display: none; }
  }
</style>
</head>
<body>
<div class="sso-shell">
  <div class="form-pane">
    <div class="form-inner">
      <p class="label">EINHORN_INDUSTRIAL &middot; IDUNA</p>
      <h1>Sign in</h1>
      <p class="sub">One IDUNA account, every app.</p>
      <form id="sso-form">
        <div>
          <label class="field" for="email">Email</label>
          <input type="email" id="email" autocomplete="username" autofocus>
        </div>
        <div>
          <label class="field" for="password">Password</label>
          <input type="password" id="password" autocomplete="current-password">
        </div>
        <div id="confirm-row">
          <label class="field" for="confirm-password">Confirm password</label>
          <input type="password" id="confirm-password" autocomplete="new-password">
        </div>
        <button type="submit" id="submit-btn">Sign in</button>
      </form>
      <button type="button" class="link" id="toggle-btn">Need an account? Register instead</button>
      <div class="msg" id="msg"></div>
      <p class="footnote">You're signing in to IDUNA, not the app that sent you here &mdash; your password is only ever typed on this page.</p>
    </div>
  </div>
  <div class="visual-pane">
    <div class="visual-inner">
      <p class="wordmark">IDUNA</p>
      <p class="tagline">The identity layer for EINHORN_INDUSTRIAL.</p>
      <p class="apps">WOTAN &middot; DEADWEIGHT &middot; SHANKPIT &middot; GFD</p>
    </div>
  </div>
</div>
<script>
  var redirectURI = {{.RedirectURI}};
  var registering = {{.Signup}};

  function applyMode() {
    document.getElementById('confirm-row').className = registering ? 'show' : '';
    document.getElementById('submit-btn').textContent = registering ? 'Register' : 'Sign in';
    document.getElementById('toggle-btn').textContent = registering
      ? 'Already have an account? Sign in instead' : 'Need an account? Register instead';
  }
  applyMode();
  document.getElementById('toggle-btn').addEventListener('click', function () {
    registering = !registering;
    applyMode();
  });

  function setMsg(text, kind) {
    var el = document.getElementById('msg');
    el.textContent = text || '';
    el.className = 'msg' + (kind ? ' ' + kind : '');
  }

  document.getElementById('sso-form').addEventListener('submit', function (ev) {
    ev.preventDefault();
    var email = document.getElementById('email').value.trim();
    var password = document.getElementById('password').value;
    if (!email || !password) { setMsg('Email and password required.', 'error'); return; }
    if (registering) {
      var confirm = document.getElementById('confirm-password').value;
      if (password !== confirm) { setMsg('Passwords do not match.', 'error'); return; }
    }
    setMsg('Working…');
    fetch('/api/v1/auth/email/' + (registering ? 'register' : 'login'), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email: email, password: password }),
    }).then(function (res) {
      return res.json().then(function (body) { return { ok: res.ok, body: body }; });
    }).then(function (r) {
      if (!r.ok) {
        setMsg((r.body && (r.body.error || r.body.message)) || 'Sign-in failed.', 'error');
        return;
      }
      var frag = 'sso_token=' + encodeURIComponent(r.body.token) +
        '&player_id=' + encodeURIComponent(r.body.player_id) +
        '&display_name=' + encodeURIComponent(r.body.display_name || '');
      window.location.href = redirectURI + '#' + frag;
    }).catch(function (e) {
      setMsg(e.message || 'Sign-in failed.', 'error');
    });
  });
</script>
</body>
</html>
`))
