package handlers

// gfd_items_page.go — the real Item Builder UI (ITEM_BUILDER_NORTHSTAR.md Phase 2a), same
// cream/gold "ceremony" style shared with /admin/kanban and /portal, and the same
// list-then-edit-inline pattern that page already established.

import "net/http"

// GfdItemsPageHandler serves the real Item Builder admin page at /admin/gfd-items.
type GfdItemsPageHandler struct{}

func (h *GfdItemsPageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(gfdItemsPageHTML))
}

const gfdItemsPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>GFD Item Builder</title>
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
  th, td { text-align: left; padding: 0.5rem 0.75rem; border-bottom: 1px solid var(--line); font-size: 0.9rem; }
  th { background: #f3ecd9; }
  tr:hover { background: #fbf8f0; }
  button { font-family: inherit; cursor: pointer; border-radius: 4px; border: 1px solid var(--gold); background: var(--gold); color: #fff; padding: 0.3rem 0.7rem; font-size: 0.85rem; }
  button.secondary { background: transparent; color: var(--gold); }
  button.danger { background: var(--danger); border-color: var(--danger); }
  input, select, textarea { font-family: inherit; padding: 0.4rem 0.5rem; border: 1px solid var(--line); border-radius: 4px; width: 100%; font-size: 0.9rem; }
  label { display: block; font-size: 0.8rem; color: #7a6f5a; margin: 0.6rem 0 0.2rem; }
  .form-card { background: var(--card); border: 1px solid var(--line); border-radius: 8px; padding: 1.25rem; margin-bottom: 1.5rem; }
  .grid2 { display: grid; grid-template-columns: 1fr 1fr; gap: 0 1rem; }
  .msg { font-size: 0.85rem; margin-top: 0.5rem; }
  .msg.error { color: var(--danger); }
  .msg.ok { color: var(--ok); }
  [hidden] { display: none !important; }
</style>
</head>
<body>
<header>
  <h1>GFD Item Builder</h1>
  <div class="sub"><a href="/admin">← Back Office</a> — creates/edits real entries in GoblinFoxDragon's own data/items.json</div>
</header>
<main>
  <div class="warning">
    <strong>Real, honest limitation:</strong> the live MUD server only loads items.json once, at
    startup — new items and edits here take effect the next time that process restarts, not
    immediately. NPC vendor shop offerings (which items an NPC actually sells) aren't managed
    here yet — that catalog is still hardcoded in Go source, a real, separate prerequisite.
  </div>

  <div class="form-card">
    <h3 style="margin-top:0">Batch propose (Vertex AI)</h3>
    <p style="font-size:0.85rem;color:#7a6f5a;margin-top:0">
      One item name per line. Stats/category/jobs are AI-hallucinated from the item name alone —
      review every proposal below before approving it. Nothing is added to the real item table
      until you explicitly approve a row.
    </p>
    <label>Item names (one per line)</label>
    <textarea id="propose-names" rows="5" placeholder="Griffon Claymore
Sunken Cuirass
Ancient Bow of the Vale"></textarea>
    <div class="grid2" style="margin-top:0.5rem">
      <div>
        <label>Min level</label>
        <input type="number" id="propose-min-level" value="1">
      </div>
      <div>
        <label>Max level</label>
        <input type="number" id="propose-max-level" value="75">
      </div>
    </div>
    <div style="margin-top:0.75rem">
      <button id="propose-btn">Go</button>
    </div>
    <div class="msg" id="propose-msg"></div>
  </div>

  <div class="form-card" id="queue-card" hidden>
    <h3 style="margin-top:0">Review queue</h3>
    <p style="font-size:0.85rem;color:#7a6f5a;margin-top:0">
      Edit any field before approving. Approving runs the exact same validation as "Add new
      item" below. Rejecting keeps the row for audit but never touches items.json.
    </p>
    <div id="queue-body"></div>
  </div>

  <div class="form-card">
    <h3 id="form-title" style="margin-top:0">Add new item</h3>
    <div class="grid2">
      <div>
        <label>ID <span style="font-weight:400">(blank = auto-assign next free id)</span></label>
        <input type="number" id="f-id">
        <label>Name</label>
        <input type="text" id="f-name">
        <label>Category</label>
        <select id="f-category"></select>
        <label>Level</label>
        <input type="number" id="f-level" value="0">
        <label>Stack Size</label>
        <input type="number" id="f-stack" value="1">
        <label>Delay <span style="font-weight:400">(weapons only -- attack speed in delay-units, 60 ≈ 1s; blank for non-weapons)</span></label>
        <input type="number" id="f-delay" placeholder="e.g. 240 for a typical 1H sword">
      </div>
      <div>
        <label>Equip Slots <span style="font-weight:400">(comma-separated, blank if not equippable)</span></label>
        <input type="text" id="f-slots" placeholder="body, head">
        <label>Jobs <span style="font-weight:400">(comma-separated, blank = all jobs)</span></label>
        <input type="text" id="f-jobs" placeholder="WAR, PLD">
        <label>Model ID</label>
        <input type="text" id="f-model">
        <label>Flags <span style="font-weight:400">(comma-separated)</span></label>
        <input type="text" id="f-flags" placeholder="rare, ex">
      </div>
    </div>
    <label>Stats <span style="font-weight:400">(JSON object, e.g. {"attack":10,"str":1})</span></label>
    <textarea id="f-stats" rows="2">{}</textarea>
    <div style="margin-top:0.75rem">
      <button id="save-btn">Save</button>
      <button class="secondary" id="cancel-btn" hidden>Cancel edit</button>
    </div>
    <div class="msg" id="form-msg"></div>
  </div>

  <table id="items-table">
    <thead><tr><th>ID</th><th>Name</th><th>Category</th><th>Level</th><th>Delay</th><th>Slots</th><th>Model</th><th></th></tr></thead>
    <tbody id="items-body"></tbody>
  </table>
</main>
<script>
let editingID = null;

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

function splitCsv(s) {
  return s.split(',').map(x => x.trim()).filter(x => x.length > 0);
}

function populateCategories() {
  const sel = document.getElementById('f-category');
  ['weapon','armor','accessory','consumable','material','crystal','key_item','temporary'].forEach(c => {
    const opt = document.createElement('option');
    opt.value = c; opt.textContent = c;
    sel.appendChild(opt);
  });
}

function formToItem() {
  let stats;
  try {
    stats = JSON.parse(document.getElementById('f-stats').value || '{}');
  } catch (e) {
    throw new Error('Stats must be valid JSON: ' + e.message);
  }
  const idVal = document.getElementById('f-id').value;
  const delayVal = document.getElementById('f-delay').value;
  return {
    id: idVal ? parseInt(idVal, 10) : 0,
    name: document.getElementById('f-name').value.trim(),
    category: document.getElementById('f-category').value,
    level: parseInt(document.getElementById('f-level').value || '0', 10),
    stack_size: parseInt(document.getElementById('f-stack').value || '1', 10),
    delay: delayVal ? parseInt(delayVal, 10) : 0,
    equip_slots: splitCsv(document.getElementById('f-slots').value),
    jobs: splitCsv(document.getElementById('f-jobs').value),
    model_id: document.getElementById('f-model').value.trim(),
    flags: splitCsv(document.getElementById('f-flags').value),
    stats: stats,
  };
}

function itemToForm(item) {
  document.getElementById('f-id').value = item.id;
  document.getElementById('f-name').value = item.name || '';
  document.getElementById('f-category').value = item.category || 'weapon';
  document.getElementById('f-level').value = item.level || 0;
  document.getElementById('f-stack').value = item.stack_size || 1;
  document.getElementById('f-delay').value = item.delay || '';
  document.getElementById('f-slots').value = (item.equip_slots || []).join(', ');
  document.getElementById('f-jobs').value = (item.jobs || []).join(', ');
  document.getElementById('f-model').value = item.model_id || '';
  document.getElementById('f-flags').value = (item.flags || []).join(', ');
  document.getElementById('f-stats').value = JSON.stringify(item.stats || {});
}

function clearForm() {
  editingID = null;
  document.getElementById('form-title').textContent = 'Add new item';
  document.getElementById('cancel-btn').hidden = true;
  ['f-id','f-name','f-slots','f-jobs','f-model','f-flags','f-delay'].forEach(id => document.getElementById(id).value = '');
  document.getElementById('f-level').value = 0;
  document.getElementById('f-stack').value = 1;
  document.getElementById('f-stats').value = '{}';
  document.getElementById('f-category').selectedIndex = 0;
}

function setMsg(text, kind) {
  const el = document.getElementById('form-msg');
  el.textContent = text || '';
  el.className = 'msg' + (kind ? ' ' + kind : '');
}

async function loadItems() {
  const items = await api('/admin/gfd-items/api/items');
  const tbody = document.getElementById('items-body');
  tbody.innerHTML = '';
  items.forEach(item => {
    const tr = document.createElement('tr');
    const slots = (item.equip_slots || []).join(', ');
    tr.innerHTML =
      '<td>' + item.id + '</td>' +
      '<td>' + escapeHtml(item.name) + '</td>' +
      '<td>' + escapeHtml(item.category) + '</td>' +
      '<td>' + (item.level || 0) + '</td>' +
      '<td>' + (item.delay || '') + '</td>' +
      '<td>' + escapeHtml(slots) + '</td>' +
      '<td>' + escapeHtml(item.model_id || '') + '</td>';
    const tdActions = document.createElement('td');
    const editBtn = document.createElement('button');
    editBtn.className = 'secondary';
    editBtn.textContent = 'Edit';
    editBtn.onclick = () => { editingID = item.id; itemToForm(item); document.getElementById('form-title').textContent = 'Edit item #' + item.id; document.getElementById('cancel-btn').hidden = false; window.scrollTo(0, 0); };
    const delBtn = document.createElement('button');
    delBtn.className = 'danger';
    delBtn.textContent = 'Delete';
    delBtn.style.marginLeft = '0.4rem';
    delBtn.onclick = async () => {
      if (!confirm('Delete "' + item.name + '" (id ' + item.id + ')?')) return;
      try { await api('/admin/gfd-items/api/items/' + item.id, { method: 'DELETE' }); await loadItems(); }
      catch (e) { alert(e.message); }
    };
    tdActions.appendChild(editBtn);
    tdActions.appendChild(delBtn);
    tr.appendChild(tdActions);
    tbody.appendChild(tr);
  });
}

function escapeHtml(s) {
  const d = document.createElement('div');
  d.textContent = s == null ? '' : String(s);
  return d.innerHTML;
}

document.getElementById('save-btn').addEventListener('click', async () => {
  setMsg('Saving…');
  try {
    const item = formToItem();
    if (!item.name) { setMsg('Name is required.', 'error'); return; }
    if (editingID != null) {
      await api('/admin/gfd-items/api/items/' + editingID, { method: 'PATCH', body: JSON.stringify(item) });
      setMsg('Updated.', 'ok');
    } else {
      await api('/admin/gfd-items/api/items', { method: 'POST', body: JSON.stringify(item) });
      setMsg('Created.', 'ok');
    }
    clearForm();
    await loadItems();
  } catch (e) {
    setMsg(e.message, 'error');
  }
});
document.getElementById('cancel-btn').addEventListener('click', () => { clearForm(); setMsg(''); });

// ---- Batch propose + review queue (Vertex AI assistant) ----

function setProposeMsg(text, kind) {
  const el = document.getElementById('propose-msg');
  el.textContent = text || '';
  el.className = 'msg' + (kind ? ' ' + kind : '');
}

document.getElementById('propose-btn').addEventListener('click', async () => {
  const names = document.getElementById('propose-names').value
    .split('\n').map(s => s.trim()).filter(s => s.length > 0);
  if (names.length === 0) { setProposeMsg('Enter at least one item name.', 'error'); return; }
  const minLevel = parseInt(document.getElementById('propose-min-level').value || '1', 10);
  const maxLevel = parseInt(document.getElementById('propose-max-level').value || '75', 10);

  setProposeMsg('Generating ' + names.length + ' proposal(s) via Vertex AI — this calls the model once per name, sequentially, so it may take a while…');
  document.getElementById('propose-btn').disabled = true;
  try {
    await api('/admin/gfd-items/api/proposals', {
      method: 'POST',
      body: JSON.stringify({ item_names: names, level_range: [minLevel, maxLevel] }),
    });
    document.getElementById('propose-names').value = '';
    setProposeMsg('Done. Review the proposals below.', 'ok');
    await loadQueue();
  } catch (e) {
    setProposeMsg(e.message, 'error');
  } finally {
    document.getElementById('propose-btn').disabled = false;
  }
});

async function loadQueue() {
  const proposals = await api('/admin/gfd-items/api/proposals?status=pending');
  const card = document.getElementById('queue-card');
  const body = document.getElementById('queue-body');
  body.innerHTML = '';
  if (proposals.length === 0) {
    card.hidden = true;
    return;
  }
  card.hidden = false;
  proposals.forEach(p => body.appendChild(renderQueueRow(p)));
}

function renderQueueRow(p) {
  const item = p.proposed_item || {};
  const row = document.createElement('div');
  row.style.cssText = 'border:1px solid var(--line); border-radius:6px; padding:0.75rem; margin-bottom:0.75rem; background:#fbf8f0';
  row.innerHTML =
    '<div style="font-size:0.8rem;color:#7a6f5a;margin-bottom:0.4rem">Proposed from: "' + escapeHtml(p.item_name) + '"</div>' +
    '<div class="grid2">' +
      '<div>' +
        '<label>Name</label><input type="text" class="q-name" value="' + escapeHtml(item.name || '') + '">' +
        '<label>Category</label><select class="q-category"></select>' +
        '<label>Level</label><input type="number" class="q-level" value="' + (item.level || 0) + '">' +
        '<label>Stack Size</label><input type="number" class="q-stack" value="' + (item.stack_size || 1) + '">' +
        '<label>Delay (weapons only)</label><input type="number" class="q-delay" value="' + (item.delay || '') + '">' +
      '</div>' +
      '<div>' +
        '<label>Equip Slots (comma-separated)</label><input type="text" class="q-slots" value="' + escapeHtml((item.equip_slots || []).join(', ')) + '">' +
        '<label>Jobs (comma-separated)</label><input type="text" class="q-jobs" value="' + escapeHtml((item.jobs || []).join(', ')) + '">' +
        '<label>Model ID</label><input type="text" class="q-model" value="' + escapeHtml(item.model_id || '') + '">' +
        '<label>Description</label><input type="text" class="q-desc" value="' + escapeHtml(item.description || '') + '">' +
      '</div>' +
    '</div>' +
    '<label>Stats (JSON)</label><textarea class="q-stats" rows="1">' + escapeHtml(JSON.stringify(item.stats || {})) + '</textarea>' +
    '<div style="margin-top:0.6rem">' +
      '<button class="q-approve">Approve</button> ' +
      '<button class="secondary q-save">Save edits</button> ' +
      '<button class="danger q-reject">Reject</button>' +
    '</div>' +
    '<div class="msg q-msg"></div>';

  const catSel = row.querySelector('.q-category');
  ['weapon','armor','accessory','consumable','material','crystal','key_item','temporary'].forEach(c => {
    const opt = document.createElement('option');
    opt.value = c; opt.textContent = c;
    if (c === item.category) opt.selected = true;
    catSel.appendChild(opt);
  });

  const readRowItem = () => {
    let stats;
    try { stats = JSON.parse(row.querySelector('.q-stats').value || '{}'); }
    catch (e) { throw new Error('Stats must be valid JSON: ' + e.message); }
    return {
      name: row.querySelector('.q-name').value.trim(),
      category: row.querySelector('.q-category').value,
      level: parseInt(row.querySelector('.q-level').value || '0', 10),
      stack_size: parseInt(row.querySelector('.q-stack').value || '1', 10),
      delay: row.querySelector('.q-delay').value ? parseInt(row.querySelector('.q-delay').value, 10) : 0,
      equip_slots: splitCsv(row.querySelector('.q-slots').value),
      jobs: splitCsv(row.querySelector('.q-jobs').value),
      model_id: row.querySelector('.q-model').value.trim(),
      description: row.querySelector('.q-desc').value.trim(),
      stats: stats,
    };
  };
  const rowMsg = (text, kind) => {
    const el = row.querySelector('.q-msg');
    el.textContent = text || '';
    el.className = 'msg q-msg' + (kind ? ' ' + kind : '');
  };

  row.querySelector('.q-save').addEventListener('click', async () => {
    try {
      await api('/admin/gfd-items/api/proposals/' + p.id, { method: 'PATCH', body: JSON.stringify(readRowItem()) });
      rowMsg('Saved.', 'ok');
    } catch (e) { rowMsg(e.message, 'error'); }
  });
  row.querySelector('.q-approve').addEventListener('click', async () => {
    try {
      await api('/admin/gfd-items/api/proposals/' + p.id, { method: 'PATCH', body: JSON.stringify(readRowItem()) });
      await api('/admin/gfd-items/api/proposals/' + p.id + '/approve', { method: 'POST' });
      await loadQueue();
      await loadItems();
    } catch (e) { rowMsg(e.message, 'error'); }
  });
  row.querySelector('.q-reject').addEventListener('click', async () => {
    if (!confirm('Reject this proposal? It stays in the audit log but nothing is added.')) return;
    try {
      await api('/admin/gfd-items/api/proposals/' + p.id + '/reject', { method: 'POST' });
      await loadQueue();
    } catch (e) { rowMsg(e.message, 'error'); }
  });

  return row;
}

populateCategories();
loadItems().catch(e => setMsg(e.message, 'error'));
loadQueue().catch(e => setProposeMsg(e.message, 'error'));
</script>
</body>
</html>`
