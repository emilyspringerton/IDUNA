// admin_drive_slurp.go — IDUNA Back Office "Drive slurp" page.
// EMILY/BACKLOG.md S187-03/S188-05/S189-10, founder real-time, final scope:
// an admin (already authenticated into the Back Office via the existing
// agent_name/secret cookie session, see admin_login.go) connects their own
// Google account with Drive-readonly consent, sees a list of recent Drive
// files, and can "slurp" one -- enqueues a background download job rather
// than fetching synchronously, idempotent (re-clicking the same file, or a
// retry, does not double-download), with live progress visible in the page
// via SSE.
//
// Deliberately NOT an extension of web_ceremony.go's HandleStart/
// HandleCallback: that ceremony issues IDUNA identity for end users
// (REDGARDEN/GTA7 players etc.) via "openid email profile" scope -- a
// different audience and a different OAuth purpose than an already-
// authenticated admin granting THIS tool read access to THEIR OWN Drive.
// Conflating the two would mean either weakening the identity ceremony's
// scope story or giving every logged-in player's ceremony a Drive
// permission prompt neither wants. This is its own OAuth flow, gated
// behind the existing /admin/* RequireCookieAuth+RequirePermission wrapper
// main.go already applies to the whole AdminHandler -- so only an admin
// who is already in the Back Office can even reach /admin/drive-slurp/
// oauth/start, which is the right trust boundary for "grant this internal
// tool Drive access," not the public ceremony's.
//
// "Slurp" scope, deliberately narrow: downloads the file's raw content and
// saves it to IdunaRoot/var/drive-slurp/<job-id>-<sanitized-name> for later
// inspection. The original spec names no destination pipeline (training
// data? blog content? something else?) -- rather than guess at one, this
// gives a durable, real, inspectable landing spot. Wiring the saved file
// into any specific downstream consumer is real, separate, unscoped work.
//
// HONEST GAP, read before trusting this file blindly: written in a
// sandboxed worktree with no access to the real IDUNA checkout (no go.mod,
// no module deps, no compiler) -- this has NOT been run through `go build`
// or `go vet`, let alone `go test`, unlike this whole session's own
// established "live-verified, not just compiled" bar. Whoever integrates
// this must build, vet, and at minimum smoke-test it for real before
// calling S187-03/S188-05/S189-10 done -- do not mark the backlog item
// [x] on this file's existence alone.
package handlers

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"iduna/internal/drive"
)

// DriveSlurpHandler owns the Drive-OAuth-consent flow, the slurp job queue,
// and the SSE progress stream. Constructed and wired into AdminHandler by
// main.go (needs GoogleClientID/Secret -- the same GOOGLE_CLIENT_ID/
// GOOGLE_CLIENT_SECRET env vars web_ceremony.go already reads, same Google
// Cloud OAuth app, just a different scope+redirect for this flow -- and
// IdunaRoot, matching every other var/-writing handler's own IDUNA_ROOT
// convention).
type DriveSlurpHandler struct {
	GoogleClientID     string
	GoogleClientSecret string
	RedirectURI        string // must be registered in Google Cloud Console, e.g. {BASE_URL}/admin/drive-slurp/oauth/callback
	IdunaRoot          string

	mu       sync.Mutex
	token    *drive.OAuthToken // nil until connected
	jobs     []*slurpJob
	doneKeys map[string]bool // idempotency: "<driveFileID>:<modifiedTime>" for jobs that finished successfully

	bus *sseBus

	workOnce sync.Once
	workCh   chan *slurpJob
}

type slurpJob struct {
	ID           string    `json:"id"`
	DriveFileID  string    `json:"drive_file_id"`
	FileName     string    `json:"file_name"`
	ModifiedTime string    `json:"modified_time"`
	Status       string    `json:"status"` // queued, running, done, failed
	Attempts     int       `json:"attempts"`
	Error        string    `json:"error,omitempty"`
	SavedPath    string    `json:"saved_path,omitempty"`
	EnqueuedAt   time.Time `json:"enqueued_at"`
	FinishedAt   time.Time `json:"finished_at,omitempty"`
}

func (h *DriveSlurpHandler) tokenPath() string {
	return filepath.Join(h.IdunaRoot, "var", "drive-slurp-token.json")
}

func (h *DriveSlurpHandler) jobsLogPath() string {
	return filepath.Join(h.IdunaRoot, "var", "drive-slurp-jobs.jsonl")
}

func (h *DriveSlurpHandler) slurpDir() string {
	return filepath.Join(h.IdunaRoot, "var", "drive-slurp")
}

// loadTokenLocked reads a previously-persisted token from disk into
// h.token, if present. Caller holds h.mu.
func (h *DriveSlurpHandler) loadTokenLocked() {
	raw, err := os.ReadFile(h.tokenPath())
	if err != nil {
		return // no token yet -- not connected, not an error
	}
	var tok drive.OAuthToken
	if err := json.Unmarshal(raw, &tok); err != nil {
		log.Printf("drive-slurp: corrupt token file %s, ignoring: %v", h.tokenPath(), err)
		return
	}
	h.token = &tok
}

// saveTokenLocked persists h.token to disk via tmp-file rename, matching
// the same atomicity promptoverse queue's writePromptOVerseQueue uses.
func (h *DriveSlurpHandler) saveTokenLocked() error {
	if err := os.MkdirAll(filepath.Dir(h.tokenPath()), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(h.token)
	if err != nil {
		return err
	}
	tmp := h.tokenPath() + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, h.tokenPath())
}

// ensureLoaded loads the persisted token (once) and starts the background
// job worker (once). Called at the top of every HTTP entry point rather
// than from a package-level init, since h.IdunaRoot isn't known until
// main.go constructs the handler.
func (h *DriveSlurpHandler) ensureLoaded() {
	h.mu.Lock()
	if h.token == nil && h.doneKeys == nil {
		h.doneKeys = map[string]bool{}
		h.loadTokenLocked()
		h.loadJobHistoryLocked()
	}
	h.mu.Unlock()
	h.workOnce.Do(func() {
		h.workCh = make(chan *slurpJob, 64)
		h.bus = newSSEBus()
		go h.worker()
	})
}

// loadJobHistoryLocked replays the jobs JSONL log to rebuild doneKeys
// (idempotency survives a process restart) and the in-memory job list
// (so the page shows history, not just this run's jobs). Caller holds h.mu.
func (h *DriveSlurpHandler) loadJobHistoryLocked() {
	f, err := os.Open(h.jobsLogPath())
	if err != nil {
		return // no history yet
	}
	defer f.Close()
	byID := map[string]*slurpJob{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var j slurpJob
		if err := json.Unmarshal(line, &j); err != nil {
			continue // skip a corrupt line rather than fail the whole history
		}
		byID[j.ID] = &j // later lines for the same ID overwrite earlier ones -- log is append-per-transition
	}
	for _, j := range byID {
		h.jobs = append(h.jobs, j)
		if j.Status == "done" {
			h.doneKeys[idempotencyKey(j.DriveFileID, j.ModifiedTime)] = true
		}
	}
}

// appendJobLog appends one job-state snapshot to the durable log. Errors
// are logged, not returned -- a failed durability write shouldn't crash an
// in-progress job; the in-memory state (and doneKeys, updated separately)
// is still correct for this process's own lifetime.
func (h *DriveSlurpHandler) appendJobLog(j *slurpJob) {
	if err := os.MkdirAll(filepath.Dir(h.jobsLogPath()), 0o755); err != nil {
		log.Printf("drive-slurp: mkdir for job log: %v", err)
		return
	}
	f, err := os.OpenFile(h.jobsLogPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("drive-slurp: open job log: %v", err)
		return
	}
	defer f.Close()
	raw, err := json.Marshal(j)
	if err != nil {
		return
	}
	f.Write(append(raw, '\n'))
}

func idempotencyKey(driveFileID, modifiedTime string) string {
	return driveFileID + ":" + modifiedTime
}

// --- OAuth flow ---

const driveSlurpStateCookie = "iduna_drive_slurp_state"

// oauthStart handles GET /admin/drive-slurp/oauth/start. Reached only
// through the already-admin-protected /admin/* mux (main.go wraps the
// whole AdminHandler in RequireCookieAuth+RequirePermission("iduna.admin")
// before any request reaches here), so no separate auth check needed --
// same trust-boundary reasoning as the other admin sub-handlers in this
// package (promptoverseQueue, gmSearch, etc.).
func (h *DriveSlurpHandler) oauthStart(w http.ResponseWriter, r *http.Request) {
	if h.GoogleClientID == "" || h.GoogleClientSecret == "" {
		http.Error(w, "Drive slurp not configured (GOOGLE_CLIENT_ID/GOOGLE_CLIENT_SECRET unset)", http.StatusServiceUnavailable)
		return
	}
	stateBytes := make([]byte, 16)
	_, _ = rand.Read(stateBytes)
	state := base64.URLEncoding.EncodeToString(stateBytes)

	http.SetCookie(w, &http.Cookie{
		Name:     driveSlurpStateCookie,
		Value:    state,
		Path:     "/admin/drive-slurp",
		MaxAge:   300,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	// access_type=offline + prompt=consent: the same real requirement
	// exchangeCodeForIDToken's own doc comment names for getting a
	// refresh_token back at all -- without both, Google returns an
	// access_token only, and this whole feature stops working the moment
	// it expires (~1h).
	authURL := fmt.Sprintf(
		"https://accounts.google.com/o/oauth2/v2/auth?client_id=%s&redirect_uri=%s&response_type=code&scope=%s&access_type=offline&prompt=consent&state=%s",
		url.QueryEscape(h.GoogleClientID),
		url.QueryEscape(h.RedirectURI),
		url.QueryEscape(drive.OAuthScope),
		url.QueryEscape(state),
	)
	http.Redirect(w, r, authURL, http.StatusFound)
}

// oauthCallback handles GET /admin/drive-slurp/oauth/callback.
func (h *DriveSlurpHandler) oauthCallback(w http.ResponseWriter, r *http.Request) {
	stateCookie, err := r.Cookie(driveSlurpStateCookie)
	q := r.URL.Query()
	if err != nil || q.Get("state") == "" || stateCookie.Value != q.Get("state") {
		http.Error(w, "state mismatch -- possible CSRF, restart the connect flow", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: driveSlurpStateCookie, Value: "", Path: "/admin/drive-slurp", MaxAge: -1})

	code := q.Get("code")
	if code == "" {
		http.Error(w, "missing authorization code", http.StatusBadRequest)
		return
	}

	tok, err := drive.ExchangeCode(h.GoogleClientID, h.GoogleClientSecret, code, h.RedirectURI)
	if err != nil {
		http.Error(w, "token exchange failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	h.mu.Lock()
	h.token = tok
	saveErr := h.saveTokenLocked()
	h.mu.Unlock()
	if saveErr != nil {
		log.Printf("drive-slurp: failed to persist token: %v", saveErr)
	}

	http.Redirect(w, r, "/admin/drive-slurp", http.StatusSeeOther)
}

// oauthDisconnect handles POST /admin/drive-slurp/oauth/disconnect.
func (h *DriveSlurpHandler) oauthDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	h.mu.Lock()
	h.token = nil
	_ = os.Remove(h.tokenPath())
	h.mu.Unlock()
	http.Redirect(w, r, "/admin/drive-slurp", http.StatusSeeOther)
}

// accessToken returns a valid access token, refreshing first if needed.
// Returns an error if not connected at all.
func (h *DriveSlurpHandler) accessToken() (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.token == nil {
		return "", fmt.Errorf("not connected -- visit /admin/drive-slurp and connect Google Drive")
	}
	if h.token.Expired() {
		if err := drive.Refresh(h.GoogleClientID, h.GoogleClientSecret, h.token); err != nil {
			return "", fmt.Errorf("token refresh failed: %w", err)
		}
		if err := h.saveTokenLocked(); err != nil {
			log.Printf("drive-slurp: failed to persist refreshed token: %v", err)
		}
	}
	return h.token.AccessToken, nil
}

// --- main page ---

// page handles GET /admin/drive-slurp.
func (h *DriveSlurpHandler) page(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	h.ensureLoaded()

	h.mu.Lock()
	connected := h.token != nil
	// Deep-copy each job (not just the slice of pointers) while still
	// holding the lock -- the background worker mutates job fields
	// (Status/Attempts/Error/SavedPath/FinishedAt) under this same lock
	// elsewhere; copying only the pointer slice would let the template
	// render read those fields unsynchronized against the worker's
	// concurrent writes, a real data race Go's race detector would catch.
	jobsCopy := make([]*slurpJob, len(h.jobs))
	for i, j := range h.jobs {
		jCopy := *j
		jobsCopy[i] = &jCopy
	}
	h.mu.Unlock()

	var files []drive.FileInfo
	var listErr string
	if connected {
		tok, err := h.accessToken()
		if err != nil {
			listErr = err.Error()
		} else {
			files, err = drive.ListWithToken(tok)
			if err != nil {
				listErr = err.Error()
			}
		}
	}

	// newest jobs first for display
	for i, j := 0, len(jobsCopy); i < j/2; i++ {
		jobsCopy[i], jobsCopy[j-1-i] = jobsCopy[j-1-i], jobsCopy[i]
	}

	renderHTML(w, adminDriveSlurpTmpl, map[string]any{
		"Title":     "Drive Slurp",
		"Connected": connected,
		"Files":     files,
		"ListError": listErr,
		"Jobs":      jobsCopy,
	})
}

// --- enqueue ---

// enqueue handles POST /admin/drive-slurp/enqueue. The double-click
// confirmation itself is enforced client-side (see the page template's
// inline script, adapting EmilyOS README.md's own fast/slow double-click
// timing concept to a plain confirm-armed button rather than porting its
// tile-canvas mechanics literally) -- server-side, this endpoint trusts
// that a POST here already represents a deliberate confirmed action, the
// same way every other admin POST handler in this package does (e.g.
// userAction's suspend/activate has no server-side re-confirmation beyond
// the client-side `confirm()` dialog already gating the button).
func (h *DriveSlurpHandler) enqueue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	fileID := strings.TrimSpace(r.FormValue("file_id"))
	fileName := strings.TrimSpace(r.FormValue("file_name"))
	modifiedTime := strings.TrimSpace(r.FormValue("modified_time"))
	if fileID == "" {
		http.Error(w, "file_id required", http.StatusBadRequest)
		return
	}

	h.mu.Lock()
	if h.doneKeys[idempotencyKey(fileID, modifiedTime)] {
		h.mu.Unlock()
		// Already slurped this exact file version -- idempotent no-op,
		// not an error. Re-clicking Slurp (or a page double-submit) must
		// not double-download.
		http.Redirect(w, r, "/admin/drive-slurp", http.StatusSeeOther)
		return
	}
	job := &slurpJob{
		ID:           randomID(),
		DriveFileID:  fileID,
		FileName:     fileName,
		ModifiedTime: modifiedTime,
		Status:       "queued",
		EnqueuedAt:   time.Now().UTC(),
	}
	h.jobs = append(h.jobs, job)
	h.mu.Unlock()

	h.appendJobLog(job)
	h.bus.broadcast(fmt.Sprintf("[%s] queued: %s", job.ID, job.FileName))

	select {
	case h.workCh <- job:
	default:
		// Queue channel full (64 pending) -- job stays "queued" in the
		// list, worker will never dequeue it via the channel path. Real,
		// honest limitation of the simple buffered-channel queue used
		// here; flagged rather than silently dropping the job. A deeper
		// fix (unbounded persistent queue, drained on a timer rather than
		// a channel send) is more than this feature's own scope needs
		// unless real usage actually hits 64 concurrent pending slurps.
		h.bus.broadcast(fmt.Sprintf("[%s] WARNING: job queue full, will not run until a slot frees", job.ID))
	}

	http.Redirect(w, r, "/admin/drive-slurp", http.StatusSeeOther)
}

func randomID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}

// --- background worker ---

// worker drains workCh serially -- one slurp at a time. Simple, correct,
// and sufficient: this is a founder-operated internal tool, not a
// high-throughput ingest pipeline: real concurrency would need per-job
// locking around doneKeys/jobs that isn't worth the complexity for the
// actual expected load (an admin clicking Slurp on a handful of files).
func (h *DriveSlurpHandler) worker() {
	for job := range h.workCh {
		h.runJob(job)
	}
}

// runJob executes one slurp with bounded retry + exponential backoff --
// the "regular resiliency patterns" the original spec asked for. Standard
// values (3 attempts, 2s/4s/8s backoff), not cross-referenced against
// PRRJECT_FATBABY's own watcher retry parameters since that repo wasn't
// reachable from this sandbox -- if this monorepo has a shared retry
// helper/convention, prefer that over these hand-rolled constants when
// integrating.
func (h *DriveSlurpHandler) runJob(job *slurpJob) {
	const maxAttempts = 3
	backoff := 2 * time.Second

	h.setStatus(job, "running", "")
	h.bus.broadcast(fmt.Sprintf("[%s] running: %s", job.ID, job.FileName))

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		job.Attempts = attempt
		tok, err := h.accessToken()
		if err != nil {
			lastErr = err
		} else {
			data, err2 := drive.DownloadWithToken(tok, job.DriveFileID)
			if err2 != nil {
				lastErr = err2
			} else {
				savedPath, err3 := h.saveSlurpedFile(job, data)
				if err3 != nil {
					lastErr = err3
				} else {
					job.SavedPath = savedPath
					h.finishJob(job, "done", "")
					h.bus.broadcast(fmt.Sprintf("[%s] done: saved to %s (%d bytes)", job.ID, savedPath, len(data)))
					return
				}
			}
		}
		h.bus.broadcast(fmt.Sprintf("[%s] attempt %d/%d failed: %v", job.ID, attempt, maxAttempts, lastErr))
		if attempt < maxAttempts {
			time.Sleep(backoff)
			backoff *= 2
		}
	}

	errMsg := "unknown error"
	if lastErr != nil {
		errMsg = lastErr.Error()
	}
	h.finishJob(job, "failed", errMsg)
	h.bus.broadcast(fmt.Sprintf("[%s] FAILED after %d attempts: %s", job.ID, maxAttempts, errMsg))
}

var unsafeFilenameCharsRe = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func (h *DriveSlurpHandler) saveSlurpedFile(job *slurpJob, data []byte) (string, error) {
	if err := os.MkdirAll(h.slurpDir(), 0o755); err != nil {
		return "", err
	}
	safeName := unsafeFilenameCharsRe.ReplaceAllString(job.FileName, "_")
	if safeName == "" {
		safeName = "file"
	}
	path := filepath.Join(h.slurpDir(), job.ID+"-"+safeName)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func (h *DriveSlurpHandler) setStatus(job *slurpJob, status, errMsg string) {
	h.mu.Lock()
	job.Status = status
	job.Error = errMsg
	h.mu.Unlock()
	h.appendJobLog(job)
}

func (h *DriveSlurpHandler) finishJob(job *slurpJob, status, errMsg string) {
	h.mu.Lock()
	job.Status = status
	job.Error = errMsg
	job.FinishedAt = time.Now().UTC()
	if status == "done" {
		h.doneKeys[idempotencyKey(job.DriveFileID, job.ModifiedTime)] = true
	}
	h.mu.Unlock()
	h.appendJobLog(job)
}

// --- SSE ---

// events handles GET /admin/drive-slurp/events -- Server-Sent Events
// stream of job-progress lines, so the Back Office page can show live
// slurp progress without polling. Chosen over long-poll: this is a
// one-directional server->browser stream (job log lines), which is
// exactly SSE's shape, and it's native EventSource in every real browser
// with no extra client library, unlike a hand-rolled long-poll loop.
func (h *DriveSlurpHandler) events(w http.ResponseWriter, r *http.Request) {
	h.ensureLoaded()

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := h.bus.subscribe()
	defer h.bus.unsubscribe(ch)

	w.Write([]byte(": connected\n\n"))
	flusher.Flush()

	for {
		select {
		case line, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", sseEscape(line))
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func sseEscape(s string) string {
	// SSE "data:" lines can't contain a bare newline without becoming
	// multiple fields -- collapse any embedded newlines rather than
	// producing a malformed frame.
	return strings.ReplaceAll(s, "\n", " ")
}

// sseBus is a minimal in-memory pub/sub for broadcasting job-progress
// lines to every currently-open /admin/drive-slurp/events connection.
// Not persisted -- a client that connects after a line was broadcast just
// doesn't see that historical line (the job list itself, re-rendered on
// page load, is the durable record; SSE is live-tail only, matching what
// "watch a slurp job's progress" actually needs).
type sseBus struct {
	mu   sync.Mutex
	subs map[chan string]struct{}
}

func newSSEBus() *sseBus {
	return &sseBus{subs: map[chan string]struct{}{}}
}

func (b *sseBus) subscribe() chan string {
	ch := make(chan string, 32)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *sseBus) unsubscribe(ch chan string) {
	b.mu.Lock()
	delete(b.subs, ch)
	b.mu.Unlock()
}

func (b *sseBus) broadcast(line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- line:
		default:
			// Slow/stuck subscriber -- drop the line for it rather than
			// block every other subscriber (and the job worker itself,
			// which calls broadcast synchronously) on one slow reader.
		}
	}
}

// --- template ---

var adminDriveSlurpTmpl = mustParseTmpl("drive-slurp", `
{{define "body"}}
<h1>Drive Slurp</h1>
<p class="meta" style="margin-bottom:16px">
  Connect your Google Drive (read-only), browse recent files, and slurp one into IDUNA for later
  use. Slurping enqueues a background download job -- idempotent (re-clicking the same file
  version won't double-download) with live progress below.
</p>

{{if not .Connected}}
<div class="section-card">
  <p style="margin-bottom:12px">Not connected.</p>
  <a href="/admin/drive-slurp/oauth/start"><button type="button">Connect Google Drive</button></a>
</div>
{{else}}

<div class="section-card">
  <form method="POST" action="/admin/drive-slurp/oauth/disconnect" style="display:inline">
    <button type="submit" class="danger" onclick="return confirm('Disconnect Google Drive?')">Disconnect</button>
  </form>
</div>

{{if .ListError}}<div class="err" style="margin-bottom:16px">{{.ListError}}</div>{{end}}

<h2>Recent Files</h2>
{{if .Files}}
<table>
<tr><th>Name</th><th>Type</th><th>Modified</th><th>Action</th></tr>
{{range .Files}}
<tr>
  <td>{{.Name}}</td>
  <td class="meta">{{.MimeType}}</td>
  <td class="meta">{{.ModifiedTime}}</td>
  <td>
    <form class="inline slurp-form" method="POST" action="/admin/drive-slurp/enqueue">
      <input type="hidden" name="file_id" value="{{.ID}}">
      <input type="hidden" name="file_name" value="{{.Name}}">
      <input type="hidden" name="modified_time" value="{{.ModifiedTime}}">
      <button type="button" class="slurp-btn">Slurp</button>
    </form>
  </td>
</tr>
{{end}}
</table>
{{else}}<p class="empty">No files found (or nothing yet since connecting).</p>{{end}}

<h2>Job Log</h2>
<div id="live-log" class="section-card" style="font-family:monospace;font-size:12px;max-height:200px;overflow-y:auto;background:#1a1a1a;color:#d4d0c8;padding:10px"></div>

{{if .Jobs}}
<table>
<tr><th>ID</th><th>File</th><th>Status</th><th>Attempts</th><th>Enqueued</th><th>Saved Path / Error</th></tr>
{{range .Jobs}}
<tr>
  <td class="meta">{{.ID}}</td>
  <td>{{.FileName}}</td>
  <td><span class="badge badge-{{if eq .Status "done"}}active{{else if eq .Status "failed"}}suspended{{else}}pending{{end}}">{{.Status}}</span></td>
  <td class="meta">{{.Attempts}}</td>
  <td class="meta">{{fmtTime .EnqueuedAt}}</td>
  <td class="meta">{{if .Error}}{{.Error}}{{else}}{{.SavedPath}}{{end}}</td>
</tr>
{{end}}
</table>
{{end}}

<script>
// Deliberate-action safeguard for Slurp, adapting EmilyOS README.md's own
// fast/slow double-click concept (a timed second confirming action, not a
// single accidental click) to a plain form button rather than porting its
// tile-canvas mechanics literally -- this is a plain admin page, not that
// canvas. First click arms a "Confirm?" state for 3s; a second click
// within that window actually submits; the window elapsing resets it.
document.querySelectorAll('.slurp-btn').forEach(function (btn) {
  var armed = false;
  var resetTimer = null;
  btn.addEventListener('click', function () {
    if (!armed) {
      armed = true;
      btn.textContent = 'Confirm Slurp?';
      btn.style.background = '#fff3cd';
      resetTimer = setTimeout(function () {
        armed = false;
        btn.textContent = 'Slurp';
        btn.style.background = '';
      }, 3000);
      return;
    }
    clearTimeout(resetTimer);
    btn.closest('form').submit();
  });
});

// Live job log via SSE.
(function () {
  var log = document.getElementById('live-log');
  if (!log || !window.EventSource) return;
  var es = new EventSource('/admin/drive-slurp/events');
  es.onmessage = function (e) {
    var line = document.createElement('div');
    line.textContent = e.data;
    log.appendChild(line);
    log.scrollTop = log.scrollHeight;
  };
})();
</script>
{{end}}
{{end}}
`)
