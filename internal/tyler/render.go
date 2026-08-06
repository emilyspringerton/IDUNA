// render.go writes TYLER episodes as static HTML, styled as a dedicated
// reading experience on the IDUNA style guide (../../styles.css: cream
// paper background, gold accent, Cormorant Garamond serif headlines) —
// deliberately NOT the blog's dark developer-blog theme, since TYLER
// episodes are meant to be read like a script/book, not scanned like a
// changelog. Includes a "Listen" audio button using the browser's own
// speechSynthesis API, same proven pattern as blog/render.go's, just
// restyled to match.
package tyler

import (
	"fmt"
	"html/template"
	"os"
	"path/filepath"
)

type Renderer struct {
	OutputDir string // e.g. /var/www/okemily/tyler
}

const pageTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>{{.Title}} &mdash; TYLER</title>
<style>
  :root {
    --bg: #f3ede2; --panel-bg: #f8f3ea; --gold: #b89b62;
    --text-main: #3f3a34; --text-soft: #6f6860; --text-whisper: #847c73;
    --rule: color-mix(in srgb, var(--gold) 35%, white 65%);
  }
  @media (prefers-color-scheme: dark) {
    :root {
      --bg: #1c1815; --panel-bg: #241f1a; --gold: #c9a86a;
      --text-main: #ece5d8; --text-soft: #bdb3a3; --text-whisper: #8f8577;
      --rule: color-mix(in srgb, var(--gold) 30%, black 70%);
    }
  }
  * { box-sizing: border-box; }
  body {
    margin: 0; background: var(--bg); color: var(--text-main);
    font-family: Georgia, "Cormorant Garamond", serif;
    line-height: 1.75;
  }
  .wrap { max-width: 700px; margin: 0 auto; padding: 3rem 1.5rem 6rem; }
  .wordmark {
    font-family: "Inter", "Segoe UI", sans-serif;
    font-size: 0.72rem; letter-spacing: 0.28em; text-transform: uppercase;
    color: var(--text-whisper); text-decoration: none;
  }
  .series-tag {
    margin: 1.6rem 0 0.3rem; font-family: "Inter", "Segoe UI", sans-serif;
    font-size: 0.75rem; letter-spacing: 0.18em; text-transform: uppercase;
    color: var(--gold);
  }
  h1.title {
    margin: 0 0 0.3rem; font-weight: 500; font-size: clamp(2rem, 5vw, 2.9rem);
    color: var(--text-main); line-height: 1.15;
  }
  .build-tag { font-size: 0.85rem; color: var(--text-whisper); margin-bottom: 2.2rem; }
  .listen-btn {
    display: inline-flex; align-items: center; gap: 0.5rem;
    margin-bottom: 2.4rem; padding: 0.55rem 1.1rem;
    background: var(--panel-bg); color: var(--text-main);
    border: 1px solid color-mix(in srgb, var(--gold) 55%, white 45%);
    border-radius: 999px; font-family: "Inter", "Segoe UI", sans-serif;
    font-size: 0.85rem; cursor: pointer; transition: border-color 160ms ease;
  }
  .listen-btn:hover { border-color: var(--gold); }
  .listen-btn[data-state="playing"] { color: var(--gold); border-color: var(--gold); }
  .listen-btn[disabled] { opacity: 0.4; cursor: not-allowed; }
  .body-text { font-size: 1.15rem; }
  .body-text p { margin: 0 0 1.35rem; }
  .body-text h1, .body-text h2, .body-text h3 {
    font-family: Georgia, "Cormorant Garamond", serif; font-weight: 600;
    color: var(--text-main); margin: 2.2rem 0 0.9rem;
  }
  .body-text h1 { font-size: 1.7rem; }
  .body-text h2 { font-size: 1.4rem; }
  .body-text h3 { font-size: 1.15rem; letter-spacing: 0.02em; }
  .body-text strong { color: var(--gold); font-weight: 600; }
  .body-text hr { border: none; border-top: 1px solid var(--rule); margin: 2.2rem 0; }
  .body-text pre {
    font-family: "SF Mono", Menlo, Consolas, monospace; font-size: 0.88rem;
    background: var(--panel-bg); border: 1px solid var(--rule); border-radius: 6px;
    padding: 1rem 1.2rem; white-space: pre-wrap; color: var(--text-soft);
    margin: 0 0 1.35rem;
  }
  .body-text ul.checklist { list-style: none; padding: 0; margin: 0 0 1.35rem; font-size: 1rem; }
  .body-text ul.checklist li { margin: 0.4rem 0; color: var(--text-soft); }
  .body-text ul.checklist input { margin-right: 0.5rem; accent-color: var(--gold); }
  .body-text table {
    width: 100%; border-collapse: collapse; margin: 0 0 1.6rem; font-size: 0.95rem;
    font-family: "Inter", "Segoe UI", sans-serif;
  }
  .body-text th, .body-text td {
    border: 1px solid var(--rule); padding: 0.55rem 0.7rem; text-align: left; vertical-align: top;
  }
  .body-text th { color: var(--gold); font-weight: 600; }
  .back {
    display: block; margin-top: 3.5rem; font-family: "Inter", "Segoe UI", sans-serif;
    font-size: 0.85rem; color: var(--text-whisper); text-decoration: none;
  }
  .back:hover { color: var(--gold); }
</style>
</head>
<body>
<div class="wrap">
  <a class="wordmark" href="/tyler/">TYLER &middot; A Reading Room</a>
  <div class="series-tag">{{.Series}} &middot; {{.EpisodeTag}}</div>
  <h1 class="title">{{.Title}}</h1>
  <div class="build-tag">Build {{.Build}} &middot; Published {{.PublishedDate}}</div>
  <button class="listen-btn" id="listen-btn" data-state="idle" type="button">&#9654; Listen</button>
  <div class="body-text" id="episode-body">{{.BodyHTML}}</div>
  <a class="back" href="/tyler/">&larr; All episodes</a>
</div>
<script>
(function () {
  var btn = document.getElementById("listen-btn");
  if (!("speechSynthesis" in window)) { btn.disabled = true; btn.textContent = "Listen (unsupported)"; return; }
  var synth = window.speechSynthesis;
  var keepAlive = null;
  function setState(state, label) { btn.setAttribute("data-state", state); btn.innerHTML = label; }
  function stop() {
    if (keepAlive) { clearInterval(keepAlive); keepAlive = null; }
    synth.cancel();
    setState("idle", "&#9654; Listen");
  }
  btn.addEventListener("click", function () {
    if (synth.speaking || synth.pending) { stop(); return; }
    synth.cancel();
    var text = document.getElementById("episode-body").innerText;
    var utter = new SpeechSynthesisUtterance(text);
    utter.onend = stop;
    utter.onerror = stop;
    synth.speak(utter);
    setState("playing", "&#9632; Stop");
    keepAlive = setInterval(function () {
      if (!synth.speaking) { clearInterval(keepAlive); keepAlive = null; return; }
      synth.pause();
      synth.resume();
    }, 10000);
  });
})();
</script>
</body>
</html>
`

const indexTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>TYLER &mdash; A Reading Room</title>
<style>
  :root {
    --bg: #f3ede2; --panel-bg: #f8f3ea; --gold: #b89b62;
    --text-main: #3f3a34; --text-soft: #6f6860; --text-whisper: #847c73;
    --rule: color-mix(in srgb, var(--gold) 35%, white 65%);
  }
  @media (prefers-color-scheme: dark) {
    :root {
      --bg: #1c1815; --panel-bg: #241f1a; --gold: #c9a86a;
      --text-main: #ece5d8; --text-soft: #bdb3a3; --text-whisper: #8f8577;
      --rule: color-mix(in srgb, var(--gold) 30%, black 70%);
    }
  }
  * { box-sizing: border-box; }
  body { margin: 0; background: var(--bg); color: var(--text-main); font-family: Georgia, "Cormorant Garamond", serif; }
  .wrap { max-width: 700px; margin: 0 auto; padding: 3rem 1.5rem 6rem; }
  .wordmark {
    font-family: "Inter", "Segoe UI", sans-serif; font-size: 0.72rem; letter-spacing: 0.28em;
    text-transform: uppercase; color: var(--text-whisper); text-decoration: none;
  }
  h1 { font-weight: 500; font-size: clamp(2rem, 5vw, 2.6rem); margin: 1.2rem 0 0.4rem; }
  .tagline { color: var(--text-soft); font-size: 1.05rem; margin-bottom: 2.5rem; }
  .episode {
    display: block; padding: 1.3rem 0; border-bottom: 1px solid var(--rule);
    text-decoration: none; color: inherit;
  }
  .episode .series-tag {
    font-family: "Inter", "Segoe UI", sans-serif; font-size: 0.72rem; letter-spacing: 0.16em;
    text-transform: uppercase; color: var(--gold);
  }
  .episode h2 { font-size: 1.35rem; font-weight: 600; margin: 0.3rem 0 0.2rem; }
  .episode:hover h2 { color: var(--gold); }
  .episode .meta { font-family: "Inter", "Segoe UI", sans-serif; font-size: 0.82rem; color: var(--text-whisper); }
</style>
</head>
<body>
<div class="wrap">
  <a class="wordmark" href="/">EINHORN_INDUSTRIAL</a>
  <h1>TYLER &mdash; A Reading Room</h1>
  <p class="tagline">Episode scripts from a documentary about a man who won't stay in his own year.</p>
  {{range .Episodes}}
  <a class="episode" href="/tyler/{{.Slug}}/">
    <div class="series-tag">{{.Series}} &middot; {{.EpisodeTag}}</div>
    <h2>{{.Title}}</h2>
    <div class="meta">Build {{.Build}} &middot; {{.PublishedDate}}</div>
  </a>
  {{end}}
</div>
</body>
</html>
`

type episodeView struct {
	Slug          string
	Title         string
	Series        string
	EpisodeTag    string
	Build         string
	PublishedDate string
	BodyHTML      template.HTML
}

type indexView struct {
	Episodes []episodeView
}

func toView(e Episode) episodeView {
	return episodeView{
		Slug:          e.Slug,
		Title:         e.Title,
		Series:        e.Series,
		EpisodeTag:    e.EpisodeTag,
		Build:         e.Build,
		PublishedDate: e.PublishedAt.Format("January 2, 2006"),
		BodyHTML:      template.HTML(toHTML(e.Body)),
	}
}

// RenderEpisode writes one episode's page to OutputDir/<slug>/index.html.
func (r *Renderer) RenderEpisode(e Episode) error {
	tmpl, err := template.New("episode").Parse(pageTemplate)
	if err != nil {
		return err
	}
	dir := filepath.Join(r.OutputDir, e.Slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	f, err := os.Create(filepath.Join(dir, "index.html"))
	if err != nil {
		return err
	}
	defer f.Close()
	return tmpl.Execute(f, toView(e))
}

// RenderIndex writes the /tyler/ listing page from all episodes.
func (r *Renderer) RenderIndex(episodes []Episode) error {
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

	views := make([]episodeView, len(episodes))
	for i, e := range episodes {
		views[i] = toView(e)
	}
	return tmpl.Execute(f, indexView{Episodes: views})
}
