// render.go writes Prompt-o-verse nodes as static HTML — semantic markup
// throughout (article/figure/figcaption/dl-dt-dd/time/section), not div
// soup, per founder direction: the taxonomy data (EZ prompt, expanded
// prompt, generated image, labeled tags) should be legible in the markup
// itself, not just styled to look right. Individual node pages stay as
// dedicated leaf-node URLs (SEO: each generated image is independently
// indexable) — the index groups those same leaf nodes by style/Label
// ("stained glass" is the subcategory; "baseball card" vs "Master Chief"
// are sibling leaf nodes under it), per founder direction not to collapse
// leaves into fewer pages. Same "own renderer, IDUNA style guide" shape as
// internal/tyler/render.go.
package promptoverse

import (
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"sort"
)

type Renderer struct {
	OutputDir string // e.g. /var/www/okemily/prompt-o-verse
	// EmilyRoot locates EMILY_ROOT/var/promptoverse-mashup-judgments.json
	// and promptoverse-style-mashup-judgments.json, written by `emily
	// promptoverse mashups` (internal/mashupjudge in emily.cli) --
	// defaults to emilyRootDefault() if empty, same cross-repo "known
	// shared location on the same box" pattern as
	// handlers.DiscoveryHandler.
	EmilyRoot string
	// GoogleClientID enables the mashup-nomination widget's "Sign in with
	// Google" button (Google Identity Services client-side flow, POSTs the
	// resulting id_token to /api/v1/auth/google -- no server-side OAuth
	// redirect/callback URL needed, so no gate.farthq.com DNS dependency).
	// Left empty until a real OAuth Client ID exists (Google Cloud Console,
	// human action -- see EMILY/BACKLOG.md S176-34); the widget renders a
	// "not yet available" state instead of a broken button when empty.
	GoogleClientID string
	// Store, when set, lets renderNodePage look up a leaf's additional
	// variants (S176-30) to render on the same page. Optional so tests and
	// call sites that only ever render nodes with no variant history don't
	// need to construct/open a real Store.
	Store *Store
}

func (r *Renderer) emilyRoot() string {
	if r.EmilyRoot != "" {
		return r.EmilyRoot
	}
	return emilyRootDefault()
}

// authStripCSS/authStripHTML/authStripScript are the site-wide "funnel" --
// founder, real-time, after the mashup-nomination widget shipped only on
// subject pages: "ok but where is the funnel? like in the footer or the
// header or something a login button?" A sign-in affordance that only
// exists buried in one page's widget isn't discoverable; every Prompt-o-
// verse page (node/index/subject/style) now carries the same small header
// strip, sharing one localStorage token and one Google Identity Services
// init so a user only ever signs in once. Spliced into each of the 4 page
// templates via plain Go string concatenation (each template is still its
// own self-contained string, matching this file's existing convention of
// duplicating CSS/JS per template rather than introducing a partial-
// template system) -- single source of truth here, not four copies to
// drift.
const authStripCSS = `
  .auth-strip { float: right; display: flex; align-items: center; gap: 0.5rem; }
  .auth-pill { font-size: 0.76rem; color: var(--text-soft); padding: 0.3rem 0.75rem; border: 1px solid var(--rule); border-radius: 999px; cursor: pointer; white-space: nowrap; }
  .auth-pill:hover { color: var(--accent); border-color: var(--accent); }
`

const authStripHTML = `<div class="auth-strip" id="po-auth-strip" data-google-client-id="{{.GoogleClientID}}">
    <span class="auth-pill" id="po-auth-pill" style="display:none"></span>
    <div id="po-auth-signin"></div>
  </div>`

const authStripScript = `{{if .GoogleClientID}}<script src="https://accounts.google.com/gsi/client" async defer></script>{{end}}
<script>
(function () {
  var strip = document.getElementById('po-auth-strip');
  if (!strip) return;
  var clientId = strip.getAttribute('data-google-client-id');
  var pill = document.getElementById('po-auth-pill');
  var signinEl = document.getElementById('po-auth-signin');
  var TOKEN_KEY = 'promptoverse_access_token';
  window.promptoverseAuth = { TOKEN_KEY: TOKEN_KEY };

  function showSignedIn() {
    signinEl.innerHTML = '';
    pill.textContent = 'Signed in ✓ (sign out)';
    pill.style.display = 'inline-block';
    pill.onclick = function () { localStorage.removeItem(TOKEN_KEY); location.reload(); };
  }

  function handleCredential(response) {
    fetch('/api/v1/auth/google', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id_token: response.credential })
    })
      .then(function (res) { return res.json(); })
      .then(function (data) { if (data.access_token) { localStorage.setItem(TOKEN_KEY, data.access_token); location.reload(); } });
  }
  window.__promptoverseHandleCredential = handleCredential;

  if (!clientId) {
    // No real GOOGLE_CLIENT_ID configured yet (S176-34, human action) --
    // show a visible, honest placeholder instead of leaving the strip
    // silently empty. Founder, real-time: "ok where is my social funnel?
    // at least a login button at the top?" -- an invisible container
    // isn't a funnel.
    pill.textContent = 'Sign in (coming soon)';
    pill.style.display = 'inline-block';
    pill.style.cursor = 'default';
    pill.style.opacity = '0.6';
    return;
  }
  if (localStorage.getItem(TOKEN_KEY)) { showSignedIn(); return; }

  signinEl.innerHTML = '<div id="po_g_id_signin"></div>';
  var tryInit = function () {
    if (!window.google || !window.google.accounts) { setTimeout(tryInit, 200); return; }
    window.google.accounts.id.initialize({ client_id: clientId, callback: handleCredential });
    window.google.accounts.id.renderButton(document.getElementById('po_g_id_signin'), { theme: 'outline', size: 'small' });
  };
  tryInit();
})();
</script>
`

// leafPollScript is the SAME live-update poll the index page has
// (see indexTemplate's own script for the flicker-fix history), extended
// to subject and style pages -- founder, real-time, after multiple
// "live reload is still broken" reports: those pages had NO poll script
// at all, only the index page did (confirmed by grepping this file for
// setInterval/insertNewCards before this fix -- exactly one hit). A page
// that never had live-reload will never look "fixed" no matter how many
// times the index page's mechanism gets rebuilt/restarted/verified.
// Reuses the container's own data attributes to know how to filter the
// shared /api/v1/promptoverse/nodes response and what to title each card
// with -- one script, two page types, no copy-drift.
const leafPollScript = `<script>
(function () {
  var POLL_MS = 10000;
  var root = document.getElementById('leaf-gallery-root');
  if (!root) return;
  var filterKey = root.getAttribute('data-filter-key'); // "subject" | "label"
  var filterValue = root.getAttribute('data-filter-value');
  var titleMode = root.getAttribute('data-title-mode'); // "label" | "subject-or-label"
  var countEl = document.getElementById('leaf-gallery-count');
  var countNoun = root.getAttribute('data-count-noun'); // "style" | "node"
  var list = root.querySelector('ul.gallery');

  var knownSlugs = {};
  Array.prototype.forEach.call(list.querySelectorAll('li > a'), function (a) {
    var href = a.getAttribute('href') || '';
    var parts = href.split('/').filter(Boolean);
    var slug = parts[parts.length - 1];
    if (slug) knownSlugs[slug] = true;
  });
  var knownCount = Object.keys(knownSlugs).length;

  function escapeHtml(s) {
    return String(s).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }

  function cardEl(n) {
    var base = '/prompt-o-verse/' + encodeURIComponent(n.slug) + '/' + encodeURIComponent(n.slug);
    var thumb = base + '-thumb.jpg';
    var original = base + '.png';
    var title = titleMode === 'subject-or-label' ? (n.subject || n.label) : n.label;
    var alt = titleMode === 'subject-or-label'
      ? escapeHtml(n.label) + (n.subject ? ' &mdash; ' + escapeHtml(n.subject) : '')
      : escapeHtml(n.label) + ' &mdash; ' + escapeHtml(n.subject || '');
    var li = document.createElement('li');
    li.innerHTML = '<a href="/prompt-o-verse/' + encodeURIComponent(n.slug) + '/">' +
      '<figure><img src="' + thumb + '" alt="' + alt + '" loading="lazy" ' +
      'onerror="this.onerror=null;this.src=\'' + original + '\';">' +
      '<figcaption><span class="kind-tag">' + escapeHtml(n.kind) + '</span>' +
      '<h2>' + escapeHtml(title) + '</h2></figcaption></figure></a>';
    return li;
  }

  function poll() {
    fetch('/api/v1/promptoverse/nodes')
      .then(function (res) { return res.json(); })
      .then(function (data) {
        var nodes = (data && data.nodes ? data.nodes : []).filter(function (n) {
          return n[filterKey] === filterValue;
        });
        var added = 0;
        nodes.forEach(function (n) {
          if (knownSlugs[n.slug]) return;
          knownSlugs[n.slug] = true;
          list.appendChild(cardEl(n));
          added++;
        });
        if (added > 0 && countEl) {
          knownCount += added;
          countEl.textContent = knownCount + ' ' + (knownCount === 1 ? countNoun : countNoun + 's');
        }
      })
      .catch(function () {});
  }

  setInterval(poll, POLL_MS);
})();
</script>
`

const pageTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>{{.Label}}{{if .Subject}} &mdash; {{.Subject}}{{end}} &mdash; Prompt-o-verse</title>
<meta name="description" content="{{.EZPrompt}}">
<style>
  :root {
    --bg: #101014; --panel-bg: #17171d; --accent: #7c8cff;
    --text-main: #eef0f4; --text-soft: #a8adb8; --text-whisper: #6f7480;
    --rule: #23262f;
  }
  @media (prefers-color-scheme: light) {
    :root {
      --bg: #fafafa; --panel-bg: #ffffff; --accent: #4451c7;
      --text-main: #14161c; --text-soft: #4a4f5c; --text-whisper: #8a8f9c;
      --rule: #e4e4e8;
    }
  }
  * { box-sizing: border-box; }
  body {
    margin: 0; background: var(--bg); color: var(--text-main);
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif;
    line-height: 1.6;
  }
  .wrap { max-width: 760px; margin: 0 auto; padding: 2.5rem 1.5rem 5rem; }
  nav.wordmark a {
    font-size: 0.72rem; letter-spacing: 0.28em; text-transform: uppercase;
    color: var(--text-whisper); text-decoration: none;
  }
  header.node-header { margin: 1.4rem 0 2rem; }
  .kind-tag {
    display: inline-block; font-size: 0.72rem; letter-spacing: 0.14em; text-transform: uppercase;
    color: var(--accent); border: 1px solid var(--accent); border-radius: 999px;
    padding: 0.2rem 0.7rem; margin-bottom: 0.6rem; margin-right: 0.4rem;
  }
  h1 { font-size: clamp(1.8rem, 5vw, 2.4rem); margin: 0.6rem 0 0.2rem; font-weight: 700; }
  h1 a { color: inherit; text-decoration: none; }
  h1 a:hover { color: var(--accent); }
  .subject-line { font-size: 1.05rem; color: var(--text-soft); margin: 0 0 0.4rem; }
  .published { font-size: 0.85rem; color: var(--text-whisper); }
  figure.node-image { margin: 0 0 2.2rem; }
  figure.node-image img {
    width: 100%; height: auto; border-radius: 10px; border: 1px solid var(--rule); display: block;
  }
  figcaption { font-size: 0.85rem; color: var(--text-whisper); margin-top: 0.6rem; }
  section.node-section { margin: 0 0 2.2rem; }
  section.node-section h2 {
    font-size: 0.78rem; letter-spacing: 0.14em; text-transform: uppercase; color: var(--accent);
    margin: 0 0 0.8rem;
  }
  .prompt-text {
    font-family: "SF Mono", Menlo, Consolas, monospace; font-size: 0.95rem;
    background: var(--panel-bg); border: 1px solid var(--rule); border-radius: 8px;
    padding: 1rem 1.2rem; color: var(--text-soft); margin: 0;
  }
  .ez-prompt-text { font-size: 1.05rem; color: var(--text-main); margin: 0; }
  dl.taxonomy { display: grid; grid-template-columns: max-content 1fr; gap: 0.55rem 1rem; margin: 0; }
  dl.taxonomy dt {
    font-size: 0.78rem; letter-spacing: 0.06em; text-transform: uppercase; color: var(--text-whisper);
    font-weight: 600;
  }
  dl.taxonomy dd { margin: 0; color: var(--text-main); }
  nav.back { margin-top: 3rem; }
  nav.back a { font-size: 0.85rem; color: var(--text-whisper); text-decoration: none; }
  nav.back a:hover { color: var(--accent); }
  .variants { margin-top: 2.6rem; padding-top: 1.8rem; border-top: 1px solid var(--rule); }
  .variants > h2 { font-size: 0.72rem; letter-spacing: 0.14em; text-transform: uppercase; color: var(--text-whisper); margin: 0 0 1.4rem; }
  article.variant { margin-bottom: 2.6rem; padding: 1.4rem 1.6rem; background: var(--panel-bg); border: 1px solid var(--rule); border-radius: 10px; }
  article.variant .variant-note { font-size: 0.95rem; color: var(--text-main); margin: 0 0 1rem; font-style: italic; }
` + authStripCSS + `
</style>
</head>
<body>
<div class="wrap">
  <nav class="wordmark"><a href="/prompt-o-verse/">Prompt-o-verse &middot; A Gallery</a></nav>
  ` + authStripHTML + `
  <article>
    <header class="node-header">
      <span class="kind-tag">{{.Kind}}</span>
      <h1><a href="{{.StyleLink}}">{{.Label}}</a></h1>
      {{if .Subject}}<p class="subject-line">Applied to: {{if .SubjectLink}}<a href="{{.SubjectLink}}">{{.Subject}}</a>{{else}}{{.Subject}}{{end}}</p>{{end}}
      <p class="published">Published <time datetime="{{.PublishedISO}}">{{.PublishedDate}}</time></p>
    </header>

    <figure class="node-image">
      <img src="{{.HeroImageFile}}" alt="{{.Label}}{{if .Subject}} &mdash; {{.Subject}}{{end}} &mdash; generated image" width="1024" height="1024" loading="lazy">
      <figcaption>Generated output for this taxonomy leaf node.</figcaption>
    </figure>

    <section class="node-section" aria-labelledby="ez-prompt-heading">
      <h2 id="ez-prompt-heading">Prompt</h2>
      <p class="ez-prompt-text">{{.EZPrompt}}</p>
    </section>

    <section class="node-section" aria-labelledby="expanded-prompt-heading">
      <h2 id="expanded-prompt-heading">Expanded Prompt</h2>
      <p class="prompt-text">{{.ExpandedPrompt}}</p>
    </section>

    <section class="node-section" aria-labelledby="taxonomy-heading">
      <h2 id="taxonomy-heading">Taxonomy</h2>
      <dl class="taxonomy">
        {{range .TagPairs}}<dt>{{.Key}}</dt><dd>{{.Value}}</dd>
        {{end}}
      </dl>
    </section>
  </article>
  {{if .Variants}}<section class="variants">
    <h2>Other variants of this generation</h2>
    {{range .Variants}}<article class="variant">
      {{if .Note}}<p class="variant-note">{{.Note}}</p>{{end}}
      <figure class="node-image">
        <img src="{{.ImageFile}}" alt="{{$.Label}}{{if $.Subject}} &mdash; {{$.Subject}}{{end}} &mdash; variant" width="1024" height="1024" loading="lazy">
        <figcaption>Generated {{.PublishedDate}}.</figcaption>
      </figure>
      <section class="node-section" aria-labelledby="ez-prompt-heading">
        <h2 id="ez-prompt-heading">Prompt</h2>
        <p class="ez-prompt-text">{{.EZPrompt}}</p>
      </section>
      <section class="node-section" aria-labelledby="expanded-prompt-heading">
        <h2 id="expanded-prompt-heading">Expanded Prompt</h2>
        <p class="prompt-text">{{.ExpandedPrompt}}</p>
      </section>
    </article>
    {{end}}
  </section>{{end}}
  <nav class="back"><a href="/prompt-o-verse/">&larr; All nodes</a></nav>
</div>
` + authStripScript + `
</body>
</html>
`

const indexTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Prompt-o-verse &mdash; A Gallery</title>
<meta name="description" content="A browsable taxonomy of generated images -- what's possible to ask a generative model for.">
<style>
  :root {
    --bg: #101014; --panel-bg: #17171d; --accent: #7c8cff;
    --text-main: #eef0f4; --text-soft: #a8adb8; --text-whisper: #6f7480;
    --rule: #23262f;
  }
  @media (prefers-color-scheme: light) {
    :root {
      --bg: #fafafa; --panel-bg: #ffffff; --accent: #4451c7;
      --text-main: #14161c; --text-soft: #4a4f5c; --text-whisper: #8a8f9c;
      --rule: #e4e4e8;
    }
  }
  * { box-sizing: border-box; }
  body { margin: 0; background: var(--bg); color: var(--text-main); font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif; }
  .wrap { max-width: 1080px; margin: 0 auto; padding: 2.5rem 1.5rem 5rem; }
  nav.wordmark a { font-size: 0.72rem; letter-spacing: 0.28em; text-transform: uppercase; color: var(--text-whisper); text-decoration: none; }
  h1 { font-weight: 700; font-size: clamp(1.9rem, 5vw, 2.6rem); margin: 1.1rem 0 0.4rem; }
  .tagline { color: var(--text-soft); font-size: 1.02rem; margin-bottom: 2.4rem; max-width: 640px; }
  section.category { margin-bottom: 2.6rem; }
  section.category h2 {
    font-size: 1.25rem; font-weight: 700; margin: 0 0 0.2rem; padding-bottom: 0.6rem;
    border-bottom: 1px solid var(--rule);
  }
  section.category h2 a { color: inherit; text-decoration: none; }
  section.category h2 a:hover { color: var(--accent); }
  section.category .category-count { font-size: 0.8rem; color: var(--text-whisper); font-weight: 400; margin-left: 0.5rem; }
  ul.gallery { list-style: none; margin: 1rem 0 0; padding: 0; display: grid; grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); gap: 1.1rem; }
  ul.gallery li { margin: 0; }
  ul.gallery a { display: block; text-decoration: none; color: inherit; }
  ul.gallery figure { margin: 0; background: var(--panel-bg); border: 1px solid var(--rule); border-radius: 10px; overflow: hidden; }
  ul.gallery img { width: 100%; aspect-ratio: 1 / 1; object-fit: cover; display: block; }
  ul.gallery figcaption { padding: 0.7rem 0.85rem; }
  .kind-tag { display: inline-block; font-size: 0.66rem; letter-spacing: 0.12em; text-transform: uppercase; color: var(--accent); margin-bottom: 0.3rem; }
  ul.gallery h3 { font-size: 0.92rem; font-weight: 600; margin: 0; }
  ul.gallery a:hover h3 { color: var(--accent); }
` + authStripCSS + `
</style>
</head>
<body>
<div class="wrap">
  <nav class="wordmark"><a href="/">EINHORN_INDUSTRIAL</a></nav>
  ` + authStripHTML + `
  <h1>Prompt-o-verse &mdash; A Gallery</h1>
  <p class="tagline">A browsable taxonomy of what's possible to ask a generative model for &mdash;
  each style groups the different subjects it's been applied to, every leaf pairs a real EZ prompt
  and expanded prompt with its generated output and labeled taxonomy tags. VS0 proof-of-concept,
  not the full vision.</p>
  <div id="gallery-root">
  {{range .Categories}}<section class="category" aria-labelledby="cat-{{.Slug}}">
    <h2 id="cat-{{.Slug}}"><a href="/prompt-o-verse/style/{{.Slug}}/">{{.Label}}</a><span class="category-count">{{.Count}} {{if eq .Count 1}}variant{{else}}variants{{end}}</span></h2>
    <ul class="gallery">
      {{range .Nodes}}<li>
        <a href="/prompt-o-verse/{{.Slug}}/">
          <figure>
            <img src="{{.Slug}}/{{.GalleryImageFile}}" alt="{{.Label}}{{if .Subject}} &mdash; {{.Subject}}{{end}}" loading="lazy">
            <figcaption>
              <span class="kind-tag">{{.Kind}}</span>
              <h3>{{if .Subject}}{{.Subject}}{{else}}{{.Label}}{{end}}</h3>
            </figcaption>
          </figure>
        </a>
      </li>
      {{end}}
    </ul>
  </section>
  {{end}}
  </div>
</div>
<script>
  // Founder direction (2026-08-17): "in the same way that live match and wotan hero rankings
  // is live updating make promptoverse a gallery home page live update when new nodes are
  // published." Same idiom as OKEMILY/tournaments.html's loadHeroLeaderboard: fetch on load,
  // then setInterval, full innerHTML re-render from the latest API response each tick (not an
  // incremental DOM diff -- simpler, and this list is small enough that re-rendering it whole
  // is cheap). 10s poll, same as tournaments.html's STATS_POLL_MS: this is DB-backed data that
  // changes on the order of minutes (a new node published), not live-match's several-times-a-
  // second positional state, so a 3s poll would just be waste.
  (function () {
    var GALLERY_POLL_MS = 10000;
    var root = document.getElementById('gallery-root');
    // knownSlugs seeds from the server-rendered cards already on the page,
    // so the first poll doesn't treat everything as "new".
    var knownSlugs = {};
    Array.prototype.forEach.call(root.querySelectorAll('ul.gallery > li > a'), function (a) {
      var href = a.getAttribute('href') || '';
      var parts = href.split('/').filter(Boolean);
      var slug = parts[parts.length - 1];
      if (slug) knownSlugs[slug] = true;
    });

    function escapeHtml(s) {
      return String(s).replace(/[&<>"']/g, function (c) {
        return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
      });
    }

    function styleSlug(label) {
      return label.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '');
    }

    function cardEl(n) {
      // Try the thumbnail cmd/promptoverse-thumbnails generates first; if
      // it doesn't exist yet (a brand-new node, or the cron hasn't run
      // since it published) the 404 falls back to the full original once,
      // via onerror -- the client-side equivalent of the Go renderer's own
      // os.Stat check for statically-rendered pages, since JS can't stat
      // the filesystem directly. Founder: "falls back to full size if they
      // arent [available]."
      var base = '/prompt-o-verse/' + encodeURIComponent(n.slug) + '/' + encodeURIComponent(n.slug);
      var thumb = base + '-thumb.jpg';
      var original = base + '.png';
      var title = n.subject ? n.subject : n.label;
      var alt = escapeHtml(n.label) + (n.subject ? ' &mdash; ' + escapeHtml(n.subject) : '');
      var li = document.createElement('li');
      li.innerHTML = '<a href="/prompt-o-verse/' + encodeURIComponent(n.slug) + '/">' +
        '<figure><img src="' + thumb + '" alt="' + alt + '" loading="lazy" ' +
        'onerror="this.onerror=null;this.src=\'' + original + '\';">' +
        '<figcaption><span class="kind-tag">' + escapeHtml(n.kind) + '</span>' +
        '<h3>' + escapeHtml(title) + '</h3></figcaption></figure></a>';
      return li;
    }

    function updateCategoryCount(section, count) {
      var el = section.querySelector('.category-count');
      if (el) el.textContent = count + ' ' + (count === 1 ? 'variant' : 'variants');
    }

    function ensureCategorySection(label) {
      var slug = styleSlug(label);
      var existing = document.getElementById('cat-' + slug);
      if (existing) return existing.closest('section.category');
      var section = document.createElement('section');
      section.className = 'category';
      section.setAttribute('aria-labelledby', 'cat-' + slug);
      section.innerHTML = '<h2 id="cat-' + slug + '"><a href="/prompt-o-verse/style/' + slug + '/">' +
        escapeHtml(label) + '</a><span class="category-count"></span></h2><ul class="gallery"></ul>';
      root.appendChild(section);
      return section;
    }

    // insertNewCards is a real incremental patch, not a re-render --
    // founder-reported flicker (2026-08-18): "there is a page flicker now
    // it randomly flickers the first image in each tag" and, after an
    // earlier fix that only skipped re-rendering on ticks with NO change
    // at all, "im still getting a flicker." Root cause of the second
    // report: publishing was happening roughly every 90-130s during this
    // session's heavy concurrent use, well inside the 10s poll window, so
    // "skip when nothing changed" barely helped -- almost every tick DID
    // have a real change, and the old code still tore down and rebuilt
    // the ENTIRE grid (every existing <img>, not just the new one) for
    // that one new card. This only ever touches DOM for nodes not already
    // known; every existing card's <img> element is never recreated, so
    // it can never flicker regardless of how often something new lands.
    function insertNewCards(nodes) {
      var countBySlugified = {};
      nodes.forEach(function (n) {
        var s = styleSlug(n.label);
        countBySlugified[s] = (countBySlugified[s] || 0) + 1;
      });

      var touchedSections = {};
      nodes.forEach(function (n) {
        if (knownSlugs[n.slug]) return;
        knownSlugs[n.slug] = true;
        var section = ensureCategorySection(n.label);
        section.querySelector('ul.gallery').appendChild(cardEl(n));
        touchedSections[styleSlug(n.label)] = section;
      });

      Object.keys(touchedSections).forEach(function (slug) {
        updateCategoryCount(touchedSections[slug], countBySlugified[slug] || 0);
      });
    }

    function poll() {
      fetch('/api/v1/promptoverse/nodes')
        .then(function (res) { return res.json(); })
        .then(function (data) {
          var nodes = data && data.nodes ? data.nodes : [];
          if (nodes.length > 0) insertNewCards(nodes);
        })
        .catch(function () {
          // Silent -- the server-rendered page underneath is still fully
          // correct, just not refreshed with anything newer this tick.
        });
    }

    // No immediate poll() on load, unlike tournaments.html's leaderboards --
    // those start from an empty container, this page is already server-
    // rendered with the current nodes, so the first fetch only needs to
    // happen once GALLERY_POLL_MS has actually passed.
    setInterval(poll, GALLERY_POLL_MS);
  })();
</script>
` + authStripScript + `
</body>
</html>
`

const subjectTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>{{.Subject}} &mdash; Prompt-o-verse</title>
<meta name="description" content="Every style Prompt-o-verse has applied to {{.Subject}}.">
<style>
  :root {
    --bg: #101014; --panel-bg: #17171d; --accent: #7c8cff;
    --text-main: #eef0f4; --text-soft: #a8adb8; --text-whisper: #6f7480;
    --rule: #23262f;
  }
  @media (prefers-color-scheme: light) {
    :root {
      --bg: #fafafa; --panel-bg: #ffffff; --accent: #4451c7;
      --text-main: #14161c; --text-soft: #4a4f5c; --text-whisper: #8a8f9c;
      --rule: #e4e4e8;
    }
  }
  * { box-sizing: border-box; }
  body { margin: 0; background: var(--bg); color: var(--text-main); font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif; }
  .wrap { max-width: 1080px; margin: 0 auto; padding: 2.5rem 1.5rem 5rem; }
  nav.wordmark a { font-size: 0.72rem; letter-spacing: 0.28em; text-transform: uppercase; color: var(--text-whisper); text-decoration: none; }
  h1 { font-weight: 700; font-size: clamp(1.9rem, 5vw, 2.6rem); margin: 1.1rem 0 0.4rem; }
  .tagline { color: var(--text-soft); font-size: 1.02rem; margin-bottom: 2.4rem; }
  ul.gallery { list-style: none; margin: 1rem 0 0; padding: 0; display: grid; grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); gap: 1.1rem; }
  ul.gallery li { margin: 0; }
  ul.gallery a { display: block; text-decoration: none; color: inherit; }
  ul.gallery figure { margin: 0; background: var(--panel-bg); border: 1px solid var(--rule); border-radius: 10px; overflow: hidden; }
  ul.gallery img { width: 100%; aspect-ratio: 1 / 1; object-fit: cover; display: block; }
  ul.gallery figcaption { padding: 0.7rem 0.85rem; }
  .kind-tag { display: inline-block; font-size: 0.66rem; letter-spacing: 0.12em; text-transform: uppercase; color: var(--accent); margin-bottom: 0.3rem; }
  ul.gallery h2 { font-size: 0.92rem; font-weight: 600; margin: 0; }
  ul.gallery a:hover h2 { color: var(--accent); }
  nav.back { margin-top: 3rem; }
  nav.back a { font-size: 0.85rem; color: var(--text-whisper); text-decoration: none; }
  nav.back a:hover { color: var(--accent); }
  .mashups { margin-top: 2.6rem; padding-top: 1.8rem; border-top: 1px solid var(--rule); }
  .mashups h2 { font-size: 0.72rem; letter-spacing: 0.14em; text-transform: uppercase; color: var(--text-whisper); margin: 0 0 0.9rem; }
  .mashups ul { list-style: none; margin: 0; padding: 0; display: flex; flex-wrap: wrap; gap: 0.6rem; }
  .mashups a { display: inline-block; padding: 0.4rem 0.8rem; border: 1px solid var(--rule); border-radius: 999px; color: var(--text-soft); text-decoration: none; font-size: 0.85rem; }
  .mashups a:hover { color: var(--accent); border-color: var(--accent); }
  .nominate { margin-top: 2.6rem; padding: 1.4rem 1.6rem; background: var(--panel-bg); border: 1px solid var(--rule); border-radius: 10px; }
  .nominate h2 { font-size: 0.72rem; letter-spacing: 0.14em; text-transform: uppercase; color: var(--text-whisper); margin: 0 0 0.9rem; }
  .nominate input[type=text] { width: 100%; max-width: 340px; padding: 0.55rem 0.7rem; border-radius: 8px; border: 1px solid var(--rule); background: var(--bg); color: var(--text-main); font-size: 0.9rem; }
  .nominate button { margin-left: 0.6rem; padding: 0.55rem 1rem; border-radius: 8px; border: 1px solid var(--accent); background: var(--accent); color: #fff; font-size: 0.9rem; cursor: pointer; }
  .nominate button:disabled { opacity: 0.5; cursor: default; }
  .nominate .status { margin-top: 0.7rem; font-size: 0.82rem; color: var(--text-soft); }
  .nominate .g-signin { margin-bottom: 0.9rem; }
` + authStripCSS + `
</style>
</head>
<body>
<div class="wrap">
  <nav class="wordmark"><a href="/prompt-o-verse/">Prompt-o-verse &middot; A Gallery</a></nav>
  ` + authStripHTML + `
  <h1>{{.Subject}}</h1>
  <p class="tagline"><span id="leaf-gallery-count">{{.Count}} {{if eq .Count 1}}style{{else}}styles{{end}}</span> applied to this subject so far.</p>
  <div id="leaf-gallery-root" data-filter-key="subject" data-filter-value="{{.Subject}}" data-title-mode="label" data-count-noun="style">
  <ul class="gallery">
    {{range .Nodes}}<li>
      <a href="/prompt-o-verse/{{.Slug}}/">
        <figure>
          <img src="/prompt-o-verse/{{.Slug}}/{{.GalleryImageFile}}" alt="{{.Label}} &mdash; {{$.Subject}}" loading="lazy">
          <figcaption>
            <span class="kind-tag">{{.Kind}}</span>
            <h2>{{.Label}}</h2>
          </figcaption>
        </figure>
      </a>
    </li>
    {{end}}
  </ul>
  </div>
  {{if .Mashups}}<section class="mashups">
    <h2>Mashups featuring {{.Subject}}</h2>
    <ul>
      {{range .Mashups}}<li><a href="{{.Link}}">{{.Label}}</a></li>
      {{end}}
    </ul>
  </section>{{end}}
  <section class="nominate" id="nominate-root" data-subject="{{.Subject}}" data-google-client-id="{{.GoogleClientID}}">
    <h2>Nominate a mashup</h2>
    <p class="status" id="nominate-signed-out" style="display:none">{{if .GoogleClientID}}Sign in using the button in the header above to nominate a mashup.{{else}}Mashup nominations need an account &mdash; sign-in is not yet available on this box.{{end}}</p>
    <div id="nominate-form" style="display:none">
      <input type="text" id="nominate-partner" list="subject-options" placeholder="Combine {{.Subject}} with&hellip;">
      <datalist id="subject-options">
        {{range .OtherSubjects}}<option value="{{.}}">{{end}}
      </datalist>
      <button id="nominate-submit">Nominate</button>
    </div>
    <p class="status" id="nominate-status"></p>
  </section>
  <nav class="back"><a href="/prompt-o-verse/">&larr; All nodes</a></nav>
</div>
` + authStripScript + leafPollScript + `
<script>
(function () {
  var root = document.getElementById('nominate-root');
  var subject = root.getAttribute('data-subject');
  var formEl = document.getElementById('nominate-form');
  var signedOutEl = document.getElementById('nominate-signed-out');
  var statusEl = document.getElementById('nominate-status');
  var TOKEN_KEY = 'promptoverse_access_token';

  function setStatus(msg) { statusEl.textContent = msg; }

  // Auth state itself (sign in/out, token) is owned by the shared header
  // strip (authStripScript) -- this widget only reads the resulting token.
  if (localStorage.getItem(TOKEN_KEY)) {
    formEl.style.display = 'block';
  } else {
    signedOutEl.style.display = 'block';
  }

  document.getElementById('nominate-submit').addEventListener('click', function () {
    var partner = document.getElementById('nominate-partner').value.trim();
    if (!partner) { setStatus('Pick a subject to combine with first.'); return; }
    var token = localStorage.getItem(TOKEN_KEY);
    if (!token) { setStatus('Please sign in first.'); return; }
    setStatus('Nominating…');
    fetch('/api/v1/promptoverse/mashup-nominations', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Authorization': 'Bearer ' + token },
      body: JSON.stringify({ subject_a: subject, subject_b: partner })
    })
      .then(function (res) { return res.json().then(function (data) { return { ok: res.ok, data: data }; }); })
      .then(function (result) {
        if (result.ok) {
          setStatus('Nominated ' + subject + ' × ' + partner + ' — pending review.');
          document.getElementById('nominate-partner').value = '';
        } else {
          setStatus(result.data.error || result.data.message || 'Could not submit that nomination.');
        }
      })
      .catch(function () { setStatus('Could not submit that nomination.'); });
  });
})();
</script>
</body>
</html>
`

// styleTemplate mirrors subjectTemplate but for the other taxonomy axis --
// founder direction (2026-08-17): "we have no way to go from node up a
// level like im on the lego baseball card but theres no way for me to go
// to the lego page to show all those nodes." Every leaf's <h1>{{.Label}}</h1>
// links here (nodeView.StyleLink, always set -- unlike Subject, Label is
// required on every node so there's no "too few to bother" threshold the
// way subject pages have).
const styleTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>{{.Label}} &mdash; Prompt-o-verse</title>
<meta name="description" content="Every subject Prompt-o-verse has applied the {{.Label}} style to.">
<style>
  :root {
    --bg: #101014; --panel-bg: #17171d; --accent: #7c8cff;
    --text-main: #eef0f4; --text-soft: #a8adb8; --text-whisper: #6f7480;
    --rule: #23262f;
  }
  @media (prefers-color-scheme: light) {
    :root {
      --bg: #fafafa; --panel-bg: #ffffff; --accent: #4451c7;
      --text-main: #14161c; --text-soft: #4a4f5c; --text-whisper: #8a8f9c;
      --rule: #e4e4e8;
    }
  }
  * { box-sizing: border-box; }
  body { margin: 0; background: var(--bg); color: var(--text-main); font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif; }
  .wrap { max-width: 1080px; margin: 0 auto; padding: 2.5rem 1.5rem 5rem; }
  nav.wordmark a { font-size: 0.72rem; letter-spacing: 0.28em; text-transform: uppercase; color: var(--text-whisper); text-decoration: none; }
  h1 { font-weight: 700; font-size: clamp(1.9rem, 5vw, 2.6rem); margin: 1.1rem 0 0.4rem; }
  .tagline { color: var(--text-soft); font-size: 1.02rem; margin-bottom: 2.4rem; }
  ul.gallery { list-style: none; margin: 1rem 0 0; padding: 0; display: grid; grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); gap: 1.1rem; }
  ul.gallery li { margin: 0; }
  ul.gallery a { display: block; text-decoration: none; color: inherit; }
  ul.gallery figure { margin: 0; background: var(--panel-bg); border: 1px solid var(--rule); border-radius: 10px; overflow: hidden; }
  ul.gallery img { width: 100%; aspect-ratio: 1 / 1; object-fit: cover; display: block; }
  ul.gallery figcaption { padding: 0.7rem 0.85rem; }
  .kind-tag { display: inline-block; font-size: 0.66rem; letter-spacing: 0.12em; text-transform: uppercase; color: var(--accent); margin-bottom: 0.3rem; }
  ul.gallery h2 { font-size: 0.92rem; font-weight: 600; margin: 0; }
  ul.gallery a:hover h2 { color: var(--accent); }
  nav.back { margin-top: 3rem; }
  nav.back a { font-size: 0.85rem; color: var(--text-whisper); text-decoration: none; }
  nav.back a:hover { color: var(--accent); }
  .mashups { margin-top: 2.6rem; padding-top: 1.8rem; border-top: 1px solid var(--rule); }
  .mashups h2 { font-size: 0.72rem; letter-spacing: 0.14em; text-transform: uppercase; color: var(--text-whisper); margin: 0 0 0.9rem; }
  .mashups ul { list-style: none; margin: 0; padding: 0; display: flex; flex-wrap: wrap; gap: 0.6rem; }
  .mashups a { display: inline-block; padding: 0.4rem 0.8rem; border: 1px solid var(--rule); border-radius: 999px; color: var(--text-soft); text-decoration: none; font-size: 0.85rem; }
  .mashups a:hover { color: var(--accent); border-color: var(--accent); }
` + authStripCSS + `
</style>
</head>
<body>
<div class="wrap">
  <nav class="wordmark"><a href="/prompt-o-verse/">Prompt-o-verse &middot; A Gallery</a></nav>
  ` + authStripHTML + `
  <h1>{{.Label}}</h1>
  <p class="tagline"><span id="leaf-gallery-count">{{.Count}} {{if eq .Count 1}}node{{else}}nodes{{end}}</span> use this style so far.</p>
  <div id="leaf-gallery-root" data-filter-key="label" data-filter-value="{{.Label}}" data-title-mode="subject-or-label" data-count-noun="node">
  <ul class="gallery">
    {{range .Nodes}}<li>
      <a href="/prompt-o-verse/{{.Slug}}/">
        <figure>
          <img src="/prompt-o-verse/{{.Slug}}/{{.GalleryImageFile}}" alt="{{$.Label}}{{if .Subject}} &mdash; {{.Subject}}{{end}}" loading="lazy">
          <figcaption>
            <span class="kind-tag">{{.Kind}}</span>
            <h2>{{if .Subject}}{{.Subject}}{{else}}{{.Label}}{{end}}</h2>
          </figcaption>
        </figure>
      </a>
    </li>
    {{end}}
  </ul>
  </div>
  {{if .Mashups}}<section class="mashups">
    <h2>Mashups featuring {{.Label}}</h2>
    <ul>
      {{range .Mashups}}<li><a href="{{.Link}}">{{.Label}}</a></li>
      {{end}}
    </ul>
  </section>{{end}}
  <nav class="back"><a href="/prompt-o-verse/">&larr; All nodes</a></nav>
</div>
` + authStripScript + leafPollScript + `
</body>
</html>
`

type tagPair struct {
	Key   string
	Value string
}

type nodeView struct {
	Slug           string
	Label          string
	StyleLink      string // always set -- Label (unlike Subject) is required on every node
	Subject        string
	SubjectLink    string // "" unless this Subject has >=2 leaf nodes (founder direction)
	Kind           string
	EZPrompt       string
	ExpandedPrompt string
	ImageFile      string // the original -- kept for anything that still wants it directly
	// GalleryImageFile / HeroImageFile resolve to the thumbnail/optimized
	// version if cmd/promptoverse-thumbnails has generated one, else fall
	// back to ImageFile -- founder: "our webapp loads the optimized or the
	// thumbnail optimized versions if they are available and falls back to
	// full size if they arent." Gallery grids (index/subject/style pages)
	// use GalleryImageFile; a leaf page's own hero image uses HeroImageFile.
	GalleryImageFile string
	HeroImageFile    string
	PublishedISO     string
	PublishedDate    string
	TagPairs         []tagPair
	// GoogleClientID only matters when this nodeView is the ROOT context of
	// a rendered page (pageTemplate) -- harmlessly empty when reused as a
	// gallery-grid list item (index/subject/style templates use their own
	// page-level GoogleClientID for the header auth strip instead).
	GoogleClientID string
	// Variants only matters at the ROOT page context too (renderNodePage
	// populates it; toView leaves it nil for gallery-grid list items) --
	// S176-30, additional images for the same slug, rendered on the same
	// page rather than as separate leaf pages.
	Variants []variantView
}

// variantView is one additional generated image for a leaf node --
// see NodeVariant / renderNodePage.
type variantView struct {
	ImageFile      string
	EZPrompt       string
	ExpandedPrompt string
	Note           string
	PublishedDate  string
}

// mashupLinkView is one cross-link to another subject/style page shown
// under a "Mashups" section -- see mashups.go for how these get computed.
type mashupLinkView struct {
	Label string
	Link  string
}

type subjectPageView struct {
	Subject        string
	Count          int
	Nodes          []nodeView
	Mashups        []mashupLinkView
	GoogleClientID string   // "" disables the nomination widget's sign-in button
	OtherSubjects  []string // every other subject with a page, for the autocomplete list
}

type stylePageView struct {
	Label          string
	Count          int
	Nodes          []nodeView
	Mashups        []mashupLinkView
	GoogleClientID string
}

type categoryView struct {
	Slug  string
	Label string
	Count int
	Nodes []nodeView
}

// ThumbFileName / OptimizedFileName are the filenames
// cmd/promptoverse-thumbnails writes alongside a node's original image --
// shared here so the renderer and the thumbnail generator can never drift
// on the naming convention.
func ThumbFileName(slug string) string     { return slug + "-thumb.jpg" }
func OptimizedFileName(slug string) string { return slug + "-optimized.jpg" }

// hasGeneratedFile checks the filesystem, not the DB -- thumbnails are
// generated by a separate cron-driven tool (cmd/promptoverse-thumbnails)
// on its own schedule, so "does one exist yet" is a real on-disk question
// at render time, not something the Node/Store model tracks.
func (r *Renderer) hasGeneratedFile(slug, name string) bool {
	_, err := os.Stat(filepath.Join(r.OutputDir, slug, name))
	return err == nil
}

func (r *Renderer) toView(n Node) nodeView {
	keys := make([]string, 0, len(n.Tags))
	for k := range n.Tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]tagPair, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, tagPair{Key: k, Value: n.Tags[k]})
	}

	galleryImage := n.ImageFile
	if r.hasGeneratedFile(n.Slug, ThumbFileName(n.Slug)) {
		galleryImage = ThumbFileName(n.Slug)
	}
	heroImage := n.ImageFile
	if r.hasGeneratedFile(n.Slug, OptimizedFileName(n.Slug)) {
		heroImage = OptimizedFileName(n.Slug)
	}

	return nodeView{
		Slug:             n.Slug,
		Label:            n.Label,
		StyleLink:        "/prompt-o-verse/style/" + slugify(n.Label) + "/",
		Subject:          n.Subject,
		Kind:             n.Kind,
		EZPrompt:         n.EZPrompt,
		ExpandedPrompt:   n.ExpandedPrompt,
		ImageFile:        n.ImageFile,
		GalleryImageFile: galleryImage,
		HeroImageFile:    heroImage,
		PublishedISO:     n.PublishedAt.Format("2006-01-02"),
		PublishedDate:    n.PublishedAt.Format("January 2, 2006"),
		TagPairs:         pairs,
		GoogleClientID:   r.GoogleClientID,
	}
}

// RenderNode writes one node's page in isolation, with no subject-link
// context (SubjectLink stays empty) -- used for simple/standalone renders
// and tests. Live publishing should use RenderAll instead, since whether a
// Subject is linkable depends on how many *other* nodes share it.
func (r *Renderer) RenderNode(n Node) error {
	return r.renderNodePage(n, "")
}

func (r *Renderer) renderNodePage(n Node, subjectLink string) error {
	tmpl, err := template.New("node").Parse(pageTemplate)
	if err != nil {
		return err
	}
	dir := filepath.Join(r.OutputDir, n.Slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	f, err := os.Create(filepath.Join(dir, "index.html"))
	if err != nil {
		return err
	}
	defer f.Close()
	v := r.toView(n)
	v.SubjectLink = subjectLink
	if r.Store != nil {
		variants, err := r.Store.ListVariants(n.Slug)
		if err != nil {
			return fmt.Errorf("list variants for %s: %w", n.Slug, err)
		}
		for _, nv := range variants {
			v.Variants = append(v.Variants, variantView{
				ImageFile:      nv.ImageFile,
				EZPrompt:       nv.EZPrompt,
				ExpandedPrompt: nv.ExpandedPrompt,
				Note:           nv.Note,
				PublishedDate:  nv.CreatedAt.Format("January 2, 2006"),
			})
		}
	}
	return tmpl.Execute(f, v)
}

// RenderIndex writes the /prompt-o-verse/ gallery listing, grouped by
// Label/style (the subcategory) -- e.g. "Renaissance oil painting" groups
// its baseball-card leaf and its Master Chief leaf together, per founder
// direction ("stained glass is top level... we don't necessarily need
// separate pages for the 2 different mediums"). Category order follows
// each style's first (most recent) appearance in the already-sorted
// (published_at DESC) node list.
func (r *Renderer) RenderIndex(nodes []Node) error {
	tmpl, err := template.New("index").Parse(indexTemplate)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(r.OutputDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", r.OutputDir, err)
	}
	f, err := os.Create(filepath.Join(r.OutputDir, "index.html"))
	if err != nil {
		return err
	}
	defer f.Close()

	order := make([]string, 0)
	byLabel := make(map[string][]nodeView)
	for _, n := range nodes {
		v := r.toView(n)
		if _, seen := byLabel[n.Label]; !seen {
			order = append(order, n.Label)
		}
		byLabel[n.Label] = append(byLabel[n.Label], v)
	}
	categories := make([]categoryView, 0, len(order))
	for _, label := range order {
		categories = append(categories, categoryView{
			Slug:  slugify(label),
			Label: label,
			Count: len(byLabel[label]),
			Nodes: byLabel[label],
		})
	}

	return tmpl.Execute(f, struct {
		Categories     []categoryView
		GoogleClientID string
	}{Categories: categories, GoogleClientID: r.GoogleClientID})
}

// RenderAll re-renders every node's own page, the index, and every subject
// page from the full current node list -- the correct entrypoint whenever a
// node is created, since whether a Subject is linkable (>=2 leaves,
// founder direction) can change for *existing* nodes too: publishing the
// second leaf under a Subject is what makes the first leaf's own page need
// a link it didn't have before. Cheap enough to always do in full at this
// data scale (dozens of nodes) rather than track incremental invalidation.
func (r *Renderer) RenderAll(nodes []Node) error {
	subjectCounts := make(map[string]int, len(nodes))
	for _, n := range nodes {
		if n.Subject != "" {
			subjectCounts[n.Subject]++
		}
	}

	for _, n := range nodes {
		link := ""
		if n.Subject != "" && subjectCounts[n.Subject] >= 2 {
			link = "/prompt-o-verse/subject/" + slugify(n.Subject) + "/"
		}
		if err := r.renderNodePage(n, link); err != nil {
			return fmt.Errorf("render node %s: %w", n.Slug, err)
		}
	}

	if err := r.RenderIndex(nodes); err != nil {
		return err
	}

	if err := r.renderSubjectPages(nodes, subjectCounts); err != nil {
		return err
	}

	return r.renderStylePages(nodes)
}

// renderSubjectPages writes /prompt-o-verse/subject/<slug>/index.html for
// every Subject with >=2 leaf nodes -- founder direction: "if taxonomies
// have at least 2 leaf nodes make the subject tag clickable... show all
// leaf nodes [sharing that subject]." Subjects with 0 or 1 leaves get no
// page (nothing meaningful to browse to yet).
func (r *Renderer) renderSubjectPages(nodes []Node, subjectCounts map[string]int) error {
	order := make([]string, 0)
	bySubject := make(map[string][]nodeView)
	for _, n := range nodes {
		if n.Subject == "" || subjectCounts[n.Subject] < 2 {
			continue
		}
		if _, seen := bySubject[n.Subject]; !seen {
			order = append(order, n.Subject)
		}
		bySubject[n.Subject] = append(bySubject[n.Subject], r.toView(n))
	}

	// Only cross-link to a subject that actually has its own page --
	// mashupCrossLinks can name a subject with <2 nodes (no page yet),
	// which would be a dead link.
	hasPage := make(map[string]bool, len(order))
	for _, s := range order {
		hasPage[s] = true
	}
	crossLinks := r.subjectMashupCrossLinks()

	tmpl, err := template.New("subject").Parse(subjectTemplate)
	if err != nil {
		return err
	}
	for _, subject := range order {
		dir := filepath.Join(r.OutputDir, "subject", slugify(subject))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
		f, err := os.Create(filepath.Join(dir, "index.html"))
		if err != nil {
			return err
		}
		otherSubjects := make([]string, 0, len(order)-1)
		for _, s := range order {
			if s != subject {
				otherSubjects = append(otherSubjects, s)
			}
		}
		view := subjectPageView{
			Subject:        subject,
			Count:          len(bySubject[subject]),
			Nodes:          bySubject[subject],
			Mashups:        buildMashupLinks(crossLinks[subject], hasPage, "/prompt-o-verse/subject/"),
			GoogleClientID: r.GoogleClientID,
			OtherSubjects:  otherSubjects,
		}
		err = tmpl.Execute(f, view)
		f.Close()
		if err != nil {
			return fmt.Errorf("render subject page %s: %w", subject, err)
		}
	}
	return nil
}

// buildMashupLinks turns a raw related-label list into stable, sorted
// {Label, Link} view entries, dropping any label that doesn't actually
// have a page to link to.
func buildMashupLinks(related []string, hasPage map[string]bool, urlPrefix string) []mashupLinkView {
	labels := make([]string, 0, len(related))
	for _, r := range related {
		if hasPage[r] {
			labels = append(labels, r)
		}
	}
	sort.Strings(labels)
	links := make([]mashupLinkView, 0, len(labels))
	for _, l := range labels {
		links = append(links, mashupLinkView{Label: l, Link: urlPrefix + slugify(l) + "/"})
	}
	return links
}

// renderStylePages writes /prompt-o-verse/style/<slug>/index.html for every
// distinct Label -- founder direction: "we have no way to go from node up a
// level like im on the lego baseball card but theres no way for me to go
// to the lego page to show all those nodes." Unlike renderSubjectPages,
// there's no >=2 threshold: Label is required on every node (it's the
// primary taxonomy axis, already the index's own grouping key), so even a
// style with exactly 1 leaf still gets a real page.
func (r *Renderer) renderStylePages(nodes []Node) error {
	order := make([]string, 0)
	byLabel := make(map[string][]nodeView)
	for _, n := range nodes {
		if _, seen := byLabel[n.Label]; !seen {
			order = append(order, n.Label)
		}
		byLabel[n.Label] = append(byLabel[n.Label], r.toView(n))
	}

	// Every style label has a page (no >=2 threshold, unlike subjects),
	// so every label in `order` is always a valid cross-link target.
	hasPage := make(map[string]bool, len(order))
	for _, l := range order {
		hasPage[l] = true
	}
	crossLinks := r.styleMashupCrossLinks()

	tmpl, err := template.New("style").Parse(styleTemplate)
	if err != nil {
		return err
	}
	for _, label := range order {
		dir := filepath.Join(r.OutputDir, "style", slugify(label))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
		f, err := os.Create(filepath.Join(dir, "index.html"))
		if err != nil {
			return err
		}
		view := stylePageView{
			Label:          label,
			Count:          len(byLabel[label]),
			Nodes:          byLabel[label],
			Mashups:        buildMashupLinks(crossLinks[label], hasPage, "/prompt-o-verse/style/"),
			GoogleClientID: r.GoogleClientID,
		}
		err = tmpl.Execute(f, view)
		f.Close()
		if err != nil {
			return fmt.Errorf("render style page %s: %w", label, err)
		}
	}
	return nil
}

func slugify(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out = append(out, r)
		case r >= 'A' && r <= 'Z':
			out = append(out, r+('a'-'A'))
		case r == ' ' || r == '-' || r == '_':
			out = append(out, '-')
		}
	}
	return string(out)
}
