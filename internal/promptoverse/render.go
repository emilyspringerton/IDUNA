// render.go writes Prompt-o-verse nodes as static HTML — semantic markup
// throughout (article/figure/figcaption/dl-dt-dd/time/data), not div soup,
// per founder direction: the taxonomy data (top-level prompt, generated
// image, labeled tags) should be legible in the markup itself, not just
// styled to look right. Same "own renderer, IDUNA style guide" shape as
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
}

const pageTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>{{.Label}} &mdash; Prompt-o-verse</title>
<meta name="description" content="{{.TopLevelPrompt}}">
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
    padding: 0.2rem 0.7rem; margin-bottom: 0.6rem;
  }
  h1 { font-size: clamp(1.8rem, 5vw, 2.4rem); margin: 0 0 0.3rem; font-weight: 700; }
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
  dl.taxonomy { display: grid; grid-template-columns: max-content 1fr; gap: 0.55rem 1rem; margin: 0; }
  dl.taxonomy dt {
    font-size: 0.78rem; letter-spacing: 0.06em; text-transform: uppercase; color: var(--text-whisper);
    font-weight: 600;
  }
  dl.taxonomy dd { margin: 0; color: var(--text-main); }
  nav.back { margin-top: 3rem; }
  nav.back a { font-size: 0.85rem; color: var(--text-whisper); text-decoration: none; }
  nav.back a:hover { color: var(--accent); }
</style>
</head>
<body>
<div class="wrap">
  <nav class="wordmark"><a href="/prompt-o-verse/">Prompt-o-verse &middot; A Gallery</a></nav>
  <article>
    <header class="node-header">
      <span class="kind-tag">{{.Kind}}</span>
      <h1>{{.Label}}</h1>
      <p class="published">Published <time datetime="{{.PublishedISO}}">{{.PublishedDate}}</time></p>
    </header>

    <figure class="node-image">
      <img src="{{.ImageFile}}" alt="{{.Label}} &mdash; generated image" width="1024" height="1024" loading="lazy">
      <figcaption>Generated output for this taxonomy node.</figcaption>
    </figure>

    <section class="node-section" aria-labelledby="prompt-heading">
      <h2 id="prompt-heading">Top-Level Prompt</h2>
      <p class="prompt-text">{{.TopLevelPrompt}}</p>
    </section>

    <section class="node-section" aria-labelledby="taxonomy-heading">
      <h2 id="taxonomy-heading">Taxonomy</h2>
      <dl class="taxonomy">
        {{range .TagPairs}}<dt>{{.Key}}</dt><dd>{{.Value}}</dd>
        {{end}}
      </dl>
    </section>
  </article>
  <nav class="back"><a href="/prompt-o-verse/">&larr; All nodes</a></nav>
</div>
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
  .tagline { color: var(--text-soft); font-size: 1.02rem; margin-bottom: 2.2rem; max-width: 640px; }
  ul.gallery { list-style: none; margin: 0; padding: 0; display: grid; grid-template-columns: repeat(auto-fill, minmax(220px, 1fr)); gap: 1.1rem; }
  ul.gallery li { margin: 0; }
  ul.gallery a { display: block; text-decoration: none; color: inherit; }
  ul.gallery figure { margin: 0; background: var(--panel-bg); border: 1px solid var(--rule); border-radius: 10px; overflow: hidden; }
  ul.gallery img { width: 100%; aspect-ratio: 1 / 1; object-fit: cover; display: block; }
  ul.gallery figcaption { padding: 0.7rem 0.85rem; }
  .kind-tag { display: inline-block; font-size: 0.66rem; letter-spacing: 0.12em; text-transform: uppercase; color: var(--accent); margin-bottom: 0.3rem; }
  ul.gallery h2 { font-size: 0.95rem; font-weight: 600; margin: 0; }
  ul.gallery a:hover h2 { color: var(--accent); }
</style>
</head>
<body>
<div class="wrap">
  <nav class="wordmark"><a href="/">EINHORN_INDUSTRIAL</a></nav>
  <h1>Prompt-o-verse &mdash; A Gallery</h1>
  <p class="tagline">A browsable taxonomy of what's possible to ask a generative model for &mdash;
  each node pairs a real top-level prompt with its generated output and its labeled taxonomy tags.
  VS0 proof-of-concept, not the full vision.</p>
  <ul class="gallery">
    {{range .Nodes}}<li>
      <a href="/prompt-o-verse/{{.Slug}}/">
        <figure>
          <img src="{{.Slug}}/{{.ImageFile}}" alt="{{.Label}}" loading="lazy">
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
	Kind           string
	TopLevelPrompt string
	ImageFile      string
	PublishedISO   string
	PublishedDate  string
	TagPairs       []tagPair
}

func toView(n Node) nodeView {
	keys := make([]string, 0, len(n.Tags))
	for k := range n.Tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]tagPair, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, tagPair{Key: k, Value: n.Tags[k]})
	}
	return nodeView{
		Slug:           n.Slug,
		Label:          n.Label,
		Kind:           n.Kind,
		TopLevelPrompt: n.TopLevelPrompt,
		ImageFile:      n.ImageFile,
		PublishedISO:   n.PublishedAt.Format("2006-01-02"),
		PublishedDate:  n.PublishedAt.Format("January 2, 2006"),
		TagPairs:       pairs,
	}
}

// RenderNode writes one node's page (and copies nothing -- the image file
// must already exist at OutputDir/<slug>/<ImageFile> before this is called,
// same "images are static assets, not template data" split as the rest of
// this package) to OutputDir/<slug>/index.html.
func (r *Renderer) RenderNode(n Node) error {
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
	return tmpl.Execute(f, toView(n))
}

// RenderIndex writes the /prompt-o-verse/ gallery listing from all nodes.
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

	views := make([]nodeView, len(nodes))
	for i, n := range nodes {
		views[i] = toView(n)
	}
	return tmpl.Execute(f, struct{ Nodes []nodeView }{Nodes: views})
}
