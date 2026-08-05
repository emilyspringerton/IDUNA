// admin_dragonsnshit.go — Back Office "create DragonsNShit account" link
// (founder, live: "maybe give me some kind of dashboard with a link to
// create dragonsnshit accounts"). Same real endpoint emily.cli's own `emily
// iduna create-account` already wraps (POST /api/v1/auth/email/register
// with character_name set, player_email_auth.go) -- calls it as a real
// same-box HTTP request rather than reimplementing the register/bcrypt/
// character-insert logic a second time in this handler.
package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"
)

func idunaBaseURL() string {
	if v := os.Getenv("BASE_URL"); v != "" {
		return v
	}
	return "http://localhost:8080"
}

func (h *AdminHandler) dragonsnshitCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		renderHTML(w, adminDragonsnshitCreateTmpl, map[string]any{"Title": "Create DragonsNShit Account"})
		return
	}
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("character_name"))
	job := strings.ToUpper(strings.TrimSpace(r.FormValue("character_job")))
	if job == "" {
		job = "WAR"
	}
	if name == "" {
		renderHTML(w, adminDragonsnshitCreateTmpl, map[string]any{
			"Title": "Create DragonsNShit Account",
			"Error": "Character name is required.",
		})
		return
	}

	email := "test-" + strings.ToLower(name) + "-" + randHexLocal(4) + "@dragonsnshit.test"
	password := randHexLocal(10)

	body, _ := json.Marshal(map[string]string{
		"email": email, "password": password,
		"display_name": name, "character_name": name, "character_job": job,
	})
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(idunaBaseURL()+"/api/v1/auth/email/register", "application/json", strings.NewReader(string(body)))
	if err != nil {
		renderHTML(w, adminDragonsnshitCreateTmpl, map[string]any{
			"Title": "Create DragonsNShit Account",
			"Error": "Request to the register endpoint failed: " + err.Error(),
		})
		return
	}
	defer resp.Body.Close()
	var out map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != http.StatusOK {
		msg := out["message"]
		if msg == "" {
			msg = resp.Status
		}
		renderHTML(w, adminDragonsnshitCreateTmpl, map[string]any{
			"Title": "Create DragonsNShit Account",
			"Error": "IDUNA returned " + resp.Status + ": " + msg,
		})
		return
	}

	renderHTML(w, adminDragonsnshitCreatedTmpl, map[string]any{
		"Title":         "DragonsNShit Account Created",
		"CharacterID":   out["character_id"],
		"PlayerID":      out["player_id"],
		"CharacterName": name,
		"Job":           job,
		"Email":         email,
		"Password":      password,
	})
}

func randHexLocal(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

var adminDragonsnshitCreateTmpl = mustParseTmpl("dragonsnshit-create", `
{{define "body"}}
<h1>Create DragonsNShit Account</h1>
<p class="meta">
  Mints a real IDUNA player + login credential + DragonsNShit character in one request
  (same endpoint <code>emily iduna create-account</code> wraps). Log in with the printed
  email/password at battlegrounds_gui's own login screen or apps2/mud.
</p>
{{if .Error}}<div class="err">{{.Error}}</div>{{end}}
<div class="section-card">
<form method="POST" action="/admin/dragonsnshit/create">
  <label for="cn">Character Name</label>
  <input type="text" id="cn" name="character_name" placeholder="e.g. LASVEGASEMILY89" autocomplete="off" required>
  <label for="cj">Job (default WAR)</label>
  <input type="text" id="cj" name="character_job" placeholder="WAR / BLM / WHM / RDM / THF / MNK / SMN" autocomplete="off">
  <input type="submit" value="Create Account">
</form>
</div>
{{end}}
`)

var adminDragonsnshitCreatedTmpl = mustParseTmpl("dragonsnshit-created", `
{{define "body"}}
<h1>Account Created</h1>
<div class="section-card">
  <table>
    <tr><th>Character</th><td>{{.CharacterName}} (job {{.Job}})</td></tr>
    <tr><th>character_id</th><td class="meta">{{.CharacterID}}</td></tr>
    <tr><th>player_id</th><td class="meta">{{.PlayerID}}</td></tr>
    <tr><th>Email</th><td>{{.Email}}</td></tr>
    <tr><th>Password</th><td><code>{{.Password}}</code></td></tr>
  </table>
  <p class="meta" style="margin-top:12px">
    This password is shown once. IDUNA only stores its hash — write it down now if you need it again.
  </p>
  <a href="/admin/dragonsnshit/create">→ Create another</a>
</div>
{{end}}
`)
