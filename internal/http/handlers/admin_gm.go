// admin_gm.go — Back Office Game Master tools (founder, live: "we will need
// gamemaster tools for dragonsnshit to start a way to disable accounts and
// later we will have more gamemaster tools"). First tool only, per the
// founder's own framing -- search a DragonsNShit account by email or
// character name, disable/enable it. players.disabled_at (migration
// 202608050001) is enforced at login in player_email_auth.go's handleLogin.
// More GM tools (kick a live session, reset a stuck character, ...) are
// real, separate follow-on work -- this file is written to make adding the
// next one a new handler + template + route, not a redesign.
package handlers

import (
	"database/sql"
	"net/http"
	"strings"
	"time"
)

type gmAccountRow struct {
	PlayerID      string
	DisplayName   string
	Email         string
	CharacterName string
	JobMain       string
	Disabled      bool
	DisabledAt    string
}

func (h *AdminHandler) gmSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	var rows []gmAccountRow
	var queryErr error
	if q != "" {
		rows, queryErr = h.gmLookup(r, q)
	}
	renderHTML(w, adminGMTmpl, map[string]any{
		"Title":   "Game Master Tools",
		"Query":   q,
		"Results": rows,
		"Error":   errString(queryErr),
	})
}

func (h *AdminHandler) gmLookup(r *http.Request, q string) ([]gmAccountRow, error) {
	like := "%" + q + "%"
	sqlRows, err := h.DB.QueryContext(r.Context(), `
		SELECT p.player_id, p.display_name, pc.email, p.disabled_at,
		       COALESCE(c.name, ''), COALESCE(c.job_main, '')
		FROM players p
		JOIN player_credentials pc ON pc.player_id = p.player_id
		LEFT JOIN characters c ON c.player_id = p.player_id
		WHERE pc.email LIKE ? OR c.name LIKE ?
		ORDER BY p.registered_at DESC
		LIMIT 50`, like, like)
	if err != nil {
		return nil, err
	}
	defer sqlRows.Close()
	var out []gmAccountRow
	for sqlRows.Next() {
		var row gmAccountRow
		var disabledAt sql.NullString
		if err := sqlRows.Scan(&row.PlayerID, &row.DisplayName, &row.Email, &disabledAt, &row.CharacterName, &row.JobMain); err != nil {
			return nil, err
		}
		row.Disabled = disabledAt.Valid
		if disabledAt.Valid {
			row.DisabledAt = disabledAt.String
		}
		out = append(out, row)
	}
	return out, sqlRows.Err()
}

// gmAccountAction handles POST /admin/gm/{player_id}/{disable|enable}
func (h *AdminHandler) gmAccountAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/admin/gm/"), "/")
	if len(parts) < 2 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	playerID, action := parts[0], parts[1]
	q := r.URL.Query().Get("q")

	var err error
	switch action {
	case "disable":
		_, err = h.DB.ExecContext(r.Context(), `UPDATE players SET disabled_at=? WHERE player_id=?`,
			time.Now().UTC().Format(time.RFC3339), playerID)
	case "enable":
		_, err = h.DB.ExecContext(r.Context(), `UPDATE players SET disabled_at=NULL WHERE player_id=?`, playerID)
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, "operation failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/gm?q="+strings.ReplaceAll(q, " ", "+"), http.StatusSeeOther)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

var adminGMTmpl = mustParseTmpl("gm", `
{{define "body"}}
<h1>Game Master Tools</h1>
<p class="meta">DragonsNShit account lookup — search by email or character name. First tool: disable/enable an account (blocks login, enforced server-side). More GM tools land here as they're built.</p>
<form method="GET" action="/admin/gm" style="margin:16px 0">
  <input type="text" name="q" value="{{.Query}}" placeholder="email or character name" autocomplete="off" style="width:280px">
  <input type="submit" value="Search">
</form>
{{if .Error}}<div class="err">{{.Error}}</div>{{end}}
{{if .Results}}
<table>
<tr><th>Character</th><th>Job</th><th>Email</th><th>Status</th><th>Actions</th></tr>
{{range .Results}}
<tr>
  <td>{{if .CharacterName}}{{.CharacterName}}{{else}}<span class="meta">(no character)</span>{{end}}</td>
  <td class="meta">{{.JobMain}}</td>
  <td>{{.Email}}</td>
  <td>{{if .Disabled}}<span class="badge badge-suspended">disabled {{.DisabledAt}}</span>{{else}}<span class="badge badge-active">active</span>{{end}}</td>
  <td>
    {{if .Disabled}}
    <form class="inline" method="POST" action="/admin/gm/{{.PlayerID}}/enable?q={{$.Query}}">
      <input type="submit" value="Enable">
    </form>
    {{else}}
    <form class="inline" method="POST" action="/admin/gm/{{.PlayerID}}/disable?q={{$.Query}}">
      <input type="submit" value="Disable" class="danger">
    </form>
    {{end}}
  </td>
</tr>
{{end}}
</table>
{{else if .Query}}<p class="empty">No accounts match "{{.Query}}".</p>{{end}}
{{end}}
`)
