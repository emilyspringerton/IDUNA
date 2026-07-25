package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"iduna/internal/mailinglist"
	"iduna/internal/vault"
)

// VaultHandler serves IDUNA Vault VS0 (EMILY/BACKLOG.md S170-03b): a
// founder-only password manager, loopback-only for the same reason the
// mailing-list unlock/init endpoints are (see MailingListHandler) --
// there's no session-token auth flow yet (that's VS1's Chrome extension
// phase, per docs/NORTHSTAR_PASSWORD_MANAGER.md §5), so every endpoint here,
// not just unlock/init, requires a caller on localhost. `emily vault ...`
// runs on the same box as IDUNA today; a session-token flow for remote
// access is explicitly deferred, not silently assumed away.
type VaultHandler struct {
	Store *vault.Store
	Vault *mailinglist.Vault // reused primitive -- see internal/vault package doc
}

func (h *VaultHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/vault/init", h.init)
	mux.HandleFunc("POST /api/v1/vault/unlock", h.unlock)
	mux.HandleFunc("POST /api/v1/vault/lock", h.lock)
	mux.HandleFunc("GET /api/v1/vault/status", h.status)
	mux.HandleFunc("POST /api/v1/vault/items", h.addItem)
	mux.HandleFunc("GET /api/v1/vault/items", h.listItems)
	mux.HandleFunc("GET /api/v1/vault/items/{id}", h.getItem)
	mux.HandleFunc("PATCH /api/v1/vault/items/{id}", h.updateItem)
	mux.HandleFunc("DELETE /api/v1/vault/items/{id}", h.deleteItem)
}

// unlockRequest is shared with mailinglist.go's identical {"passphrase":...}
// shape -- same package, one type.

// POST /api/v1/vault/init — loopback-only, one-time setup. Refuses to run
// if a vault already exists (Store.InitVault enforces this), same guard as
// the mailing-list vault's init endpoint.
func (h *VaultHandler) init(w http.ResponseWriter, r *http.Request) {
	if !requireLoopback(w, r) {
		return
	}
	var req unlockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}
	if len(req.Passphrase) < 12 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "passphrase must be at least 12 characters"})
		return
	}

	salt, err := mailinglist.NewSalt()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
		return
	}
	canaryCT, canaryNonce, err := mailinglist.NewCanary(req.Passphrase, salt)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
		return
	}
	if err := h.Store.InitVault(salt, canaryCT, canaryNonce); err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}
	if err := h.Vault.Unlock(req.Passphrase, salt, canaryCT, canaryNonce); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "init succeeded but unlock failed — this should never happen"})
		return
	}
	log.Printf("[vault] initialized and unlocked")
	writeJSON(w, http.StatusOK, map[string]any{"status": "initialized and unlocked"})
}

// POST /api/v1/vault/unlock — loopback-only, driven by `emily vault unlock`
// (prompts interactively, never a CLI arg — see cmd/mailing-list-unlock's
// same rationale for the equivalent mailing-list command).
func (h *VaultHandler) unlock(w http.ResponseWriter, r *http.Request) {
	if !requireLoopback(w, r) {
		return
	}
	var req unlockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}

	salt, canaryCT, canaryNonce, err := h.Store.VaultMeta()
	if err != nil {
		writeJSON(w, http.StatusPreconditionRequired, map[string]any{"error": "vault not initialized — run: emily vault init"})
		return
	}

	if err := h.Vault.Unlock(req.Passphrase, salt, canaryCT, canaryNonce); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "incorrect passphrase"})
		return
	}
	log.Printf("[vault] unlocked")
	writeJSON(w, http.StatusOK, map[string]any{"status": "unlocked"})
}

// POST /api/v1/vault/lock — loopback-only. Discards the in-memory key
// immediately rather than waiting for a process restart; useful after a
// founder is done with a CLI session on a machine they don't fully trust.
func (h *VaultHandler) lock(w http.ResponseWriter, r *http.Request) {
	if !requireLoopback(w, r) {
		return
	}
	h.Vault.Lock()
	log.Printf("[vault] locked")
	writeJSON(w, http.StatusOK, map[string]any{"status": "locked"})
}

// GET /api/v1/vault/status — loopback-only (locked/unlocked is itself
// operationally sensitive: it tells a caller whether secrets are currently
// reachable at all). Used by `emily vault` commands to give a clear error
// instead of a generic decrypt failure when the vault isn't unlocked.
func (h *VaultHandler) status(w http.ResponseWriter, r *http.Request) {
	if !requireLoopback(w, r) {
		return
	}
	initialized, err := h.Store.Initialized()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"initialized": initialized,
		"locked":      h.Vault.Locked(),
	})
}

// itemPayload is the plaintext shape encrypted as one JSON blob per row —
// see internal/vault/store.go's doc comment on why name lives inside the
// ciphertext rather than as its own plaintext column.
type itemPayload struct {
	Name   string            `json:"name"`
	Fields map[string]string `json:"fields"`
}

type addItemRequest struct {
	ItemType string            `json:"item_type"`
	Name     string            `json:"name"`
	Fields   map[string]string `json:"fields"`
}

func validItemType(t string) bool {
	switch vault.ItemType(t) {
	case vault.ItemLogin, vault.ItemNote, vault.ItemAPIKey, vault.ItemTOTP, vault.ItemDocument:
		return true
	}
	return false
}

// POST /api/v1/vault/items — loopback-only, requires the vault to be
// unlocked (fails closed with 423 Locked otherwise, same fail-closed
// convention as the mailing-list subscribe path).
func (h *VaultHandler) addItem(w http.ResponseWriter, r *http.Request) {
	if !requireLoopback(w, r) {
		return
	}
	var req addItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}
	if !validItemType(req.ItemType) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "item_type must be one of: login, note, api_key, totp, document"})
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name is required"})
		return
	}
	if h.Vault.Locked() {
		writeJSON(w, http.StatusLocked, map[string]any{"error": "vault is locked — run: emily vault unlock"})
		return
	}

	plain, err := json.Marshal(itemPayload{Name: req.Name, Fields: req.Fields})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
		return
	}
	ciphertext, nonce, err := h.Vault.Encrypt(plain)
	if err != nil {
		log.Printf("[vault] encrypt failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
		return
	}
	id, err := h.Store.AddItem(vault.ItemType(req.ItemType), ciphertext, nonce)
	if err != nil {
		log.Printf("[vault] store failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
		return
	}
	log.Printf("[vault] item added id=%d type=%s", id, req.ItemType)
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (h *VaultHandler) decryptItem(raw vault.RawItem) (vault.Item, error) {
	plain, err := h.Vault.Decrypt(raw.DataCiphertext, raw.DataNonce)
	if err != nil {
		return vault.Item{}, err
	}
	var payload itemPayload
	if err := json.Unmarshal(plain, &payload); err != nil {
		return vault.Item{}, err
	}
	return vault.Item{
		ID:        raw.ID,
		ItemType:  vault.ItemType(raw.ItemType),
		Name:      payload.Name,
		Fields:    payload.Fields,
		CreatedAt: raw.CreatedAt,
		UpdatedAt: raw.UpdatedAt,
	}, nil
}

// GET /api/v1/vault/items — loopback-only. Returns every item fully
// decrypted (VS0 scale — a founder's own item count — makes decrypting the
// whole list on every call fine; would need pagination/lazy-decrypt well
// before this stopped being true).
func (h *VaultHandler) listItems(w http.ResponseWriter, r *http.Request) {
	if !requireLoopback(w, r) {
		return
	}
	if h.Vault.Locked() {
		writeJSON(w, http.StatusLocked, map[string]any{"error": "vault is locked — run: emily vault unlock"})
		return
	}
	raws, err := h.Store.ListRaw()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
		return
	}
	items := make([]vault.Item, 0, len(raws))
	for _, raw := range raws {
		item, err := h.decryptItem(raw)
		if err != nil {
			log.Printf("[vault] decrypt failed for item id=%d: %v", raw.ID, err)
			continue
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func parseItemID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil
}

// GET /api/v1/vault/items/{id} — loopback-only, one fully decrypted item.
func (h *VaultHandler) getItem(w http.ResponseWriter, r *http.Request) {
	if !requireLoopback(w, r) {
		return
	}
	id, ok := parseItemID(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid item id"})
		return
	}
	if h.Vault.Locked() {
		writeJSON(w, http.StatusLocked, map[string]any{"error": "vault is locked — run: emily vault unlock"})
		return
	}
	raw, err := h.Store.GetRaw(id)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "item not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
		return
	}
	item, err := h.decryptItem(raw)
	if err != nil {
		log.Printf("[vault] decrypt failed for item id=%d: %v", id, err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "decrypt failed"})
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// PATCH /api/v1/vault/items/{id} — loopback-only, replaces an item's fields.
func (h *VaultHandler) updateItem(w http.ResponseWriter, r *http.Request) {
	if !requireLoopback(w, r) {
		return
	}
	id, ok := parseItemID(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid item id"})
		return
	}
	var req addItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}
	if !validItemType(req.ItemType) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "item_type must be one of: login, note, api_key, totp, document"})
		return
	}
	if h.Vault.Locked() {
		writeJSON(w, http.StatusLocked, map[string]any{"error": "vault is locked — run: emily vault unlock"})
		return
	}
	plain, err := json.Marshal(itemPayload{Name: req.Name, Fields: req.Fields})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
		return
	}
	ciphertext, nonce, err := h.Vault.Encrypt(plain)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
		return
	}
	if err := h.Store.UpdateItem(id, vault.ItemType(req.ItemType), ciphertext, nonce); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "item not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
		return
	}
	log.Printf("[vault] item updated id=%d", id)
	writeJSON(w, http.StatusOK, map[string]any{"status": "updated"})
}

// DELETE /api/v1/vault/items/{id} — loopback-only. No unlock required
// (deleting an already-opaque ciphertext row reveals nothing new), matching
// the principle that only encrypt/decrypt operations need the key.
func (h *VaultHandler) deleteItem(w http.ResponseWriter, r *http.Request) {
	if !requireLoopback(w, r) {
		return
	}
	id, ok := parseItemID(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid item id"})
		return
	}
	if err := h.Store.DeleteItem(id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "item not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
		return
	}
	log.Printf("[vault] item deleted id=%d", id)
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted"})
}
