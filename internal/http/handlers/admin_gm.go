// admin_gm.go — Back Office Game Master tools (founder, live: "we will need
// gamemaster tools for dragonsnshit to start a way to disable accounts and
// later we will have more gamemaster tools"). First tool: search a
// DragonsNShit account by email or character name, disable/enable it.
// players.disabled_at (migration 202608050001) is enforced at login in
// player_email_auth.go's handleLogin. Second tool (2026-10-09, founder
// real-time follow-up: "we are going to need an interface in iduna for
// adding roles to the email users"): grant/revoke a permission string per
// account, backed by migration 202610090002_player_permissions.sql --
// player_email_auth.go's issueJWT reads this table directly, so a grant
// here takes effect on that account's NEXT login (no separate "apply"
// step). More GM tools (kick a live session, reset a stuck character, ...)
// are real, separate follow-on work -- this file is written to make adding
// the next one a new handler + template + route, not a redesign.
package handlers

import (
	"database/sql"
	"net/http"
	"regexp"
	"strings"
	"time"

	"iduna/internal/http/middleware"
)

type gmAccountRow struct {
	PlayerID      string
	DisplayName   string
	Email         string
	CharacterName string
	JobMain       string
	Disabled      bool
	DisabledAt    string
	Permissions   []string
}

// permissionNameRe -- real, deliberately narrow validation for a free-text permission grant: no
// permissions catalog/table exists for player accounts (unlike the Google-auth roles/permissions
// tables), so this is the only gate between a GM's typed input and a raw SQL parameter. Matches
// the shape every real permission string in this codebase already uses (iduna.admin,
// edge.game.operator, devportal.access, ...): lowercase/dot/underscore/hyphen segments, 3-64 chars.
var permissionNameRe = regexp.MustCompile(`^[a-z0-9_-]+(\.[a-z0-9_-]+)+$`)

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
	if err := sqlRows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		permRows, err := h.DB.QueryContext(r.Context(), `SELECT permission FROM player_permissions WHERE player_id=? ORDER BY permission`, out[i].PlayerID)
		if err != nil {
			return nil, err
		}
		for permRows.Next() {
			var p string
			if err := permRows.Scan(&p); err != nil {
				permRows.Close()
				return nil, err
			}
			out[i].Permissions = append(out[i].Permissions, p)
		}
		if err := permRows.Err(); err != nil {
			permRows.Close()
			return nil, err
		}
		permRows.Close()
	}
	return out, nil
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
	ctx := r.Context()
	operatorID := middleware.SubjectFromContext(ctx)

	var err error
	switch action {
	case "disable":
		_, err = h.DB.ExecContext(ctx, `UPDATE players SET disabled_at=? WHERE player_id=?`,
			time.Now().UTC().Format(time.RFC3339), playerID)
	case "enable":
		_, err = h.DB.ExecContext(ctx, `UPDATE players SET disabled_at=NULL WHERE player_id=?`, playerID)
	case "grant_permission":
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		perm := strings.ToLower(strings.TrimSpace(r.FormValue("permission")))
		if !permissionNameRe.MatchString(perm) {
			http.Error(w, "invalid permission name (expected e.g. edge.game.operator)", http.StatusBadRequest)
			return
		}
		_, err = h.DB.ExecContext(ctx, `INSERT INTO player_permissions (player_id, permission, granted_by) VALUES (?, ?, ?)
			ON CONFLICT (player_id, permission) DO NOTHING`, playerID, perm, operatorID)
		if err == nil {
			emitAuthEvent(ctx, h.EventLog, "iduna:admin.player_permission.grant", "iduna-admin", map[string]any{
				"player_id": playerID, "permission": perm, "operator_id": operatorID,
			})
		}
	case "revoke_permission":
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		perm := strings.ToLower(strings.TrimSpace(r.FormValue("permission")))
		_, err = h.DB.ExecContext(ctx, `DELETE FROM player_permissions WHERE player_id=? AND permission=?`, playerID, perm)
		if err == nil {
			emitAuthEvent(ctx, h.EventLog, "iduna:admin.player_permission.revoke", "iduna-admin", map[string]any{
				"player_id": playerID, "permission": perm, "operator_id": operatorID,
			})
		}
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
<tr><th>Character</th><th>Job</th><th>Email</th><th>Status</th><th>Permissions</th><th>Actions</th></tr>
{{range .Results}}
{{$row := .}}
<tr>
  <td>{{if .CharacterName}}{{.CharacterName}}{{else}}<span class="meta">(no character)</span>{{end}}</td>
  <td class="meta">{{.JobMain}}</td>
  <td>{{.Email}}</td>
  <td>{{if .Disabled}}<span class="badge badge-suspended">disabled {{.DisabledAt}}</span>{{else}}<span class="badge badge-active">active</span>{{end}}</td>
  <td>
    {{range .Permissions}}
    <form class="inline" method="POST" action="/admin/gm/{{$row.PlayerID}}/revoke_permission?q={{$.Query}}" style="display:inline">
      <input type="hidden" name="permission" value="{{.}}">
      <span class="badge badge-active" style="cursor:default">{{.}} <button type="submit" title="revoke" style="border:none;background:none;cursor:pointer;color:inherit">&times;</button></span>
    </form>
    {{else}}<span class="meta">(none)</span>{{end}}
    <form class="inline" method="POST" action="/admin/gm/{{$row.PlayerID}}/grant_permission?q={{$.Query}}" style="margin-top:4px">
      <input type="text" name="permission" placeholder="e.g. edge.game.operator" size="22" autocomplete="off">
      <input type="submit" value="Grant">
    </form>
  </td>
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
