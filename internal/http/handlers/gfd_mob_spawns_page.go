package handlers

// gfd_mob_spawns_page.go — the real Mob Spawns manager UI (GFD-MOBSPAWN-001 Phase 3), same
// cream/gold ceremony style and list-then-edit-inline pattern the other GFD admin pages use.
//
// GFD-NM-124 ("actual web interface for modifying the notorious monsters... keep it simple ish"):
// each rule's real, optional NM association (server/spawn.NMRule, GFD-NM-123) is now editable
// here too -- a real Add/Edit NM button (plain prompt()-based fields, matching the "simple ish"
// framing rather than a full modal form) plus a Clear NM button. Real, honest, explicitly NOT
// built here: the card's other half, "allow plugins call special abilities via special named
// functions" -- there is no generic special-ability-hook mechanism for ANY mob today (regular or
// NM), and building one just for NMs would be a redundant, NM-only one-off instead of the real,
// general mod-event-broker mechanism GFD-x-123/GFD-x-124 (next in the priority queue) are
// already about to scope -- flagged there, not duplicated here.

import "net/http"

// GfdMobSpawnsPageHandler serves the real Mob Spawns admin page at /admin/gfd-mob-spawns.
type GfdMobSpawnsPageHandler struct{}

func (h *GfdMobSpawnsPageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(gfdMobSpawnsPageHTML))
}

const gfdMobSpawnsPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>GFD Mob Spawns</title>
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
  th, td { text-align: left; padding: 0.5rem 0.75rem; border-bottom: 1px solid var(--line); font-size: 0.9rem; }
  th { background: #f3ecd9; }
  tr:hover { background: #fbf8f0; }
  button { font-family: inherit; cursor: pointer; border-radius: 4px; border: 1px solid var(--gold); background: var(--gold); color: #fff; padding: 0.3rem 0.7rem; font-size: 0.85rem; }
  button.secondary { background: transparent; color: var(--gold); }
  button.danger { background: var(--danger); border-color: var(--danger); }
  input, select { font-family: inherit; padding: 0.4rem 0.5rem; border: 1px solid var(--line); border-radius: 4px; width: 100%; font-size: 0.9rem; }
  label { display: block; font-size: 0.8rem; color: #7a6f5a; margin: 0.6rem 0 0.2rem; }
  .form-card { background: var(--card); border: 1px solid var(--line); border-radius: 8px; padding: 1.25rem; margin-bottom: 1.5rem; }
  .grid3 { display: grid; grid-template-columns: 1fr 1fr 1fr; gap: 0 1rem; }
  .msg { font-size: 0.85rem; margin-top: 0.5rem; }
  .msg.error { color: var(--danger); }
  .msg.ok { color: var(--ok); }
  .toggle { width: auto; }
  .pill { display: inline-block; padding: 0.1rem 0.5rem; border-radius: 999px; font-size: 0.78rem; font-weight: 600; }
  .pill.on { background: #e3f0e3; color: var(--ok); }
  .pill.off { background: #f6e3e0; color: var(--danger); }
</style>
</head>
<body>
<header>
  <h1>GFD Mob Spawns</h1>
  <div class="sub"><a href="/admin">← Back Office</a> · <a href="/admin/gfd-mob-drops">Mob Drops</a> · <a href="/admin/gfd-items">Items</a> · <a href="/admin/gfd-dungeon-roster">Dungeon Roster</a> — edits GoblinFoxDragon's own data/mob_spawns.json</div>
</header>
<main>
  <div class="warning">
    <strong>Real, honest scope:</strong> this is the on/off toggle only — whether a mob Kind is
    allowed to spawn in a zone at all. Exact spawn positions/counts still live in
    GoblinFoxDragon's own Go source (each Kind's <code>*Spawns()</code> constructor); a real
    per-grid-cell placement editor is separate, not-yet-built follow-up. The live MUD server
    only loads this file once, at startup — edits here take effect on its next restart.
  </div>

  <div class="form-card">
    <h3 id="form-title" style="margin-top:0">Add rule</h3>
    <div class="grid3">
      <div>
        <label>Zone</label>
        <select id="f-zone"></select>
      </div>
      <div>
        <label>Mob kind <span style="font-weight:400">(e.g. rabbit, cave-bat)</span></label>
        <input type="text" id="f-kind" placeholder="rabbit">
      </div>
      <div>
        <label>Enabled</label>
        <select id="f-enabled">
          <option value="true">Enabled</option>
          <option value="false">Disabled</option>
        </select>
      </div>
    </div>
    <div style="margin-top:0.75rem">
      <button id="save-btn">Save</button>
    </div>
    <div class="msg" id="form-msg"></div>
  </div>

  <table id="rules-table">
    <thead><tr><th>Zone</th><th>Kind</th><th>Status</th><th>Notorious Monster</th><th></th></tr></thead>
    <tbody id="rules-body"></tbody>
  </table>
</main>
<script>
const ZONES = { 0: 'Meadow', 1: 'Hills', 2: 'Caves', 3: 'Swampville', 4: 'New Handington' };

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

function populateZones() {
  const sel = document.getElementById('f-zone');
  Object.keys(ZONES).forEach(id => {
    const opt = document.createElement('option');
    opt.value = id; opt.textContent = ZONES[id] + ' (' + id + ')';
    sel.appendChild(opt);
  });
}

function setMsg(text, kind) {
  const el = document.getElementById('form-msg');
  el.textContent = text || '';
  el.className = 'msg' + (kind ? ' ' + kind : '');
}

async function loadRules() {
  const rules = await api('/admin/gfd-mob-spawns/api/rules');
  const tbody = document.getElementById('rules-body');
  tbody.innerHTML = '';
  rules.forEach(rule => {
    const tr = document.createElement('tr');
    const zoneName = ZONES[rule.zone_id] || ('Zone ' + rule.zone_id);
    tr.innerHTML =
      '<td>' + escapeHtml(zoneName) + ' (' + rule.zone_id + ')</td>' +
      '<td>' + escapeHtml(rule.kind) + '</td>' +
      '<td><span class="pill ' + (rule.enabled ? 'on' : 'off') + '">' + (rule.enabled ? 'Enabled' : 'Disabled') + '</span></td>' +
      '<td>' + (rule.nm
        ? escapeHtml(rule.nm.id) + ' <span style="color:#7a6f5a;font-size:0.8rem">(' + Math.round(rule.nm.spawn_chance * 100) + '% · ' + rule.nm.respawn_minutes + 'min respawn)</span>'
        : '<span style="color:#7a6f5a">— none —</span>') + '</td>';
    const tdActions = document.createElement('td');
    const toggleBtn = document.createElement('button');
    toggleBtn.className = 'secondary';
    toggleBtn.textContent = rule.enabled ? 'Disable' : 'Enable';
    toggleBtn.onclick = async () => {
      try {
        // nm: rule.nm (not omitted) -- PATCH always sets both fields together (see the
        // handler's own doc comment), so a plain enable/disable toggle must re-send any
        // existing NM or it would silently get cleared.
        await api('/admin/gfd-mob-spawns/api/rules/' + rule.zone_id + '/' + encodeURIComponent(rule.kind), {
          method: 'PATCH', body: JSON.stringify({ enabled: !rule.enabled, nm: rule.nm || null }),
        });
        await loadRules();
      } catch (e) { alert(e.message); }
    };
    const nmBtn = document.createElement('button');
    nmBtn.className = 'secondary';
    nmBtn.style.marginLeft = '0.4rem';
    nmBtn.textContent = rule.nm ? 'Edit NM' : 'Add NM';
    nmBtn.onclick = async () => {
      const cur = rule.nm || { id: '', spawn_chance: 0.25, window_open_sec: 300, window_close_sec: 1800, respawn_minutes: 0 };
      const id = prompt('NM mob ID (e.g. nm-slime-king):', cur.id);
      if (id === null) return; // cancelled
      if (!id.trim()) { alert('NM ID is required — use "Clear NM" instead to remove it.'); return; }
      const chance = parseFloat(prompt('Spawn chance per check, 0.0-1.0:', cur.spawn_chance));
      const openSec = parseInt(prompt('Window opens N seconds after the placeholder dies:', cur.window_open_sec), 10);
      const closeSec = parseInt(prompt('Window closes N seconds after the placeholder dies:', cur.window_close_sec), 10);
      const respawn = parseInt(prompt('Respawn minutes after the NM is killed (0 = no respawn):', cur.respawn_minutes), 10);
      if ([chance, openSec, closeSec, respawn].some(Number.isNaN)) { alert('All fields must be real numbers.'); return; }
      try {
        await api('/admin/gfd-mob-spawns/api/rules/' + rule.zone_id + '/' + encodeURIComponent(rule.kind), {
          method: 'PATCH', body: JSON.stringify({ enabled: rule.enabled, nm: {
            id: id.trim(), spawn_chance: chance, window_open_sec: openSec, window_close_sec: closeSec, respawn_minutes: respawn,
          } }),
        });
        await loadRules();
      } catch (e) { alert(e.message); }
    };
    tdActions.appendChild(nmBtn);
    if (rule.nm) {
      const clearNmBtn = document.createElement('button');
      clearNmBtn.className = 'secondary';
      clearNmBtn.style.marginLeft = '0.4rem';
      clearNmBtn.textContent = 'Clear NM';
      clearNmBtn.onclick = async () => {
        if (!confirm('Remove the ' + rule.nm.id + ' Notorious Monster from ' + rule.kind + ' in ' + zoneName + '?')) return;
        try {
          await api('/admin/gfd-mob-spawns/api/rules/' + rule.zone_id + '/' + encodeURIComponent(rule.kind), {
            method: 'PATCH', body: JSON.stringify({ enabled: rule.enabled }), // omits "nm" -> cleared, see handler's own doc comment
          });
          await loadRules();
        } catch (e) { alert(e.message); }
      };
      tdActions.appendChild(clearNmBtn);
    }
    const delBtn = document.createElement('button');
    delBtn.className = 'danger';
    delBtn.textContent = 'Delete';
    delBtn.style.marginLeft = '0.4rem';
    delBtn.onclick = async () => {
      if (!confirm('Delete this rule? ' + rule.kind + ' will revert to default-enabled in ' + zoneName + '.')) return;
      try {
        await api('/admin/gfd-mob-spawns/api/rules/' + rule.zone_id + '/' + encodeURIComponent(rule.kind), { method: 'DELETE' });
        await loadRules();
      } catch (e) { alert(e.message); }
    };
    tdActions.appendChild(toggleBtn);
    tdActions.appendChild(delBtn);
    tr.appendChild(tdActions);
    tbody.appendChild(tr);
  });
}

document.getElementById('save-btn').addEventListener('click', async () => {
  setMsg('Saving…');
  const rule = {
    zone_id: parseInt(document.getElementById('f-zone').value, 10),
    kind: document.getElementById('f-kind').value.trim(),
    enabled: document.getElementById('f-enabled').value === 'true',
  };
  if (!rule.kind) { setMsg('Mob kind is required.', 'error'); return; }
  try {
    await api('/admin/gfd-mob-spawns/api/rules', { method: 'POST', body: JSON.stringify(rule) });
    document.getElementById('f-kind').value = '';
    setMsg('Created.', 'ok');
    await loadRules();
  } catch (e) {
    setMsg(e.message, 'error');
  }
});

populateZones();
loadRules().catch(e => setMsg(e.message, 'error'));
</script>
</body>
</html>`
