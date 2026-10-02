const endpointEl = document.getElementById("endpoint");
const tokenEl = document.getElementById("token");
const statusEl = document.getElementById("status");

const DEFAULT_ENDPOINT = "https://okemily.com/api/v1/mixforge/cookies";

chrome.storage.local.get(["endpoint", "token"]).then(({ endpoint, token }) => {
  endpointEl.value = endpoint || DEFAULT_ENDPOINT;
  tokenEl.value = token || "";
});

document.getElementById("save").addEventListener("click", async () => {
  await chrome.storage.local.set({ endpoint: endpointEl.value.trim(), token: tokenEl.value.trim() });
  statusEl.textContent = "saved";
  setTimeout(() => { statusEl.textContent = ""; }, 1500);
});
