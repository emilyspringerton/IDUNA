package handlers

// gfd_mob_drops_page.go — the real Mob Drops manager UI (kanban GFD-MD-001), same cream/gold
// "ceremony" style and list-then-edit-inline pattern gfd_items_page.go already established.
// Deliberately no Vertex batch-propose assistant here -- that was a real, separate, explicit
// ask scoped to items specifically; not requested for mob drops, not added speculatively.

import "net/http"

// GfdMobDropsPageHandler serves the real Mob Drops admin page at /admin/gfd-mob-drops.
type GfdMobDropsPageHandler struct{}

func (h *GfdMobDropsPageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(gfdMobDropsPageHTML))
}

const gfdMobDropsPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>GFD Mob Drops</title>
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
  main { padding: 1.5rem 2rem; max-width: 1200px; margin: 0 auto; }
  .warning { background: #fff3e0; border: 1px solid #e0b877; border-radius: 6px; padding: 0.75rem 1rem; margin-bottom: 1.5rem; font-size: 0.9rem; }
  table { width: 100%; border-collapse: collapse; background: var(--card); border: 1px solid var(--line); }
  th, td { text-align: left; padding: 0.5rem 0.75rem; border-bottom: 1px solid var(--line); font-size: 0.9rem; vertical-align: top; }
  th { background: #f3ecd9; }
  tr:hover { background: #fbf8f0; }
  button { font-family: inherit; cursor: pointer; border-radius: 4px; border: 1px solid var(--gold); background: var(--gold); color: #fff; padding: 0.3rem 0.7rem; font-size: 0.85rem; }
  button.secondary { background: transparent; color: var(--gold); }
  button.danger { background: var(--danger); border-color: var(--danger); }
  input, select, textarea { font-family: inherit; padding: 0.4rem 0.5rem; border: 1px solid var(--line); border-radius: 4px; width: 100%; font-size: 0.9rem; }
  label { display: block; font-size: 0.8rem; color: #7a6f5a; margin: 0.6rem 0 0.2rem; }
  .form-card { background: var(--card); border: 1px solid var(--line); border-radius: 8px; padding: 1.25rem; margin-bottom: 1.5rem; }
  .msg { font-size: 0.85rem; margin-top: 0.5rem; }
  .msg.error { color: var(--danger); }
  .msg.ok { color: var(--ok); }
  .drop-item-row { display: flex; gap: 0.5rem; margin-bottom: 0.4rem; }
  .drop-item-row input { flex: 1; }
  [hidden] { display: none !important; }
</style>
</head>
<body>
<header>
  <h1>GFD Mob Drops</h1>
  <div class="sub"><a href="/admin">← Back Office</a> · <a href="/admin/gfd-items">Item Builder</a> — edits GoblinFoxDragon's own data/mob_drops.json</div>
</header>
<main>
  <div class="warning">
    <strong>Real, honest limitations:</strong> the live MUD server only loads mob_drops.json once,
    at startup — edits here take effect the next time that process restarts, not immediately.
    A drop table is keyed by mob <em>kind</em> only, not by (kind, zone) — every mob of a given
    kind drops from the same table everywhere it spawns; GFD's mob spawn code doesn't track a
    zone as a property of a kind, so a real per-zone override isn't representable yet.
  </div>

  <div class="form-card">
    <h3 id="form-title" style="margin-top:0">Add drop table</h3>
    <label>Mob kind <span style="font-weight:400">(e.g. worm, cave-bat, King Worm for a boss/NM)</span></label>
    <input type="text" id="f-kind" placeholder="worm">
    <label>Drops</label>
    <div id="f-items"></div>
    <button class="secondary" id="add-item-btn" type="button">+ Add drop item</button>
    <div style="margin-top:0.75rem">
      <button id="save-btn">Save</button>
      <button class="secondary" id="cancel-btn" hidden>Cancel edit</button>
    </div>
    <div class="msg" id="form-msg"></div>
  </div>

  <table id="tables-table">
    <thead><tr><th>Kind</th><th>Drops</th><th></th></tr></thead>
    <tbody id="tables-body"></tbody>
  </table>
</main>
<script>
let editingKind = null;

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

function addItemRow(id, name) {
  const wrap = document.getElementById('f-items');
  const row = document.createElement('div');
  row.className = 'drop-item-row';
  row.innerHTML =
    '<input type="text" class="fi-id" placeholder="item id (e.g. worm-sinew)" value="' + escapeHtml(id || '') + '">' +
    '<input type="text" class="fi-name" placeholder="display name (e.g. Worm Sinew)" value="' + escapeHtml(name || '') + '">' +
    '<button type="button" class="danger fi-remove">×</button>';
  row.querySelector('.fi-remove').addEventListener('click', () => row.remove());
  wrap.appendChild(row);
}

function formToTable() {
  const kind = document.getElementById('f-kind').value.trim();
  const items = [];
  document.querySelectorAll('#f-items .drop-item-row').forEach(row => {
    const id = row.querySelector('.fi-id').value.trim();
    const name = row.querySelector('.fi-name').value.trim();
    if (id) items.push({ id: id, name: name || id });
  });
  return { kind: kind, items: items };
}

function tableToForm(t) {
  document.getElementById('f-kind').value = t.kind;
  document.getElementById('f-items').innerHTML = '';
  (t.items || []).forEach(it => addItemRow(it.id, it.name));
  if ((t.items || []).length === 0) addItemRow();
}

function clearForm() {
  editingKind = null;
  document.getElementById('form-title').textContent = 'Add drop table';
  document.getElementById('cancel-btn').hidden = true;
  document.getElementById('f-kind').value = '';
  document.getElementById('f-items').innerHTML = '';
  addItemRow();
}

function setMsg(text, kind) {
  const el = document.getElementById('form-msg');
  el.textContent = text || '';
  el.className = 'msg' + (kind ? ' ' + kind : '');
}

async function loadTables() {
  const tables = await api('/admin/gfd-mob-drops/api/tables');
  const tbody = document.getElementById('tables-body');
  tbody.innerHTML = '';
  tables.forEach(t => {
    const tr = document.createElement('tr');
    const dropsStr = (t.items || []).map(it => it.name + ' (' + it.id + ')').join(', ');
    tr.innerHTML =
      '<td>' + escapeHtml(t.kind) + '</td>' +
      '<td>' + escapeHtml(dropsStr) + '</td>';
    const tdActions = document.createElement('td');
    const editBtn = document.createElement('button');
    editBtn.className = 'secondary';
    editBtn.textContent = 'Edit';
    editBtn.onclick = () => { editingKind = t.kind; tableToForm(t); document.getElementById('form-title').textContent = 'Edit drop table: ' + t.kind; document.getElementById('cancel-btn').hidden = false; window.scrollTo(0, 0); };
    const delBtn = document.createElement('button');
    delBtn.className = 'danger';
    delBtn.textContent = 'Delete';
    delBtn.style.marginLeft = '0.4rem';
    delBtn.onclick = async () => {
      if (!confirm('Delete drop table for "' + t.kind + '"? Mobs of this kind will fall back to the default Flow drop.')) return;
      try { await api('/admin/gfd-mob-drops/api/tables/' + encodeURIComponent(t.kind), { method: 'DELETE' }); await loadTables(); }
      catch (e) { alert(e.message); }
    };
    tdActions.appendChild(editBtn);
    tdActions.appendChild(delBtn);
    tr.appendChild(tdActions);
    tbody.appendChild(tr);
  });
}

document.getElementById('add-item-btn').addEventListener('click', () => addItemRow());

document.getElementById('save-btn').addEventListener('click', async () => {
  setMsg('Saving…');
  try {
    const t = formToTable();
    if (!t.kind) { setMsg('Mob kind is required.', 'error'); return; }
    if (editingKind != null) {
      await api('/admin/gfd-mob-drops/api/tables/' + encodeURIComponent(editingKind), { method: 'PATCH', body: JSON.stringify(t) });
      setMsg('Updated.', 'ok');
    } else {
      await api('/admin/gfd-mob-drops/api/tables', { method: 'POST', body: JSON.stringify(t) });
      setMsg('Created.', 'ok');
    }
    clearForm();
    await loadTables();
  } catch (e) {
    setMsg(e.message, 'error');
  }
});
document.getElementById('cancel-btn').addEventListener('click', () => { clearForm(); setMsg(''); });

addItemRow();
loadTables().catch(e => setMsg(e.message, 'error'));
</script>
</body>
</html>`
