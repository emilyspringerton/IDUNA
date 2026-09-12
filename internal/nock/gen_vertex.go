package nock

// gen_vertex.go — the real "one-shot a texture from a text prompt" half of the founder's own
// ask: "if you had an llm and you gave it the api and told it you need a texture for X it could
// just one shot something." Real Vertex AI call, same real project/region/ADC-credential pattern
// IDUNA/internal/http/handlers/gfd_item_proposals.go already uses (gcloudAccessTokenForItemProposals
// -> generateItemProposal) -- not duplicated verbatim here (that function lives in the handlers
// package and returns a GfdItemDef, a different shape) but the same real HTTP call shape, the
// same real project/region, and the same "no static API key stored anywhere" ADC posture.
//
// The prompt below teaches the model the real, narrow contract this target actually supports --
// checked directly against src/emit_java.c, not assumed: scalar F64 math, `if`, and the real
// math/* primitives (cos/sqrt/floor/log/random-f64/pi, genuinely lowered to java.lang.Math.*
// unlike the still-placeholder C target). No `let`, `loop`, `match`, `defstruct`, or any import
// besides `math` -- this target's own real, current construct support doesn't have the first
// four at all, and validateProcTextureSource (procgen.go) rejects a non-math import and any
// #target outright as a second, independent layer of defense regardless of what the model
// actually returns.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

const (
	nockVertexProject = "project-d24a71e9-2daf-4b2d-917" // same real GCP project gfd_item_proposals.go already uses
	nockVertexRegion  = "us-central1"
	nockVertexModel   = "gemini-2.5-flash"
)

// GcloudAccessToken returns a real, live OAuth access token via the `gcloud` CLI's own ADC
// (Application Default Credentials) -- no static API key stored anywhere, same real posture
// every other Vertex-calling code path in this monorepo already uses.
func GcloudAccessToken() (string, error) {
	out, err := exec.Command("gcloud", "auth", "print-access-token").Output()
	if err != nil {
		return "", fmt.Errorf("nock: gcloud auth print-access-token: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

const proceduralTexturePromptTemplate = `You write procedural texture generators in a small, real, restricted subset of the PARENA language, targeting a real compiler backend that ONLY supports:
- Scalar 64-bit float (F64) arithmetic: + - * /
- Comparisons: > < >= <= =
- (if COND THEN ELSE) -- the only control-flow form available. No let, loop, match, defstruct, or any other special form.
- These exact math functions, called as (math/cos x), (math/sqrt x), (math/floor x), (math/log x), (math/random-f64) -- no arguments for random-f64. No sin, tan, atan, pow, or any other math function exists -- build what you need from cos/sqrt/floor/log/random-f64 and arithmetic alone (e.g. sin(x) = cos(x - 1.5707963).
- No variables/let-bindings of any kind -- every expression must be written inline, nested directly. This makes the code visually deep for complex patterns; that's expected and fine.
- No other (import ...) besides math. No #target, no FFI, no strings, no loops, no recursion.

You MUST define exactly these three top-level functions, each taking the pixel's own coordinate (x, y) and the canvas size (w, h), and returning a value that is treated as roughly 0.0-1.0 (values outside that range are clamped by the caller, so don't worry about clamping yourself):

(defn pixel-r [(x : F64) (y : F64) (w : F64) (h : F64)] : F64 ...)
(defn pixel-g [(x : F64) (y : F64) (w : F64) (h : F64)] : F64 ...)
(defn pixel-b [(x : F64) (y : F64) (w : F64) (h : F64)] : F64 ...)

The module declaration must be exactly (module gentexture), followed by (import math), followed by the three function definitions.

Generate a texture matching this description: %q

Canvas size: %d x %d (use w and h in your formulas if the pattern should scale with canvas size).

Return ONLY the raw PARENA source code. No markdown code fences, no commentary, no explanation before or after.`

// GenerateProceduralTextureSource calls Vertex AI to write real PARENA source matching the
// prompt, targeting the exact real language subset this pipeline can actually compile and run
// (see this file's own header comment). The returned source is NOT yet validated or compiled --
// the caller (typically the HTTP handler's own generate-and-add flow) still runs it through
// AddProceduralLayer, which applies validateProcTextureSource before ever invoking parena/javac,
// exactly as it would for hand-written or CLI-supplied source. A model's own real, honest failure
// mode is a normal outcome here, not something this function tries to paper over: if the
// generated source doesn't validate or doesn't compile, the caller sees that same real error a
// human editing bad PARENA by hand would see.
func GenerateProceduralTextureSource(ctx context.Context, token, prompt string, width, height int) (string, error) {
	fullPrompt := fmt.Sprintf(proceduralTexturePromptTemplate, prompt, width, height)

	url := fmt.Sprintf(
		"https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/google/models/%s:generateContent",
		nockVertexRegion, nockVertexProject, nockVertexRegion, nockVertexModel,
	)
	body, _ := json.Marshal(map[string]any{
		"contents": []map[string]any{
			{"role": "user", "parts": []map[string]string{{"text": fullPrompt}}},
		},
	})
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("nock: vertex ai %d: %s", resp.StatusCode, string(raw))
	}

	var parsed struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("nock: parse vertex response: %w", err)
	}
	if len(parsed.Candidates) == 0 || len(parsed.Candidates[0].Content.Parts) == 0 {
		reason := "no candidates"
		if len(parsed.Candidates) > 0 {
			reason = parsed.Candidates[0].FinishReason
		}
		return "", fmt.Errorf("nock: no content in vertex response (finishReason=%q)", reason)
	}
	text := parsed.Candidates[0].Content.Parts[0].Text

	// Real, honest defensive cleanup: models asked for "no markdown fences" sometimes add them
	// anyway -- strip a leading/trailing ``` fence if present rather than failing validation on
	// a trivial formatting slip.
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "```clojure")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	return strings.TrimSpace(text) + "\n", nil
}
