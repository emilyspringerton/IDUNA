package nock

import (
	"strings"
	"testing"
)

func TestLoadGLTFRejectsBlendWithClearError(t *testing.T) {
	_, err := loadGLTFBytes([]byte("BLENDER-v292RENDH...."))
	if err == nil || !strings.Contains(err.Error(), "glTF 2.0 (.glb)") {
		t.Fatalf("want friendly .blend error, got %v", err)
	}
}
