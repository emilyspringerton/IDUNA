// shankpit-lab-builder -- SECTION 592 (founder real-time: "CLONE [INTERIOR] into LAB and start building
// out the lab using PARENA and BIGO and SHANKPIT WIDGETS system, just use the API to programatically
// create your own widgets"). Goes through the same validated shankpit.LevelStore / WidgetStore Go API the
// /admin/nock handlers call: creates the lab station widgets, clones the source level into LAB, places
// the widgets, then writes LAB's export JSON (what the native client loads).
//
// It never touches the live DB by default: point -db at a COPY (sqlite3 var/iduna.db ".backup copy.db").
// Re-running is idempotent: existing widgets with these names are updated, an existing LAB level is
// re-used and its objects replaced.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	_ "modernc.org/sqlite"

	"iduna/internal/shankpit"
)

type box struct {
	name       string
	x, y, z    float64 // centre, widget-local; y is above the widget's floor
	sx, sy, sz float64
	r, g, b    float64
	material   string
}

func (b box) wall(id int) shankpit.Wall {
	return shankpit.Wall{ID: id, X: b.x, Y: b.y, Z: b.z, SX: b.sx, SY: b.sy, SZ: b.sz, R: b.r, G: b.g, B: b.b, Friction: 0.8, Material: b.material, Name: b.name}
}

// Station widgets. A wall named lab_<kind> becomes an interactive station in the level export
// (shankpit.labStationsForExport); everything else is body dressing.
var widgets = []struct {
	name  string
	boxes []box
	place [3]float64 // x, z, rot_y within the lab
}{
	{"LAB_SPLICE_BENCH", []box{
		{"bench", 0, 1.5, 0, 8, 3, 4, 0.75, 0.78, 0.8, "metal"},
		{"lab_splice", 0, 3.2, 0, 3, 0.4, 2, 0.2, 0.9, 0.5, "ips_light"},
		{"hood", 0, 7, -1.6, 8, 0.6, 1, 0.3, 0.3, 0.35, "metal"},
	}, [3]float64{-130, -205, 0}},
	{"LAB_CENTRIFUGE", []box{
		{"base", 0, 1.5, 0, 4, 3, 4, 0.7, 0.72, 0.75, "metal"},
		{"lab_centrifuge", 0, 3.4, 0, 3, 0.8, 3, 0.9, 0.3, 0.25, "ips_light"},
	}, [3]float64{-112, -205, 0}},
	{"LAB_PCR", []box{
		{"base", 0, 1.5, 0, 3, 3, 3, 0.7, 0.72, 0.75, "metal"},
		{"lab_pcr", 0, 3.3, 0, 2, 0.6, 2, 0.9, 0.8, 0.2, "ips_light"},
	}, [3]float64{-98, -205, 0}},
	{"LAB_CLONE_VATS", []box{
		{"rack", 0, 0.5, 0, 14, 1, 4, 0.3, 0.32, 0.36, "metal"},
		{"lab_vat", -4.5, 4.5, 0, 3, 8, 3, 0.2, 0.8, 0.9, "ips_light"},
		{"lab_vat", 0, 4.5, 0, 3, 8, 3, 0.2, 0.8, 0.9, "ips_light"},
		{"lab_vat", 4.5, 4.5, 0, 3, 8, 3, 0.2, 0.8, 0.9, "ips_light"},
	}, [3]float64{-111, -180, 0}},
	{"LAB_SAMPLE_FRIDGE", []box{
		{"lab_fridge", 0, 4, 0, 4, 8, 3.5, 0.85, 0.88, 0.9, "metal"},
		{"handle", 2.1, 4, 0, 0.3, 4, 0.5, 0.2, 0.2, 0.2, "metal"},
	}, [3]float64{-140, -185, 90}},
	{"LAB_CONSOLE", []box{
		{"desk", 0, 1.8, 0, 7, 3.6, 3, 0.4, 0.4, 0.45, "concrete"},
		{"lab_console", 0, 5.2, -0.8, 5, 3, 0.5, 0.3, 0.6, 0.95, "ips_light"},
	}, [3]float64{-84, -185, 270}},
}

func main() {
	dbPath := flag.String("db", "", "path to a COPY of iduna.db (required)")
	source := flag.String("source", "INTERRIOR_1", "level to clone")
	name := flag.String("name", "LAB", "name of the new level")
	out := flag.String("out", "", "write LAB's export JSON here (e.g. ../SHANKPIT/var/lab/lab.json)")
	flag.Parse()
	if *dbPath == "" {
		log.Fatal("-db is required (use a copy, not the live var/iduna.db)")
	}
	db, err := sql.Open("sqlite", *dbPath+"?_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	materials := &shankpit.MaterialStore{DB: db}
	ws := &shankpit.WidgetStore{DB: db}
	ls := &shankpit.LevelStore{DB: db, Materials: materials, Widgets: ws}

	// 1. widgets (create or update by name)
	var objects []shankpit.LevelObject
	for i, wd := range widgets {
		var walls []shankpit.Wall
		for j, b := range wd.boxes {
			walls = append(walls, b.wall(j+1))
		}
		w, err := ws.GetWidgetByName(ctx, wd.name)
		if err != nil || w == nil {
			w, err = ws.CreateWidget(ctx, wd.name, walls, nil)
		} else {
			w, err = ws.UpdateWidget(ctx, w.ID, walls, nil)
		}
		if err != nil {
			log.Fatalf("widget %s: %v", wd.name, err)
		}
		objects = append(objects, shankpit.LevelObject{ID: i + 1, RefWidgetID: w.ID, X: wd.place[0], Y: 2, Z: wd.place[1], RotY: int(wd.place[2])})
		fmt.Printf("widget %-18s id=%d walls=%d\n", wd.name, w.ID, len(walls))
	}

	// 2. clone the source level into LAB (or reuse an existing LAB)
	list, err := ls.ListLevels(ctx)
	if err != nil {
		log.Fatalf("list levels: %v", err)
	}
	var srcID, labID int64
	for _, l := range list {
		if l.Name == *source {
			srcID = l.ID
		}
		if l.Name == *name {
			labID = l.ID
		}
	}
	if labID == 0 {
		if srcID == 0 {
			log.Fatalf("source level %q not found in this DB", *source)
		}
		lab, err := ls.CloneLevel(ctx, srcID, *name)
		if err != nil {
			log.Fatalf("clone: %v", err)
		}
		labID = lab.ID
		fmt.Printf("cloned %s -> %s id=%d\n", *source, *name, labID)
	}

	// 3. place the widgets as LAB's objects (keep every other field of the clone as-is)
	lab, err := ls.GetLevel(ctx, labID)
	if err != nil {
		log.Fatalf("get lab: %v", err)
	}
	if _, err := ls.UpdateLevel(ctx, labID, lab.Width, lab.Height, lab.Depth, lab.GroundPlaneEnabled, lab.GroundPlaneSquares,
		lab.Walls, objects, lab.Spawners, lab.Doors, lab.NavNodes, lab.Characters, lab.LevelExits, lab.NextLevelID); err != nil {
		log.Fatalf("update lab: %v", err)
	}
	doc, err := ls.Export(ctx, labID)
	if err != nil {
		log.Fatalf("export: %v", err)
	}
	fmt.Printf("LAB export: %d walls, %d lab_stations, %d spawners\n", len(doc.Walls), len(doc.LabStations), len(doc.Spawners))
	if *out != "" {
		b, _ := json.MarshalIndent(doc, "", " ")
		if err := os.WriteFile(*out, b, 0o644); err != nil {
			log.Fatalf("write: %v", err)
		}
		fmt.Println("wrote", *out)
	}
}
