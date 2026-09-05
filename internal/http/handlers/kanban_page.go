package handlers

import (
	"net/http"
)

// KanbanPageHandler serves the GUI half of the kanban prioritization layer
// (see kanban.go's own doc comment for the full founder-quote chain and
// design reasoning): a 3-column board (Backlog | Priority | Cruise) with
// real drag-and-drop, backed by kanban.go's own JSON API at
// /admin/kanban/api/cards (same-origin fetch, cookie-authenticated --
// browser attaches the /admin session cookie automatically, no separate
// bearer token needed here). The CLI/agent path ("i can ask the ai agent
// to work from the priority or cruise backlog") uses the SAME KanbanHandler
// struct mounted separately at /api/v1/kanban/cards under bearer auth --
// see main.go's route wiring, not duplicated logic.
//
// Same cream/gold ceremony style guide as /portal and /admin/login
// (Cormorant Garamond + Spectral, gold-bordered panels) -- copied token
// values directly from portal.go rather than reinvented.
type KanbanPageHandler struct{}

func (h *KanbanPageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// KBUX-CACHE-001 -- see kanban.go's ServeHTTP doc comment for the full real-time
	// report/investigation. The page shell itself had no explicit cache header either;
	// no-store here rules out a stale HTML document (with the "Refresh" button's own
	// no-store fetches below covering the live card data on top of that).
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(kanbanPageHTML))
}

const kanbanPageHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Kanban — Back Office</title>
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
  header {
    padding: 1.6rem 2rem 1.2rem;
    border-bottom: 1px solid color-mix(in srgb, var(--gold) 35%, var(--line-soft) 65%);
    display: flex; align-items: baseline; justify-content: space-between; flex-wrap: wrap; gap: 0.6rem;
  }
  h1 { margin: 0; font-family: "Cormorant Garamond", serif; font-weight: 500; font-size: 2rem; letter-spacing: 0.01em; }
  .sub { color: var(--text-muted); font-size: 0.85rem; }
  main { padding: 1.6rem 2rem 3rem; }
  .board { display: grid; grid-template-columns: repeat(4, minmax(240px, 1fr)); gap: 1.2rem; align-items: start; }
  .col.inbox { background: color-mix(in srgb, var(--panel) 80%, var(--bg-soft) 20%); }
  .card.inbox-card { cursor: grab; border-style: dashed; }
  .card.inbox-card .id { color: var(--text-muted); }
  .col {
    border: 1px solid color-mix(in srgb, var(--gold) 45%, var(--line-soft) 55%);
    border-radius: 8px; background: color-mix(in srgb, var(--panel) 92%, white 8%);
    min-height: 200px; padding: 0.9rem;
    /* Bounded, independently-scrollable columns (2026-09-02, real UX bug
       found live: with 170 real open backlog items, Inbox grew far taller
       than a viewport, so scrolling down to find one meant the OTHER
       columns' own drop targets scrolled out of view too -- couldn't drag
       between them at all). Every column now fits within one screen at
       once and scrolls on its own, the same real pattern Trello-style
       boards use -- no column's own length can ever push another
       column's drop target off-screen again. */
    display: flex; flex-direction: column; max-height: calc(100vh - 12rem);
  }
  .col.dragover { border-color: var(--gold-highlight); background: color-mix(in srgb, var(--panel) 84%, white 16%); }
  .col h2 {
    margin: 0 0 0.8rem; font-family: "Cormorant Garamond", serif; font-weight: 600; font-size: 1.15rem;
    letter-spacing: 0.06em; text-transform: uppercase; color: var(--text-muted);
    display: flex; justify-content: space-between; align-items: baseline; flex: none;
  }
  .col h2 .count { font-family: "Spectral", serif; font-weight: 400; font-size: 0.85rem; color: var(--text-faint); }
  .cards { overflow-y: auto; min-height: 0; flex: 1 1 auto; }
  .card {
    border: 1px solid color-mix(in srgb, var(--gold) 55%, var(--line-soft) 45%);
    border-radius: 6px; background: color-mix(in srgb, var(--panel) 97%, white 3%);
    padding: 0.65rem 0.8rem; margin-bottom: 0.6rem; cursor: grab;
    box-shadow: 0 1px 2px rgba(20,18,14,0.06);
  }
  .card:active { cursor: grabbing; }
  .card.dragging { opacity: 0.4; }
  /* KBUX-092929: "hitting done on a card can be optimistic and immediately disappear or like
     fly off the screen to the right and have the other cards slide up... it seems like we are
     waiting on a sync request" -- real, decisive root cause was exactly that: sendToQueue
     awaited the PATCH response, then a full loadCards()/loadInbox() re-fetch, before the DOM
     changed at all. .card.completing (applied by markCardDone below, BEFORE the fetch fires)
     is the real fly-off-right animation the card's own wording asks for; max-height/margin
     transition out too so the column visibly closes the gap ("other cards slide up") instead
     of leaving a hole until the next full render. */
  .card.completing {
    transition: transform 0.28s ease-in, opacity 0.28s ease-in, max-height 0.28s ease-in 0.15s,
                margin-bottom 0.28s ease-in 0.15s, padding 0.28s ease-in 0.15s;
    transform: translateX(40px); opacity: 0; max-height: 0; margin-bottom: 0; padding-top: 0;
    padding-bottom: 0; overflow: hidden; pointer-events: none;
  }
  .card .id { font-size: 0.72rem; letter-spacing: 0.05em; color: var(--gold-soft); font-weight: 600; }
  .card .title { font-size: 0.92rem; margin-top: 0.15rem; }
  .card .del { float: right; color: var(--text-faint); text-decoration: none; font-size: 0.85rem; }
  .card .del:hover { color: #a24; }
  /* S207-68 "i should have the ability to sort the cards in a column" --
     a real, click-based reorder alternative to drag (see moveCardBy in the
     script below), floated left opposite the existing delete "x". */
  .card .sort-btns { float: left; display: flex; flex-direction: column; line-height: 0.8; margin-right: 0.4rem; }
  .card .sort-btn { color: var(--text-faint); text-decoration: none; font-size: 0.6rem; }
  .card .sort-btn:hover { color: var(--gold-highlight); }
  .card .sort-btn-disabled { color: var(--text-faint); opacity: 0.3; cursor: default; }
  /* "Send to" quick-move (2026-09-02, same UX fix as the scrollable
     columns above) -- founder: "we need to either extend the columns down
     or add a right click send to interface." A small always-visible
     select per card is more reliable than a real right-click context menu
     (no custom positioning/dismiss-on-click-outside code, works via
     keyboard, works on touch) and fully covers the same real need: moving
     a card without dragging it across the screen at all. */
  .card .move-to { margin-top: 0.4rem; }
  .card .move-to select {
    font-family: "Spectral", serif; font-size: 0.78rem; padding: 0.2rem 0.35rem;
    border: 1px solid color-mix(in srgb, var(--gold) 45%, var(--line-soft) 55%); border-radius: 4px;
    background: color-mix(in srgb, var(--panel) 97%, white 3%); color: var(--text-muted); cursor: pointer;
  }
  .empty { color: var(--text-faint); font-size: 0.85rem; font-style: italic; padding: 0.4rem 0; }
  .add-row {
    margin-top: 1.4rem; display: flex; gap: 0.6rem; flex-wrap: wrap; align-items: center;
    border-top: 1px solid color-mix(in srgb, var(--gold) 30%, var(--line-soft) 70%); padding-top: 1.2rem;
  }
  input {
    font-family: "Spectral", serif; font-size: 0.9rem; padding: 0.45rem 0.6rem;
    border: 1px solid color-mix(in srgb, var(--gold) 50%, var(--line-soft) 50%); border-radius: 5px;
    background: color-mix(in srgb, var(--panel) 97%, white 3%); color: var(--text-main);
  }
  input[name="backlog_item_id"] { width: 9rem; }
  input[name="title"] { flex: 1; min-width: 220px; }
  button {
    font-family: "Spectral", serif; font-size: 0.88rem; padding: 0.48rem 1.1rem; cursor: pointer;
    border: 1px solid color-mix(in srgb, var(--gold) 60%, var(--line-soft) 40%); border-radius: 5px;
    background: color-mix(in srgb, var(--gold) 22%, var(--panel) 78%); color: var(--text-main);
  }
  button:hover { background: color-mix(in srgb, var(--gold) 32%, var(--panel) 68%); }
  #status { font-size: 0.82rem; color: var(--text-muted); min-height: 1.2em; margin-top: 0.5rem; }
  /* Quick filter (kanban IDUXN-003 -- "kanban needs a quick filter at the top to help me find
     cards quick when i think of one that needs to move, filtering on the card name priority
     over the rest of the text"): a real, client-side, instant filter over every already-loaded
     card -- no server round-trip, matches as you type. Real-value cards (title matched) sort
     ahead of id-only matches within a column, per the card's own "name priority" ask. */
  #quick-filter { min-width: 220px; }
  .card.filtered-out { display: none; }
  /* KBUX-CACHE-001 (founder real-time: "maybe we can get a little refresh icon in the top
     right to do a full cache clear for the kanban board") -- reuses the existing button
     style, just tightened to sit inline with the filter box and the Back Office link. */
  #refresh-btn { padding: 0.45rem 0.7rem; margin: 0 0.6rem; font-size: 0.82rem; }
  #refresh-btn.spinning { opacity: 0.6; cursor: default; }
</style>
</head>
<body>
<header>
  <div>
    <h1>Kanban</h1>
    <div class="sub">Priority layer over EMILY/BACKLOG.md — drag a card between columns, or drag a real open backlog item in from Inbox to sort it. Backlog item ids/content stay authoritative in BACKLOG.md itself; completed items are hidden here to save DOM nodes (view those in BACKLOG.md directly).</div>
  </div>
  <div class="sub">
    <input type="text" id="quick-filter" placeholder="Filter cards…" oninput="applyFilter()"
           title="Instant filter over every loaded card, by id or title -- matches nothing typed yet show every card">
    <button type="button" id="refresh-btn" onclick="hardRefresh()" title="Re-fetch everything from the server right now, bypassing any cache (KBUX-CACHE-001)">⟳ Refresh</button>
    <a href="/admin">← Back Office</a>
  </div>
</header>
<main>
  <div class="board">
    <div class="col inbox">
      <h2>Inbox <span class="count" id="count-inbox">0</span></h2>
      <div class="cards" id="cards-inbox"></div>
    </div>
    <div class="col" data-queue="backlog" ondragover="onDragOver(event)" ondragleave="onDragLeave(event)" ondrop="onDrop(event)">
      <h2>Backlog <span class="count" id="count-backlog">0</span></h2>
      <div class="cards" id="cards-backlog"></div>
    </div>
    <div class="col" data-queue="priority" ondragover="onDragOver(event)" ondragleave="onDragLeave(event)" ondrop="onDrop(event)">
      <h2>Priority <span class="count" id="count-priority">0</span></h2>
      <div class="cards" id="cards-priority"></div>
    </div>
    <div class="col" data-queue="cruise" ondragover="onDragOver(event)" ondragleave="onDragLeave(event)" ondrop="onDrop(event)">
      <h2>Cruise <span class="count" id="count-cruise">0</span></h2>
      <div class="cards" id="cards-cruise"></div>
    </div>
  </div>
  <form class="add-row" id="add-form">
    <input type="text" name="backlog_item_id" placeholder="S202-27 (or just S202)" maxlength="32" required
           title="A full id (S202-27) or just the section (S202) -- the item number is optional, a real unused one is auto-assigned">
    <input type="text" name="title" placeholder="Short card title" maxlength="200" required>
    <button type="submit">+ Add card</button>
  </form>
  <div id="status"></div>
</main>
<script>
const API = '/admin/kanban/api/cards';
const INBOX_API = '/admin/kanban/api/inbox';
let dragId = null;
let dragKind = null;   // 'card' (an existing kanban_cards row) or 'inbox' (a real, un-carded open backlog item)
let dragTitle = null;  // only meaningful for dragKind === 'inbox' -- the real title to use when creating the card

function esc(s) {
  const d = document.createElement('div');
  d.textContent = s;
  return d.innerHTML;
}

const QUEUE_LABELS = { backlog: 'Backlog', priority: 'Priority', cruise: 'Cruise' };

// moveToSelectHTML: the "send to" quick-move control for one card -- see
// this file's own CSS/JS comments on why this exists alongside drag.
// excludeQueue omits a card's own current queue from the options (not
// meaningful for an inbox item, which has no current queue yet).
//
// "Done" (2026-09-02, founder real-time: "we need a Done option for send
// to its fine we dont have a done column") is deliberately real-card-only,
// not offered on an Inbox item -- it's not a literal board column
// (kanban.go's own KanbanHandler.completeCard doc comment explains why:
// it's a real action -- archive the backlog line, file a real Apple,
// remove the card), and completing it needs a real numeric kanban_cards
// id an Inbox item doesn't have yet (drag/send it into a real column
// first).
function moveToSelectHTML(kind, id, title, excludeQueue) {
  const titleAttr = title != null ? ' data-title="' + esc(title) + '"' : '';
  let opts = '<option value="">Send to…</option>';
  for (const q of ['backlog', 'priority', 'cruise']) {
    if (q === excludeQueue) continue;
    opts += '<option value="' + q + '">' + QUEUE_LABELS[q] + '</option>';
  }
  if (kind === 'card') {
    opts += '<option value="done">✓ Done</option>';
  }
  return '<div class="move-to"><select data-kind="' + kind + '" data-id="' + esc(id) + '"' + titleAttr +
    ' onchange="onMoveToSelect(event)" onclick="event.stopPropagation()">' + opts + '</select></div>';
}

function setStatus(msg, isError) {
  const el = document.getElementById('status');
  el.textContent = msg || '';
  el.style.color = isError ? '#a24' : '';
}

async function loadCards() {
  setStatus('Loading…');
  try {
    // cache: 'no-store' -- KBUX-CACHE-001: belt-and-suspenders alongside the server's own new
    // Cache-Control: no-store response header (kanban.go's ServeHTTP) so a stale response can
    // never come from either side, whatever sits between this tab and IDUNA.
    const res = await fetch(API, { credentials: 'same-origin', cache: 'no-store' });
    if (!res.ok) throw new Error('HTTP ' + res.status);
    const cards = await res.json();
    render(cards);
    setStatus('');
  } catch (e) {
    setStatus('Failed to load cards: ' + e.message, true);
  }
}

// loadInbox: real, open (unchecked) BACKLOG.md items with no kanban card
// yet -- see kanban_inbox.go's own doc comment. Refreshed alongside
// loadCards() any time a card is created/moved/deleted, since an item
// leaving/entering "un-carded" status changes what belongs here.
async function loadInbox() {
  try {
    const res = await fetch(INBOX_API, { credentials: 'same-origin', cache: 'no-store' });
    if (!res.ok) throw new Error('HTTP ' + res.status);
    const items = await res.json();
    renderInbox(items || []);
  } catch (e) {
    const container = document.getElementById('cards-inbox');
    container.innerHTML = '';
    const el = document.createElement('div');
    el.className = 'empty';
    el.textContent = 'Failed to load inbox: ' + e.message;
    container.appendChild(el);
  }
}

function renderInbox(items) {
  document.getElementById('count-inbox').textContent = items.length;
  const container = document.getElementById('cards-inbox');
  container.innerHTML = '';
  if (items.length === 0) {
    const e = document.createElement('div');
    e.className = 'empty';
    e.textContent = 'No open, un-sorted backlog items.';
    container.appendChild(e);
    return;
  }
  for (const it of items) {
    const el = document.createElement('div');
    el.className = 'card inbox-card';
    el.draggable = true;
    el.dataset.id = it.id;
    el.dataset.title = it.title;
    el.dataset.kind = 'inbox';
    el.ondragstart = onDragStart;
    el.ondragend = onDragEnd;
    el.title = 'Drag into a column to sort it';
    el.innerHTML = '<div class="id">' + esc(it.id) + '</div>' +
      '<div class="title">' + esc(it.title) + '</div>' +
      moveToSelectHTML('inbox', it.id, it.title, null);
    container.appendChild(el);
  }
  applyFilter();
}

// kanbanOrder caches each column's current id order (server-confirmed, not
// live drag state) so the real +/- sort buttons below can compute a swap
// without re-walking the DOM -- a real, non-drag path to the same S207-68
// ask ("i should have the ability to sort the cards in a column"), since
// drag-and-drop alone isn't reliably usable/testable on every input device.
let kanbanOrder = { backlog: [], priority: [], cruise: [] };

function render(cards) {
  const byQueue = { backlog: [], priority: [], cruise: [] };
  for (const c of cards) {
    (byQueue[c.queue] || byQueue.backlog).push(c);
  }
  for (const q of ['backlog', 'priority', 'cruise']) {
    const list = byQueue[q].sort((a, b) => a.position - b.position);
    kanbanOrder[q] = list.map(c => c.id);
    document.getElementById('count-' + q).textContent = list.length;
    const container = document.getElementById('cards-' + q);
    container.innerHTML = '';
    if (list.length === 0) {
      const e = document.createElement('div');
      e.className = 'empty';
      e.textContent = 'No cards.';
      container.appendChild(e);
      continue;
    }
    list.forEach((c, idx) => {
      const el = document.createElement('div');
      el.className = 'card';
      el.draggable = true;
      el.dataset.id = c.id;
      el.dataset.kind = 'card';
      el.ondragstart = onDragStart;
      el.ondragend = onDragEnd;
      el.ondragover = onCardDragOver;
      const upBtn = idx === 0
        ? '<span class="sort-btn sort-btn-disabled" title="Already first">▲</span>'
        : '<a href="#" class="sort-btn" title="Move up" onclick="moveCardBy(' + c.id + ',\'' + q + '\',-1); return false;">▲</a>';
      const downBtn = idx === list.length - 1
        ? '<span class="sort-btn sort-btn-disabled" title="Already last">▼</span>'
        : '<a href="#" class="sort-btn" title="Move down" onclick="moveCardBy(' + c.id + ',\'' + q + '\',1); return false;">▼</a>';
      el.innerHTML = '<a href="#" class="del" title="Remove card" onclick="removeCard(' + c.id + '); return false;">✕</a>' +
        '<div class="sort-btns">' + upBtn + downBtn + '</div>' +
        '<div class="id">' + esc(c.backlog_item_id) + '</div>' +
        '<div class="title">' + esc(c.title) + '</div>' +
        moveToSelectHTML('card', c.id, null, q);
      container.appendChild(el);
    });
  }
  applyFilter();
}

// applyFilter -- real, client-side, instant filter (IDUXN-003: "kanban needs a quick filter at
// the top to help me find cards quick when i think of one that needs to move, filtering on the
// card name priority over the rest of the text"). Runs over every already-loaded .card element
// (Inbox + all 3 columns) -- no server round-trip, so it stays instant regardless of board size.
// Re-called at the end of render()/renderInbox() too, so a reload (after a move/add/delete)
// keeps respecting whatever the operator already typed, not just the moment they type it.
function applyFilter() {
  const q = document.getElementById('quick-filter').value.trim().toLowerCase();
  document.querySelectorAll('.card').forEach(el => {
    if (!q) { el.classList.remove('filtered-out'); return; }
    const title = (el.querySelector('.title')?.textContent || '').toLowerCase();
    const id = (el.querySelector('.id')?.textContent || '').toLowerCase();
    el.classList.toggle('filtered-out', !(title.includes(q) || id.includes(q)));
  });
  if (!q) return;
  // Real "card name priority over the rest of the text" -- a title match sorts ahead of an
  // id-only match within each column while a filter is active. Purely visual/DOM-order; the
  // real, server-confirmed position (kanbanOrder) is untouched, and this whole reorder is
  // skipped entirely when the filter is empty (the default state), so normal drag/sort behavior
  // is unaffected unless a filter is actually typed.
  document.querySelectorAll('.cards').forEach(container => {
    const cards = Array.from(container.children).filter(el => el.classList.contains('card') && !el.classList.contains('filtered-out'));
    cards.sort((a, b) => {
      const aTitle = (a.querySelector('.title')?.textContent || '').toLowerCase().includes(q);
      const bTitle = (b.querySelector('.title')?.textContent || '').toLowerCase().includes(q);
      return aTitle === bTitle ? 0 : (aTitle ? -1 : 1);
    });
    cards.forEach(el => container.appendChild(el));
  });
}

function onDragStart(e) {
  dragId = e.currentTarget.dataset.id;
  dragKind = e.currentTarget.dataset.kind || 'card';
  dragTitle = e.currentTarget.dataset.title || null;
  e.currentTarget.classList.add('dragging');
  e.dataTransfer.effectAllowed = 'move';
}
function onDragEnd(e) {
  e.currentTarget.classList.remove('dragging');
  dragId = null;
  dragKind = null;
  dragTitle = null;
}
function onDragOver(e) {
  e.preventDefault();
  e.currentTarget.classList.add('dragover');
}
function onDragLeave(e) {
  e.currentTarget.classList.remove('dragover');
}
// onCardDragOver: real, live within-column sort (S207-68 -- "i should have
// the ability to sort the cards in a column"). Only wired on real cards
// (kind 'card'), not Inbox entries, which have no persisted position of
// their own. Live-reorders the actual dragged DOM node as the cursor passes
// over a sibling card's top/bottom half; onDrop below reads the resulting
// DOM order and persists it via a real position PATCH per card, so this is
// not just a cosmetic drag -- it survives a reload.
function onCardDragOver(e) {
  e.preventDefault();
  if (dragKind !== 'card') return;
  const target = e.currentTarget;
  if (target.classList.contains('dragging')) return;
  const container = target.parentElement;
  const dragging = container.querySelector('.dragging');
  if (!dragging || dragging === target) return;
  const rect = target.getBoundingClientRect();
  const before = (e.clientY - rect.top) < rect.height / 2;
  container.insertBefore(dragging, before ? target : target.nextSibling);
}
// moveCardBy: the ▲/▼ button handler -- swaps a card with its immediate
// neighbor in kanbanOrder's cached, server-confirmed order and persists the
// whole column. A click-based alternative to drag-and-drop for S207-68,
// since drag isn't reliable/testable everywhere a keyboard or touch-only
// input is.
function moveCardBy(id, queue, delta) {
  const ids = (kanbanOrder[queue] || []).slice();
  const idx = ids.findIndex(x => String(x) === String(id));
  if (idx === -1) return;
  const newIdx = idx + delta;
  if (newIdx < 0 || newIdx >= ids.length) return;
  [ids[idx], ids[newIdx]] = [ids[newIdx], ids[idx]];
  persistColumnOrder(queue, ids);
}
// persistColumnOrder: writes the real, final drag result back through the
// same PATCH .../cards/{id} API every other kanban write uses -- one call
// per card in the column, each setting queue (covers a cross-column drop
// too) + a fresh, gapless 0..n-1 position matching the live DOM order.
async function persistColumnOrder(queue, orderedIds) {
  try {
    await Promise.all(orderedIds.map((id, idx) =>
      fetch(API + '/' + id, {
        method: 'PATCH',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ queue: queue, position: idx })
      }).then(res => {
        if (!res.ok) throw new Error('HTTP ' + res.status + ' for card ' + id);
      })
    ));
    await Promise.all([loadCards(), loadInbox()]);
  } catch (e2) {
    setStatus('Failed to save card order: ' + e2.message, true);
    await loadCards(); // real state may be partially applied -- reload rather than trust stale DOM
  }
}
// sendToQueue: the one real place both drag-and-drop (onDrop) and the
// "send to" quick-select (onMoveToSelect) route through -- either creates
// a real card from an inbox item, or moves an existing one, then refreshes
// both panes. Pulled out 2026-09-02 alongside the quick-select itself
// (real UX bug found live: a card far down a long Inbox column couldn't be
// dragged to a column scrolled out of view -- see this file's own CSS
// comment on .col for the other real half of that fix).
// markCardDone -- KBUX-092929's own real fix: applies the .card.completing fly-off animation to
// the given card's own DOM element immediately (before the network request even fires), and
// returns a real Promise that resolves once the CSS transition actually finishes (a real
// a real transitionend listener, not a guessed setTimeout duration that could drift out of sync with
// the CSS above). sendToQueue awaits this ALONGSIDE the fetch (Promise.all, not sequentially) so
// the full reload it still needs to run afterward (to pick up any other real server-side change)
// never wipes the element out from under a still-playing animation -- the real, live-found race
// the naive "start the animation, immediately reload" version of this fix had.
function markCardDone(id) {
  const el = document.querySelector('.card[data-id="' + id + '"]');
  if (!el) return Promise.resolve();
  el.classList.add('completing');
  return new Promise(resolve => {
    el.addEventListener('transitionend', () => { el.remove(); resolve(); }, { once: true });
  });
}

async function sendToQueue(kind, id, title, queue) {
  // KBUX-092929: "hitting done on a card can be optimistic and immediately disappear or like
  // fly off the screen to the right and have the other cards slide up... it seems like we are
  // waiting on a sync request" -- real, decisive root cause: this function used to await the
  // PATCH response, then a full loadCards()/loadInbox() re-fetch, before the DOM changed at all
  // (every existing caller -- drag-to-Done, the quick-select dropdown -- funnels through here,
  // so this one real choke point is also the one real fix point). Only the real "mark an
  // existing card done" case gets the optimistic treatment -- an inbox item becoming a brand
  // new card has no existing DOM node to animate away, and a plain in-board move (backlog ->
  // priority, say) isn't what this card's own report is about.
  const animation = (kind === 'card' && queue === 'done') ? markCardDone(id) : null;
  try {
    const doFetch = (kind === 'inbox')
      // Real, un-carded backlog item -- create the real card (title comes
      // from the live BACKLOG.md parse, not hand-typed).
      ? fetch(API, {
          method: 'POST',
          credentials: 'same-origin',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ backlog_item_id: id, title: title, queue: queue })
        })
      : fetch(API + '/' + id, {
          method: 'PATCH',
          credentials: 'same-origin',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ queue: queue })
        });
    // Real fix: wait for the network call AND the fly-off animation together (whichever takes
    // longer), not the network call THEN a full reload that would otherwise cut the animation
    // short mid-flight.
    const [res] = await Promise.all(animation ? [doFetch, animation] : [doFetch]);
    if (!res.ok) throw new Error('HTTP ' + res.status);
    await Promise.all([loadCards(), loadInbox()]);
  } catch (e2) {
    setStatus('Failed to move card: ' + e2.message, true);
    // Real, honest reversal: the optimistic fly-off already played (or is still mid-flight, in
    // which case its own already-scheduled transitionend still fires and removes the node) even
    // though the server call that was supposed to make it real failed -- a full reload here
    // re-renders the real, unchanged server state, which is the honest recovery either way.
    await Promise.all([loadCards(), loadInbox()]);
  }
}

function onMoveToSelect(e) {
  const el = e.currentTarget;
  const queue = el.value;
  if (!queue) return;
  sendToQueue(el.dataset.kind, el.dataset.id, el.dataset.title || null, queue);
  el.value = ''; // reset to the placeholder -- this is an action, not a persistent field
}

async function onDrop(e) {
  e.preventDefault();
  e.currentTarget.classList.remove('dragover');
  if (!dragId) return;
  const queue = e.currentTarget.dataset.queue;
  if (dragKind === 'card') {
    // Real cards support in-column sort (S207-68): if onCardDragOver already
    // live-moved the dragged node into this column's own DOM, use that
    // order as-is. If it was dropped on empty space (or an empty column, or
    // never crossed a sibling card) instead, the node is still sitting in
    // its old container -- append it here so it isn't silently dropped.
    const container = document.getElementById('cards-' + queue);
    const draggingEl = document.querySelector('.card.dragging');
    if (draggingEl && draggingEl.parentElement !== container) {
      const emptyMsg = container.querySelector('.empty');
      if (emptyMsg) emptyMsg.remove();
      container.appendChild(draggingEl);
    }
    const orderedIds = Array.from(container.querySelectorAll('.card'))
      .map(el => el.dataset.id)
      .filter(Boolean);
    await persistColumnOrder(queue, orderedIds);
  } else {
    await sendToQueue(dragKind, dragId, dragTitle, queue);
  }
}

async function removeCard(id) {
  if (!confirm('Remove this card from the kanban board? (BACKLOG.md itself is untouched.)')) return;
  try {
    const res = await fetch(API + '/' + id, { method: 'DELETE', credentials: 'same-origin' });
    if (!res.ok && res.status !== 204) throw new Error('HTTP ' + res.status);
    await Promise.all([loadCards(), loadInbox()]);
  } catch (e) {
    setStatus('Failed to remove card: ' + e.message, true);
  }
}

document.getElementById('add-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const form = e.currentTarget;
  const backlogItemId = form.backlog_item_id.value.trim();
  const title = form.title.value.trim();
  if (!backlogItemId || !title) return;
  try {
    const res = await fetch(API, {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ backlog_item_id: backlogItemId, title: title })
    });
    if (!res.ok) throw new Error('HTTP ' + res.status);
    const created = await res.json();
    form.reset();
    // A bare section reference (e.g. "S202") gets auto-resolved to a real, specific id
    // server-side (resolveBareSectionID) -- surface what actually got assigned so a caller who
    // used the shortcut can see it, not just silently trust it worked.
    if (created && created.backlog_item_id && created.backlog_item_id !== backlogItemId) {
      setStatus('Added as ' + created.backlog_item_id + '.', false);
    }
    await Promise.all([loadCards(), loadInbox()]);
  } catch (e2) {
    setStatus('Failed to add card: ' + e2.message, true);
  }
});

// hardRefresh: the "⟳ Refresh" button (KBUX-CACHE-001). A real investigation into "my web
// interface shows 30 items in priority queue - is that right? ... maybe we lost state" found
// this wasn't actually a caching bug this time (the board's own live fetch already matched the
// real, current server state) -- but there was genuinely no way for an operator to force a
// from-scratch reload short of a full page reload, and no server response here carried any
// cache header at all to rule a cache out with confidence. This does both at once: a real
// re-fetch of the page's own live data (loadCards already sends cache: no-store, so this is
// already a real hard refresh of the board state, not a cosmetic spinner) plus a visible
// spin/disable so a click always gets visible feedback.
function hardRefresh() {
  const btn = document.getElementById('refresh-btn');
  btn.disabled = true;
  btn.classList.add('spinning');
  const label = btn.textContent;
  btn.textContent = '⟳ Refreshing…';
  Promise.all([loadCards(), loadInbox()]).finally(() => {
    btn.disabled = false;
    btn.classList.remove('spinning');
    btn.textContent = label;
  });
}

loadCards();
loadInbox();
</script>
</body>
</html>
`
