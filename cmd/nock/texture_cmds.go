package main

// texture_cmds.go — CLI dispatch for the texture-library commands (see main.go's own
// textureCommands map). openTextureStore connects to the SAME real SQLite DB the running IDUNA
// server uses by default (NOCK_DB_PATH, defaulting to IDUNA's own var/iduna.db and migrations
// dir) -- a texture created via this CLI shows up in the web GUI, and vice versa, matching the
// founder's own "same shape CLI and GUI" framing applied to the new texture-manager primitives.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"iduna/internal/nock"
	"iduna/internal/store"
)

func openTextureStore() (*nock.TextureStore, error) {
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
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err := store.RunSQLiteMigrations(db, migrationsDir); err != nil {
		return nil, fmt.Errorf("run migrations: %w", err)
	}
	return &nock.TextureStore{DB: db}, nil
}

func dispatchTextureCmd(cmd string, store *nock.TextureStore, args []string) error {
	switch cmd {
	case "texture-list":
		return cmdTextureList(store, args)
	case "texture-create":
		return cmdTextureCreate(store, args)
	case "texture-generate":
		return cmdTextureGenerate(store, args)
	case "texture-get":
		return cmdTextureGet(store, args)
	case "texture-image":
		return cmdTextureImage(store, args)
	case "texture-rename":
		return cmdTextureRename(store, args)
	case "texture-delete":
		return cmdTextureDelete(store, args)
	case "texture-clone":
		return cmdTextureClone(store, args)
	case "texture-regenerate":
		return cmdTextureRegenerate(store, args)
	}
	return fmt.Errorf("unknown texture command %q", cmd)
}

func printTexture(t *nock.Texture) {
	// Never dump real PNG bytes to a terminal -- print everything else.
	out := struct {
		ID           int64  `json:"id"`
		Name         string `json:"name"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		ParenaSource string `json:"parena_source,omitempty"`
		Prompt       string `json:"prompt,omitempty"`
		CreatedAt    string `json:"created_at"`
		UpdatedAt    string `json:"updated_at"`
	}{t.ID, t.Name, t.Width, t.Height, t.ParenaSource, t.Prompt, t.CreatedAt, t.UpdatedAt}
	data, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(data))
}

func cmdTextureList(s *nock.TextureStore, args []string) error {
	list, err := s.ListTextures(context.Background())
	if err != nil {
		return err
	}
	data, _ := json.MarshalIndent(list, "", "  ")
	fmt.Println(string(data))
	return nil
}

func cmdTextureCreate(s *nock.TextureStore, args []string) error {
	fs := flag.NewFlagSet("texture-create", flag.ExitOnError)
	name := fs.String("name", "", "texture name")
	width := fs.Int("width", 0, "width")
	height := fs.Int("height", 0, "height")
	file := fs.String("file", "", "source PNG file")
	fs.Parse(args)
	pngData, err := os.ReadFile(*file)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}
	t, err := s.CreateTexture(context.Background(), *name, *width, *height, pngData, "", "")
	if err != nil {
		return err
	}
	printTexture(t)
	return nil
}

func cmdTextureGenerate(s *nock.TextureStore, args []string) error {
	fs := flag.NewFlagSet("texture-generate", flag.ExitOnError)
	name := fs.String("name", "", "texture name")
	prompt := fs.String("prompt", "", "text description of the desired texture")
	width := fs.Int("width", 256, "width")
	height := fs.Int("height", 256, "height")
	fs.Parse(args)

	token, err := nock.GcloudAccessToken()
	if err != nil {
		return fmt.Errorf("no Vertex AI credential available: %w", err)
	}
	src, err := nock.GenerateProceduralTextureSource(context.Background(), token, *prompt, *width, *height)
	if err != nil {
		return fmt.Errorf("vertex generation failed: %w", err)
	}
	t, err := s.CreateProceduralTexture(context.Background(), *name, src, *prompt, *width, *height)
	if err != nil {
		fmt.Fprintln(os.Stderr, "generated source failed validation/compile -- printing it below so you can fix it by hand:")
		fmt.Fprintln(os.Stderr, src)
		return err
	}
	printTexture(t)
	return nil
}

func cmdTextureGet(s *nock.TextureStore, args []string) error {
	fs := flag.NewFlagSet("texture-get", flag.ExitOnError)
	id := fs.Int64("id", 0, "texture id")
	fs.Parse(args)
	t, err := s.GetTexture(context.Background(), *id)
	if err != nil {
		return err
	}
	printTexture(t)
	return nil
}

func cmdTextureImage(s *nock.TextureStore, args []string) error {
	fs := flag.NewFlagSet("texture-image", flag.ExitOnError)
	id := fs.Int64("id", 0, "texture id")
	out := fs.String("out", "", "output PNG path")
	fs.Parse(args)
	t, err := s.GetTexture(context.Background(), *id)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, t.PNGData, 0o644); err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	fmt.Println(*out)
	return nil
}

func cmdTextureRename(s *nock.TextureStore, args []string) error {
	fs := flag.NewFlagSet("texture-rename", flag.ExitOnError)
	id := fs.Int64("id", 0, "texture id")
	name := fs.String("name", "", "new name")
	fs.Parse(args)
	t, err := s.RenameTexture(context.Background(), *id, *name)
	if err != nil {
		return err
	}
	printTexture(t)
	return nil
}

func cmdTextureDelete(s *nock.TextureStore, args []string) error {
	fs := flag.NewFlagSet("texture-delete", flag.ExitOnError)
	id := fs.Int64("id", 0, "texture id")
	fs.Parse(args)
	return s.DeleteTexture(context.Background(), *id)
}

func cmdTextureClone(s *nock.TextureStore, args []string) error {
	fs := flag.NewFlagSet("texture-clone", flag.ExitOnError)
	id := fs.Int64("id", 0, "source texture id")
	name := fs.String("name", "", "new, independent texture's name")
	fs.Parse(args)
	t, err := s.CloneTexture(context.Background(), *id, *name)
	if err != nil {
		return err
	}
	printTexture(t)
	return nil
}

func cmdTextureRegenerate(s *nock.TextureStore, args []string) error {
	fs := flag.NewFlagSet("texture-regenerate", flag.ExitOnError)
	id := fs.Int64("id", 0, "texture id")
	source := fs.String("source", "", "path to the edited .prn file")
	fs.Parse(args)
	src, err := os.ReadFile(*source)
	if err != nil {
		return fmt.Errorf("read source: %w", err)
	}
	t, err := s.RegenerateTexture(context.Background(), *id, string(src))
	if err != nil {
		return err
	}
	printTexture(t)
	return nil
}
