// admin_openexecutive.go — Back Office status + provisioning page for OpenExecutive (S506).
// Founder real-time: "is it ready? add to the IDUNA left menu." Answering that honestly is this
// page's real job: the IDUNA/Gemini integration code is merged to OpenExecutive's own main, fully
// tested and twice adversarially reviewed -- but nobody has ever run it live.
//
// CORRECTED (2026-09-21): this page originally claimed the remaining gap was "a real GCP project
// with Vertex AI enabled (a human-only Console step)" -- founder pushed back, correctly: this
// sandbox only checked `gcloud auth login`/ADC, not this monorepo's own established
// interim-secrets convention (EMILY/var/*.env). EINHORN_INDUSTRIAL already has a real, working
// Gemini Developer API key there (project einhorn-mjolnir) -- OpenExecutive's own
// GeminiVertexProvider now supports that credential mode too (no GCP Console work needed for it),
// and running the real provider code against the real key got a genuine, correctly-authenticated
// `402 RESOURCE_EXHAUSTED` (billing credits depleted), not an auth error -- see OpenExecutive's
// own NORTHSTAR.md §7 for the full account. The real remaining gap is smaller than originally
// stated: top up billing on that existing key, or provision a Vertex-mode GCP project instead.
// An actual M2M credential minted here is still needed before OpenExecutive can authenticate to
// IDUNA -- that's the provisioning form below, reusing openexecutive.go's own provisionCore so
// this and the JSON API can never drift apart on the actual provisioning logic.
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
<h2>Status: code-ready, not yet live</h2>
<p>The IDUNA auth + Google Gemini integration is merged to
<a href="https://github.com/emilyspringerton/OpenExecutive" target="_blank" rel="noopener noreferrer">OpenExecutive's own <code>main</code></a>
-- fully unit tested and reviewed twice by independent adversarial passes (a real
authorization-bypass finding and a JWKS staleness regression were both found and fixed before
merge). <strong>Nobody has actually run it yet</strong>, but the remaining gap is smaller than it
first looked: EINHORN_INDUSTRIAL already has a real, working Gemini Developer API key
(<code>EMILY/var/gemini-api-key.env</code>) -- OpenExecutive's provider was extended to use it (no
GCP Console work needed for that path), and a real call against it authenticated correctly
(a genuine <code>402</code> billing-credits-depleted response, not an auth error). Two real steps
remain:</p>
<ol>
<li>Top up billing credits on that existing key at <a href="https://ai.studio/projects" target="_blank" rel="noopener noreferrer">ai.studio/projects</a>
(or provision a separate Vertex AI GCP project instead, if that's preferred for this deployment).</li>
<li>An M2M credential provisioned here so OpenExecutive can authenticate to IDUNA as itself --
use the form below once you're ready for that.</li>
</ol>
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
