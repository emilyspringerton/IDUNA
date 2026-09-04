package handlers

// gfd_registration_page.go — the real waitlist toggle admin page (kanban GFD-UA-001, second
// half), same cream/gold ceremony style as the other GFD admin pages.

import "net/http"

// GfdRegistrationPageHandler serves the admin page at /admin/gfd-registration.
type GfdRegistrationPageHandler struct{}

func (h *GfdRegistrationPageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(gfdRegistrationPageHTML))
}

const gfdRegistrationPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>GFD Registration</title>
<style>
  :root {
    --cream: #faf6ee; --gold: #b8934a; --ink: #2b2620; --line: #e2d8c3;
    --card: #ffffff; --danger: #b3432b; --ok: #4a7c4e;
  }
  * { box-sizing: border-box; }
  body { margin: 0; background: var(--cream); color: var(--ink); font-family: Georgia, 'Times New Roman', serif; }
  header { padding: 1.5rem 2rem 1rem; border-bottom: 1px solid var(--line); }
  h1 { margin: 0; font-weight: 600; letter-spacing: 0.02em; }
  .sub { color: #7a6f5a; font-size: 0.9rem; margin-top: 0.25rem; }
  .sub a { color: var(--gold); }
  main { padding: 1.5rem 2rem; max-width: 1000px; margin: 0 auto; }
  table { width: 100%; border-collapse: collapse; background: var(--card); border: 1px solid var(--line); }
  th, td { text-align: left; padding: 0.5rem 0.75rem; border-bottom: 1px solid var(--line); font-size: 0.9rem; }
  th { background: #f3ecd9; }
  tr:hover { background: #fbf8f0; }
  button { font-family: inherit; cursor: pointer; border-radius: 4px; border: 1px solid var(--gold); background: var(--gold); color: #fff; padding: 0.3rem 0.7rem; font-size: 0.85rem; }
  button.secondary { background: transparent; color: var(--gold); }
  button:disabled { opacity: 0.5; cursor: default; }
  .form-card { background: var(--card); border: 1px solid var(--line); border-radius: 8px; padding: 1.25rem; margin-bottom: 1.5rem; }
  .msg { font-size: 0.85rem; margin-top: 0.5rem; }
  .msg.error { color: var(--danger); }
  .msg.ok { color: var(--ok); }
  .mode-pill { display: inline-block; padding: 0.15rem 0.6rem; border-radius: 999px; font-size: 0.8rem; font-weight: 600; }
  .mode-pill.open { background: #e3f0e3; color: var(--ok); }
  .mode-pill.waitlist { background: #fff3e0; color: #a06a1a; }
</style>
</head>
<body>
<header>
  <h1>GFD Registration</h1>
  <div class="sub"><a href="/admin">← Back Office</a> — real signup mode toggle + waitlist</div>
</header>
<main>
  <div class="form-card">
    <h3 style="margin-top:0">Registration mode</h3>
    <p style="font-size:0.85rem;color:#7a6f5a">
      Current mode: <span id="mode-pill" class="mode-pill">…</span>
    </p>
    <p style="font-size:0.85rem;color:#7a6f5a">
      <strong>Open</strong> — anyone can register a real GFD account immediately.<br>
      <strong>Waitlist</strong> — the register form still collects email/password/character
      name, but no real account is created until you approve the entry below. The player's
      password is stored hashed at signup time, so approving later never requires them to
      re-register.
    </p>
    <button id="toggle-btn"></button>
    <div class="msg" id="mode-msg"></div>
  </div>

  <div class="form-card">
    <h3 style="margin-top:0">Waitlist</h3>
    <table id="waitlist-table">
      <thead><tr><th>Email</th><th>Display Name</th><th>Character</th><th>Requested</th><th>Status</th><th></th></tr></thead>
      <tbody id="waitlist-body"></tbody>
    </table>
  </div>
</main>
<script>
function api(path, opts) {
  opts = opts || {};
  opts.headers = Object.assign({ 'Content-Type': 'application/json' }, opts.headers || {});
  return fetch(path, opts).then(async res => {
    let body = null;
    try { body = await res.json(); } catch (e) {}
    if (!res.ok) throw new Error((body && (body.error || body.message)) || res.statusText);
    return body;
  });
}

function escapeHtml(s) {
  const d = document.createElement('div');
  d.textContent = s == null ? '' : String(s);
  return d.innerHTML;
}

let currentMode = null;

async function loadMode() {
  const { mode } = await api('/admin/gfd-registration/api/mode');
  currentMode = mode;
  const pill = document.getElementById('mode-pill');
  pill.textContent = mode;
  pill.className = 'mode-pill ' + mode;
  const btn = document.getElementById('toggle-btn');
  btn.textContent = mode === 'open' ? 'Switch to waitlist' : 'Switch to open registration';
}

document.getElementById('toggle-btn').addEventListener('click', async () => {
  const next = currentMode === 'open' ? 'waitlist' : 'open';
  const msg = document.getElementById('mode-msg');
  msg.textContent = ''; msg.className = 'msg';
  try {
    await api('/admin/gfd-registration/api/mode', { method: 'PATCH', body: JSON.stringify({ mode: next }) });
    await loadMode();
    msg.textContent = 'Updated.'; msg.className = 'msg ok';
  } catch (e) {
    msg.textContent = e.message; msg.className = 'msg error';
  }
});

async function loadWaitlist() {
  const entries = await api('/admin/gfd-registration/api/waitlist');
  const tbody = document.getElementById('waitlist-body');
  tbody.innerHTML = '';
  entries.forEach(e => {
    const tr = document.createElement('tr');
    tr.innerHTML =
      '<td>' + escapeHtml(e.email) + '</td>' +
      '<td>' + escapeHtml(e.display_name) + '</td>' +
      '<td>' + escapeHtml(e.character_name || '') + (e.character_job ? ' (' + escapeHtml(e.character_job) + ')' : '') + '</td>' +
      '<td>' + escapeHtml(e.requested_at) + '</td>' +
      '<td>' + (e.approved_at ? 'Approved ' + escapeHtml(e.approved_at) : 'Pending') + '</td>';
    const tdAction = document.createElement('td');
    if (!e.approved_at) {
      const btn = document.createElement('button');
      btn.textContent = 'Approve';
      btn.onclick = async () => {
        btn.disabled = true;
        try {
          await api('/admin/gfd-registration/api/waitlist/' + e.id + '/approve', { method: 'POST' });
          await loadWaitlist();
        } catch (err) {
          alert(err.message);
          btn.disabled = false;
        }
      };
      tdAction.appendChild(btn);
    }
    tr.appendChild(tdAction);
    tbody.appendChild(tr);
  });
}

loadMode().catch(e => { document.getElementById('mode-msg').textContent = e.message; document.getElementById('mode-msg').className = 'msg error'; });
loadWaitlist().catch(e => console.error(e));
</script>
</body>
</html>`
