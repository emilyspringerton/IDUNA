// admin_openexecutive.go — Back Office status + provisioning page for OpenExecutive (S506).
// Founder real-time: "is it ready? add to the IDUNA left menu." Answering that honestly is this
// page's real job: the IDUNA/Gemini integration code is merged to OpenExecutive's own main
// (e890939), fully tested (3605+ unit tests) and twice adversarially reviewed -- but nobody has
// ever run it live. It needs a real GCP project with Vertex AI enabled (a human-only Console
// step, the same class of gap IDUNA's own Google OAuth devportal gate already names) and an
// actual M2M credential minted here before OpenExecutive can start. This page states that
// directly rather than implying it's already live, and provides the one real, currently missing
// admin affordance for the second half: a working form over the existing provision API
// (previously JSON-only, no Back Office UI), reusing openexecutive.go's own provisionCore so
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
<p>The IDUNA auth + Google Vertex AI (Gemini) integration is merged to
<a href="https://github.com/emilyspringerton/OpenExecutive" target="_blank" rel="noopener noreferrer">OpenExecutive's own <code>main</code></a>
(commit <code>e890939</code>) -- fully unit tested and reviewed twice by independent adversarial passes
(a real authorization-bypass finding and a JWKS staleness regression were both found and fixed
before merge). <strong>Nobody has actually run it yet.</strong> Two real, human-only steps remain
before it can go live, same class of gap as this Back Office's own Google OAuth devportal gate:</p>
<ol>
<li>A real GCP project with the Vertex AI API enabled and billing on (<code>gcloud auth login</code> +
console steps this sandbox genuinely cannot do on its own).</li>
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
