package hslz4

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/rand"
	"os"
	"testing"
)

// Vectors were produced by PARENA's compress/lz4_block.prn (real LZ4 block format).
func TestDecodeBlockKnownAnswers(t *testing.T) {
	for _, name := range []string{"log60k", "rep36"} {
		raw, err := os.ReadFile("testdata/" + name + ".raw")
		if err != nil {
			t.Fatal(err)
		}
		comp, err := os.ReadFile("testdata/" + name + ".lz4")
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeBlock(comp, len(raw))
		if err != nil || !bytes.Equal(got, raw) {
			t.Fatalf("%s: err=%v equal=%v", name, err, bytes.Equal(got, raw))
		}
		if len(comp) >= len(raw) {
			t.Fatalf("%s: not smaller: %d >= %d", name, len(comp), len(raw))
		}
	}
}

func TestDecodeFramesAndLimits(t *testing.T) {
	raw, _ := os.ReadFile("testdata/log60k.raw")
	comp, _ := os.ReadFile("testdata/log60k.lz4")
	frame := func(rawLen, compLen int, blk []byte) []byte {
		h := make([]byte, 8)
		binary.LittleEndian.PutUint32(h, uint32(rawLen))
		binary.LittleEndian.PutUint32(h[4:], uint32(compLen))
		return append(h, blk...)
	}
	two := append(frame(len(raw), len(comp), comp), frame(len(raw), len(comp), comp)...)
	got, err := DecodeFrames(two, 1<<20)
	if err != nil || len(got) != 2*len(raw) {
		t.Fatalf("two frames: %v len=%d", err, len(got))
	}
	if _, err := DecodeFrames(two, len(raw)); err == nil {
		t.Fatal("maxTotal not enforced")
	}
	if _, err := DecodeFrames(frame(MaxBlock+1, 1, []byte{0}), 1<<30); err == nil {
		t.Fatal("oversize frame accepted")
	}
}

// Truncations and bit flips must error or yield exactly outLen bytes -- never panic.
func TestDecodeBlockMalformedNeverPanics(t *testing.T) {
	raw, _ := os.ReadFile("testdata/log60k.raw")
	comp, _ := os.ReadFile("testdata/log60k.lz4")
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 2000; i++ {
		c := append([]byte(nil), comp...)
		switch i % 3 {
		case 0:
			c = c[:r.Intn(len(c))]
		case 1:
			c[r.Intn(len(c))] ^= byte(1 << uint(r.Intn(8)))
		default:
			c = c[r.Intn(len(c)/2):]
		}
		if out, err := DecodeBlock(c, len(raw)); err == nil && len(out) != len(raw) {
			t.Fatalf("iter %d: ok with wrong length %d", i, len(out))
		}
	}
}

// pc_powershell51_frames.bin was produced by the uplink's own C# compressor (HsTail.Frames) running
// in Windows PowerShell 5.1 on a real PC; it must decode to exactly the data below.
func TestDecodeFramesFromRealPowerShellUplink(t *testing.T) {
	f, err := os.ReadFile("testdata/pc_powershell51_frames.bin")
	if err != nil {
		t.Fatal(err)
	}
	var want bytes.Buffer
	for i := 0; i < 1800; i++ {
		fmt.Fprintf(&want, "D 12:00:00.0000000 GameState.DebugPrintPower() - TAG_CHANGE Entity=[entityName=Card %d id=%d zone=HAND zonePos=%d cardId=CORE_X%d player=1] tag=ZONE value=PLAY\n",
			i, i%97, i%7, i%31)
	}
	got, err := DecodeFrames(f, 1<<24)
	if err != nil || !bytes.Equal(got, want.Bytes()) {
		t.Fatalf("err=%v len got=%d want=%d", err, len(got), want.Len())
	}
}
