// admin_openexecutive.go — Back Office status + provisioning page for OpenExecutive (S506).
// Founder real-time: "is it ready? add to the IDUNA left menu." Answering that honestly is this
// page's real job.
//
// LIVE (2026-09-21): it's ready. A real chat request, through the full real stack -- a real
// IDUNA-issued M2M JWT (openexec-prod), IDUNAJWTValidator, the orchestrator, real specialist tool
// schemas, GeminiVertexProvider, real Gemini -- returned a real, correct response end to end for
// the first time, verified against the real public https://okemily.com/.well-known/jwks.json (no
// bypasses). Getting there found and fixed three more real bugs invisible to every existing unit
// test, because none of them exercised the real, full path: this repo's own public JWKS endpoint
// had a genuine 404 (nginx never proxied it -- fixed, sudo-queue/86, this comment's own prior
// version named it as still-queued, now applied and confirmed live); OpenExecutive's own JWKS
// error-wrapping had a bug masking that 404 behind a confusing, unrelated TypeError; and Gemini's
// own tool-schema translation had two more real bugs (a Pydantic-style nullable-type crash, and a
// wire-serialization quirk in the installed SDK's own additionalProperties handling). Full account
// in OpenExecutive's own NORTHSTAR.md §7-8 and commit 40ae9ed.
//
// The one thing that actually needed a human: the original Gemini key's account never got its
// billing resolved (AI Studio's own prepayment credits, a genuinely different system than regular
// GCP Cloud Billing -- the founder's own Cloud Console click didn't touch it). Founder supplied a
// different account's key instead, live-verified working. That account's free tier has zero quota
// for the `pro` model specifically, so the default model is `gemini-flash-latest` for now, not the
// original `gemini-3.1-pro-preview` -- upgrading past free tier would lift that, not attempted here.
package handlers

import (
	"net/http"
	"strings"

	"iduna/internal/http/middleware"
)

func (h *AdminHandler) openExecutiveStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	renderHTML(w, adminOpenExecutiveTmpl, map[string]any{})
}

func (h *AdminHandler) openExecutiveProvision(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	var permissions []string
	for _, p := range strings.Split(r.FormValue("permissions"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			permissions = append(permissions, p)
		}
	}

	operator := middleware.SubjectFromContext(r.Context())
	resp, provErr := h.OpenExecutive.provisionCore(r.Context(), operator, name, permissions)
	if provErr != nil {
		renderHTML(w, adminOpenExecutiveTmpl, map[string]any{"Error": provErr.publicMessage})
		return
	}
	renderHTML(w, adminOpenExecutiveTmpl, map[string]any{"Provisioned": resp})
}

var adminOpenExecutiveTmpl = mustParseTmpl("openexecutive", `
{{define "body"}}
<h1>OpenExecutive</h1>
<div class="section-card">
<h2>Status: live</h2>
<p><strong>A real chat request has gone through the full stack end to end and returned a real,
correct response.</strong> The IDUNA auth + Google Gemini integration is merged to
<a href="https://github.com/emilyspringerton/OpenExecutive" target="_blank" rel="noopener noreferrer">OpenExecutive's own <code>main</code></a>
-- fully unit tested (3617 tests) and reviewed twice by independent adversarial passes (a real
authorization-bypass finding and a JWKS staleness regression were both found and fixed before
merge). Verified live, not simulated: a real IDUNA-issued M2M JWT (<code>openexec-prod</code>)
validated against this instance's own real public
<a href="https://okemily.com/.well-known/jwks.json" target="_blank" rel="noopener noreferrer">JWKS endpoint</a>,
routed through real specialist tool schemas, reached Gemini, and got a real answer back.</p>
<p>What it took to get there, since none of it was caught by unit tests alone: this instance's own
JWKS endpoint had a real 404 (nginx never proxied it -- fixed), OpenExecutive's own JWKS
error-handling had a bug masking that 404 behind a confusing unrelated error, and Gemini's tool-schema
translation had two more real bugs only visible with real specialist tool schemas. Full account in
OpenExecutive's own <code>NORTHSTAR.md</code> §7-8.</p>
<p class="meta">Currently running on <code>gemini-flash-latest</code> (the active key's account has
zero free-tier quota for the <code>pro</code> model specifically) via a key supplied directly by the
founder after the original account's AI Studio prepayment credits ran out with no way to top them up
from here.</p>
</div>

{{if .Error}}<div class="err section-card">{{.Error}}</div>{{end}}

{{if .Provisioned}}
<div class="section-card">
<h2>Provisioned: {{.Provisioned.AgentName}}</h2>
<p class="meta">agent_id: {{.Provisioned.AgentID}}</p>
<p><strong>Secret (shown once, never retrievable again):</strong></p>
<pre>{{.Provisioned.Secret}}</pre>
<p>Set as <code>IDUNA_AGENT_NAME={{.Provisioned.AgentName}}</code> and
<code>IDUNA_AGENT_SECRET=&lt;the secret above&gt;</code> in OpenExecutive's own environment. It will
validate against <code>{{.Provisioned.JWKSUrl}}</code>.</p>
</div>
{{end}}

<div class="section-card">
<h2>Provision a new M2M credential</h2>
<form method="POST" action="/admin/openexecutive/provision">
  <p><label>Agent name<br><input type="text" name="name" placeholder="openexec-prod" autocomplete="off" style="width:280px" required></label></p>
  <p><label>Permissions (comma-separated)<br><input type="text" name="permissions" placeholder="openexec.read, openexec.admin" autocomplete="off" style="width:400px" value="openexec.read"></label></p>
  <input type="submit" value="Provision">
</form>
</div>
{{end}}
`)
