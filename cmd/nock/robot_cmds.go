package main

// robot_cmds.go — CLI for NOCK's robot registry (2026-09-27, founder real-time: "build in the
// affordances that slurp it into the database"). Same DB the running IDUNA uses (NOCK_DB_PATH /
// NOCK_MIGRATIONS_DIR, see texture_cmds.go) and the same nock.RobotStore.SyncFromGit the server's
// startup hook and the NOCK "Sync from git" button call.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"iduna/internal/nock"
	"iduna/internal/store"
)

var robotCommands = map[string]bool{"robots-sync": true, "robots-list": true}

func openRobotStore() (*nock.RobotStore, error) {
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
	return &nock.RobotStore{DB: db}, nil
}

func dispatchRobotCmd(cmd string, st *nock.RobotStore, args []string) error {
	ctx := context.Background()
	switch cmd {
	case "robots-sync":
		fs := flag.NewFlagSet(cmd, flag.ExitOnError)
		def := os.Getenv("NOCK_ROBOTS_GIT_DIR")
		if def == "" || def == "off" {
			def = "/home/fatbaby/GOLDENBAND"
		}
		dir := fs.String("dir", def, "GOLDENBAND working tree (robots/*.grobot.json + assets/robots/)")
		fs.Parse(args)
		rep, err := st.SyncFromGit(ctx, *dir)
		if err != nil {
			return err
		}
		fmt.Printf("synced robots from %s @ %s\n", rep.Dir, rep.Revision)
		failed := false
		for _, it := range rep.Items {
			fmt.Printf("  %-10s %-9s %s\n", it.Name, it.Action, it.Detail)
			failed = failed || it.Action == "error"
		}
		for _, n := range rep.NotInGit {
			fmt.Printf("  %-10s no longer in git (left in the registry)\n", n)
		}
		if failed {
			return fmt.Errorf("one or more robots failed to import")
		}
		return nil
	case "robots-list":
		list, err := st.ListRobots(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(list)
	}
	return fmt.Errorf("unknown robot command %q", cmd)
}
