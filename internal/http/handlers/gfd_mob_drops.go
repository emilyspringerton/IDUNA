package handlers

// gfd_mob_drops.go — GFD Mob Drops manager (kanban GFD-MD-001, "we needs some kind of
// complimentary gui to the item management page you made in GFD we need a page to manage mob
// drops for the different mobs in different zones and the different bosses etc"). Same real
// design as gfd_items.go, applied to the newly data-driven server/mobdrop.Registry:
// GoblinFoxDragon/data/mob_drops.json (a hardcoded Go switch statement, apps2/mud's own
// dropsForMob, until this same pass) is read/written directly since IDUNA and GoblinFoxDragon
// are sibling checkouts on the same box -- the exact same KanbanHandler.BacklogPath precedent
// gfd_items.go already established, reused rather than re-argued here.
//
// Real, honest limitation named directly, not hidden: drop tables key on mob Kind only, not on
// (Kind, zone) -- GFD's own mob spawn code doesn't track which zone a Kind spawns in as data
// either, a mob's zone is a runtime fact of which zone registry it was spawned into, not a
// property of its Kind. So despite the founder's own framing ("different mobs in different
// zones"), a single Kind's drop table applies everywhere that Kind spawns. This mirrors the
// exact same real limitation already named for the NPC vendor catalog (S251-06). A real,
// zone-scoped drop table is a separate, larger follow-up (would need mob spawn code itself to
// carry a zone tag, not just this page).
//
// Same real "apps2/mud only loads its JSON at startup" limitation as gfd_items.go: an edit here
// takes effect on that process's next restart, not live.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
)

// GfdMobDropItem mirrors server/mobdrop.Item's own real JSON shape exactly.
type GfdMobDropItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// GfdMobDropTable mirrors server/mobdrop.DropTable's own real JSON shape exactly -- duplicated
// here (not imported) for the same cross-module reason GfdItemDef gives in gfd_items.go.
type GfdMobDropTable struct {
	Kind  string           `json:"kind"`
	Items []GfdMobDropItem `json:"items"`
}

// GfdMobDropsHandler serves the real CRUD API backing the Mob Drops page. Rows are keyed by
// Kind (a string, lowercase-compared, mirroring mobdrop.Registry's own lookup) rather than a
// numeric id -- there's no separate id space for drop tables, Kind already is the real key.
//
//	GET    /admin/gfd-mob-drops/api/tables            -> [GfdMobDropTable, ...] (sorted by kind)
//	POST   /admin/gfd-mob-drops/api/tables            {GfdMobDropTable} -> 201, rejects a duplicate kind
//	PATCH  /admin/gfd-mob-drops/api/tables/{kind}     {GfdMobDropTable} -> 200, full replace of that kind's row
//	DELETE /admin/gfd-mob-drops/api/tables/{kind}     -> 204
type GfdMobDropsHandler struct {
	DropsJSONPath string
	mu            sync.Mutex
}

func (h *GfdMobDropsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	const prefix = "/admin/gfd-mob-drops/api/tables"
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

func (h *GfdMobDropsHandler) readAll() ([]GfdMobDropTable, error) {
	data, err := os.ReadFile(h.DropsJSONPath)
	if err != nil {
		return nil, err
	}
	var tables []GfdMobDropTable
	if err := json.Unmarshal(data, &tables); err != nil {
		return nil, err
	}
	return tables, nil
}

func (h *GfdMobDropsHandler) writeAll(tables []GfdMobDropTable) error {
	sort.Slice(tables, func(i, j int) bool { return strings.ToLower(tables[i].Kind) < strings.ToLower(tables[j].Kind) })
	data, err := json.MarshalIndent(tables, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(h.DropsJSONPath, data, 0644)
}

func (h *GfdMobDropsHandler) list(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	tables, err := h.readAll()
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sort.Slice(tables, func(i, j int) bool { return strings.ToLower(tables[i].Kind) < strings.ToLower(tables[j].Kind) })
	writeJSON(w, http.StatusOK, tables)
}

func (h *GfdMobDropsHandler) create(w http.ResponseWriter, r *http.Request) {
	var t GfdMobDropTable
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if t.Kind == "" {
		mmoWriteError(w, http.StatusBadRequest, "kind required")
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	tables, err := h.readAll()
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, existing := range tables {
		if strings.EqualFold(existing.Kind, t.Kind) {
			mmoWriteError(w, http.StatusConflict, fmt.Sprintf("drop table for kind %q already exists", t.Kind))
			return
		}
	}
	tables = append(tables, t)
	if err := h.writeAll(tables); err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (h *GfdMobDropsHandler) update(w http.ResponseWriter, r *http.Request, kind string) {
	var t GfdMobDropTable
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	t.Kind = kind // the URL kind is authoritative -- a mismatched body kind can't silently rename the row

	h.mu.Lock()
	defer h.mu.Unlock()
	tables, err := h.readAll()
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	found := false
	for i := range tables {
		if strings.EqualFold(tables[i].Kind, kind) {
			tables[i] = t
			found = true
			break
		}
	}
	if !found {
		mmoWriteError(w, http.StatusNotFound, "drop table not found")
		return
	}
	if err := h.writeAll(tables); err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (h *GfdMobDropsHandler) delete(w http.ResponseWriter, r *http.Request, kind string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	tables, err := h.readAll()
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := tables[:0]
	found := false
	for _, t := range tables {
		if strings.EqualFold(t.Kind, kind) {
			found = true
			continue
		}
		out = append(out, t)
	}
	if !found {
		mmoWriteError(w, http.StatusNotFound, "drop table not found")
		return
	}
	if err := h.writeAll(out); err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
