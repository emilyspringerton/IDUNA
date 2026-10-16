package shankpit460

import (
	"bufio"
	_ "embed"
	"fmt"
	"strings"
)

// v0_shankpit.map is shankpit-460's own launch deathmatch map (maps/v0_shankpit.map, the external map format of
// packages/common/map_loader.h), copied verbatim: the room checks lines of sight against the same boxes the
// browser's engine collides with.
//
//go:embed v0_shankpit.map
var mapFile string

// Box is an axis-aligned box: centre and full size, as the map file writes it.
type Box struct{ X, Y, Z, W, H, D float32 }

// Spawn is a spawn point from the map file.
type Spawn struct{ X, Y, Z float32 }

// Arena is the parsed map.
type Arena struct {
	Boxes  []Box // index 0 is the floor slab, skipped by every consumer (as in map_loader.h)
	Spawns []Spawn
}

// ParseArena reads the map format: `box x y z w h d`, `spawn x y z`, `poi ...`; '#' starts a comment.
func ParseArena(src string) (*Arena, error) {
	a := &Arena{}
	sc := bufio.NewScanner(strings.NewReader(src))
	line := 0
	for sc.Scan() {
		line++
		t := sc.Text()
		if i := strings.IndexByte(t, '#'); i >= 0 {
			t = t[:i]
		}
		f := strings.Fields(t)
		if len(f) == 0 {
			continue
		}
		switch f[0] {
		case "box":
			var b Box
			if _, err := fmt.Sscanf(strings.Join(f[1:], " "), "%f %f %f %f %f %f", &b.X, &b.Y, &b.Z, &b.W, &b.H, &b.D); err != nil || b.W <= 0 || b.H <= 0 || b.D <= 0 {
				return nil, fmt.Errorf("map line %d: bad box", line)
			}
			a.Boxes = append(a.Boxes, b)
		case "spawn":
			var s Spawn
			if _, err := fmt.Sscanf(strings.Join(f[1:], " "), "%f %f %f", &s.X, &s.Y, &s.Z); err != nil {
				return nil, fmt.Errorf("map line %d: bad spawn", line)
			}
			a.Spawns = append(a.Spawns, s)
		case "poi":
		default:
			return nil, fmt.Errorf("map line %d: unknown keyword %q", line, f[0])
		}
	}
	if len(a.Boxes) < 2 || len(a.Spawns) == 0 {
		return nil, fmt.Errorf("map: %d boxes, %d spawns", len(a.Boxes), len(a.Spawns))
	}
	return a, nil
}

// slab clips the parametric ray o + t*d to [lo, hi] on one axis.
func slab(o, d, lo, hi float32, tn, tf *float32) bool {
	if d > -1e-6 && d < 1e-6 {
		return o >= lo && o <= hi
	}
	t1, t2 := (lo-o)/d, (hi-o)/d
	if t1 > t2 {
		t1, t2 = t2, t1
	}
	if t1 > *tn {
		*tn = t1
	}
	if t2 < *tf {
		*tf = t2
	}
	return *tn <= *tf
}

// LineClear reports whether the straight segment between two points passes through none of the arena's boxes
// (the floor slab at index 0 is the ground, which a segment between two standing points never meets).
func (a *Arena) LineClear(x0, y0, z0, x1, y1, z1 float32) bool {
	dx, dy, dz := x1-x0, y1-y0, z1-z0
	for i := 1; i < len(a.Boxes); i++ {
		b := a.Boxes[i]
		tn, tf := float32(0.0001), float32(1)
		if slab(x0, dx, b.X-b.W/2, b.X+b.W/2, &tn, &tf) &&
			slab(y0, dy, b.Y-b.H/2, b.Y+b.H/2, &tn, &tf) &&
			slab(z0, dz, b.Z-b.D/2, b.Z+b.D/2, &tn, &tf) {
			if tn < 0.9995 {
				return false
			}
		}
	}
	return true
}

// Inside reports whether a point is inside the playable envelope (physics.h: STADIUM_BOUNDS 420, kill plane -80).
func (a *Arena) Inside(x, y, z float32) bool {
	return x >= -420 && x <= 420 && z >= -420 && z <= 420 && y >= -80 && y <= 600
}
