package handlers

// gfd_mob_spawns.go — GFD Mob Spawns manager (GFD-MOBSPAWN-001 Phase 3, per
// GoblinFoxDragon/docs2/MOB_SPAWN_NORTHSTAR.md). Same real direct-file-access precedent as
// gfd_items.go and gfd_mob_drops.go: reads/writes GoblinFoxDragon/data/mob_spawns.json directly.
//
// Real, honest scope match to what Phase 2 actually shipped: this edits the per-(zone, kind)
// on/off toggle server/spawn.Registry reads (data/mob_spawns.json) -- it does NOT edit exact
// spawn positions or counts, since those still live in server/mob's own real per-Kind
// *Spawns() constructors (Phase 2's own deliberate design: a flat toggle layered on top of the
// existing stat-block code, not a rewrite of it). "Which grid cell a kind spawns in" (the
// founder's own literal "I-7" framing) isn't editable here yet -- Phase 1's zone/grid math
// exists, but nothing in Phase 2 threads a per-cell position through this data file. Named
// directly on the page, not hidden.
//
// Real, honest limitation shared with the other two GFD admin pages: apps2/mud only loads
// mob_spawns.json once, at startup -- an edit here takes effect on that process's next restart.

import (
	"encoding/json"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// GfdMobSpawnRule mirrors server/spawn.Rule's own real JSON shape exactly.
type GfdMobSpawnRule struct {
	ZoneID  int    `json:"zone_id"`
	Kind    string `json:"kind"`
	Enabled bool   `json:"enabled"`
}

// GfdZoneNames is the real, fixed zone roster zone.DefaultZones() itself defines -- duplicated
// here for the same cross-module reason GfdItemCategories duplicates itemdef.Category. Exposed
// so the page can render a real dropdown instead of a free-text zone id a typo could break.
var GfdZoneNames = map[int]string{
	0: "Meadow",
	1: "Hills",
	2: "Caves",
	3: "Swampville",
	4: "New Handington",
}

// GfdMobSpawnsHandler serves the real CRUD API backing the Mob Spawns page. Rows are keyed by
// the composite (zone_id, kind) pair -- there's no separate id space, that pair already is the
// real key server/spawn.Registry itself looks up by.
//
//	GET    /admin/gfd-mob-spawns/api/rules                    -> [GfdMobSpawnRule, ...] (sorted by zone then kind)
//	POST   /admin/gfd-mob-spawns/api/rules                    {GfdMobSpawnRule} -> 201, rejects a duplicate (zone_id, kind)
//	PATCH  /admin/gfd-mob-spawns/api/rules/{zone_id}/{kind}   {"enabled":bool} -> 200
//	DELETE /admin/gfd-mob-spawns/api/rules/{zone_id}/{kind}   -> 204, reverts that pair to the registry's own default-enabled
type GfdMobSpawnsHandler struct {
	RulesJSONPath string
	mu            sync.Mutex
}

func (h *GfdMobSpawnsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	const prefix = "/admin/gfd-mob-spawns/api/rules"
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

func (h *GfdMobSpawnsHandler) readAll() ([]GfdMobSpawnRule, error) {
	data, err := os.ReadFile(h.RulesJSONPath)
	if err != nil {
		return nil, err
	}
	var rules []GfdMobSpawnRule
	if err := json.Unmarshal(data, &rules); err != nil {
		return nil, err
	}
	return rules, nil
}

func (h *GfdMobSpawnsHandler) writeAll(rules []GfdMobSpawnRule) error {
	sort.Slice(rules, func(i, j int) bool {
		if rules[i].ZoneID != rules[j].ZoneID {
			return rules[i].ZoneID < rules[j].ZoneID
		}
		return strings.ToLower(rules[i].Kind) < strings.ToLower(rules[j].Kind)
	})
	data, err := json.MarshalIndent(rules, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(h.RulesJSONPath, data, 0644)
}

func (h *GfdMobSpawnsHandler) list(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	rules, err := h.readAll()
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sort.Slice(rules, func(i, j int) bool {
		if rules[i].ZoneID != rules[j].ZoneID {
			return rules[i].ZoneID < rules[j].ZoneID
		}
		return strings.ToLower(rules[i].Kind) < strings.ToLower(rules[j].Kind)
	})
	writeJSON(w, http.StatusOK, rules)
}

func (h *GfdMobSpawnsHandler) create(w http.ResponseWriter, r *http.Request) {
	var rule GfdMobSpawnRule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if rule.Kind == "" {
		mmoWriteError(w, http.StatusBadRequest, "kind required")
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	rules, err := h.readAll()
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, existing := range rules {
		if existing.ZoneID == rule.ZoneID && strings.EqualFold(existing.Kind, rule.Kind) {
			mmoWriteError(w, http.StatusConflict, "a rule for this zone+kind already exists")
			return
		}
	}
	rules = append(rules, rule)
	if err := h.writeAll(rules); err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, rule)
}

// splitZoneKind parses the "{zone_id}/{kind}" URL suffix.
func splitZoneKind(rest string) (int, string, bool) {
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[1] == "" {
		return 0, "", false
	}
	zoneID, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, "", false
	}
	return zoneID, parts[1], true
}

func (h *GfdMobSpawnsHandler) update(w http.ResponseWriter, r *http.Request, rest string) {
	zoneID, kind, ok := splitZoneKind(rest)
	if !ok {
		mmoWriteError(w, http.StatusBadRequest, "expected /rules/{zone_id}/{kind}")
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	rules, err := h.readAll()
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	found := false
	for i := range rules {
		if rules[i].ZoneID == zoneID && strings.EqualFold(rules[i].Kind, kind) {
			rules[i].Enabled = body.Enabled
			found = true
			break
		}
	}
	if !found {
		mmoWriteError(w, http.StatusNotFound, "rule not found")
		return
	}
	if err := h.writeAll(rules); err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, GfdMobSpawnRule{ZoneID: zoneID, Kind: kind, Enabled: body.Enabled})
}

func (h *GfdMobSpawnsHandler) delete(w http.ResponseWriter, r *http.Request, rest string) {
	zoneID, kind, ok := splitZoneKind(rest)
	if !ok {
		mmoWriteError(w, http.StatusBadRequest, "expected /rules/{zone_id}/{kind}")
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	rules, err := h.readAll()
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := rules[:0]
	found := false
	for _, rule := range rules {
		if rule.ZoneID == zoneID && strings.EqualFold(rule.Kind, kind) {
			found = true
			continue
		}
		out = append(out, rule)
	}
	if !found {
		mmoWriteError(w, http.StatusNotFound, "rule not found")
		return
	}
	if err := h.writeAll(out); err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
