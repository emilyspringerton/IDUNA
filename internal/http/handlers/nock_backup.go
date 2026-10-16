package handlers

// nock_backup.go -- GET /admin/nock/api/backup: one zip with a consistent snapshot of the whole IDUNA
// SQLite database (iduna.db, via VACUUM INTO, so it is safe while the server is live) plus a readable
// JSON dump of every NOCK / SHANKPIT / BRAWLPIT content table (levels, widgets, materials, cube styles,
// sprays, textures, door scripts, sounds, animations ...). Admin-only (iduna.admin): iduna.db also holds
// users, hashed credentials and the Apples ledger.
//
// Not included: files on disk outside the DB (NOCK video clips under NOCK_VIDEO_DIR), the separate
// vault.db / mailinglist.db files, and the RL checkpoint blob tables (git-lfs synced already).

import (
	"archive/zip"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

type NockBackupHandler struct {
	DB *sql.DB
	// SQLite is false when the server runs on MySQL: the snapshot and table dumps are then unavailable
	// (use mysqldump) and the zip says so.
	SQLite bool
}

var backupTableSkip = map[string]bool{"shankpit_rl_checkpoints": true, "brawlpit_rl_checkpoints": true}

func backupTableWanted(n string) bool {
	if backupTableSkip[n] {
		return false
	}
	return strings.HasPrefix(n, "nock_") || strings.HasPrefix(n, "shankpit_") || strings.HasPrefix(n, "brawlpit_")
}

func (h *NockBackupHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	if r.URL.Query().Get("list") == "1" {
		h.listSources(w, r)
		return
	}
	// ?sources=database,shankpit_levels,... picks what goes in the archive; absent = everything.
	want := map[string]bool{}
	for _, x := range strings.Split(r.URL.Query().Get("sources"), ",") {
		if x = strings.TrimSpace(x); x != "" {
			want[x] = true
		}
	}
	all := len(want) == 0
	stamp := time.Now().UTC().Format("20060102-150405")
	notes := []string{"IDUNA / NOCK backup " + stamp}
	var snap string
	if h.SQLite && (all || want["database"]) {
		dir, err := os.MkdirTemp("", "iduna-backup-")
		if err != nil {
			mmoWriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer os.RemoveAll(dir)
		snap = filepath.Join(dir, "iduna.db")
		if _, err := h.DB.ExecContext(r.Context(), "VACUUM INTO ?", snap); err != nil {
			mmoWriteError(w, http.StatusInternalServerError, "snapshot failed: "+err.Error())
			return
		}
		notes = append(notes, "iduna.db: full SQLite snapshot (restore: stop IDUNA, replace var/iduna.db, start).")
	} else if !h.SQLite {
		notes = append(notes, "MySQL mode: no snapshot or table dumps here -- use mysqldump.")
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="iduna-nock-backup-%s.zip"`, stamp))
	zw := zip.NewWriter(w)
	defer zw.Close()
	if snap != "" {
		if f, err := os.Open(snap); err == nil {
			if zf, err := zw.Create("iduna.db"); err == nil {
				_, _ = io.Copy(zf, f)
			}
			f.Close()
		}
	}
	if h.SQLite {
		tables, counts := h.dumpTables(r, zw, want, all)
		notes = append(notes, "nock/*.json: readable per-table dumps ("+strings.Join(tables, ", ")+").")
		for _, t := range tables {
			notes = append(notes, fmt.Sprintf("  %s: %d rows", t, counts[t]))
		}
	}
	notes = append(notes, "Not included: NOCK video files on disk, vault.db, mailinglist.db, RL checkpoint blobs.")
	if zf, err := zw.Create("README.txt"); err == nil {
		_, _ = io.WriteString(zf, strings.Join(notes, "\n")+"\n")
	}
}

func (h *NockBackupHandler) dumpTables(r *http.Request, zw *zip.Writer, want map[string]bool, all bool) ([]string, map[string]int) {
	counts := map[string]int{}
	rows, err := h.DB.QueryContext(r.Context(), `SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`)
	if err != nil {
		return nil, counts
	}
	var names []string
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil && backupTableWanted(n) && (all || want[n]) {
			names = append(names, n)
		}
	}
	rows.Close()
	var done []string
	for _, n := range names {
		data, count, err := h.dumpTable(r, n)
		if err != nil {
			continue
		}
		if zf, err := zw.Create("nock/" + n + ".json"); err == nil {
			_, _ = zf.Write(data)
			done = append(done, n)
			counts[n] = count
		}
	}
	return done, counts
}

func (h *NockBackupHandler) dumpTable(r *http.Request, table string) ([]byte, int, error) {
	// table comes from sqlite_master and passed backupTableWanted (a fixed prefix + identifier), never user input.
	rows, err := h.DB.QueryContext(r.Context(), `SELECT * FROM "`+strings.ReplaceAll(table, `"`, ``)+`"`)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	out := []map[string]any{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, 0, err
		}
		m := make(map[string]any, len(cols))
		for i, c := range cols {
			if b, ok := vals[i].([]byte); ok && utf8.Valid(b) {
				m[c] = string(b)
			} else {
				m[c] = vals[i] // []byte that is not UTF-8 marshals as base64
			}
		}
		out = append(out, m)
	}
	data, err := json.MarshalIndent(out, "", " ")
	return data, len(out), err
}

// listSources feeds the NOCK export dialog's checkboxes: the full database plus one entry per content table.
func (h *NockBackupHandler) listSources(w http.ResponseWriter, r *http.Request) {
	type src struct {
		ID    string `json:"id"`
		Label string `json:"label"`
		Group string `json:"group"`
		Rows  int    `json:"rows"`
	}
	out := []src{}
	if !h.SQLite {
		writeJSON(w, http.StatusOK, out)
		return
	}
	out = append(out, src{ID: "database", Label: "Full database snapshot (iduna.db: everything, incl. users and Apples)", Group: "Database", Rows: -1})
	rows, err := h.DB.QueryContext(r.Context(), `SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`)
	if err != nil {
		writeJSON(w, http.StatusOK, out)
		return
	}
	var names []string
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil && backupTableWanted(n) {
			names = append(names, n)
		}
	}
	rows.Close()
	for _, n := range names {
		var c int
		_ = h.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM "`+n+`"`).Scan(&c)
		g := "NOCK"
		if strings.HasPrefix(n, "shankpit_") {
			g = "SHANKPIT"
		} else if strings.HasPrefix(n, "brawlpit_") {
			g = "BRAWLPIT"
		}
		out = append(out, src{ID: n, Label: n, Group: g, Rows: c})
	}
	writeJSON(w, http.StatusOK, out)
}
