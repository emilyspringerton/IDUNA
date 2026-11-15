package main

// wotan_site.go -- the WOTAN site served by IDUNA itself, for running the arcade with no cloud (founder real-time,
// 2026-11-15: "ship new iduna" / "esports and stats up asap ... the full wotan games suite" / "matchmaking and bot
// queues up for our wotan arcade" / "get this running as emily os for raspi").
//
// In production nginx serves WOTAN's static files and sends /api/ to IDUNA (WOTAN ops/nginx-wotan.conf). With
// WOTAN_SITE_DIR set, IDUNA does the same on a second listener (WOTAN_ADDR, default 127.0.0.1:8090): the site's files,
// and IDUNA's own routes for the paths the pages call (/api/, /services/, ...), answered in-process by the same mux, so
// the pages, sign-in, leaderboards and the matchmaking WebSockets (shankpit-460, Bedrock Racers) share one origin and
// one program. Used by the run-anywhere release bundles (scripts/package-release.sh) and EmilyOS.

import (
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// wotanAPIPrefixes are answered by IDUNA, never by a file on disk: everything the WOTAN pages call on their own origin.
var wotanAPIPrefixes = []string{"/api/", "/services/", "/.well-known/", "/q/", "/nock/upload/"}

func wotanIsAPIPath(p string) bool {
	if p == "/health" {
		return true
	}
	for _, pre := range wotanAPIPrefixes {
		if strings.HasPrefix(p, pre) {
			return true
		}
	}
	return false
}

// wotanSiteHandler serves dir as the WOTAN site, sending API paths to api. A folder is served only through its
// index.html (no directory listings), and dot-files (.git, .github, ...) are never served.
func wotanSiteHandler(dir string, api http.Handler) http.Handler {
	root := http.Dir(dir)
	files := http.FileServer(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if wotanIsAPIPath(p) {
			api.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		clean := path.Clean("/" + p)
		for _, part := range strings.Split(clean, "/") {
			if strings.HasPrefix(part, ".") {
				http.NotFound(w, r)
				return
			}
		}
		if f, err := root.Open(clean); err == nil {
			st, serr := f.Stat()
			f.Close()
			if serr == nil && st.IsDir() {
				idx, ierr := root.Open(path.Join(clean, "index.html"))
				if ierr != nil {
					http.NotFound(w, r)
					return
				}
				idx.Close()
			}
		}
		// The game pages are rebuilt in place (wasm + host js), so a browser must revalidate rather than run a stale
		// pair; ETag-less static files still get Last-Modified from FileServer.
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	})
}

// serveWotanSite runs the WOTAN listener until it fails; a failure is logged, never fatal to IDUNA itself.
func serveWotanSite(dir, addr string, api http.Handler) {
	abs, err := filepath.Abs(dir)
	if err == nil {
		dir = abs
	}
	if st, err := os.Stat(filepath.Join(dir, "index.html")); err != nil || st.IsDir() {
		log.Printf("wotan site: not serving %s: no index.html there", dir)
		return
	}
	srv := &http.Server{Addr: addr, Handler: wotanSiteHandler(dir, api), ReadHeaderTimeout: 10 * time.Second}
	log.Printf("wotan site: %s on http://%s", dir, addr)
	if err := srv.ListenAndServe(); err != nil {
		log.Printf("wotan site: stopped: %v", err)
	}
}
