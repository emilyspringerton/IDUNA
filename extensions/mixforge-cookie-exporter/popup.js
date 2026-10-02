import { exportYoutubeCookiesTxt } from "./cookies.js";

const statusEl = document.getElementById("status");
const goBtn = document.getElementById("go");

document.getElementById("opts").addEventListener("click", (e) => {
  e.preventDefault();
  chrome.runtime.openOptionsPage();
});

goBtn.addEventListener("click", async () => {
  goBtn.disabled = true;
  statusEl.textContent = "reading cookies…";
  try {
    const { endpoint, token } = await chrome.storage.local.get(["endpoint", "token"]);
    if (!endpoint || !token) {
      statusEl.textContent = "set the endpoint + token first (Configure endpoint / token below)";
      goBtn.disabled = false;
      return;
    }
    const { text, count } = await exportYoutubeCookiesTxt();
    if (count === 0) {
      statusEl.textContent = "no youtube.com cookies found -- are you logged into YouTube in this browser?";
      goBtn.disabled = false;
      return;
    }
    statusEl.textContent = `uploading ${count} real cookies…`;
    const res = await fetch(endpoint, {
      method: "POST",
      headers: { "Authorization": `Bearer ${token}`, "Content-Type": "text/plain" },
      body: text,
    });
    const body = await res.json().catch(() => ({}));
    if (res.ok && body.ok) {
      statusEl.textContent = `done -- uploaded ${count} cookies (${body.bytes} bytes) to IDUNA`;
    } else {
      statusEl.textContent = `upload failed: ${body.error || res.status}`;
    }
  } catch (err) {
    statusEl.textContent = `error: ${err.message}`;
  }
  goBtn.disabled = false;
});
