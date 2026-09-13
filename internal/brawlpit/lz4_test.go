package brawlpit

import (
	"bytes"
	"strings"
	"testing"
)

func TestCompressDecompressLZ4_RoundTrip(t *testing.T) {
	original := []byte(strings.Repeat(`{"x":0,"y":-5,"w":60,"h":10,"type":0},`, 5))
	compressed := CompressLZ4(original)
	if compressed == nil {
		t.Fatal("CompressLZ4 returned nil")
	}
	decompressed := DecompressLZ4(compressed)
	if !bytes.Equal(decompressed, original) {
		t.Fatalf("round trip mismatch: got %q, want %q", decompressed, original)
	}
}

// TestCompressLZ4_RealRepetitiveDataCompresses guards the actual point of this feature: a real,
// level-shaped repetitive payload must come out SMALLER, not just round-trip correctly.
func TestCompressLZ4_RealRepetitiveDataCompresses(t *testing.T) {
	original := []byte(strings.Repeat(`{"x": 0.0, "y": -5.0, "w": 60.0, "h": 10.0, "type": 0},`, 10))
	compressed := CompressLZ4(original)
	if len(compressed) >= len(original) {
		t.Errorf("expected real compression on repetitive data: original=%d compressed=%d", len(original), len(compressed))
	}
}

// TestCompressLZ4_NeverExpandsBeyondStoredFallback is the real safety guarantee found live while
// building this: a naive per-token encoding could expand small/non-repetitive input by 5x+.
// The real fallback caps worst-case growth at exactly 1 byte (the format flag).
func TestCompressLZ4_NeverExpandsBeyondStoredFallback(t *testing.T) {
	// Genuinely incompressible-ish: no repeats longer than the real min-match.
	original := []byte("qwzxjkvbnmpl1029384756")
	compressed := CompressLZ4(original)
	if len(compressed) > len(original)+1 {
		t.Errorf("expected at most len(original)+1 bytes (STORED fallback), got %d for %d original bytes",
			len(compressed), len(original))
	}
}

func TestCompressDecompressLZ4_EmptyInput(t *testing.T) {
	compressed := CompressLZ4(nil)
	if compressed == nil {
		t.Fatal("CompressLZ4(nil) returned nil, want a real (1-byte) STORED-flag buffer")
	}
	decompressed := DecompressLZ4(compressed)
	if len(decompressed) != 0 {
		t.Errorf("expected empty round trip, got %d bytes", len(decompressed))
	}
}

func TestDecompressLZ4_MalformedInputReturnsNil(t *testing.T) {
	if got := DecompressLZ4([]byte{}); got != nil {
		t.Errorf("expected nil for empty input, got %v", got)
	}
	if got := DecompressLZ4([]byte{0xFF}); got != nil {
		t.Errorf("expected nil for an unrecognized format flag, got %v", got)
	}
}

// TestCompressDecompressLZ4_MatchesRealExportShape is a direct guard on this feature's own real
// use case: compressing an actual ExportDoc-shaped JSON payload round-trips correctly.
func TestCompressDecompressLZ4_MatchesRealExportShape(t *testing.T) {
	original := []byte(`{"version":1,"name":"Timeline","width":80,"height":60,"platforms":[` +
		`{"x":0,"y":-8,"w":52,"h":4,"type":0},{"x":-18,"y":2,"w":18,"h":1,"type":1},` +
		`{"x":18,"y":2,"w":18,"h":1,"type":1}]}`)
	compressed := CompressLZ4(original)
	decompressed := DecompressLZ4(compressed)
	if !bytes.Equal(decompressed, original) {
		t.Fatalf("real export-shaped payload didn't round trip: got %q", decompressed)
	}
}
