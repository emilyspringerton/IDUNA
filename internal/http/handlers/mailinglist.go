package handlers

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"regexp"
	"strings"

	"iduna/internal/http/middleware"
	"iduna/internal/mailinglist"
)

// CurrentConsentVersion identifies the exact privacy-policy/consent copy
// shown on OKEMILY's signup form. Bump this (and the copy on the page) any
// time the consent language materially changes — old subscriber rows keep
// the version they actually agreed to, never silently reattributed.
const CurrentConsentVersion = "okemily-v1-2026-07-17"

var emailRe = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

// MailingListHandler serves the public subscribe endpoint (CORS-scoped,
// rate-limited, fails closed while the vault is locked) and the loopback-only
// unlock/init endpoints an operator drives via cmd/mailing-list-unlock.
type MailingListHandler struct {
	Store       *mailinglist.Store
	Vault       *mailinglist.Vault
	Mailchimp   *mailinglist.MailchimpClient
	AllowOrigin []string // exact-match allowlist, e.g. "https://okemily.com"
	Limiter     *middleware.IPRateLimiter
}

func (h *MailingListHandler) Register(mux *http.ServeMux) {
	subscribe := http.HandlerFunc(h.subscribe)
	if h.Limiter != nil {
		mux.Handle("POST /api/v1/mailing-list/subscribe", middleware.AuthRateLimit(h.Limiter)(subscribe))
	} else {
		mux.Handle("POST /api/v1/mailing-list/subscribe", subscribe)
	}
	mux.HandleFunc("OPTIONS /api/v1/mailing-list/subscribe", h.preflight)
	mux.HandleFunc("POST /api/v1/mailing-list/unlock", h.unlock)
	mux.HandleFunc("POST /api/v1/mailing-list/init", h.init)
}

func (h *MailingListHandler) corsOrigin(r *http.Request) string {
	origin := r.Header.Get("Origin")
	for _, allowed := range h.AllowOrigin {
		if origin == allowed {
			return origin
		}
	}
	return ""
}

func (h *MailingListHandler) preflight(w http.ResponseWriter, r *http.Request) {
	if origin := h.corsOrigin(r); origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	}
	w.WriteHeader(http.StatusNoContent)
}

type subscribeRequest struct {
	Email   string `json:"email"`
	Consent bool   `json:"consent"`
}

// POST /api/v1/mailing-list/subscribe — public, rate-limited (see main.go
// wiring), CORS-restricted to the OKEMILY origin(s).
func (h *MailingListHandler) subscribe(w http.ResponseWriter, r *http.Request) {
	if origin := h.corsOrigin(r); origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
	}

	var req subscribeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}
	email := strings.TrimSpace(req.Email)
	if !req.Consent {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "consent is required"})
		return
	}
	if !emailRe.MatchString(email) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid email address"})
		return
	}

	if h.Vault.Locked() {
		// Fails closed — this is the accepted trade-off for "never at rest
		// unencrypted": signups pause until a human runs the unlock CLI.
		// Nothing else in IDUNA is affected (see mailinglist package doc).
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "signups are temporarily paused — please try again shortly",
		})
		return
	}

	ciphertext, nonce, err := h.Vault.Encrypt([]byte(email))
	if err != nil {
		log.Printf("[mailinglist] encrypt failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
		return
	}

	id, err := h.Store.AddSubscriber(ciphertext, nonce, CurrentConsentVersion)
	if err != nil {
		log.Printf("[mailinglist] store failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
		return
	}

	// Best-effort Mailchimp sync using the plaintext already in hand from
	// this request — never decrypted back out of storage for this. Failure
	// here does not fail the request; IDUNA's own store already has it.
	if h.Mailchimp != nil {
		if err := h.Mailchimp.Subscribe(email); err != nil {
			log.Printf("[mailinglist] mailchimp sync failed for subscriber id=%d: %v", id, err)
		} else if err := h.Store.MarkMailchimpSynced(id); err != nil {
			log.Printf("[mailinglist] failed to mark synced id=%d: %v", id, err)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

type unlockRequest struct {
	Passphrase string `json:"passphrase"`
}

func requireLoopback(w http.ResponseWriter, r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		http.Error(w, "forbidden — loopback only", http.StatusForbidden)
		return false
	}
	return true
}

// POST /api/v1/mailing-list/unlock — loopback-only. Driven by
// cmd/mailing-list-unlock, which prompts for the passphrase interactively
// (never as a CLI arg — that would leak via `ps`/shell history) and POSTs it
// here over localhost only; never exposed through nginx/the public domain.
func (h *MailingListHandler) unlock(w http.ResponseWriter, r *http.Request) {
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
		writeJSON(w, http.StatusPreconditionRequired, map[string]any{"error": "vault not initialized — run: mailing-list-unlock -init"})
		return
	}

	if err := h.Vault.Unlock(req.Passphrase, salt, canaryCT, canaryNonce); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "incorrect passphrase"})
		return
	}
	log.Printf("[mailinglist] vault unlocked")
	writeJSON(w, http.StatusOK, map[string]any{"status": "unlocked"})
}

// POST /api/v1/mailing-list/init — loopback-only, one-time setup. Refuses to
// run if a vault already exists (Store.InitVault enforces this) so it can
// never be used to accidentally overwrite and permanently orphan existing
// encrypted subscriber rows.
func (h *MailingListHandler) init(w http.ResponseWriter, r *http.Request) {
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
	log.Printf("[mailinglist] vault initialized and unlocked")
	writeJSON(w, http.StatusOK, map[string]any{"status": "initialized and unlocked"})
}
