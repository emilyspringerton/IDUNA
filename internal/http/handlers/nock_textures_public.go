package handlers

// nock_textures_public.go -- a real, PUBLIC (unauthenticated), READ-ONLY by-name image route for
// NOCK texture-library entries: GET /api/v1/nock-textures/by-name/{name}/image. Real consumer:
// SHANKPIT's native client fetching its per-gun bullet-hole decals (`bullet-hole-<gun>`, authored
// in PARENA -- PARENA/stdlib/shankpit/textures/bullet_hole_<gun>.prn -- rendered and stored by
// NOCK; founder real-time 2026-10-01: "using the nock tools parena texture generator and
// database"). Same posture as nock_door_scripts_public.go / shankpit_sprays' public image
// route: a separate, minimal handler (GET image only, no list/create/update/delete) from the
// admin-gated NockTexturesHandler, so a routing mistake can't expose a write path. To avoid
// publishing the whole private texture library, only names under an allowlisted prefix are
// served; everything else is a 404 indistinguishable from "no such texture".

import (
	"net/http"
	"net/url"
	"strings"

	"iduna/internal/nock"
)

// publicTexturePrefixes -- texture names a native game client may fetch without a login.
var publicTexturePrefixes = []string{"bullet-hole-"}

// NockTexturesPublicHandler serves GET /api/v1/nock-textures/by-name/{name}/image.
type NockTexturesPublicHandler struct {
	Store *nock.TextureStore
}

func publicTextureNameAllowed(name string) bool {
	for _, p := range publicTexturePrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func (h *NockTexturesPublicHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		mmoWriteError(w, http.StatusMethodNotAllowed, "this endpoint is read-only")
		return
	}
	const prefix = "/api/v1/nock-textures/by-name/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, prefix), "/")
	if len(parts) != 2 || parts[1] != "image" {
		http.NotFound(w, r)
		return
	}
	name, err := url.PathUnescape(parts[0])
	if err != nil || !publicTextureNameAllowed(name) {
		http.NotFound(w, r)
		return
	}
	t, err := h.Store.GetTextureByName(r.Context(), name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Write(t.PNGData) //nolint:errcheck
}
