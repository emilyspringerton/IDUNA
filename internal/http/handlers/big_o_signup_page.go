// Kanban card 123214231 (founder real-time): "we need a big_o account creation interface off of
// iduna". BIG_O itself has no server or native client yet (NORTHSTAR only, see
// BIG_O/NORTHSTAR.md) -- unlike DEADWEIGHT, whose account-creation UI lives INSIDE its native
// dw_gui client, there is no BIG_O client to put a signup screen in. This is a plain, public,
// unauthenticated web page hosted directly on IDUNA that drives the exact same generic guest-
// account API (internal/http/handlers/game_online.go's guest-register/guest-login/guest-upgrade,
// already wired for every internal/games.Registry entry) so real playtester accounts can start
// existing before the game itself does.
package handlers

import "net/http"

type BigOSignupPageHandler struct{}

func (h *BigOSignupPageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(bigOSignupPageHTML))
}

const bigOSignupPageHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>BIG_O — Create Account</title>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Cormorant+Garamond:wght@400;500;600&family=Spectral:wght@400;500&display=swap" rel="stylesheet">
<style>
  :root {
    --bg: #12141a; --panel: #1b1e27; --line: #333846;
    --gold: #c6a75e; --gold-highlight: #d6bc7a;
    --text-main: #e8e5da; --text-muted: #9a9689; --warn: #d18a5a; --err: #d17a6a;
  }
  * { box-sizing: border-box; }
  body {
    margin: 0; min-height: 100vh; background: var(--bg); color: var(--text-main);
    font-family: "Spectral", Georgia, serif; line-height: 1.5;
  }
  h1 { font-family: "Cormorant Garamond", serif; font-weight: 600; letter-spacing: .04em; font-size: 2.4rem; margin-bottom: .2rem; }
  .tag { color: var(--text-muted); font-size: .85rem; letter-spacing: .08em; text-transform: uppercase; margin-bottom: 2rem; }
  main { max-width: 560px; margin: 0 auto; padding: 3rem 1.5rem 4rem; }
  section {
    background: var(--panel); border: 1px solid var(--line); border-radius: 10px;
    padding: 1.5rem; margin-bottom: 1.5rem;
  }
  section h2 { font-family: "Cormorant Garamond", serif; font-size: 1.4rem; margin: 0 0 .8rem; color: var(--gold-highlight); }
  input {
    width: 100%; font-family: inherit; font-size: .95rem; padding: .6rem .7rem; margin-bottom: .6rem;
    border: 1px solid var(--line); border-radius: 6px; background: #10131a; color: var(--text-main);
  }
  button {
    font-family: inherit; font-size: .9rem; padding: .6rem 1.1rem; border-radius: 6px;
    border: 1px solid var(--gold); background: var(--gold); color: #1c1810; cursor: pointer; font-weight: 600;
  }
  button:hover { background: var(--gold-highlight); }
  button:disabled { opacity: .5; cursor: not-allowed; }
  .warn { color: var(--warn); font-size: .85rem; border-left: 2px solid var(--warn); padding-left: .7rem; margin: .8rem 0; }
  .err { color: var(--err); font-size: .85rem; margin-top: .5rem; }
  .ok { color: var(--gold-highlight); font-size: .85rem; margin-top: .5rem; }
  code { background: #10131a; padding: .1rem .35rem; border-radius: 4px; font-size: .85rem; word-break: break-all; }
  .creds { background: #10131a; border: 1px solid var(--line); border-radius: 6px; padding: .8rem; margin-top: .8rem; }
  .creds div { margin-bottom: .4rem; font-size: .85rem; }
  #claim-section, #logged-in-banner { display: none; }
</style>
</head>
<body>
<main>
  <h1>BIG_O</h1>
  <div class="tag">A SHANKPIT Story — account creation</div>

  <div id="logged-in-banner" class="ok" style="margin-bottom:1.5rem;"></div>

  <section id="create-section">
    <h2>Create Account</h2>
    <input type="text" id="create-name" placeholder="Display name (optional — a name is picked for you if blank)" maxlength="16">
    <button id="create-btn">Create Account</button>
    <div id="create-status"></div>
    <div id="create-creds" class="creds" style="display:none;"></div>
  </section>

  <section id="login-section">
    <h2>Log In</h2>
    <input type="text" id="login-pid" placeholder="Player ID">
    <input type="text" id="login-secret" placeholder="Guest Secret">
    <button id="login-btn">Log In</button>
    <div id="login-status"></div>
  </section>

  <section id="claim-section">
    <h2>Link Email (recommended)</h2>
    <p class="warn">A guest account cannot be recovered if you lose your Player ID / Guest Secret. Linking an email + password lets you log in without them.</p>
    <input type="email" id="claim-email" placeholder="Email">
    <input type="password" id="claim-password" placeholder="Password (min 8 characters)">
    <button id="claim-btn">Link Email</button>
    <div id="claim-status"></div>
  </section>
</main>
<script>
const API = '/api/v1/games/big_o';
let currentToken = null;
let currentPlayerId = null;

function setStatus(el, msg, cls) {
  el.textContent = msg;
  el.className = cls || '';
}
function esc(s) { return String(s ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c])); }

function onLoggedIn(playerId, token, displayName) {
  currentPlayerId = playerId;
  currentToken = token;
  const banner = document.getElementById('logged-in-banner');
  banner.style.display = 'block';
  banner.textContent = 'Logged in as ' + displayName + ' (' + playerId.slice(0, 8) + '…)';
  document.getElementById('claim-section').style.display = 'block';
  try { localStorage.setItem('big_o_player_id', playerId); } catch (e) {}
}

document.getElementById('create-btn').addEventListener('click', async () => {
  const btn = document.getElementById('create-btn');
  const statusEl = document.getElementById('create-status');
  const name = document.getElementById('create-name').value.trim();
  btn.disabled = true;
  try {
    const res = await fetch(API + '/guest-register', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ display_name: name })
    });
    const body = await res.json();
    if (!res.ok) throw new Error(body.error || ('HTTP ' + res.status));
    setStatus(statusEl, 'Account created.', 'ok');
    const credsEl = document.getElementById('create-creds');
    credsEl.style.display = 'block';
    credsEl.innerHTML =
      '<div><strong>Save these now — they cannot be shown again:</strong></div>' +
      '<div>Player ID: <code>' + esc(body.player_id) + '</code></div>' +
      '<div>Guest Secret: <code>' + esc(body.guest_secret) + '</code></div>';
    onLoggedIn(body.player_id, body.token, body.display_name);
  } catch (err) {
    setStatus(statusEl, 'Failed: ' + err.message, 'err');
  } finally {
    btn.disabled = false;
  }
});

document.getElementById('login-btn').addEventListener('click', async () => {
  const btn = document.getElementById('login-btn');
  const statusEl = document.getElementById('login-status');
  const playerId = document.getElementById('login-pid').value.trim();
  const secret = document.getElementById('login-secret').value.trim();
  if (!playerId || !secret) { setStatus(statusEl, 'Player ID and Guest Secret are both required.', 'err'); return; }
  btn.disabled = true;
  try {
    const res = await fetch(API + '/guest-login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ player_id: playerId, guest_secret: secret })
    });
    const body = await res.json();
    if (!res.ok) throw new Error(body.error || ('HTTP ' + res.status));
    setStatus(statusEl, 'Logged in.', 'ok');
    onLoggedIn(body.player_id, body.token, body.display_name);
  } catch (err) {
    setStatus(statusEl, 'Failed: ' + err.message, 'err');
  } finally {
    btn.disabled = false;
  }
});

document.getElementById('claim-btn').addEventListener('click', async () => {
  const btn = document.getElementById('claim-btn');
  const statusEl = document.getElementById('claim-status');
  const email = document.getElementById('claim-email').value.trim();
  const password = document.getElementById('claim-password').value;
  if (!currentToken) { setStatus(statusEl, 'Create an account or log in first.', 'err'); return; }
  btn.disabled = true;
  try {
    const res = await fetch(API + '/guest-upgrade', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Authorization': 'Bearer ' + currentToken },
      body: JSON.stringify({ email, password })
    });
    const body = await res.json();
    if (!res.ok) throw new Error(body.error || ('HTTP ' + res.status));
    setStatus(statusEl, 'Email linked — you can now recover this account with email + password.', 'ok');
  } catch (err) {
    setStatus(statusEl, 'Failed: ' + err.message, 'err');
  } finally {
    btn.disabled = false;
  }
});

try {
  const saved = localStorage.getItem('big_o_player_id');
  if (saved) document.getElementById('login-pid').value = saved;
} catch (e) {}
</script>
</body>
</html>`
