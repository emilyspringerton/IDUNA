package handlers

// gfd_item_proposals.go — GFD Item Builder batch-propose assistant (ITEM_BUILDER_NORTHSTAR.md
// Phase 2d). Founder real-time: "can we also build a vertex powered assistant where i can drop a
// list of item names onto a textarea and hit go and it like does a batch add with totally
// halucinated whatever it thinks stats we can give it the setup 1-75 these are the classes etc
// and so it will start batching through the items and proposing items into a queue where we can
// review and approve them and edit them and approve or just reject."
//
// Real, existing Vertex AI credential reused directly, not a new one provisioned: "we have a
// vertex key for promptoverse" — the exact same project/region/auth (`gcloud auth
// print-access-token`, real ADC, no static API key stored anywhere) `emily.cli/cmd/
// promptoverse.go`'s own `vertexGenerateImage`/`gcloudAccessToken` already use for image
// generation, live-verified working against a plain text model (`gemini-2.5-flash`, not the
// `-image` variant that package uses) before writing this file. Real, honest answer to "if the
// item builder already learned some from the image data thats not terrible i dunno if it works
// like that": it doesn't — Vertex/Gemini calls are stateless per request; reusing the same real
// GCP project only shares auth/billing infrastructure, not any cross-request "memory" between
// the image-generation calls and this text-generation one.
//
// Real, deliberate design: a proposal NEVER writes straight into data/items.json. It lands in
// the new gfd_item_proposals table as a real, reviewable row (status 'pending'), editable before
// a human explicitly approves or rejects it — approval reuses GfdItemsHandler's own real create
// logic so an approved proposal goes through the exact same validation an operator's own manual
// "Add new item" already does.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	gfdItemProposalVertexProject = "project-d24a71e9-2daf-4b2d-917"
	gfdItemProposalVertexRegion  = "us-central1"
	gfdItemProposalVertexModel   = "gemini-2.5-flash"
	// gfdItemProposalMaxBatch caps a single "Go" click's own real cost/latency -- each name is
	// one real, separate Vertex call (sequential, not parallel, to stay a good citizen against
	// the same real project promptoverse's own image generation already bills against), so an
	// unbounded batch could both take a long time and run up a real bill with one click.
	gfdItemProposalMaxBatch = 40
)

// GfdItemProposalHandler serves the real batch-propose + review-queue API.
//
//	POST   /admin/gfd-items/api/proposals            {"item_names":[...],"level_range":[1,75]}
//	  -> 201, [GfdItemProposal, ...] (one per name, real Vertex call per name, sequential)
//	GET    /admin/gfd-items/api/proposals[?status=pending]
//	  -> [GfdItemProposal, ...]
//	PATCH  /admin/gfd-items/api/proposals/{id}       {GfdItemDef}  -> 200, edits proposed_json
//	POST   /admin/gfd-items/api/proposals/{id}/approve -> 200, commits into items.json via
//	  GfdItemsHandler's own real create path, marks status approved
//	POST   /admin/gfd-items/api/proposals/{id}/reject  -> 200, marks status rejected
type GfdItemProposalHandler struct {
	DB    *sql.DB
	Items *GfdItemsHandler // reused for the real create-item logic on approval
}

// GfdItemProposal is one row of the real review queue.
type GfdItemProposal struct {
	ID           int64      `json:"id"`
	ItemName     string     `json:"item_name"`
	ProposedItem GfdItemDef `json:"proposed_item"`
	Status       string     `json:"status"`
	BatchID      string     `json:"batch_id"`
	CreatedAt    string     `json:"created_at"`
}

func (h *GfdItemProposalHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	const prefix = "/admin/gfd-items/api/proposals"
	if !strings.HasPrefix(path, prefix) {
		http.NotFound(w, r)
		return
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(path, prefix), "/")

	switch {
	case rest == "" && r.Method == http.MethodGet:
		h.list(w, r)
	case rest == "" && r.Method == http.MethodPost:
		h.propose(w, r)
	case strings.HasSuffix(rest, "/approve") && r.Method == http.MethodPost:
		h.approve(w, r, strings.TrimSuffix(rest, "/approve"))
	case strings.HasSuffix(rest, "/reject") && r.Method == http.MethodPost:
		h.reject(w, r, strings.TrimSuffix(rest, "/reject"))
	case rest != "" && r.Method == http.MethodPatch:
		h.update(w, r, rest)
	default:
		http.NotFound(w, r)
	}
}

func (h *GfdItemProposalHandler) list(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	var rows *sql.Rows
	var err error
	if status != "" {
		rows, err = h.DB.QueryContext(r.Context(),
			`SELECT id, item_name, proposed_json, status, batch_id, created_at FROM gfd_item_proposals WHERE status = ? ORDER BY created_at DESC`, status)
	} else {
		rows, err = h.DB.QueryContext(r.Context(),
			`SELECT id, item_name, proposed_json, status, batch_id, created_at FROM gfd_item_proposals ORDER BY created_at DESC`)
	}
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	out := []GfdItemProposal{}
	for rows.Next() {
		var p GfdItemProposal
		var raw string
		if err := rows.Scan(&p.ID, &p.ItemName, &raw, &p.Status, &p.BatchID, &p.CreatedAt); err != nil {
			mmoWriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		json.Unmarshal([]byte(raw), &p.ProposedItem)
		out = append(out, p)
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *GfdItemProposalHandler) propose(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ItemNames  []string `json:"item_names"`
		LevelRange [2]int   `json:"level_range"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	names := make([]string, 0, len(req.ItemNames))
	for _, n := range req.ItemNames {
		n = strings.TrimSpace(n)
		if n != "" {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		mmoWriteError(w, http.StatusBadRequest, "item_names required")
		return
	}
	if len(names) > gfdItemProposalMaxBatch {
		mmoWriteError(w, http.StatusBadRequest, fmt.Sprintf("batch too large: %d names, max %d per request", len(names), gfdItemProposalMaxBatch))
		return
	}
	minLevel, maxLevel := 1, 75
	if req.LevelRange[1] > 0 {
		minLevel, maxLevel = req.LevelRange[0], req.LevelRange[1]
	}

	token, err := gcloudAccessTokenForItemProposals()
	if err != nil {
		mmoWriteError(w, http.StatusServiceUnavailable, "vertex auth: "+err.Error())
		return
	}

	batchID := uuid.NewString()
	out := make([]GfdItemProposal, 0, len(names))
	for _, name := range names {
		item, genErr := generateItemProposal(r.Context(), token, name, minLevel, maxLevel)
		if genErr != nil {
			// Real, honest partial failure: one bad generation doesn't abort the whole batch --
			// every other name still gets a real attempt. A synthetic "failed" placeholder
			// lands in the queue instead, visible for the operator to see and reject rather
			// than silently vanishing.
			item = GfdItemDef{Name: name, Description: "GENERATION FAILED: " + genErr.Error(), Category: "material", StackSize: 1}
		}
		raw, _ := json.Marshal(item)
		res, err := h.DB.ExecContext(r.Context(),
			`INSERT INTO gfd_item_proposals (item_name, proposed_json, status, batch_id) VALUES (?, ?, 'pending', ?)`,
			name, string(raw), batchID)
		if err != nil {
			mmoWriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		id, _ := res.LastInsertId()
		out = append(out, GfdItemProposal{ID: id, ItemName: name, ProposedItem: item, Status: "pending", BatchID: batchID})
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *GfdItemProposalHandler) update(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var item GfdItemDef
	if err := json.NewDecoder(r.Body).Decode(&item); err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	raw, _ := json.Marshal(item)
	res, err := h.DB.ExecContext(r.Context(),
		`UPDATE gfd_item_proposals SET proposed_json = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND status = 'pending'`,
		string(raw), id)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		mmoWriteError(w, http.StatusNotFound, "proposal not found or already resolved")
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *GfdItemProposalHandler) approve(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var raw, status string
	if err := h.DB.QueryRowContext(r.Context(),
		`SELECT proposed_json, status FROM gfd_item_proposals WHERE id = ?`, id).Scan(&raw, &status); err != nil {
		mmoWriteError(w, http.StatusNotFound, "proposal not found")
		return
	}
	if status != "pending" {
		mmoWriteError(w, http.StatusConflict, "proposal already "+status)
		return
	}
	var item GfdItemDef
	if err := json.Unmarshal([]byte(raw), &item); err != nil {
		mmoWriteError(w, http.StatusInternalServerError, "corrupt proposal: "+err.Error())
		return
	}

	// Reuse GfdItemsHandler's own real create logic -- an approved proposal goes through the
	// exact same validation (category check, duplicate-id check, auto-id-assignment) a manual
	// "Add new item" submission already does, not a second, parallel path that could drift.
	created, err := h.Items.createFromDef(item)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := h.DB.ExecContext(r.Context(),
		`UPDATE gfd_item_proposals SET status = 'approved', updated_at = CURRENT_TIMESTAMP WHERE id = ?`, id); err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, created)
}

func (h *GfdItemProposalHandler) reject(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		mmoWriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	res, err := h.DB.ExecContext(r.Context(),
		`UPDATE gfd_item_proposals SET status = 'rejected', updated_at = CURRENT_TIMESTAMP WHERE id = ? AND status = 'pending'`, id)
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		mmoWriteError(w, http.StatusNotFound, "proposal not found or already resolved")
		return
	}
	w.WriteHeader(http.StatusOK)
}

// gcloudAccessTokenForItemProposals mirrors emily.cli/cmd/promptoverse.go's own real
// gcloudAccessToken exactly (real ADC via the gcloud CLI already configured on this box, no
// static API key stored anywhere) -- duplicated rather than imported since emily.cli and IDUNA
// are separate modules with no shared internal package for this today.
func gcloudAccessTokenForItemProposals() (string, error) {
	out, err := exec.Command("gcloud", "auth", "print-access-token").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// generateItemProposal calls Vertex AI's real generateContent endpoint (same real project/
// region promptoverse's own image generation already uses, see this file's own header comment)
// with a text-only Gemini model, requesting structured JSON matching GfdItemDef's own shape
// directly via responseMimeType -- no markdown-fence stripping needed, live-verified against
// the real endpoint before this was written.
func generateItemProposal(ctx context.Context, token, itemName string, minLevel, maxLevel int) (GfdItemDef, error) {
	prompt := fmt.Sprintf(`You are designing items for an FFXI-style MMO. Real, established conventions: 22 jobs (WAR MNK WHM BLM RDM THF PLD DRK BST BRD RNG SAM NIN DRG SMN BLU COR PUP DNC SCH GEO RUN), levels %d-%d, stat keys like attack/defense/str/dex/vit/agi/int/mnd/chr/accuracy/evasion/magic_attack_bonus/magic_defense_bonus/haste, equip_slots use hyphenated names (main, off, head, body, hand-l, hand-r, legs, feet, neck, ear-l, ear-r, ring-l, ring-r, back, waist, ammo) -- omit equip_slots/jobs entirely for non-equipment (consumable/material/crystal/key_item). Categories are one of weapon/armor/accessory/consumable/material/crystal/key_item/temporary.

Given this item name: %q -- propose a real, plausible item definition as JSON matching this exact schema: {"name":string,"category":string,"level":int,"equip_slots":[string],"jobs":[string],"stats":{string:int},"stack_size":int,"model_id":string,"description":string}. Return ONLY the JSON object, no markdown fences, no commentary.`, minLevel, maxLevel, itemName)

	url := fmt.Sprintf(
		"https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/google/models/%s:generateContent",
		gfdItemProposalVertexRegion, gfdItemProposalVertexProject, gfdItemProposalVertexRegion, gfdItemProposalVertexModel,
	)
	body, _ := json.Marshal(map[string]any{
		"contents": []map[string]any{
			{"role": "user", "parts": []map[string]string{{"text": prompt}}},
		},
		"generationConfig": map[string]any{"responseMimeType": "application/json"},
	})
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return GfdItemDef{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return GfdItemDef{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if resp.StatusCode != http.StatusOK {
		return GfdItemDef{}, fmt.Errorf("vertex ai %d: %s", resp.StatusCode, string(raw))
	}

	var parsed struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return GfdItemDef{}, fmt.Errorf("parse vertex response: %w", err)
	}
	if len(parsed.Candidates) == 0 || len(parsed.Candidates[0].Content.Parts) == 0 {
		return GfdItemDef{}, fmt.Errorf("no content in vertex response (finishReason=%q)", firstFinishReason(parsed.Candidates))
	}
	text := parsed.Candidates[0].Content.Parts[0].Text

	var item GfdItemDef
	if err := json.Unmarshal([]byte(text), &item); err != nil {
		return GfdItemDef{}, fmt.Errorf("model returned non-matching JSON: %w", err)
	}
	if item.StackSize == 0 {
		item.StackSize = 1
	}
	return item, nil
}

func firstFinishReason(candidates []struct {
	Content struct {
		Parts []struct {
			Text string `json:"text"`
		} `json:"parts"`
	} `json:"content"`
	FinishReason string `json:"finishReason"`
}) string {
	if len(candidates) == 0 {
		return "no candidates"
	}
	return candidates[0].FinishReason
}
