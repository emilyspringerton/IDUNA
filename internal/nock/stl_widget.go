package nock

// stl_widget.go — STL -> SHANKPIT Widget geometry. An STL file is one anonymous triangle soup (no
// scene graph, no nodes, no materials), so it maps to exactly ONE box: the AABB of every vertex.
// STL is conventionally Z-up (Blender's exporter keeps Z up by default) while SHANKPIT is Y-up,
// so vertices are rotated (x,y,z) -> (x,z,-y) before bounds are taken. Both binary and ASCII STL
// are read. Units are taken as-is (Blender writes meters); the caller's scale multiplies them.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
)

// isSTL reports whether raw is a binary STL (size matches its own triangle count exactly -- the
// only reliable test, since binary headers may start with "solid") or an ASCII STL.
func isSTL(raw []byte) bool {
	if len(raw) >= 84 {
		n := binary.LittleEndian.Uint32(raw[80:84])
		if uint64(len(raw)) == 84+50*uint64(n) {
			return true
		}
	}
	head := bytes.TrimLeft(raw[:min(len(raw), 512)], " \t\r\n\xef\xbb\xbf")
	return bytes.HasPrefix(head, []byte("solid")) && bytes.Contains(raw, []byte("vertex"))
}

func stlBox(raw []byte, scale float64) (WidgetBox, error) {
	minV := [3]float64{math.Inf(1), math.Inf(1), math.Inf(1)}
	maxV := [3]float64{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
	add := func(x, y, z float64) {
		// Z-up -> Y-up.
		for i, v := range [3]float64{x, z, -y} {
			minV[i] = math.Min(minV[i], v)
			maxV[i] = math.Max(maxV[i], v)
		}
	}
	count := 0
	if len(raw) >= 84 && uint64(len(raw)) == 84+50*uint64(binary.LittleEndian.Uint32(raw[80:84])) {
		n := int(binary.LittleEndian.Uint32(raw[80:84]))
		for t := 0; t < n; t++ {
			off := 84 + t*50 + 12 // skip the facet normal
			for v := 0; v < 3; v++ {
				var c [3]float64
				for k := 0; k < 3; k++ {
					c[k] = float64(math.Float32frombits(binary.LittleEndian.Uint32(raw[off+v*12+k*4:])))
				}
				add(c[0], c[1], c[2])
				count++
			}
		}
	} else {
		fields := bytes.Fields(raw)
		for i := 0; i+3 < len(fields); i++ {
			if string(fields[i]) != "vertex" {
				continue
			}
			var c [3]float64
			for k := 0; k < 3; k++ {
				f, err := strconv.ParseFloat(string(fields[i+1+k]), 64)
				if err != nil {
					return WidgetBox{}, fmt.Errorf("ASCII STL: bad vertex coordinate %q", fields[i+1+k])
				}
				c[k] = f
			}
			add(c[0], c[1], c[2])
			count++
		}
	}
	if count == 0 || math.IsInf(minV[0], 1) || math.IsNaN(minV[0]+maxV[0]+minV[1]+maxV[1]+minV[2]+maxV[2]) {
		return WidgetBox{}, fmt.Errorf("STL file has no usable triangles")
	}
	b := WidgetBox{Name: "stl", R: 0.6, G: 0.6, B: 0.65}
	b.CX = (minV[0] + maxV[0]) / 2 * scale
	b.CY = (minV[1] + maxV[1]) / 2 * scale
	b.CZ = (minV[2] + maxV[2]) / 2 * scale
	b.SX = math.Max((maxV[0]-minV[0])*scale, MinBoxExtent)
	b.SY = math.Max((maxV[1]-minV[1])*scale, MinBoxExtent)
	b.SZ = math.Max((maxV[2]-minV[2])*scale, MinBoxExtent)
	return b, nil
}
