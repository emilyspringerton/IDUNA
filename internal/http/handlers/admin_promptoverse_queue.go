// admin_promptoverse_queue.go — Back Office view of the Prompt-o-verse
// generation queue (founder, real-time: "i fat fingereed icelabdic horse
// into promptoverse queue can you clear that out and then queue a way for
// me to do that via iduna back office?"). Reads/writes the exact same
// EMILY_ROOT/var/promptoverse-queue.jsonl file emily.cli's `emily
// promptoverse add`/`work` commands already use -- same format
// (queueItem: subject, style_label, enqueued_at, forced), so this and the
// CLI are two front doors onto one real queue, not a parallel copy of it.
//
// Kept intentionally minimal: list + remove-by-index + a plain single-item
// add (subject + free-text style label). It does not reimplement the
// CLI's `add`'s node-existence dedupe or pity-weighted auto-subject
// picking -- those are real, separate CLI features; this exists so a
// fat-fingered entry can be found and deleted without shelling in, and a
// simple correction re-queued, not to replace the CLI's richer add flow.
package handlers

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type promptoverseQueueItem struct {
	Subject    string    `json:"subject"`
	StyleLabel string    `json:"style_label"`
	EnqueuedAt time.Time `json:"enqueued_at"`
	Forced     bool      `json:"forced,omitempty"`
}

func promptoverseQueuePath() string {
	return filepath.Join(emilyRootDefault(), "var", "promptoverse-queue.jsonl")
}

func loadPromptOVerseQueue(path string) ([]promptoverseQueueItem, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var items []promptoverseQueueItem
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var it promptoverseQueueItem
		if err := json.Unmarshal(line, &it); err != nil {
			continue // skip a corrupt line rather than fail the whole queue, same as emily.cli's loadQueue
		}
		items = append(items, it)
	}
	return items, sc.Err()
}

// writePromptOVerseQueue overwrites the queue file with exactly items, in
// order, via a tmp-file rename -- same atomicity emily.cli's writeQueue
// uses, so a crash mid-write can't leave a truncated queue file behind.
func writePromptOVerseQueue(path string, items []promptoverseQueueItem) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	for _, it := range items {
		b, err := json.Marshal(it)
		if err != nil {
			f.Close()
			return err
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			f.Close()
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

type promptoverseQueueRow struct {
	Index      int
	Subject    string
	StyleLabel string
	EnqueuedAt string
	Forced     bool
}

func (h *AdminHandler) promptoverseQueue(w http.ResponseWriter, r *http.Request) {
	path := promptoverseQueuePath()

	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		subject := strings.TrimSpace(r.FormValue("subject"))
		styleLabel := strings.TrimSpace(r.FormValue("style_label"))
		if subject == "" || styleLabel == "" {
			h.renderPromptOVerseQueue(w, path, "Subject and style label are both required.")
			return
		}
		items, err := loadPromptOVerseQueue(path)
		if err != nil {
			h.renderPromptOVerseQueue(w, path, "Load queue: "+err.Error())
			return
		}
		items = append(items, promptoverseQueueItem{
			Subject:    subject,
			StyleLabel: styleLabel,
			EnqueuedAt: time.Now().UTC(),
			Forced:     r.FormValue("forced") == "on",
		})
		if err := writePromptOVerseQueue(path, items); err != nil {
			h.renderPromptOVerseQueue(w, path, "Write queue: "+err.Error())
			return
		}
		http.Redirect(w, r, "/admin/promptoverse-queue", http.StatusSeeOther)
		return
	}

	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	h.renderPromptOVerseQueue(w, path, "")
}

func (h *AdminHandler) renderPromptOVerseQueue(w http.ResponseWriter, path, errMsg string) {
	items, err := loadPromptOVerseQueue(path)
	if err != nil && errMsg == "" {
		errMsg = "Load queue: " + err.Error()
	}
	rows := make([]promptoverseQueueRow, 0, len(items))
	for i, it := range items {
		rows = append(rows, promptoverseQueueRow{
			Index:      i,
			Subject:    it.Subject,
			StyleLabel: it.StyleLabel,
			EnqueuedAt: it.EnqueuedAt.Format("2006-01-02 15:04 MST"),
			Forced:     it.Forced,
		})
	}
	renderHTML(w, adminPromptOVerseQueueTmpl, map[string]any{
		"Title": "Prompt-o-verse Queue",
		"Rows":  rows,
		"Error": errMsg,
	})
}

// promptoverseQueueRemove handles POST /admin/promptoverse-queue/remove.
// Removes by index into a queue file re-read fresh at request time (not
// whatever index the last GET rendered), so it stays correct even if the
// background drain (emily promptoverse work) advances the file between
// page-load and click -- the same "re-read before mutate" discipline
// emily.cli's own appendQueue uses against the equivalent TOCTOU race.
func (h *AdminHandler) promptoverseQueueRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	path := promptoverseQueuePath()
	idx, err := strconv.Atoi(r.FormValue("index"))
	if err != nil {
		h.renderPromptOVerseQueue(w, path, "Invalid index.")
		return
	}
	items, err := loadPromptOVerseQueue(path)
	if err != nil {
		h.renderPromptOVerseQueue(w, path, "Load queue: "+err.Error())
		return
	}
	if idx < 0 || idx >= len(items) {
		h.renderPromptOVerseQueue(w, path, "That item is no longer at that position -- the queue changed underneath you. Refresh and try again.")
		return
	}
	items = append(items[:idx], items[idx+1:]...)
	if err := writePromptOVerseQueue(path, items); err != nil {
		h.renderPromptOVerseQueue(w, path, "Write queue: "+err.Error())
		return
	}
	http.Redirect(w, r, "/admin/promptoverse-queue", http.StatusSeeOther)
}

var adminPromptOVerseQueueTmpl = mustParseTmpl("promptoverse-queue", `
{{define "body"}}
<h1>Prompt-o-verse Queue</h1>
<p class="meta">
  Same queue file <code>emily promptoverse add</code> / <code>emily promptoverse work</code> use
  (<code>EMILY_ROOT/var/promptoverse-queue.jsonl</code>) -- remove a fat-fingered entry or queue a
  quick one-off without shelling in. Style label must exactly match one of the registered styles
  (<code>emily promptoverse styles</code> lists them) or the drain will skip it with a warning.
</p>
{{if .Error}}<div class="err">{{.Error}}</div>{{end}}
<div class="section-card">
<form method="POST" action="/admin/promptoverse-queue">
  <label for="subject">Subject</label>
  <input type="text" id="subject" name="subject" placeholder="e.g. Icelandic Horse" autocomplete="off" required>
  <label for="style_label">Style Label</label>
  <input type="text" id="style_label" name="style_label" placeholder="e.g. 8-bit pixel art" autocomplete="off" required>
  <label><input type="checkbox" name="forced"> Forced (never auto-requeued/re-picked)</label>
  <input type="submit" value="Add to Queue">
</form>
</div>
{{if .Rows}}
<table>
<tr><th>#</th><th>Subject</th><th>Style</th><th>Queued At</th><th>Forced</th><th>Actions</th></tr>
{{range .Rows}}
<tr>
  <td class="meta">{{.Index}}</td>
  <td>{{.Subject}}</td>
  <td>{{.StyleLabel}}</td>
  <td class="meta">{{.EnqueuedAt}}</td>
  <td>{{if .Forced}}yes{{else}}<span class="meta">no</span>{{end}}</td>
  <td>
    <form class="inline" method="POST" action="/admin/promptoverse-queue/remove">
      <input type="hidden" name="index" value="{{.Index}}">
      <input type="submit" value="Remove" class="danger">
    </form>
  </td>
</tr>
{{end}}
</table>
{{else}}<p class="empty">Queue is empty.</p>{{end}}
{{end}}
`)
