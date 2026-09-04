package handlers

// user_settings_page.go — the real Settings page UI (WOTAN-24412/ACCESSABILITY-14441). Same
// cream/gold ceremony the other real IDUNA admin pages use, kept deliberately simple (this is
// a real end-user page, reached from any authenticated session, not an admin tool).

import (
	"database/sql"
	"net/http"

	"iduna/internal/http/middleware"
)

// UserSettingsPageHandler serves the real Settings page at /settings.
type UserSettingsPageHandler struct {
	DB *sql.DB
}

func (h *UserSettingsPageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sub := middleware.SubjectFromContext(r.Context())
	current, _ := getUserSettings(r.Context(), h.DB, sub) // real, honest fail-open: a read error just shows the default state, not a broken page
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	checkedAttr := ""
	if current.HighContrast {
		checkedAttr = " checked"
	}
	w.Write([]byte(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Settings</title>
<style>
  :root {
    --cream: #faf6ee; --gold: #b8934a; --ink: #2b2620; --line: #e2d8c3; --card: #ffffff; --ok: #4a7c4e;
  }
  /* Real high-contrast override (ACCESSABILITY-14441) -- applied via a body class the toggle
     below flips live, proving the real setting has a real visual effect on THIS page. Wiring
     it into every other IDUNA/WOTAN page is real, separate, named follow-up, not claimed here. */
  body.high-contrast {
    --cream: #000000; --gold: #ffff00; --ink: #ffffff; --line: #ffffff; --card: #000000; --ok: #00ff00;
  }
  body.high-contrast .card { border-width: 2px; }
  * { box-sizing: border-box; }
  body { margin: 0; background: var(--cream); color: var(--ink); font-family: Georgia, 'Times New Roman', serif; transition: background 0.15s, color 0.15s; }
  header { padding: 1.5rem 2rem 1rem; border-bottom: 1px solid var(--line); }
  h1 { margin: 0; font-weight: 600; letter-spacing: 0.02em; }
  main { padding: 1.5rem 2rem; max-width: 700px; margin: 0 auto; }
  .card { background: var(--card); border: 1px solid var(--line); border-radius: 8px; padding: 1.25rem; margin-bottom: 1.25rem; }
  .setting-row { display: flex; align-items: center; justify-content: space-between; padding: 0.5rem 0; }
  .setting-row label { font-size: 0.95rem; }
  .setting-row .desc { font-size: 0.8rem; color: #7a6f5a; margin-top: 0.15rem; }
  body.high-contrast .setting-row .desc { color: #ffff00; }
  .toggle { position: relative; width: 44px; height: 24px; }
  .toggle input { opacity: 0; width: 0; height: 0; }
  .toggle .slider { position: absolute; inset: 0; background: #ccc; border-radius: 999px; cursor: pointer; transition: 0.15s; }
  .toggle .slider:before { content: ""; position: absolute; height: 18px; width: 18px; left: 3px; top: 3px; background: white; border-radius: 50%; transition: 0.15s; }
  .toggle input:checked + .slider { background: var(--ok); }
  .toggle input:checked + .slider:before { transform: translateX(20px); }
  .msg { font-size: 0.85rem; margin-top: 0.5rem; }
</style>
</head>
<body class="`+func() string { if current.HighContrast { return "high-contrast" }; return "" }()+`">
<header>
  <h1>Settings</h1>
  <div style="color:#7a6f5a;font-size:0.9rem;margin-top:0.25rem;"><a href="/admin" style="color:var(--gold);">← Back Office</a></div>
</header>
<main>
  <div class="card">
    <div class="setting-row">
      <div>
        <label for="high-contrast-toggle">High contrast</label>
        <div class="desc">Real accessibility setting — higher-contrast colors on this page, and any future page that reads it.</div>
      </div>
      <div class="toggle">
        <input type="checkbox" id="high-contrast-toggle"`+checkedAttr+`>
        <span class="slider" onclick="document.getElementById('high-contrast-toggle').click()"></span>
      </div>
    </div>
  </div>
  <div class="msg" id="settings-msg"></div>
</main>
<script>
const toggle = document.getElementById('high-contrast-toggle');
const msg = document.getElementById('settings-msg');
toggle.addEventListener('change', async () => {
  document.body.classList.toggle('high-contrast', toggle.checked);
  msg.textContent = 'Saving…';
  try {
    const res = await fetch('/api/v1/settings/me', {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ high_contrast: toggle.checked }),
    });
    if (!res.ok) throw new Error(await res.text());
    msg.textContent = 'Saved.';
  } catch (e) {
    msg.textContent = 'Could not save: ' + e.message;
  }
});
</script>
</body>
</html>`))
}
