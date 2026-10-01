package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"iduna/internal/http/handlers"
)

func TestNockTexturesPublicHandler_ByNameImage(t *testing.T) {
	th := newTexturesTestHandler(t)
	ctx := context.Background()
	if _, err := th.Store.CreateTexture(ctx, "bullet-hole-ar", 8, 8, []byte("png-ar"), "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := th.Store.CreateTexture(ctx, "private-brick", 8, 8, []byte("png-private"), "", ""); err != nil {
		t.Fatal(err)
	}
	h := &handlers.NockTexturesPublicHandler{Store: th.Store}
	get := func(method, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		return rec
	}

	rec := get(http.MethodGet, "/api/v1/nock-textures/by-name/bullet-hole-ar/image")
	if rec.Code != 200 || rec.Body.String() != "png-ar" || rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("allowlisted fetch: code=%d ct=%q body=%q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	if rec := get(http.MethodGet, "/api/v1/nock-textures/by-name/private-brick/image"); rec.Code != 404 {
		t.Errorf("non-allowlisted texture must 404, got %d", rec.Code)
	}
	if rec := get(http.MethodGet, "/api/v1/nock-textures/by-name/bullet-hole-nope/image"); rec.Code != 404 {
		t.Errorf("missing texture must 404, got %d", rec.Code)
	}
	if rec := get(http.MethodPost, "/api/v1/nock-textures/by-name/bullet-hole-ar/image"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST must be 405, got %d", rec.Code)
	}
	if rec := get(http.MethodGet, "/api/v1/nock-textures/by-name/bullet-hole-ar"); rec.Code != 404 {
		t.Errorf("missing /image suffix must 404, got %d", rec.Code)
	}
}
