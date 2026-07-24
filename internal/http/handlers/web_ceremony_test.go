package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"iduna/internal/auth"
	"iduna/internal/auth/jwt"
	"iduna/internal/honorcode"
	"iduna/internal/http/handlers"
	"iduna/internal/http/middleware"
	"iduna/internal/store"
)

// stubCeremonyStore is an in-memory fake covering exactly what
// WebCeremonyHandler needs, layered over stubApplesStore's no-op base.
type stubCeremonyStore struct {
	stubApplesStore
	users        map[string]*auth.User // by IDString
	takenHandles map[string]bool
}

func newStubCeremonyStore() *stubCeremonyStore {
	return &stubCeremonyStore{
		users:        map[string]*auth.User{},
		takenHandles: map[string]bool{},
	}
}

func (s *stubCeremonyStore) GetUserByID(_ context.Context, id string) (*auth.User, error) {
	u, ok := s.users[id]
	if !ok {
		return nil, errors.New("not found")
	}
	cp := *u
	return &cp, nil
}

func (s *stubCeremonyStore) AcceptHonorCode(_ context.Context, userID string, version int, sha, text, _ string) error {
	u, ok := s.users[userID]
	if !ok {
		return errors.New("not found")
	}
	u.HonorAccepted = true
	u.HonorCurrentSHA = sha
	u.HonorCurrentVer = version
	u.HonorCurrentText = text
	return nil
}

func (s *stubCeremonyStore) ClaimHandle(_ context.Context, userID, handle, _ string) error {
	u, ok := s.users[userID]
	if !ok {
		return errors.New("not found")
	}
	if u.Handle != "" {
		return store.ErrHandleAlreadySet
	}
	if s.takenHandles[handle] {
		return store.ErrHandleTaken
	}
	u.Handle = handle
	s.takenHandles[handle] = true
	return nil
}

func (s *stubCeremonyStore) IsHandleAvailable(_ context.Context, handle string) (bool, error) {
	return !s.takenHandles[handle], nil
}

func ceremonyHandlerWithAuth(keys *jwt.Keys, s *stubCeremonyStore) (public, protected http.Handler) {
	h := &handlers.WebCeremonyHandler{Keys: keys, Store: s, Issuer: "https://test.internal"}
	pubMux := http.NewServeMux()
	h.Register(pubMux)

	protMux := http.NewServeMux()
	protMux.Handle("/me", http.HandlerFunc(h.HandleMe))
	protMux.Handle("/honor-code/accept", http.HandlerFunc(h.HandleHonorAccept))
	protMux.Handle("/me/handle", http.HandlerFunc(h.HandleMeHandle))
	return pubMux, middleware.RequireAuth(keys)(protMux)
}

// TestHandleMe_ReportsHonorCodeRequired_ForFreshUser verifies a user who has
// never accepted anything gets honor_code.required=true from /me.
func TestHandleMe_ReportsHonorCodeRequired_ForFreshUser(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	s := newStubCeremonyStore()
	s.users["u1"] = &auth.User{IDString: "u1", Email: "u1@example.com"}
	_, protected := ceremonyHandlerWithAuth(keys, s)

	token := makeAgentToken(t, keys, "u1", nil)
	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	protected.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	honor := out["honor_code"].(map[string]any)
	if honor["required"] != true {
		t.Errorf("expected honor_code.required=true for fresh user, got %v", honor["required"])
	}
}

// TestHandleHonorAccept_RejectsStaleSHA verifies a mismatched sha256 is
// rejected with 403 and a honor_code payload (matching app.js's error branch).
func TestHandleHonorAccept_RejectsStaleSHA(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	s := newStubCeremonyStore()
	s.users["u1"] = &auth.User{IDString: "u1"}
	_, protected := ceremonyHandlerWithAuth(keys, s)

	token := makeAgentToken(t, keys, "u1", nil)
	body := `{"sha256":"not-the-real-sha"}`
	req := httptest.NewRequest(http.MethodPost, "/honor-code/accept", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	protected.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", rr.Code, rr.Body.String())
	}
	var out map[string]any
	json.Unmarshal(rr.Body.Bytes(), &out)
	if out["honor_code"] == nil {
		t.Errorf("expected honor_code payload on rejection, got %v", out)
	}
	if s.users["u1"].HonorAccepted {
		t.Errorf("should not have recorded acceptance on sha mismatch")
	}
}

// TestHandleHonorAccept_AcceptsCurrentSHA verifies the real current sha
// succeeds and is persisted via the store.
func TestHandleHonorAccept_AcceptsCurrentSHA(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	s := newStubCeremonyStore()
	s.users["u1"] = &auth.User{IDString: "u1"}
	_, protected := ceremonyHandlerWithAuth(keys, s)

	token := makeAgentToken(t, keys, "u1", nil)
	body := `{"sha256":"` + honorcode.CurrentSHA256 + `"}`
	req := httptest.NewRequest(http.MethodPost, "/honor-code/accept", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	protected.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !s.users["u1"].HonorAccepted {
		t.Errorf("expected acceptance to be recorded")
	}
}

// TestHandleMeHandle_RequiresHonorCodeFirst verifies claiming a gamertag
// before accepting the honor code is rejected with 403, not silently allowed.
func TestHandleMeHandle_RequiresHonorCodeFirst(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	s := newStubCeremonyStore()
	s.users["u1"] = &auth.User{IDString: "u1"} // HonorAccepted defaults false
	_, protected := ceremonyHandlerWithAuth(keys, s)

	token := makeAgentToken(t, keys, "u1", nil)
	body := `{"handle":"CoolName"}`
	req := httptest.NewRequest(http.MethodPost, "/me/handle", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	protected.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403 (honor code required first), got %d: %s", rr.Code, rr.Body.String())
	}
	if s.users["u1"].Handle != "" {
		t.Errorf("handle should not have been claimed")
	}
}

// TestHandleMeHandle_SucceedsAfterHonorAccepted verifies the full order:
// accept honor code, then claim a handle, succeeds end to end.
func TestHandleMeHandle_SucceedsAfterHonorAccepted(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	s := newStubCeremonyStore()
	s.users["u1"] = &auth.User{
		IDString:        "u1",
		HonorAccepted:   true,
		HonorCurrentVer: honorcode.CurrentVersion,
	}
	_, protected := ceremonyHandlerWithAuth(keys, s)

	token := makeAgentToken(t, keys, "u1", nil)
	body := `{"handle":"CoolName"}`
	req := httptest.NewRequest(http.MethodPost, "/me/handle", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	protected.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if s.users["u1"].Handle != "CoolName" {
		t.Errorf("expected handle 'CoolName', got %q", s.users["u1"].Handle)
	}
}

// TestHandleGamertagCheck_ReflectsAvailability verifies the public
// availability-check endpoint against both a free and a taken handle.
func TestHandleGamertagCheck_ReflectsAvailability(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	s := newStubCeremonyStore()
	s.takenHandles["Taken"] = true
	public, _ := ceremonyHandlerWithAuth(keys, s)

	req := httptest.NewRequest(http.MethodGet, "/gamertag/check?handle=Taken", nil)
	rr := httptest.NewRecorder()
	public.ServeHTTP(rr, req)
	var out map[string]any
	json.Unmarshal(rr.Body.Bytes(), &out)
	if out["available"] != false {
		t.Errorf("expected Taken to be unavailable, got %v", out)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/gamertag/check?handle=Free", nil)
	rr2 := httptest.NewRecorder()
	public.ServeHTTP(rr2, req2)
	var out2 map[string]any
	json.Unmarshal(rr2.Body.Bytes(), &out2)
	if out2["available"] != true {
		t.Errorf("expected Free to be available, got %v", out2)
	}
}

// TestHandleGamertagCheck_RejectsReservedWord verifies server-side reserved
// word rejection (not just client-side, which app.js already had).
func TestHandleGamertagCheck_RejectsReservedWord(t *testing.T) {
	keys, _ := jwt.GenerateKeys()
	s := newStubCeremonyStore()
	public, _ := ceremonyHandlerWithAuth(keys, s)

	req := httptest.NewRequest(http.MethodGet, "/gamertag/check?handle=admin", nil)
	rr := httptest.NewRecorder()
	public.ServeHTTP(rr, req)
	var out map[string]any
	json.Unmarshal(rr.Body.Bytes(), &out)
	if out["available"] != false {
		t.Errorf("expected 'admin' to be rejected as reserved, got %v", out)
	}
}
