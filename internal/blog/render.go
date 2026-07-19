package blog

import (
	"fmt"
	"html"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Renderer writes posts as static HTML into OutputDir, matching okemily.com's
// existing visual style (same CSS variables/layout as index.html) so the
// blog reads as part of the same site, not a bolted-on WordPress theme.
type Renderer struct {
	OutputDir string // e.g. /var/www/okemily/blog
}

const pageTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>{{.Title}} &mdash; Emily Blog</title>
<style>
  :root { --bg: #0b0c10; --bg-card: #14161c; --fg: #eef0f4; --fg-dim: #a8adb8; --accent: #7c8cff; --border: #23262f; }
  @media (prefers-color-scheme: light) {
    :root { --bg: #fafafa; --bg-card: #ffffff; --fg: #14161c; --fg-dim: #4a4f5c; --accent: #4451c7; --border: #e4e4e8; }
  }
  * { box-sizing: border-box; }
  body { margin: 0; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif; background: var(--bg); color: var(--fg); line-height: 1.65; }
  .wrap { max-width: 700px; margin: 0 auto; padding: 3rem 1.5rem 5rem; }
  .wordmark { font-size: 1rem; letter-spacing: 0.08em; color: var(--fg-dim); text-transform: uppercase; }
  h1 { font-size: 2rem; margin: 0.5rem 0 0.25rem; line-height: 1.2; }
  .meta { color: var(--fg-dim); font-size: 0.9rem; margin-bottom: 2rem; }
  .body p { color: var(--fg); margin: 0 0 1.2rem; }
  a { color: var(--accent); }
  .back { margin-top: 3rem; display: block; }
  .post-ad {
    margin-top: 3rem; padding-top: 1.5rem; border-top: 1px solid var(--border);
    font-size: 0.9rem; color: var(--fg-dim);
  }
  .post-ad a { font-weight: 600; }
</style>
</head>
<body>
<div class="wrap">
  <div class="wordmark"><a href="/">EINHORN_INDUSTRIAL</a> / Blog</div>
  <h1>{{.Title}}</h1>
  <p class="meta">By {{.Author}} &middot; {{.PublishedDate}}</p>
  <div class="body">{{.BodyHTML}}</div>
  <p class="post-ad">{{.AdLine}} <a href="{{.AdHref}}">{{.AdCTA}}</a></p>
  <a class="back" href="/blog/">&larr; All posts</a>
</div>
<script src="/dis.js" defer></script>
</body>
</html>
`

const indexTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Blog &mdash; EINHORN_INDUSTRIAL</title>
<style>
  :root { --bg: #0b0c10; --fg: #eef0f4; --fg-dim: #a8adb8; --accent: #7c8cff; --border: #23262f; }
  @media (prefers-color-scheme: light) {
    :root { --bg: #fafafa; --fg: #14161c; --fg-dim: #4a4f5c; --accent: #4451c7; --border: #e4e4e8; }
  }
  body { margin: 0; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif; background: var(--bg); color: var(--fg); line-height: 1.6; }
  .wrap { max-width: 700px; margin: 0 auto; padding: 3rem 1.5rem 5rem; }
  .wordmark { font-size: 1rem; letter-spacing: 0.08em; color: var(--fg-dim); text-transform: uppercase; }
  h1 { font-size: 1.8rem; }
  a { color: var(--accent); text-decoration: none; }
  .post { padding: 1rem 0; border-bottom: 1px solid var(--border); }
  .post h2 { font-size: 1.2rem; margin: 0 0 0.3rem; }
  .post .meta { color: var(--fg-dim); font-size: 0.85rem; }
</style>
</head>
<body>
<div class="wrap">
  <div class="wordmark"><a href="/">EINHORN_INDUSTRIAL</a> / Blog</div>
  <h1>Blog</h1>
  {{range .Posts}}
  <div class="post">
    <h2><a href="/blog/{{.Slug}}/">{{.Title}}</a></h2>
    <div class="meta">By {{.Author}} &middot; {{.PublishedDate}}</div>
  </div>
  {{end}}
</div>
</body>
</html>
`

type postView struct {
	Slug          string
	Title         string
	Author        string
	PublishedDate string
	BodyHTML      template.HTML
	AdLine        string
	AdCTA         string
	AdHref        string
}

// Default ad copy for posts that don't set their own (e.g. published before
// this field existed, or a future post that skips it). Existing posts are
// backfilled with unique per-post lines instead — see cmd/blog-adlines.
const (
	defaultAdLine = "STINKIES COMMISSAIRE — the first physical thing EINHORN_INDUSTRIAL has made."
	defaultAdCTA  = "Join the waiting list for the hoodie →"
	defaultAdHref = "/stinkies.html"
)

type indexView struct {
	Posts []postView
}

var paragraphSplit = regexp.MustCompile(`\n\s*\n`)

// toParagraphs does minimal, dependency-free "poor man's markdown": splits on
// blank lines into <p> tags and escapes everything else. Not full markdown —
// a deliberate scope cut (see package doc's memory-constraint rationale); a
// real markdown renderer is a reasonable future upgrade if posts need more
// than paragraphs/links.
func toParagraphs(body string) template.HTML {
	paras := paragraphSplit.Split(strings.TrimSpace(body), -1)
	var b strings.Builder
	for _, p := range paras {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		b.WriteString("<p>")
		b.WriteString(html.EscapeString(p))
		b.WriteString("</p>\n")
	}
	return template.HTML(b.String())
}

func toView(p Post) postView {
	adLine, adCTA, adHref := p.AdLine, p.AdCTA, p.AdHref
	if adLine == "" {
		adLine = defaultAdLine
	}
	if adCTA == "" {
		adCTA = defaultAdCTA
	}
	if adHref == "" {
		adHref = defaultAdHref
	}
	return postView{
		Slug:          p.Slug,
		Title:         p.Title,
		Author:        p.Author,
		PublishedDate: p.PublishedAt.Format("January 2, 2006"),
		BodyHTML:      toParagraphs(p.Body),
		AdLine:        adLine,
		AdCTA:         adCTA,
		AdHref:        adHref,
	}
}

// RenderPost writes one post's page to OutputDir/<slug>/index.html.
func (r *Renderer) RenderPost(p Post) error {
	tmpl, err := template.New("post").Parse(pageTemplate)
	if err != nil {
		return err
	}
	dir := filepath.Join(r.OutputDir, p.Slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	f, err := os.Create(filepath.Join(dir, "index.html"))
	if err != nil {
		return err
	}
	defer f.Close()
	return tmpl.Execute(f, toView(p))
}

// RenderIndex writes the /blog/ listing page from all posts.
func (r *Renderer) RenderIndex(posts []Post) error {
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

	views := make([]postView, len(posts))
	for i, p := range posts {
		views[i] = toView(p)
	}
	return tmpl.Execute(f, indexView{Posts: views})
}
