package handlers

// gfd_dungeon_roster_page.go — the real Dungeon Roster manager UI (GFD-MOBSPAWN-001 Phase 5),
// same cream/gold ceremony style the other three GFD admin pages use.

import "net/http"

// GfdDungeonRosterPageHandler serves the real Dungeon Roster admin page at
// /admin/gfd-dungeon-roster.
type GfdDungeonRosterPageHandler struct{}

func (h *GfdDungeonRosterPageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(gfdDungeonRosterPageHTML))
}

const gfdDungeonRosterPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>GFD Dungeon Roster</title>
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
  .warning { background: #fff3e0; border: 1px solid #e0b877; border-radius: 6px; padding: 0.75rem 1rem; margin-bottom: 1.5rem; font-size: 0.9rem; }
  table { width: 100%; border-collapse: collapse; background: var(--card); border: 1px solid var(--line); margin-bottom: 1.5rem; }
  th, td { text-align: left; padding: 0.5rem 0.75rem; border-bottom: 1px solid var(--line); font-size: 0.9rem; vertical-align: top; }
  th { background: #f3ecd9; }
  tr:hover { background: #fbf8f0; }
  button { font-family: inherit; cursor: pointer; border-radius: 4px; border: 1px solid var(--gold); background: var(--gold); color: #fff; padding: 0.3rem 0.7rem; font-size: 0.85rem; }
  button.secondary { background: transparent; color: var(--gold); }
  button.danger { background: var(--danger); border-color: var(--danger); }
  input { font-family: inherit; padding: 0.4rem 0.5rem; border: 1px solid var(--line); border-radius: 4px; width: 100%; font-size: 0.9rem; }
  label { display: block; font-size: 0.8rem; color: #7a6f5a; margin: 0.6rem 0 0.2rem; }
  .form-card { background: var(--card); border: 1px solid var(--line); border-radius: 8px; padding: 1.25rem; margin-bottom: 1.5rem; }
  .msg { font-size: 0.85rem; margin-top: 0.5rem; }
  .msg.error { color: var(--danger); }
  .msg.ok { color: var(--ok); }
  .idx { color: #7a6f5a; font-size: 0.8rem; }
</style>
</head>
<body>
<header>
  <h1>GFD Dungeon Roster</h1>
  <div class="sub"><a href="/admin">← Back Office</a> · <a href="/admin/gfd-mob-spawns">Mob Spawns</a> — edits GoblinFoxDragon's own data/dungeon_roster.json</div>
</header>
<main>
  <div class="warning">
    <strong>Real, honest limits:</strong> order matters — a run's dungeon is picked by
    <code>index % row count</code>, so adding/removing rows shifts which dungeon every later
    index resolves to. New dungeons are always appended at the end to avoid reshuffling existing
    ones. If this file is ever missing or malformed, the live server falls back to its own
    compiled-in default roster rather than failing to start. The live MUD server only loads this
    file once, at startup.
  </div>

  <div class="form-card">
    <h3 style="margin-top:0">Add dungeon</h3>
    <label>Dungeon name</label>
    <input type="text" id="f-name" placeholder="The Sealed Archive">
    <label>Boss <span style="font-weight:400">(real ARENA_HERO_* identifier)</span></label>
    <input type="text" id="f-boss" placeholder="ARENA_HERO_CART">
    <label>Elites <span style="font-weight:400">(comma-separated ARENA_HERO_* identifiers, blank for none)</span></label>
    <input type="text" id="f-elite" placeholder="ARENA_HERO_NOOR1, ARENA_HERO_GARY">
    <div style="margin-top:0.75rem">
      <button id="save-btn">Add at end</button>
    </div>
    <div class="msg" id="form-msg"></div>
  </div>

  <table id="dungeons-table">
    <thead><tr><th>#</th><th>Name</th><th>Boss</th><th>Elites</th><th></th></tr></thead>
    <tbody id="dungeons-body"></tbody>
  </table>
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

function splitCsv(s) {
  return s.split(',').map(x => x.trim()).filter(x => x.length > 0);
}

function setMsg(text, kind) {
  const el = document.getElementById('form-msg');
  el.textContent = text || '';
  el.className = 'msg' + (kind ? ' ' + kind : '');
}

async function loadDungeons() {
  const roster = await api('/admin/gfd-dungeon-roster/api/dungeons');
  const tbody = document.getElementById('dungeons-body');
  tbody.innerHTML = '';
  roster.forEach((d, i) => {
    const tr = document.createElement('tr');
    tr.innerHTML =
      '<td class="idx">' + i + '</td>' +
      '<td>' + escapeHtml(d.name) + '</td>' +
      '<td>' + escapeHtml(d.boss) + '</td>' +
      '<td>' + escapeHtml((d.elite || []).join(', ')) + '</td>';
    const tdActions = document.createElement('td');
    const delBtn = document.createElement('button');
    delBtn.className = 'danger';
    delBtn.textContent = 'Delete';
    delBtn.onclick = async () => {
      if (!confirm('Delete "' + d.name + '" (index ' + i + ')? Every dungeon index after it will shift.')) return;
      try { await api('/admin/gfd-dungeon-roster/api/dungeons/' + i, { method: 'DELETE' }); await loadDungeons(); }
      catch (e) { alert(e.message); }
    };
    tdActions.appendChild(delBtn);
    tr.appendChild(tdActions);
    tbody.appendChild(tr);
  });
}

document.getElementById('save-btn').addEventListener('click', async () => {
  setMsg('Saving…');
  const d = {
    name: document.getElementById('f-name').value.trim(),
    boss: document.getElementById('f-boss').value.trim(),
    elite: splitCsv(document.getElementById('f-elite').value),
  };
  if (!d.name || !d.boss) { setMsg('Name and boss are required.', 'error'); return; }
  try {
    await api('/admin/gfd-dungeon-roster/api/dungeons', { method: 'POST', body: JSON.stringify(d) });
    document.getElementById('f-name').value = '';
    document.getElementById('f-boss').value = '';
    document.getElementById('f-elite').value = '';
    setMsg('Added.', 'ok');
    await loadDungeons();
  } catch (e) {
    setMsg(e.message, 'error');
  }
});

loadDungeons().catch(e => setMsg(e.message, 'error'));
</script>
</body>
</html>`
