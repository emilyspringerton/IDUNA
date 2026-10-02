package handlers

// shankpit_models_public.go — a real, PUBLIC (unauthenticated), READ-ONLY route that serves a NOCK
// widget as a model for the native SHANKPIT client (card #447: "use the model in 3p"). The hammer is
// modelled in Blender, imported through NOCK's Widgets glTF importer and saved as the widget
// MODEL_HAMMER; the game fetches it here at startup and draws its boxes in the player's hand.
//
// Only widgets whose name starts with shankpit.ModelWidgetPrefix ("MODEL_") are served -- every other
// widget stays admin-only, so this can never become a general widget dump. Like the public level
// export the native client has no IDUNA login, so the route is unauthenticated; the data is the
// model's own box list, nothing private. GET only; no write method exists on this type.

import (
	"net/http"
	"strings"

	"iduna/internal/shankpit"
)

// ShankpitModelsPublicHandler serves GET /api/v1/shankpit-models/{NAME}.
type ShankpitModelsPublicHandler struct {
	Widgets *shankpit.WidgetStore
}

func (h *ShankpitModelsPublicHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		mmoWriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/v1/shankpit-models/")
	if name == "" || strings.Contains(name, "/") || !strings.HasPrefix(name, shankpit.ModelWidgetPrefix) {
		http.NotFound(w, r)
		return
	}
	widget, err := h.Widgets.GetWidgetByName(r.Context(), name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, http.StatusOK, map[string]any{"name": widget.Name, "walls": widget.Walls})
}
