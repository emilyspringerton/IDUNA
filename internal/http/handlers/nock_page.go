package handlers

// nock_page.go — serves the real, built NOCK React+TypeScript app (frontend/nock, Vite) at
// /admin/nock/*. `go:embed` bakes the built dist/ directory straight into the IDUNA binary --
// no Node process, no separate static-file deploy step, matching this monorepo's own general
// bias toward "no extra runtime dependency in production" (the same real reason this repo's
// other admin pages are plain Go string constants, just applied here via a real build step
// instead, since a hand-rolled Photoshop-shaped UI genuinely needs a real component/state
// framework, not another raw HTML string).
//
// Real, honest limitation named directly: the embedded bundle is whatever was checked in at
// `go build` time (frontend/nock/dist/, committed to the repo) -- a frontend source change
// needs `npm run build` re-run and the new dist/ committed before it reaches a running IDUNA
// binary. A real CI build step (frontend/nock's own `npm ci && npm run build` wired into
// whatever builds this repo) is real, named future follow-up, not built in this pass.
//
// A bare GET /admin/nock/ is served by plain http.FileServer's own standard behavior (it
// serves dist/index.html for a directory request automatically) -- no separate page handler is
// needed for v0, which has no client-side routing yet; a real SPA-fallback (any unknown path
// under /admin/nock/ also serving index.html) is real, named future work for whenever this app
// grows deep-linkable routes.

import (
	"io/fs"
	"net/http"

	nockui "iduna/frontend/nock"
)

// NockAssetsHandler serves the built NOCK app (index.html at the root, plus every real built
// JS/CSS/favicon asset) at /admin/nock/.
type NockAssetsHandler struct {
	fsys http.Handler
}

// NewNockAssetsHandler strips nockui.Dist's own embedded dist/ prefix so URLs match the plain
// "/admin/nock/assets/foo.js" shape the built index.html itself already requests (see
// vite.config.ts's own `base: '/admin/nock/'`).
func NewNockAssetsHandler() (*NockAssetsHandler, error) {
	sub, err := fs.Sub(nockui.Dist, "dist")
	if err != nil {
		return nil, err
	}
	return &NockAssetsHandler{fsys: http.StripPrefix("/admin/nock/", http.FileServer(http.FS(sub)))}, nil
}

func (h *NockAssetsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.fsys.ServeHTTP(w, r)
}
