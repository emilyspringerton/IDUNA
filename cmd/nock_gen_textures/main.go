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
		{"tyler-ecs-screen-glow", "/home/fatbaby/PARENA/stdlib/shankpit/textures/ecs_screen_glow.prn",
			"phosphor-green CRT terminal glow with scanlines and vignette, for an ECS-brand 1986 monitor prop", 256},
	}

	for _, t := range textures {
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
