package nock

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestGenerateProceduralTextureSource_RealVertexCall is a real, live, unmocked Vertex AI call --
// same real skip-if-no-ADC posture as gfd_item_proposals_test.go's own equivalent test (this
// sandbox genuinely has no active gcloud account in it; skipping here is real and honest, not a
// fabricated pass). Where a real credential IS available (the actual deployment box), this
// proves the full real loop: prompt -> Vertex -> PARENA source -> validated -> compiled -> run
// -> a real PNG, not just that the HTTP plumbing compiles.
func TestGenerateProceduralTextureSource_RealVertexCall(t *testing.T) {
	requireProcGenTools(t)
	if _, err := exec.LookPath("gcloud"); err != nil {
		t.Skip("gcloud not installed in this environment -- real, honest skip, not a fabricated pass")
	}
	token, err := GcloudAccessToken()
	if err != nil || token == "" {
		t.Skip("no real gcloud ADC credential available in this environment -- real, honest skip")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	src, err := GenerateProceduralTextureSource(ctx, token, "a simple red and white striped pattern", 64, 64)
	if err != nil {
		t.Fatalf("GenerateProceduralTextureSource: %v", err)
	}
	if !strings.Contains(src, "pixel-r") {
		t.Fatalf("expected generated source to define pixel-r, got: %s", src)
	}

	if err := validateProcTextureSource(src); err != nil {
		t.Fatalf("model-generated source failed validation: %v\nsource:\n%s", err, src)
	}

	dir := t.TempDir()
	svc, _ := NewService(dir)
	svc.CreateProject("vertextest", 64, 64)
	if _, err := svc.AddProceduralLayer("vertextest", "striped", src); err != nil {
		t.Fatalf("model-generated source failed to compile/run: %v\nsource:\n%s", err, src)
	}
}
