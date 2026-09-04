package handlers

// gfd_dungeon_roster.go — GFD Dungeon Roster manager (GFD-MOBSPAWN-001 Phase 5, the final
// phase of GoblinFoxDragon/docs2/MOB_SPAWN_NORTHSTAR.md). Same real direct-file-access precedent
// as the other three GFD admin pages: reads/writes GoblinFoxDragon/data/dungeon_roster.json
// directly.
//
// Real, honest difference from the other three: mob.DungeonRoster is real, compendium-grounded
// content with a compiled-in Go default (server/mob/dungeon.go) that keeps working even if this
// file is ever missing or malformed -- server/mobdrop and server/spawn, by contrast, start out
// genuinely empty/default-on with no compiled fallback content. This page edits the override
// file, not the compiled default; the compiled default is real, deliberate insurance against a
// broken or absent file, not something this page can touch.
//
// Order matters: apps2/mud's own GenerateDungeonSpawns selects DungeonRoster[dungeonIndex %
// len(DungeonRoster)] -- reordering rows here changes which dungeon a given index resolves to.
// This page preserves array order (no alphabetical re-sort on write, unlike the other three GFD
// admin pages, which are keyed by an id/kind with no real order dependency).

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
)

// GfdDungeonAssignment mirrors server/mob.DungeonBossAssignment's own real JSON shape exactly.
type GfdDungeonAssignment struct {
	Name  string   `json:"name"`
	Boss  string   `json:"boss"`
	Elite []string `json:"elite"`
}

// GfdDungeonRosterHandler serves the real CRUD API backing the Dungeon Roster page. Rows are
// addressed by their real array index (order-dependent, see the header comment above), not a
// separate id field -- there isn't one in the real underlying data.
//
//	GET    /admin/gfd-dungeon-roster/api/dungeons          -> [GfdDungeonAssignment, ...] (real array order preserved)
//	POST   /admin/gfd-dungeon-roster/api/dungeons          {GfdDungeonAssignment} -> 201, appended at the end
//	PATCH  /admin/gfd-dungeon-roster/api/dungeons/{index}  {GfdDungeonAssignment} -> 200, full replace of that row
//	DELETE /admin/gfd-dungeon-roster/api/dungeons/{index}  -> 204
type GfdDungeonRosterHandler struct {
	RosterJSONPath string
	mu             sync.Mutex
}

func (h *GfdDungeonRosterHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	const prefix = "/admin/gfd-dungeon-roster/api/dungeons"
	if !strings.HasPrefix(path, prefix) {
		http.NotFound(w, r)
		return
	}
	rest := strings.TrimPrefix(path, prefix)
	rest = strings.TrimPrefix(rest, "/")

	switch {
	case rest == "" && r.Method == http.MethodGet:
		h.list(w, r)
	case rest == "" && r.Method == http.MethodPost:
		h.create(w, r)
	case rest != "" && r.Method == http.MethodPatch:
		h.update(w, r, rest)
	case rest != "" && r.Method == http.MethodDelete:
		h.delete(w, r, rest)
	default:
		http.NotFound(w, r)
	}
}

func (h *GfdDungeonRosterHandler) readAll() ([]GfdDungeonAssignment, error) {
	data, err := os.ReadFile(h.RosterJSONPath)
	if err != nil {
		return nil, err
	}
	var roster []GfdDungeonAssignment
	if err := json.Unmarshal(data, &roster); err != nil {
		return nil, err
	}
	return roster, nil
}

func (h *GfdDungeonRosterHandler) writeAll(roster []GfdDungeonAssignment) error {
	data, err := json.MarshalIndent(roster, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(h.RosterJSONPath, data, 0644)
}

func (h *GfdDungeonRosterHandler) list(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	roster, err := h.readAll()
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, roster)
}

func (h *GfdDungeonRosterHandler) create(w http.ResponseWriter, r *http.Request) {
	var d GfdDungeonAssignment
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if d.Name == "" || d.Boss == "" {
		mmoWriteError(w, http.StatusBadRequest, "name and boss required")
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	roster, err := h.readAll()
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	roster = append(roster, d) // appended at the end -- preserves every existing dungeonIndex
	if err := h.writeAll(roster); err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

func (h *GfdDungeonRosterHandler) update(w http.ResponseWriter, r *http.Request, idxStr string) {
	idx, err := strconv.Atoi(idxStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid index")
		return
	}
	var d GfdDungeonAssignment
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if d.Name == "" || d.Boss == "" {
		mmoWriteError(w, http.StatusBadRequest, "name and boss required")
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	roster, err := h.readAll()
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if idx < 0 || idx >= len(roster) {
		mmoWriteError(w, http.StatusNotFound, "dungeon index out of range")
		return
	}
	roster[idx] = d
	if err := h.writeAll(roster); err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (h *GfdDungeonRosterHandler) delete(w http.ResponseWriter, r *http.Request, idxStr string) {
	idx, err := strconv.Atoi(idxStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid index")
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	roster, err := h.readAll()
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if idx < 0 || idx >= len(roster) {
		mmoWriteError(w, http.StatusNotFound, "dungeon index out of range")
		return
	}
	roster = append(roster[:idx], roster[idx+1:]...)
	if err := h.writeAll(roster); err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
