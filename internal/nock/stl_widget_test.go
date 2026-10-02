package nock

import (
	"encoding/binary"
	"math"
	"testing"
)

func binSTL(tris [][9]float32) []byte {
	b := make([]byte, 84+50*len(tris))
	copy(b, "Exported from Blender")
	binary.LittleEndian.PutUint32(b[80:], uint32(len(tris)))
	for i, t := range tris {
		for k, v := range t {
			binary.LittleEndian.PutUint32(b[84+i*50+12+k*4:], math.Float32bits(v))
		}
	}
	return b
}

// Triangle spans x 0..2, y 0..4 (STL), z 0..6 (STL up). Y-up result: size (2,6,4), center (1,3,-2).
func TestSTLBinaryToBox(t *testing.T) {
	raw := binSTL([][9]float32{{0, 0, 0, 2, 0, 6, 0, 4, 0}})
	boxes, err := GLTFToWidgetBoxes(raw, 1)
	if err != nil || len(boxes) != 1 {
		t.Fatalf("err=%v boxes=%v", err, boxes)
	}
	b := boxes[0]
	if b.SX != 2 || b.SY != 6 || b.SZ != 4 || b.CX != 1 || b.CY != 3 || b.CZ != -2 {
		t.Fatalf("got %+v", b)
	}
}

func TestSTLASCIIToBoxScaled(t *testing.T) {
	raw := []byte("solid x\nfacet normal 0 0 1\nouter loop\nvertex 0 0 0\nvertex 2 0 6\nvertex 0 4 0\nendloop\nendfacet\nendsolid x\n")
	boxes, err := GLTFToWidgetBoxes(raw, 2)
	if err != nil || len(boxes) != 1 {
		t.Fatalf("err=%v", err)
	}
	if b := boxes[0]; b.SX != 4 || b.SY != 12 || b.SZ != 8 {
		t.Fatalf("got %+v", b)
	}
}
