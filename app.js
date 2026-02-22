/* IDUNA VS0 — Tailwind + Vanilla JS
   Flow: Google OAuth -> Honor Code -> Unique Gamertag
*/

const API_BASE = window.__IDUNA_API_BASE__ || "";
const LS_TOKEN = "iduna_token";
const LS_HONOR = "iduna_honor_sha";
const LS_RETURN = "iduna_return_to";

const els = {
  status: document.getElementById("status"),
  login: document.getElementById("screen-login"),
  honor: document.getElementById("screen-honor"),
  honorBody: document.getElementById("honor-body"),
  honorCheck: document.getElementById("honor-check"),
  handle: document.getElementById("screen-handle"),
  handleInput: document.getElementById("handle-input"),
  handleHint: document.getElementById("handle-hint"),
  done: document.getElementById("screen-done"),
  btnGoogle: document.getElementById("btn-google"),
  btnAccept: document.getElementById("btn-accept"),
  btnLock: document.getElementById("btn-lock"),
  btnEnter: document.getElementById("btn-enter"),
};

function setStatus(msg) { els.status.textContent = msg; }
function show(screen) {
  [els.login, els.honor, els.handle, els.done].forEach((x) => x.classList.add("hidden"));
  screen.classList.remove("hidden");
}

function getToken() { return localStorage.getItem(LS_TOKEN); }
function setToken(t) { if (t) localStorage.setItem(LS_TOKEN, t); }

function authHeaders() {
  const t = getToken();
  return t ? { Authorization: `Bearer ${t}` } : {};
}

async function api(path, opts = {}) {
  const res = await fetch(API_BASE + path, {
    method: opts.method || "GET",
    headers: {
      "Content-Type": "application/json",
      ...authHeaders(),
      ...(opts.headers || {}),
    },
    body: opts.body ? JSON.stringify(opts.body) : undefined,
    credentials: "include",
  });

  let data = null;
  const ct = res.headers.get("content-type") || "";
  if (ct.includes("application/json")) {
    try { data = await res.json(); } catch {}
  } else {
    try { data = { text: await res.text() }; } catch {}
  }

  if (!res.ok) {
    const err = new Error((data && (data.message || data.error || data.code)) || `HTTP_${res.status}`);
    err.status = res.status;
    err.data = data;
    throw err;
  }
  return data;
}

function normalizeHandle(raw) {
  return (raw || "")
    .trim()
    .replace(/\s+/g, "_")
    .replace(/[^A-Za-z0-9_]/g, "")
    .slice(0, 16);
}

function validateHandle(h) {
  if (!h) return "Enter a gamertag.";
  if (h.length < 3) return "Too short (min 3).";
  if (h.length > 16) return "Too long (max 16).";
  if (!/^[A-Za-z0-9_]+$/.test(h)) return "Only letters, numbers, underscore.";
  const r = h.toLowerCase();
  const reserved = new Set(["admin", "moderator", "system", "root", "support", "iduna"]);
  if (reserved.has(r)) return "Reserved word.";
  return null;
}

let honorCurrent = null;
let handleCheckTimer = null;

async function bootstrap() {
  setStatus("BOOT: checking session…");

  const url = new URL(window.location.href);
  const code = url.searchParams.get("code");
  const oauthError = url.searchParams.get("error");

  if (oauthError) {
    setStatus(`AUTH: error (${oauthError})`);
    show(els.login);
    return;
  }

  if (code) {
    setStatus("AUTH: exchanging code…");
    try {
      const redirectUri = `${url.origin}${url.pathname}`;
      const out = await api("/auth/google/callback", {
        method: "POST",
        body: { code, redirect_uri: redirectUri },
      });
      if (out && out.token) setToken(out.token);

      url.searchParams.delete("code");
      window.history.replaceState({}, document.title, url.toString());

      await routeAfterAuth(out);
      return;
    } catch {
      setStatus("AUTH: failed");
      show(els.login);
      return;
    }
  }

  try {
    const me = await api("/me");
    await routeAfterMe(me);
  } catch {
    setStatus("BOOT: idle");
    show(els.login);
  }
}

async function routeAfterAuth(authOut) {
  if (authOut && authOut.honor_code && authOut.honor_code.required) {
    honorCurrent = authOut.honor_code;
    localStorage.setItem(LS_HONOR, honorCurrent.sha256 || "");
    renderHonor(honorCurrent);
    show(els.honor);
    setStatus("HONOR: required");
    return;
  }

  const me = await api("/me");
  await routeAfterMe(me);
}

async function routeAfterMe(meOut) {
  const honor = meOut?.honor_code;
  const user = meOut?.user;

  if (honor?.required) {
    honorCurrent = honor;
    localStorage.setItem(LS_HONOR, honor.sha256 || "");
    renderHonor(honorCurrent);
    show(els.honor);
    setStatus("HONOR: required");
    return;
  }

  if (!user?.handle) {
    show(els.handle);
    setStatus("HANDLE: required");
    focusHandle();
    return;
  }

  complete(user.handle);
}

function renderHonor(h) {
  els.honorBody.textContent = (h.body_markdown || "").trim() || "(missing honor code text)";
  els.honorCheck.checked = false;
  els.btnAccept.disabled = true;
}

function focusHandle() {
  setTimeout(() => els.handleInput?.focus(), 50);
}

function complete(handle) {
  setStatus(`READY: ${handle}`);
  show(els.done);
  const dest = localStorage.getItem(LS_RETURN) || "/town";
  els.btnEnter.href = dest;
}

els.btnGoogle.addEventListener("click", async () => {
  setStatus("AUTH: preparing…");
  try {
    const out = await api("/auth/google/start");
    if (!out?.url) throw new Error("missing_url");
    localStorage.setItem(LS_RETURN, "/town");
    window.location.href = out.url;
  } catch {
    setStatus("AUTH: cannot start");
  }
});

els.honorCheck.addEventListener("change", () => {
  els.btnAccept.disabled = !els.honorCheck.checked;
});

els.btnAccept.addEventListener("click", async () => {
  if (!honorCurrent?.sha256) {
    setStatus("HONOR: missing version");
    return;
  }
  setStatus("HONOR: submitting…");
  els.btnAccept.disabled = true;
  try {
    await api("/honor-code/accept", { method: "POST", body: { sha256: honorCurrent.sha256 } });
    const me = await api("/me");
    await routeAfterMe(me);
  } catch (e) {
    const honor = e?.data?.honor_code;
    if (e?.status === 403 && honor) {
      honorCurrent = honor;
      renderHonor(honorCurrent);
      show(els.honor);
      setStatus("HONOR: required");
      return;
    }
    setStatus("HONOR: failed");
    els.btnAccept.disabled = !els.honorCheck.checked;
  }
});

els.handleInput.addEventListener("input", () => {
  const norm = normalizeHandle(els.handleInput.value);
  if (norm !== els.handleInput.value) els.handleInput.value = norm;

  const err = validateHandle(norm);
  if (err) {
    els.handleHint.textContent = err;
    els.handleHint.className = "text-xs text-amber-300";
    els.btnLock.disabled = true;
    return;
  }

  els.handleHint.textContent = "Checking availability…";
  els.handleHint.className = "text-xs text-zinc-400";
  els.btnLock.disabled = true;

  if (handleCheckTimer) clearTimeout(handleCheckTimer);
  handleCheckTimer = setTimeout(() => checkHandle(norm), 250);
});

async function checkHandle(handle) {
  try {
    const out = await api(`/gamertag/check?handle=${encodeURIComponent(handle)}`);
    if (out.available) {
      els.handleHint.textContent = "Available.";
      els.handleHint.className = "text-xs text-emerald-300";
      els.btnLock.disabled = false;
    } else {
      els.handleHint.textContent = out.reason || "Not available.";
      els.handleHint.className = "text-xs text-amber-300";
      els.btnLock.disabled = true;
    }
  } catch {
    els.handleHint.textContent = "Cannot verify right now.";
    els.handleHint.className = "text-xs text-amber-300";
    els.btnLock.disabled = true;
  }
}

els.btnLock.addEventListener("click", async () => {
  const handle = normalizeHandle(els.handleInput.value);
  const err = validateHandle(handle);
  if (err) {
    els.handleHint.textContent = err;
    els.handleHint.className = "text-xs text-amber-300";
    return;
  }

  setStatus("HANDLE: saving…");
  els.btnLock.disabled = true;

  try {
    const out = await api("/me/handle", { method: "POST", body: { handle } });
    complete(out?.user?.handle || handle);
  } catch (e) {
    const honor = e?.data?.honor_code;
    if (e?.status === 403 && honor) {
      honorCurrent = honor;
      renderHonor(honorCurrent);
      show(els.honor);
      setStatus("HONOR: required");
      return;
    }
    setStatus("HANDLE: failed");
    els.btnLock.disabled = false;
  }
});

bootstrap();
