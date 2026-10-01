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

	"iduna/internal/nock"
	"iduna/internal/store"
)

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
