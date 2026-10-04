package shankpit

import (
	"math"
	"testing"
)

func TestEulerRoundTrip(t *testing.T) {
	for _, e := range [][3]float64{{0, 0, 0}, {30, 0, 0}, {0, 45, 0}, {15, 60, -30}, {90, 0, 45}} {
		x, y, z := matEuler(eulerMat(e[0], e[1], e[2]))
		m1, m2 := eulerMat(e[0], e[1], e[2]), eulerMat(x, y, z)
		for i := 0; i < 3; i++ {
			for j := 0; j < 3; j++ {
				if math.Abs(m1[i][j]-m2[i][j]) > 1e-3 {
					t.Fatalf("euler %v -> (%v,%v,%v) changed the rotation", e, x, y, z)
				}
			}
		}
	}
}

func TestCarryOrientationComposesParentYaw(t *testing.T) {
	// A child wall tilted 30 deg about X inside a parent object turned 90 deg about Y: composed
	// rotation must equal Ry(90)*Rx(30), and sx/sz must NOT be swapped (it is not axis-aligned).
	src := Wall{SX: 4, SY: 2, SZ: 8, RotX: 30}
	dst := Wall{SX: 8, SY: 2, SZ: 4} // what rotateY90 would have produced
	carryOrientation(src, &dst, 90)
	if dst.SX != 4 || dst.SZ != 8 {
		t.Fatalf("sizes swapped for an oriented wall: %v %v", dst.SX, dst.SZ)
	}
	want := eulerMat(0, 90, 0)
	rl := eulerMat(30, 0, 0)
	var m [3][3]float64
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			for k := 0; k < 3; k++ {
				m[i][j] += want[i][k] * rl[k][j]
			}
		}
	}
	got := eulerMat(dst.RotX, dst.RotY, dst.RotZ)
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			if math.Abs(m[i][j]-got[i][j]) > 1e-3 {
				t.Fatalf("composed rotation mismatch at %d,%d", i, j)
			}
		}
	}
	// plain axis-aligned walls are untouched
	d2 := Wall{SX: 8, SY: 2, SZ: 4}
	carryOrientation(Wall{SX: 4, SY: 2, SZ: 8}, &d2, 90)
	if d2.SX != 8 || d2.Ramp || d2.RotX != 0 {
		t.Fatal("plain wall modified")
	}
	// ramp flag carries
	d3 := Wall{}
	carryOrientation(Wall{SX: 1, SY: 1, SZ: 1, Ramp: true}, &d3, 0)
	if !d3.Ramp {
		t.Fatal("ramp not carried")
	}
}
