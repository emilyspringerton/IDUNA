// cmd/nock_gen_textures — one-shot loader for hand-authored PARENA procedural texture sources
// (bypasses cmd/nock's texture-generate, which requires a live Vertex AI credential this sandbox
// doesn't have -- calls nock.TextureStore.CreateProceduralTexture directly with real, checked-in
// .prn source instead of LLM-generated source). TYLER VALHANNA (episodes/vh01_valhanna_coldopen.md)
// needs two real materials: an institutional floor/wall tile and an ECS-brand terminal glow.
// Throwaway: not wired into cmd/nock's own command dispatch, not meant to be a permanent CLI verb.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"iduna/internal/nock"
	"iduna/internal/store"
)

// parenaPath resolves one of PARENA's hand-authored texture sources. The default is the real checkout
// on this box; PARENA_DIR overrides it so the loader (and the render test beside it) can run against a
// second checkout without editing this file.
func parenaPath(rel string) string {
	dir := os.Getenv("PARENA_DIR")
	if dir == "" {
		dir = "/home/fatbaby/PARENA"
	}
	return filepath.Join(dir, rel)
}

func main() {
	dbPath := os.Getenv("NOCK_DB_PATH")
	if dbPath == "" {
		dbPath = "/home/fatbaby/IDUNA/var/iduna.db"
	}
	migrationsDir := os.Getenv("NOCK_MIGRATIONS_DIR")
	if migrationsDir == "" {
		migrationsDir = "/home/fatbaby/IDUNA/migrations/truestore"
	}
	db, err := store.OpenSQLite(dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open db:", err)
		os.Exit(1)
	}
	if err := store.RunSQLiteMigrations(db, migrationsDir); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
	ts := &nock.TextureStore{DB: db}

	textures := []struct {
		name, path, prompt string
		size               int
	}{
		{"tyler-institutional-tile", "/home/fatbaby/PARENA/stdlib/shankpit/textures/institutional_tile.prn",
			"cold institutional floor/wall tile for windowless server rooms and lab corridors, scaled-down grout grid", 256},
		// SHANKPIT per-gun bullet-hole decals (founder real-time 2026-10-01) -- multiply-blended,
		// pure-white-background; fetched by the game via GET /api/v1/nock-textures/by-name/<name>/image.
		{"bullet-hole-magnum", "/home/fatbaby/PARENA/stdlib/shankpit/textures/bullet_hole_magnum.prn",
			"magnum bullet hole: medium punched core with scorch halo and short cracks", 64},
		{"bullet-hole-ar", "/home/fatbaby/PARENA/stdlib/shankpit/textures/bullet_hole_ar.prn",
			"assault rifle bullet hole: small tight hole, light scorch", 64},
		{"bullet-hole-shotgun", "/home/fatbaby/PARENA/stdlib/shankpit/textures/bullet_hole_shotgun.prn",
			"shotgun pellet hole: tiny ragged pit with flaking, drawn many per blast", 64},
		{"bullet-hole-sniper", "/home/fatbaby/PARENA/stdlib/shankpit/textures/bullet_hole_sniper.prn",
			"sniper bullet hole: large core with wide spalled halo and long radiating cracks", 64},
		{"tyler-ecs-screen-glow", "/home/fatbaby/PARENA/stdlib/shankpit/textures/ecs_screen_glow.prn",
			"phosphor-green CRT terminal glow with scanlines and vignette, for an ECS-brand 1986 monitor prop", 256},
		// SHANKPIT's municipal district materials (2026-11-05): the six level materials the civic
		// buildings are made of. Named `shankpit-material-<level material name>` so a HOST can address
		// a material's art by the NAME that is in the level file instead of by a database id it would
		// have to be told about -- the level files carry `tile`/`plaster`/`marble`/`wood_oak`/..., and
		// nothing else. This is the id the NOCK library assigns to each one; the game/editor resolves
		// the name through the public by-name route (nock_textures_public.go's allowlist).
		{"shankpit-material-tile", parenaPath("stdlib/shankpit/textures/institutional_tile.prn"),
			"cold institutional wall/floor tile grid, the civic district's own floor surface", 256},
		{"shankpit-material-plaster", parenaPath("stdlib/shankpit/textures/courthouse_plaster.prn"),
			"troweled lime plaster over brick, warm bone paint, hand-mottled", 256},
		{"shankpit-material-marble", parenaPath("stdlib/shankpit/textures/courthouse_marble.prn"),
			"pale courthouse marble slab with a few dark wandering veins", 256},
		{"shankpit-material-wood-oak", parenaPath("stdlib/shankpit/textures/wood_oak.prn"),
			"quarter-sawn oak boards, tight rings, warm honey -- the city's heaviest wood (720 kg/m^3)", 256},
		{"shankpit-material-wood-pine", parenaPath("stdlib/shankpit/textures/wood_pine.prn"),
			"wide pale pine boards, soft rings, cheap partition timber (460 kg/m^3)", 256},
		{"shankpit-material-wood-mahogany", parenaPath("stdlib/shankpit/textures/wood_mahogany.prn"),
			"close-grained dark mahogany, red-brown going near-black in the seams (580 kg/m^3)", 256},
	}

	for _, t := range textures {
		// Idempotent: re-running the loader must not duplicate or fail on an existing name.
		if existing, err := ts.GetTextureByName(context.Background(), t.name); err == nil {
			fmt.Printf("exists  texture id=%d name=%s (skipped)\n", existing.ID, existing.Name)
			continue
		}
		src, err := os.ReadFile(t.path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "read", t.path, ":", err)
			os.Exit(1)
		}
		tex, err := ts.CreateProceduralTexture(context.Background(), t.name, string(src), t.prompt, t.size, t.size)
		if err != nil {
			fmt.Fprintln(os.Stderr, "CreateProceduralTexture", t.name, ":", err)
			fmt.Fprintln(os.Stderr, "--- source ---")
			fmt.Fprintln(os.Stderr, string(src))
			os.Exit(1)
		}
		fmt.Printf("created texture id=%d name=%s width=%d height=%d\n", tex.ID, tex.Name, tex.Width, tex.Height)
	}
}
