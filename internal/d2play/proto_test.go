package d2play

import "testing"

// MATCH_FOUND is 28 bytes of payload from an older server (packing time a u16) and 30 from the current one (a u32: 120 s does not fit a u16 of ms).
// The gateway must take both, so it can be rolled out before the server and an old server keeps working until it is replaced.
func TestSplitFrameMatchFoundBothWidths(t *testing.T) {
	for _, payload := range []int{28, 30} {
		f := append([]byte{byte(1 + payload), 0, SMatchFound}, make([]byte, payload)...)
		got, used, err := splitFrame(f)
		if err != nil || used != len(f) || got[0] != SMatchFound {
			t.Fatalf("payload %d: err=%v used=%d", payload, err, used)
		}
	}
	for _, payload := range []int{27, 29, 31} {
		f := append([]byte{byte(1 + payload), 0, SMatchFound}, make([]byte, payload)...)
		if _, _, err := splitFrame(f); err == nil {
			t.Fatalf("payload %d should be rejected", payload)
		}
	}
	// a frame of another type is still held to its exact size
	f := append([]byte{byte(1 + 30), 0, SWelcome}, make([]byte, 30)...)
	if _, _, err := splitFrame(f); err == nil {
		t.Fatal("a 30-byte WELCOME should be rejected")
	}
}
