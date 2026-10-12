package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"iduna/internal/http/handlers"
)

func TestSSOLogin_GoogleButtonOnlyWhenEnabled(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		h := &handlers.SSOLoginHandler{AllowedHosts: []string{"localhost"}, GoogleEnabled: enabled}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/login?redirect_uri=http://localhost/x", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("status %d", w.Code)
		}
		has := strings.Contains(w.Body.String(), `id="google-btn"`)
		if has != enabled {
			t.Errorf("GoogleEnabled=%v but button present=%v", enabled, has)
		}
	}
}
