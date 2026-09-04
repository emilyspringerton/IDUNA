package handlers

// gfd_items.go — GFD Item Builder (ITEM_BUILDER_NORTHSTAR.md Phase 2a). Founder real-time: "we
// need some kind of big gui to help us manage our items" -> "if i wanted to add another potion
// like max potion i should be able to do that via GFD world building tools either in iduna or
// whatever" -> "we can name and give stats to weapons and armor etc... a lot of items are gonna
// just be stronger armor different model thats how ffxi works."
//
// Real, direct-file-access design, matching the exact same precedent KanbanHandler's own
// BacklogPath already established for EMILY/BACKLOG.md: IDUNA and GoblinFoxDragon are sibling
// checkouts on the same box, so this reads/writes GoblinFoxDragon/data/items.json directly
// rather than standing up a new cross-process API. Field shape is a byte-for-byte mirror of
// server/itemdef.ItemDef's own real JSON tags (checked directly against that file) so this
// writes back exactly the format apps2/mud's own itemdefReg.LoadFile already parses --
// deliberately NOT importing that package (GOWORK=off breaks a cross-repo import the same real
// way logs.go's own header comment already names for a different package).
//
// Real, honest limitation named directly, not hidden: apps2/mud only loads data/items.json
// once, at startup (checked directly in that file) -- an edit here takes effect on that
// process's next restart, not live. A real reload mechanism (matching the mods-manifest
// SIGHUP-reload precedent named in ITEM_BUILDER_NORTHSTAR.md's own Phase 1) is real, separate,
// not-yet-built follow-up.
//
// Real, explicitly out of scope for this pass (named in the northstar): NPC vendor catalog
// management. npcVendorCatalog in apps2/mud/main.go is a hardcoded Go map today, not
// data-driven at all -- a real, separate prerequisite (moving it to its own editable file, the
// same shape items.json already has) before any GUI could manage it.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// GfdItemDef mirrors server/itemdef.ItemDef's own real JSON shape exactly -- see that file for
// the authoritative field meanings. Duplicated here (not imported) because this package can't
// depend on GoblinFoxDragon's own module.
type GfdItemDef struct {
	ID              int            `json:"id"`
	Name            string         `json:"name"`
	Description     string         `json:"description,omitempty"`
	Category        string         `json:"category"`
	EquipSlots      []string       `json:"equip_slots,omitempty"`
	Jobs            []string       `json:"jobs,omitempty"`
	Level           int            `json:"level,omitempty"`
	Stats           map[string]int `json:"stats,omitempty"`
	StackSize       int            `json:"stack_size"`
	FlagsRaw        []string       `json:"flags,omitempty"`
	ModelID         string         `json:"model_id,omitempty"`
	IconID          int            `json:"icon_id,omitempty"`
	DisguiseFaction string         `json:"disguise_faction,omitempty"`
	// Delay is a weapon's own attack speed, in server/combat's own real delay-units (60 du ≈ 1
	// second) -- mirrors itemdef.ItemDef.Delay exactly. Real, individually-set per weapon, not
	// a fixed value per weapon type (founder: "not a standard delay per item type"). Only
	// meaningful for Category == "weapon"; 0 means "not set yet."
	Delay int `json:"delay,omitempty"`
}

// GfdItemCategories is the real, fixed category set itemdef.Category itself defines --
// duplicated here for the same cross-module reason as GfdItemDef above. Exposed so the page can
// render a real dropdown instead of a free-text field a typo could silently break.
var GfdItemCategories = []string{"weapon", "armor", "accessory", "consumable", "material", "crystal", "key_item", "temporary"}

// GfdItemsHandler serves the real CRUD API backing the Item Builder page.
//
//	GET    /admin/gfd-items/api/items          -> [GfdItemDef, ...] (sorted by id)
//	POST   /admin/gfd-items/api/items          {GfdItemDef}  -> 201, rejects a duplicate id
//	PATCH  /admin/gfd-items/api/items/{id}     {GfdItemDef}  -> 200, full replace of that id's row
//	DELETE /admin/gfd-items/api/items/{id}     -> 204
type GfdItemsHandler struct {
	ItemsJSONPath string
	mu            sync.Mutex
}

func (h *GfdItemsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	const prefix = "/admin/gfd-items/api/items"
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

func (h *GfdItemsHandler) readAll() ([]GfdItemDef, error) {
	data, err := os.ReadFile(h.ItemsJSONPath)
	if err != nil {
		return nil, err
	}
	var items []GfdItemDef
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (h *GfdItemsHandler) writeAll(items []GfdItemDef) error {
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(h.ItemsJSONPath, data, 0644)
}

func (h *GfdItemsHandler) list(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	items, err := h.readAll()
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	writeJSON(w, http.StatusOK, items)
}

func (h *GfdItemsHandler) create(w http.ResponseWriter, r *http.Request) {
	var def GfdItemDef
	if err := json.NewDecoder(r.Body).Decode(&def); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	created, err := h.createFromDef(def)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "already exists") {
			status = http.StatusConflict
		}
		mmoWriteError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// createFromDef is the real, shared create-item logic -- used by the HTTP create endpoint
// above AND by GfdItemProposalHandler's own approve path, so an AI-proposed item that gets
// approved goes through the exact same validation (category check, duplicate-id check,
// auto-id-assignment) a manual "Add new item" submission already does, not a second, separate
// path that could silently drift from it.
func (h *GfdItemsHandler) createFromDef(def GfdItemDef) (GfdItemDef, error) {
	if def.Name == "" {
		return GfdItemDef{}, fmt.Errorf("name required")
	}
	if !validGfdCategory(def.Category) {
		return GfdItemDef{}, fmt.Errorf("invalid category")
	}
	if def.StackSize == 0 {
		def.StackSize = 1
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	items, err := h.readAll()
	if err != nil {
		return GfdItemDef{}, err
	}
	if def.ID == 0 {
		def.ID = nextGfdItemID(items)
	}
	for _, existing := range items {
		if existing.ID == def.ID {
			return GfdItemDef{}, fmt.Errorf("item id %d already exists", def.ID)
		}
	}
	items = append(items, def)
	if err := h.writeAll(items); err != nil {
		return GfdItemDef{}, err
	}
	return def, nil
}

func (h *GfdItemsHandler) update(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := strconv.Atoi(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var def GfdItemDef
	if err := json.NewDecoder(r.Body).Decode(&def); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if def.Name == "" {
		mmoWriteError(w, http.StatusBadRequest, "name required")
		return
	}
	if !validGfdCategory(def.Category) {
		mmoWriteError(w, http.StatusBadRequest, "invalid category")
		return
	}
	if def.StackSize == 0 {
		def.StackSize = 1
	}
	def.ID = id // the URL id is authoritative -- a mismatched body id can't silently rename the row

	h.mu.Lock()
	defer h.mu.Unlock()
	items, err := h.readAll()
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	found := false
	for i := range items {
		if items[i].ID == id {
			items[i] = def
			found = true
			break
		}
	}
	if !found {
		mmoWriteError(w, http.StatusNotFound, "item not found")
		return
	}
	if err := h.writeAll(items); err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, def)
}

func (h *GfdItemsHandler) delete(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := strconv.Atoi(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	items, err := h.readAll()
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := items[:0]
	found := false
	for _, it := range items {
		if it.ID == id {
			found = true
			continue
		}
		out = append(out, it)
	}
	if !found {
		mmoWriteError(w, http.StatusNotFound, "item not found")
		return
	}
	if err := h.writeAll(out); err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func validGfdCategory(cat string) bool {
	for _, c := range GfdItemCategories {
		if c == cat {
			return true
		}
	}
	return false
}

// nextGfdItemID picks a real, unused id one above the current highest -- real v0 convenience so
// the page's "add new item" form doesn't force the operator to know the whole existing id space.
func nextGfdItemID(items []GfdItemDef) int {
	max := 0
	for _, it := range items {
		if it.ID > max {
			max = it.ID
		}
	}
	return max + 1
}
