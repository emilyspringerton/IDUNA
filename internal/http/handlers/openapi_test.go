package handlers

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// TestOpenAPISpec_CoversRecentEndpoints is a real regression guard against
// the exact class of drift CHANGELOG.md already flagged once (a Swagger
// spec found "known-stale" -- missing endpoints added earlier the same
// day). Add a path/schema here whenever a real new route ships, same as
// updating this file's own idunaOpenAPISpec.
func TestOpenAPISpec_CoversRecentEndpoints(t *testing.T) {
	h := &OpenAPIHandler{}
	req := httptest.NewRequest("GET", "/api/v1/openapi.json", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var spec map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &spec); err != nil {
		t.Fatalf("decode spec: %v", err)
	}
	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatal("spec has no paths object")
	}

	for _, want := range []string{
		"/admin/kanban",
		"/api/v1/kanban/cards",
		"/api/v1/kanban/cards/{id}",
		"/portal",
		"/portal/login",
	} {
		if _, ok := paths[want]; !ok {
			t.Errorf("openapi spec is missing path %q", want)
		}
	}

	schemas, ok := spec["components"].(map[string]any)["schemas"].(map[string]any)
	if !ok {
		t.Fatal("spec has no components.schemas object")
	}
	if _, ok := schemas["KanbanCard"]; !ok {
		t.Error("openapi spec is missing the KanbanCard schema")
	}
}
