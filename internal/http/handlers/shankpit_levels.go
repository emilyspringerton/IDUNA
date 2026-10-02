package handlers

// shankpit_levels.go — real CRUD API for the SHANKPIT NOCK level editor v0 (EMILY/BACKLOG.md
// SECTION 459, founder real-time: "so v0 it and start working dont worry about the current
// levels lets just go full level select brawlpit repo exact model for now"). Every operation is a
// thin wrapper over internal/shankpit.LevelStore -- deliberately the same real, established shape
// as brawlpit_levels.go's own handler (mirrored field-for-field per the founder's own explicit
// "brawlpit repo exact model" instruction), served alongside it under the same /admin/nock/api/
// surface so the NOCK React app's own existing UI shell hosts this new tab too.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"iduna/internal/nock"
	"iduna/internal/shankpit"
)

// ShankpitLevelsHandler serves every /admin/nock/api/shankpit-levels... route.
type ShankpitLevelsHandler struct {
	Store *shankpit.LevelStore
}

func (h *ShankpitLevelsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	const prefix = "/admin/nock/api/shankpit-levels"
	if !strings.HasPrefix(path, prefix) {
		http.NotFound(w, r)
		return
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(path, prefix), "/")
	parts := []string{}
	if rest != "" {
		parts = strings.Split(rest, "/")
	}

	switch {
	case len(parts) == 0 && r.Method == http.MethodGet:
		h.list(w, r)
	case len(parts) == 0 && r.Method == http.MethodPost:
		h.create(w, r)
	case len(parts) == 1 && parts[0] == "import-gltf" && r.Method == http.MethodPost:
		h.importGLTF(w, r)
	case len(parts) == 1 && r.Method == http.MethodGet:
		h.get(w, r, parts[0])
	case len(parts) == 1 && r.Method == http.MethodPut:
		h.update(w, r, parts[0])
	case len(parts) == 1 && r.Method == http.MethodPatch:
		h.rename(w, r, parts[0])
	case len(parts) == 1 && r.Method == http.MethodDelete:
		h.delete(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "clone" && r.Method == http.MethodPost:
		h.clone(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "export" && r.Method == http.MethodGet:
		h.export(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "default-queue" && r.Method == http.MethodPatch:
		h.setDefaultQueue(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "story-start" && r.Method == http.MethodPatch:
		h.setStoryStart(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "zombie-default" && r.Method == http.MethodPatch:
		h.setZombieDefault(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "floor-tint" && r.Method == http.MethodPatch:
		h.setFloorTint(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "enclosed" && r.Method == http.MethodPatch:
		h.setEnclosed(w, r, parts[0])
	default:
		http.NotFound(w, r)
	}
}

func parseShankpitLevelID(idStr string) (int64, error) {
	return strconv.ParseInt(idStr, 10, 64)
}

func (h *ShankpitLevelsHandler) list(w http.ResponseWriter, r *http.Request) {
	list, err := h.Store.ListLevelsIn(r.Context(), r.URL.Query().Get("collection"))
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type createShankpitLevelReq struct {
	Name               string                 `json:"name"`
	Width              float64                `json:"width"`
	Height             float64                `json:"height"`
	Depth              float64                `json:"depth"`
	GroundPlaneEnabled bool                   `json:"ground_plane_enabled"`
	GroundPlaneSquares int                    `json:"ground_plane_squares"`
	Walls              []shankpit.Wall        `json:"walls"`
	Objects            []shankpit.LevelObject `json:"objects"`
	Spawners           []shankpit.Spawner     `json:"spawners"`
	Doors              []shankpit.Door        `json:"doors"`
	NavNodes           []shankpit.NavNode     `json:"nav_nodes"`
	Characters         []shankpit.Character   `json:"characters"`
	LevelExits         []shankpit.LevelExit   `json:"level_exits"`
	NextLevelID        *int64                 `json:"next_level_id"`
}

func (h *ShankpitLevelsHandler) create(w http.ResponseWriter, r *http.Request) {
	var req createShankpitLevelReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	lvl, err := h.Store.CreateLevel(r.Context(), req.Name, req.Width, req.Height, req.Depth, req.GroundPlaneEnabled, req.GroundPlaneSquares, req.Walls, req.Objects, req.Spawners, req.Doors, req.NavNodes, req.Characters, req.LevelExits, req.NextLevelID)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, lvl)
}

func (h *ShankpitLevelsHandler) get(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseShankpitLevelID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	lvl, err := h.Store.GetLevel(r.Context(), id)
	if err != nil {
		mmoWriteError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, lvl)
}

type updateShankpitLevelReq struct {
	Width              float64                `json:"width"`
	Height             float64                `json:"height"`
	Depth              float64                `json:"depth"`
	GroundPlaneEnabled bool                   `json:"ground_plane_enabled"`
	GroundPlaneSquares int                    `json:"ground_plane_squares"`
	Walls              []shankpit.Wall        `json:"walls"`
	Objects            []shankpit.LevelObject `json:"objects"`
	Spawners           []shankpit.Spawner     `json:"spawners"`
	Doors              []shankpit.Door        `json:"doors"`
	NavNodes           []shankpit.NavNode     `json:"nav_nodes"`
	Characters         []shankpit.Character   `json:"characters"`
	LevelExits         []shankpit.LevelExit   `json:"level_exits"`
	NextLevelID        *int64                 `json:"next_level_id"`
}

// update is the real editor "save" action -- dimensions + the full wall layout replace the
// level's own current state together in one call (PUT, matching "replace this resource"
// semantics; the separate rename below is PATCH, matching brawlpit_levels.go's own established
// convention). This is the one endpoint both "create a cube" (append a default wall, save) and
// "face-drag editing" (adjust an existing wall's center/size, save) go through.
func (h *ShankpitLevelsHandler) update(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseShankpitLevelID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req updateShankpitLevelReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	lvl, err := h.Store.UpdateLevel(r.Context(), id, req.Width, req.Height, req.Depth, req.GroundPlaneEnabled, req.GroundPlaneSquares, req.Walls, req.Objects, req.Spawners, req.Doors, req.NavNodes, req.Characters, req.LevelExits, req.NextLevelID)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, lvl)
}

type renameShankpitLevelReq struct {
	Name string `json:"name"`
}

func (h *ShankpitLevelsHandler) rename(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseShankpitLevelID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req renameShankpitLevelReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	lvl, err := h.Store.RenameLevel(r.Context(), id, req.Name)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, lvl)
}

// setDefaultQueue is the real S459-41 endpoint (founder: "need to add an option to shankpit
// levels to set a level as default for queue") -- mirrors ShankpitSpraysHandler's own
// setDefault exactly.
func (h *ShankpitLevelsHandler) setDefaultQueue(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseShankpitLevelID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	lvl, err := h.Store.SetDefaultQueueLevel(r.Context(), id)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, lvl)
}

// setStoryStart is the real S473 endpoint (STORY_LEVEL_SEQUENCING_NORTHSTAR.md Phase 1) --
// mirrors setDefaultQueue above exactly, same "exactly one at a time" real shape.
func (h *ShankpitLevelsHandler) setStoryStart(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseShankpitLevelID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	lvl, err := h.Store.SetStoryStartLevel(r.Context(), id)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, lvl)
}

// setZombieDefault makes the level the ZOMBIES sandbox's level (copying it into the zombies
// repository first when it comes from the main registry) -- "set for zombies".
func (h *ShankpitLevelsHandler) setZombieDefault(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseShankpitLevelID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	lvl, err := h.Store.SetZombieDefaultLevel(r.Context(), id)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, lvl)
}

// setEnclosedReq carries the desired state -- unlike setDefaultQueue/setStoryStart above (real,
// exclusive "set THIS one" toggles with no body), Enclosed is a plain per-level on/off flag, so a
// designer needs to be able to turn it OFF just as easily as on.
type setEnclosedReq struct {
	Enclosed bool `json:"enclosed"`
}

// setEnclosed is the real S493 endpoint (founder real-time: "theres not much difference between
// having lights on and not having lights - its still basically illuminated in this totally
// enclosed level"). See Level.Enclosed's own doc comment for the full rationale.
func (h *ShankpitLevelsHandler) setEnclosed(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseShankpitLevelID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req setEnclosedReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	lvl, err := h.Store.SetEnclosed(r.Context(), id, req.Enclosed)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, lvl)
}

// setFloorTint (kanban #533): body {"floor_tint": {"r","g","b","a"}} sets, {"floor_tint": null} clears.
func (h *ShankpitLevelsHandler) setFloorTint(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseShankpitLevelID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		FloorTint *shankpit.FloorTint `json:"floor_tint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	lvl, err := h.Store.SetFloorTint(r.Context(), id, req.FloorTint)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, lvl)
}

func (h *ShankpitLevelsHandler) delete(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseShankpitLevelID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.Store.DeleteLevel(r.Context(), id); err != nil {
		mmoWriteError(w, http.StatusNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type cloneShankpitLevelReq struct {
	Name string `json:"name"`
}

func (h *ShankpitLevelsHandler) clone(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseShankpitLevelID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req cloneShankpitLevelReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	lvl, err := h.Store.CloneLevel(r.Context(), id, req.Name)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, lvl)
}

// export returns the real, native-loader-facing document -- the exact shape SHANKPIT's own
// map loader would read once it gains a JSON path (not built yet, see internal/shankpit's own
// package doc).
func (h *ShankpitLevelsHandler) export(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := parseShankpitLevelID(idStr)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	doc, err := h.Store.Export(r.Context(), id)
	if err != nil {
		mmoWriteError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

// importGLTF converts a Blender (or any) glTF export into level walls -- card #462 "add gltf
// importer for shankpit levels". Stateless: it saves nothing and returns {walls, doors} for the
// editor to MERGE into the level being edited (so an import adds to an existing level instead of
// replacing it, and the normal Save / undo paths still apply). Wall ids are 1..n in scene order;
// the editor renumbers them past its own ids. Same multipart contract as the Widgets importer
// (`file`, repeated `resource` for a .gltf's .bin/textures, `scale`) and the same one-box-per-mesh-
// node mapping and honest limits -- see internal/nock/gltf_widget.go. A node named door* becomes a
// scriptless door on its wall, exactly like widgets.
func (h *ShankpitLevelsHandler) importGLTF(w http.ResponseWriter, r *http.Request) {
	fileData, resources, scale, ok := readGLTFUpload(w, r)
	if !ok {
		return
	}
	boxes, err := nock.GLTFToWidgetBoxesWithResources(fileData, scale, resources)
	if err != nil {
		mmoWriteError(w, http.StatusUnprocessableEntity, fmt.Sprintf("glTF to level conversion failed: %v", err))
		return
	}
	walls, doors := WidgetFromGLTFBoxes(boxes)
	if len(walls) > shankpit.MaxWalls {
		mmoWriteError(w, http.StatusUnprocessableEntity, fmt.Sprintf("this file has %d mesh nodes; a level holds at most %d walls (one per mesh node) -- join meshes in Blender and re-export", len(walls), shankpit.MaxWalls))
		return
	}
	if len(doors) > shankpit.MaxDoors {
		mmoWriteError(w, http.StatusUnprocessableEntity, fmt.Sprintf("this file has %d door* nodes; a level holds at most %d doors", len(doors), shankpit.MaxDoors))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"walls": walls, "doors": doors})
}
