package handlers

// game_claim_codes_page.go -- founder real-time (2026-09-21): "give me iduna back office tools
// to craft a DEADWEIGHT Premium key so i can make one to test - simple form just like GFD
// tools". Everything this needs already exists (game_claim_codes table, the redeem() handler in
// game_online.go, cmd/gen-claim-codes' own randomCode format) -- this is a thin web front end
// over the exact same INSERT cmd/gen-claim-codes already does, so a code minted here and one
// minted by the CLI are byte-for-byte the same shape and redeem identically. Same cream/gold
// ceremony style as the other GFD admin pages (gfd_mob_drops_page.go etc.) per "simple form just
// like GFD tools".
//
// Generating a single Premium test key is: game=deadweight, tickets=0, founder=true, tier=blank
// -- founder_flag=1 is what flips is_founder (Premium/unlimited, S516's effectiveDailyCap), no
// separate "premium" concept exists in the schema. The form defaults to exactly that so the
// founder's own stated use case ("craft a Premium key so i can make one to test") is the
// zero-typing path; count/tickets/tier stay editable for batch/other-tier generation too.

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"iduna/internal/games"
)

// claimCodeAlphabet matches cmd/gen-claim-codes exactly (capitals + numbers, no 0/O/1/I -- avoids
// customer transcription errors) so a code minted here is indistinguishable from a CLI-minted one.
const claimCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func randomClaimCode() (string, error) {
	const blocks, blockLen = 5, 5
	buf := make([]byte, blocks*blockLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	var b strings.Builder
	for i, v := range buf {
		if i > 0 && i%blockLen == 0 {
			b.WriteByte('-')
		}
		b.WriteByte(claimCodeAlphabet[int(v)%len(claimCodeAlphabet)])
	}
	return b.String(), nil
}

// GameClaimCodesHandler serves both the generate/list API (/admin/game-claim-codes/api/*) and
// is wired alongside GameClaimCodesPageHandler for the form itself.
type GameClaimCodesHandler struct {
	DB *sql.DB
}

type claimCodeRow struct {
	Code       string  `json:"code"`
	Game       string  `json:"game"`
	Tickets    int     `json:"tickets"`
	Founder    bool    `json:"founder"`
	Tier       *string `json:"tier,omitempty"`
	UsedBy     *string `json:"used_by_player_id,omitempty"`
	UsedAt     *string `json:"used_at,omitempty"`
	CreatedAt  string  `json:"created_at"`
}

func (h *GameClaimCodesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasSuffix(r.URL.Path, "/api/games") && r.Method == http.MethodGet:
		h.listGames(w, r)
	case strings.HasSuffix(r.URL.Path, "/api/generate") && r.Method == http.MethodPost:
		h.generate(w, r)
	case strings.HasSuffix(r.URL.Path, "/api/codes") && r.Method == http.MethodGet:
		h.list(w, r)
	default:
		http.NotFound(w, r)
	}
}

// listGames feeds the form's game dropdown from games.Registry -- never hardcoded, so a new
// game.Registry entry shows up here automatically (same reasoning games.go's own doc comment
// gives for the registry existing at all).
func (h *GameClaimCodesHandler) listGames(w http.ResponseWriter, r *http.Request) {
	slugs := make([]string, 0, len(games.Registry))
	for slug := range games.Registry {
		slugs = append(slugs, slug)
	}
	writeJSON(w, http.StatusOK, slugs)
}

func (h *GameClaimCodesHandler) generate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Game    string `json:"game"`
		N       int    `json:"n"`
		Tickets int    `json:"tickets"`
		Founder bool   `json:"founder"`
		Tier    string `json:"tier"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if _, ok := games.Registry[req.Game]; !ok {
		mmoWriteError(w, http.StatusBadRequest, "unknown game slug")
		return
	}
	if req.N <= 0 {
		req.N = 1
	}
	if req.N > 500 {
		// A back-office form is for hand-testing/small batches -- a real bulk store-launch batch
		// still goes through cmd/gen-claim-codes (no request-size limit there), not this page.
		mmoWriteError(w, http.StatusBadRequest, "max 500 codes per form submission; use cmd/gen-claim-codes for larger batches")
		return
	}
	if req.Tickets < 0 {
		mmoWriteError(w, http.StatusBadRequest, "tickets must be >= 0")
		return
	}

	founderFlag := 0
	if req.Founder {
		founderFlag = 1
	}
	var tierArg any
	if strings.TrimSpace(req.Tier) != "" {
		tierArg = strings.TrimSpace(req.Tier)
	}

	ctx := r.Context()
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback()

	codes := make([]string, 0, req.N)
	seen := map[string]bool{}
	for len(codes) < req.N {
		c, err := randomClaimCode()
		if err != nil {
			mmoWriteError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if seen[c] {
			continue // vanishingly rare at 25 real chars, but never silently under-deliver the requested count
		}
		seen[c] = true
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO game_claim_codes (code, game, tickets, founder_flag, tier) VALUES (?,?,?,?,?)`,
			c, req.Game, req.Tickets, founderFlag, tierArg); err != nil {
			mmoWriteError(w, http.StatusInternalServerError, "internal error")
			return
		}
		codes = append(codes, c)
	}
	if err := tx.Commit(); err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"codes": codes})
}

// list returns the most recent codes for the requested game, used or not -- so the founder can
// see the key they just minted (and its used/unused state after testing redeem) without a shell.
func (h *GameClaimCodesHandler) list(w http.ResponseWriter, r *http.Request) {
	game := r.URL.Query().Get("game")
	if _, ok := games.Registry[game]; !ok {
		mmoWriteError(w, http.StatusBadRequest, "unknown game slug")
		return
	}
	rows, err := h.DB.QueryContext(r.Context(),
		`SELECT code, game, tickets, founder_flag, tier, used_by_player_id, used_at, created_at
		 FROM game_claim_codes WHERE game = ? ORDER BY created_at DESC LIMIT 50`, game)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	out := []claimCodeRow{}
	for rows.Next() {
		var row claimCodeRow
		var founderFlag int
		var tier, usedBy, usedAt sql.NullString
		if err := rows.Scan(&row.Code, &row.Game, &row.Tickets, &founderFlag, &tier, &usedBy, &usedAt, &row.CreatedAt); err != nil {
			mmoWriteError(w, http.StatusInternalServerError, "internal error")
			return
		}
		row.Founder = founderFlag != 0
		if tier.Valid {
			row.Tier = &tier.String
		}
		if usedBy.Valid {
			row.UsedBy = &usedBy.String
		}
		if usedAt.Valid {
			row.UsedAt = &usedAt.String
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, out)
}

// GameClaimCodesPageHandler serves the form itself at /admin/game-claim-codes.
type GameClaimCodesPageHandler struct{}

func (h *GameClaimCodesPageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(gameClaimCodesPageHTML))
}

const gameClaimCodesPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Game Claim Codes</title>
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
  main { padding: 1.5rem 2rem; max-width: 900px; margin: 0 auto; }
  table { width: 100%; border-collapse: collapse; background: var(--card); border: 1px solid var(--line); }
  th, td { text-align: left; padding: 0.5rem 0.75rem; border-bottom: 1px solid var(--line); font-size: 0.85rem; vertical-align: top; }
  th { background: #f3ecd9; }
  tr:hover { background: #fbf8f0; }
  button { font-family: inherit; cursor: pointer; border-radius: 4px; border: 1px solid var(--gold); background: var(--gold); color: #fff; padding: 0.4rem 0.9rem; font-size: 0.9rem; }
  button.secondary { background: transparent; color: var(--gold); }
  input, select { font-family: inherit; padding: 0.4rem 0.5rem; border: 1px solid var(--line); border-radius: 4px; width: 100%; font-size: 0.9rem; }
  label { display: block; font-size: 0.8rem; color: #7a6f5a; margin: 0.6rem 0 0.2rem; }
  .form-card { background: var(--card); border: 1px solid var(--line); border-radius: 8px; padding: 1.25rem; margin-bottom: 1.5rem; }
  .row { display: flex; gap: 1rem; flex-wrap: wrap; }
  .row > div { flex: 1; min-width: 140px; }
  .checkbox-row { display: flex; align-items: center; gap: 0.5rem; margin-top: 0.6rem; }
  .checkbox-row input { width: auto; }
  .checkbox-row label { margin: 0; }
  .msg { font-size: 0.85rem; margin-top: 0.5rem; }
  .msg.error { color: var(--danger); }
  .msg.ok { color: var(--ok); }
  .results { background: #f3ecd9; border: 1px solid var(--line); border-radius: 6px; padding: 0.75rem 1rem; margin-top: 1rem; font-family: monospace; font-size: 0.95rem; }
  .results div { padding: 0.15rem 0; }
  .pill { display: inline-block; padding: 0.05rem 0.4rem; border-radius: 3px; font-size: 0.75rem; }
  .pill.unused { background: #e4f0e5; color: var(--ok); }
  .pill.used { background: #f0e5e5; color: var(--danger); }
</style>
</head>
<body>
<header>
  <h1>Game Claim Codes</h1>
  <div class="sub"><a href="/admin">← Back Office</a> — mints game_claim_codes rows (redeemed via the client's "Redeem Code" box). "Premium key" = founder_flag on, 0 tickets.</div>
</header>
<main>
  <div class="form-card">
    <h3 style="margin-top:0">Generate code(s)</h3>
    <div class="row">
      <div>
        <label>Game</label>
        <select id="f-game"></select>
      </div>
      <div>
        <label>How many</label>
        <input type="number" id="f-n" value="1" min="1" max="500">
      </div>
      <div>
        <label>Tickets granted</label>
        <input type="number" id="f-tickets" value="0" min="0">
      </div>
      <div>
        <label>Tier (optional)</label>
        <input type="text" id="f-tier" placeholder="e.g. tier_premium">
      </div>
    </div>
    <div class="checkbox-row">
      <input type="checkbox" id="f-founder" checked>
      <label for="f-founder">Founder flag (Premium / unlimited — this is what a $15 Override Code sets)</label>
    </div>
    <div style="margin-top:0.9rem">
      <button id="gen-btn">Generate</button>
    </div>
    <div class="msg" id="form-msg"></div>
    <div class="results" id="results" hidden></div>
  </div>

  <table id="codes-table">
    <thead><tr><th>Code</th><th>Tickets</th><th>Founder</th><th>Tier</th><th>Status</th><th>Created</th></tr></thead>
    <tbody id="codes-body"></tbody>
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

function setMsg(text, kind) {
  const el = document.getElementById('form-msg');
  el.textContent = text || '';
  el.className = 'msg' + (kind ? ' ' + kind : '');
}

function currentGame() {
  return document.getElementById('f-game').value;
}

async function loadCodes() {
  const game = currentGame();
  if (!game) return;
  const codes = await api('/admin/game-claim-codes/api/codes?game=' + encodeURIComponent(game));
  const tbody = document.getElementById('codes-body');
  tbody.innerHTML = '';
  codes.forEach(c => {
    const tr = document.createElement('tr');
    const status = c.used_by_player_id
      ? '<span class="pill used">used</span>'
      : '<span class="pill unused">unused</span>';
    tr.innerHTML =
      '<td style="font-family: monospace">' + escapeHtml(c.code) + '</td>' +
      '<td>' + c.tickets + '</td>' +
      '<td>' + (c.founder ? 'yes' : '') + '</td>' +
      '<td>' + escapeHtml(c.tier || '') + '</td>' +
      '<td>' + status + '</td>' +
      '<td>' + escapeHtml(c.created_at) + '</td>';
    tbody.appendChild(tr);
  });
}

async function loadGames() {
  const games = await api('/admin/game-claim-codes/api/games');
  const sel = document.getElementById('f-game');
  sel.innerHTML = '';
  games.forEach(g => {
    const opt = document.createElement('option');
    opt.value = g; opt.textContent = g;
    sel.appendChild(opt);
  });
  sel.addEventListener('change', () => loadCodes().catch(e => setMsg(e.message, 'error')));
  await loadCodes();
}

document.getElementById('gen-btn').addEventListener('click', async () => {
  setMsg('Generating…');
  document.getElementById('results').hidden = true;
  try {
    const body = {
      game: currentGame(),
      n: parseInt(document.getElementById('f-n').value, 10) || 1,
      tickets: parseInt(document.getElementById('f-tickets').value, 10) || 0,
      founder: document.getElementById('f-founder').checked,
      tier: document.getElementById('f-tier').value.trim(),
    };
    const res = await api('/admin/game-claim-codes/api/generate', { method: 'POST', body: JSON.stringify(body) });
    const resultsEl = document.getElementById('results');
    resultsEl.innerHTML = res.codes.map(c => '<div>' + escapeHtml(c) + '</div>').join('');
    resultsEl.hidden = false;
    setMsg('Generated ' + res.codes.length + ' code(s). Copy them now — this is the only time the plaintext is shown here beyond the table below.', 'ok');
    await loadCodes();
  } catch (e) {
    setMsg(e.message, 'error');
  }
});

loadGames().catch(e => setMsg(e.message, 'error'));
</script>
</body>
</html>`
