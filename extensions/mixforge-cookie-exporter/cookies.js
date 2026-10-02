// cookies.js -- real, shared logic between popup.js and any future caller. Reads youtube.com's
// own real cookies via chrome.cookies (the sanctioned extension API scoped by manifest.json's
// own host_permissions -- no security boundary defeated, this is what the permission is for) and
// renders them as a real Netscape cookies.txt, the exact format yt-dlp's own --cookies flag
// expects (see MIXFORGE/server/room_server.mjs and IDUNA/internal/http/handlers/
// mixforge_cookies.go, the real receiving end).

// toNetscapeLine -- one real cookie -> one real Netscape-format line. Field order and meaning
// match the format's own real, long-standing spec (also what browser "export cookies.txt"
// extensions already produce): domain, includeSubdomains, path, secure, expiration (unix
// seconds, 0 for a session cookie), name, value.
function toNetscapeLine(c) {
  const domain = c.domain.startsWith(".") ? c.domain : (c.hostOnly ? c.domain : "." + c.domain);
  const includeSubdomains = domain.startsWith(".") ? "TRUE" : "FALSE";
  const expiration = c.session ? 0 : Math.round(c.expirationDate || 0);
  return [domain, includeSubdomains, c.path, c.secure ? "TRUE" : "FALSE", expiration, c.name, c.value].join("\t");
}

export async function exportYoutubeCookiesTxt() {
  const cookies = await chrome.cookies.getAll({ domain: "youtube.com" });
  const lines = ["# Netscape HTTP Cookie File", "# Exported by MIXFORGE Cookie Exporter"];
  for (const c of cookies) lines.push(toNetscapeLine(c));
  return { text: lines.join("\n") + "\n", count: cookies.length };
}
