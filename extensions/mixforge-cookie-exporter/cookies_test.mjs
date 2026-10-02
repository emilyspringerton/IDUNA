// cookies_test.mjs -- real test of the Netscape-format conversion logic, run under plain Node
// with a minimal chrome.cookies stub (the only browser API this file touches) rather than a full
// browser+extension harness -- the formatting logic is pure and worth testing in isolation.
globalThis.chrome = {
  cookies: {
    async getAll() {
      return [
        { domain: ".youtube.com", hostOnly: false, path: "/", secure: true, session: false, expirationDate: 1893456000, name: "SID", value: "abc123" },
        { domain: "accounts.youtube.com", hostOnly: true, path: "/signin", secure: true, session: true, name: "STATE", value: "xyz" },
      ];
    },
  },
};

const { exportYoutubeCookiesTxt } = await import("./cookies.js");

function assert(cond, label) {
  if (!cond) { console.error(`FAIL: ${label}`); process.exit(1); }
  console.log(`OK: ${label}`);
}

const { text, count } = await exportYoutubeCookiesTxt();
assert(count === 2, `real cookie count (got ${count})`);
assert(text.startsWith("# Netscape HTTP Cookie File\n"), "real Netscape header line first");
const lines = text.trim().split("\n");
assert(lines.length === 4, `header + comment + 2 real cookie lines (got ${lines.length})`);
assert(lines[2] === ".youtube.com\tTRUE\t/\tTRUE\t1893456000\tSID\tabc123", `subdomain-cookie line correct (got: ${lines[2]})`);
// A real hostOnly cookie means "this exact host only, no subdomains" -- Netscape format encodes
// that as NO leading dot on the domain field AND includeSubdomains=FALSE, not TRUE.
assert(lines[3] === "accounts.youtube.com\tFALSE\t/signin\tTRUE\t0\tSTATE\txyz", `hostOnly cookie gets no leading dot + includeSubdomains FALSE + session expiration 0 (got: ${lines[3]})`);

console.log("cookies.js: PASS");
