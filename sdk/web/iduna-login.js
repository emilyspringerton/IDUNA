/* iduna-login.js -- IDUNA SSO login for browser apps. No dependencies. See ../README.md for the contract.
 *
 *   IdunaLogin.configure({ base: 'https://iam.okemily.com' });      // optional
 *   IdunaLogin.start();                    // redirects to IDUNA; returns here with #sso_token=...
 *   const s = IdunaLogin.consume();        // on page load: {token, playerId, displayName} | null (fragment scrubbed)
 *   const s = IdunaLogin.session();        // remembered session (sessionStorage) if token still valid, else null
 *   IdunaLogin.logout();
 * The redirect target's host must be on IDUNA's SSO_ALLOWED_REDIRECT_HOSTS allowlist.
 */
(function (root) {
  'use strict';
  var cfg = { base: 'https://iam.okemily.com', storageKey: 'iduna_login_session' };
  function rnd() {
    var a = new Uint8Array(16); (root.crypto || {}).getRandomValues ? root.crypto.getRandomValues(a) : a.forEach(function (_, i) { a[i] = Math.random() * 256; });
    return Array.prototype.map.call(a, function (b) { return ('0' + b.toString(16)).slice(-2); }).join('');
  }
  function jwtExp(t) {
    try { return JSON.parse(atob(t.split('.')[1].replace(/-/g, '+').replace(/_/g, '/'))).exp * 1000; } catch (e) { return 0; }
  }
  function load() { try { return JSON.parse(sessionStorage.getItem(cfg.storageKey) || 'null'); } catch (e) { return null; } }
  var IdunaLogin = {
    configure: function (o) { for (var k in o) cfg[k] = o[k]; return IdunaLogin; },
    loginUrl: function (returnTo) {
      var state = rnd();
      try { sessionStorage.setItem(cfg.storageKey + '_state', state); } catch (e) {}
      var back = new URL(returnTo || (location.origin + location.pathname + location.search));
      back.searchParams.set('iduna_state', state);
      return cfg.base + '/api/v1/auth/sso/login?redirect_uri=' + encodeURIComponent(back.toString());
    },
    start: function (returnTo) { location.href = IdunaLogin.loginUrl(returnTo); },
    consume: function () {
      var h = location.hash.replace(/^#/, '');
      if (h.indexOf('sso_token=') < 0) return null;
      var p = new URLSearchParams(h), q = new URLSearchParams(location.search), want = null;
      try { want = sessionStorage.getItem(cfg.storageKey + '_state'); sessionStorage.removeItem(cfg.storageKey + '_state'); } catch (e) {}
      var clean = location.pathname + (function () { q.delete('iduna_state'); var s = q.toString(); return s ? '?' + s : ''; })();
      var got = new URLSearchParams(location.search).get('iduna_state');
      history.replaceState(null, '', clean);      // never leave the token in the address bar / history
      if (!want || got !== want) return null;      // CSRF / stale: ignore
      var s = { token: p.get('sso_token'), playerId: p.get('player_id'), displayName: p.get('display_name') || '' };
      if (!s.token) return null;
      try { sessionStorage.setItem(cfg.storageKey, JSON.stringify(s)); } catch (e) {}
      return s;
    },
    session: function () {
      var s = load();
      if (!s || !s.token || jwtExp(s.token) < Date.now() + 60000) return null;
      return s;
    },
    logout: function () { try { sessionStorage.removeItem(cfg.storageKey); } catch (e) {} }
  };
  if (typeof module !== 'undefined' && module.exports) module.exports = IdunaLogin; else root.IdunaLogin = IdunaLogin;
})(typeof window !== 'undefined' ? window : globalThis);
