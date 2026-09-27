package handlers

// nock_videos.go — NOCK video editor HTTP surface (founder real-time, 2026-09-27: "blue ocean we
// need a nock video editor that can take uploads from any phone via nock"). Every operation is a
// thin wrapper over internal/nock.VideoStore; see that file's header for the design.
//
// Admin (iduna.admin, same gate as every other /admin/nock route):
//
//	GET    /admin/nock/api/videos                     -> list clips
//	POST   /admin/nock/api/videos                     multipart "file" (+ optional "name") -> upload from desktop
//	GET    /admin/nock/api/videos/{id}                -> one clip
//	PATCH  /admin/nock/api/videos/{id}                {"name"} -> rename
//	DELETE /admin/nock/api/videos/{id}                -> delete (refused while on a timeline)
//	GET    /admin/nock/api/videos/{id}/original       -> original file (Range-capable)
//	GET    /admin/nock/api/videos/{id}/proxy          -> 720p H.264 preview (Range-capable)
//	GET    /admin/nock/api/videos/{id}/thumb          -> JPEG thumbnail
//	POST   /admin/nock/api/videos/{id}/reproxy        -> regenerate proxy + thumbnail
//
//	GET    /admin/nock/api/video-upload-links         -> list phone links
//	POST   /admin/nock/api/video-upload-links         {"label","ttl_minutes","max_uploads"} -> mint
//	DELETE /admin/nock/api/video-upload-links/{id}    -> revoke
//	GET    /admin/nock/api/video-upload-links/{id}/qr -> QR PNG of the phone URL
//
//	GET    /admin/nock/api/video-timelines            -> list
//	POST   /admin/nock/api/video-timelines            {"name","edl"?} -> create
//	GET    /admin/nock/api/video-timelines/{id}       -> one
//	PUT    /admin/nock/api/video-timelines/{id}       {"name"?,"edl"} -> save the edit
//	DELETE /admin/nock/api/video-timelines/{id}
//	POST   /admin/nock/api/video-timelines/{id}/render -> start a background render
//	GET    /admin/nock/api/video-timelines/{id}/output -> rendered MP4 (?download=1 for attachment)
//
// Public (no login — the link token IS the credential):
//
//	GET    /nock/upload/{token}   -> mobile upload page
//	POST   /nock/upload/{token}   multipart "file" -> upload one video
//
// Real, named deployment requirement: the nginx vhost in front of IDUNA needs a
// client_max_body_size at least NOCK_VIDEO_MAX_MB for /nock/upload/ and /admin/nock/api/videos,
// or phone uploads die at nginx with a 413 before they ever reach this handler.

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"iduna/internal/http/middleware"
	"iduna/internal/nock"
)

// NockVideosHandler serves the admin video API.
type NockVideosHandler struct {
	Store    *nock.VideoStore
	BaseURL  string // public origin phones reach, e.g. https://okemily.com
	MaxBytes int64
}

type videoDTO struct {
	nock.Video
	HasThumb bool `json:"has_thumb"`
}

type uploadLinkDTO struct {
	nock.UploadLink
	UploadURL string `json:"upload_url"`
	Active    bool   `json:"active"`
}

func (h *NockVideosHandler) uploadURL(token string) string {
	return strings.TrimRight(h.BaseURL, "/") + "/nock/upload/" + token
}

func (h *NockVideosHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	const base = "/admin/nock/api/"
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, base), "/")
	parts := strings.Split(rest, "/")
	if len(parts) == 0 {
		http.NotFound(w, r)
		return
	}
	resource, parts := parts[0], parts[1:]
	var id int64
	if len(parts) >= 1 {
		n, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			mmoWriteError(w, http.StatusBadRequest, "invalid id")
			return
		}
		id = n
	}
	verb := ""
	if len(parts) == 2 {
		verb = parts[1]
	} else if len(parts) > 2 {
		http.NotFound(w, r)
		return
	}

	switch resource {
	case "videos":
		h.videos(w, r, len(parts), id, verb)
	case "video-upload-links":
		h.links(w, r, len(parts), id, verb)
	case "video-timelines":
		h.timelines(w, r, len(parts), id, verb)
	default:
		http.NotFound(w, r)
	}
}

func (h *NockVideosHandler) videoOut(v *nock.Video) videoDTO {
	return videoDTO{Video: *v, HasThumb: v.HasThumb()}
}

func (h *NockVideosHandler) videos(w http.ResponseWriter, r *http.Request, n int, id int64, verb string) {
	ctx := r.Context()
	switch {
	case n == 0 && r.Method == http.MethodGet:
		list, err := h.Store.ListVideos(ctx)
		if err != nil {
			mmoWriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out := make([]videoDTO, 0, len(list))
		for i := range list {
			out = append(out, h.videoOut(&list[i]))
		}
		writeJSON(w, http.StatusOK, out)
	case n == 0 && r.Method == http.MethodPost:
		v, err := streamVideoUpload(w, r, h.MaxBytes, func(name, filename string, body io.Reader) (*nock.Video, error) {
			return h.Store.SaveUpload(ctx, body, nock.UploadOpts{
				Name: name, OriginalFilename: filename, MaxBytes: h.MaxBytes,
				UploadedBy: middleware.SubjectFromContext(ctx),
			})
		})
		if err != nil {
			writeUploadError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, h.videoOut(v))
	case n == 1 && r.Method == http.MethodGet:
		v, err := h.Store.GetVideo(ctx, id)
		if err != nil {
			mmoWriteError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, h.videoOut(v))
	case n == 1 && r.Method == http.MethodPatch:
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		v, err := h.Store.RenameVideo(ctx, id, body.Name)
		if err != nil {
			mmoWriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, h.videoOut(v))
	case n == 1 && r.Method == http.MethodDelete:
		if err := h.Store.DeleteVideo(ctx, id); err != nil {
			mmoWriteError(w, http.StatusConflict, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case n == 2 && verb == "original" && r.Method == http.MethodGet:
		path, v, err := h.Store.VideoFile(ctx, id)
		if err != nil {
			mmoWriteError(w, http.StatusNotFound, err.Error())
			return
		}
		serveMediaFile(w, r, path, "application/octet-stream", v.OriginalFilename, r.URL.Query().Get("download") == "1")
	case n == 2 && verb == "proxy" && r.Method == http.MethodGet:
		path, err := h.Store.ProxyFile(ctx, id)
		if err != nil {
			mmoWriteError(w, http.StatusNotFound, err.Error())
			return
		}
		serveMediaFile(w, r, path, "video/mp4", "", false)
	case n == 2 && verb == "thumb" && r.Method == http.MethodGet:
		path, err := h.Store.ThumbFile(ctx, id)
		if err != nil {
			mmoWriteError(w, http.StatusNotFound, err.Error())
			return
		}
		serveMediaFile(w, r, path, "image/jpeg", "", false)
	case n == 2 && verb == "reproxy" && r.Method == http.MethodPost:
		if err := h.Store.RegenerateProxy(ctx, id); err != nil {
			mmoWriteError(w, http.StatusNotFound, err.Error())
			return
		}
		w.WriteHeader(http.StatusAccepted)
	default:
		http.NotFound(w, r)
	}
}

func (h *NockVideosHandler) linkOut(l *nock.UploadLink) uploadLinkDTO {
	return uploadLinkDTO{UploadLink: *l, UploadURL: h.uploadURL(l.Token), Active: l.Active(time.Now().UTC())}
}

func (h *NockVideosHandler) links(w http.ResponseWriter, r *http.Request, n int, id int64, verb string) {
	ctx := r.Context()
	switch {
	case n == 0 && r.Method == http.MethodGet:
		list, err := h.Store.ListUploadLinks(ctx)
		if err != nil {
			mmoWriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out := make([]uploadLinkDTO, 0, len(list))
		for i := range list {
			out = append(out, h.linkOut(&list[i]))
		}
		writeJSON(w, http.StatusOK, out)
	case n == 0 && r.Method == http.MethodPost:
		var body struct {
			Label      string `json:"label"`
			TTLMinutes int    `json:"ttl_minutes"`
			MaxUploads int    `json:"max_uploads"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if body.TTLMinutes == 0 {
			body.TTLMinutes = 60
		}
		l, err := h.Store.CreateUploadLink(ctx, body.Label, time.Duration(body.TTLMinutes)*time.Minute, body.MaxUploads, middleware.SubjectFromContext(ctx))
		if err != nil {
			mmoWriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, h.linkOut(l))
	case n == 1 && r.Method == http.MethodDelete:
		if err := h.Store.RevokeUploadLink(ctx, id); err != nil {
			mmoWriteError(w, http.StatusNotFound, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case n == 2 && verb == "qr" && r.Method == http.MethodGet:
		l, err := h.Store.GetUploadLink(ctx, id)
		if err != nil {
			mmoWriteError(w, http.StatusNotFound, err.Error())
			return
		}
		png, err := qrcode.Encode(h.uploadURL(l.Token), qrcode.Medium, 384)
		if err != nil {
			mmoWriteError(w, http.StatusInternalServerError, "could not render qr image")
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(png)
	default:
		http.NotFound(w, r)
	}
}

func (h *NockVideosHandler) timelines(w http.ResponseWriter, r *http.Request, n int, id int64, verb string) {
	ctx := r.Context()
	switch {
	case n == 0 && r.Method == http.MethodGet:
		list, err := h.Store.ListTimelines(ctx)
		if err != nil {
			mmoWriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, list)
	case n == 0 && r.Method == http.MethodPost:
		var body struct {
			Name string    `json:"name"`
			EDL  *nock.EDL `json:"edl"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		t, err := h.Store.CreateTimeline(ctx, body.Name, body.EDL)
		if err != nil {
			mmoWriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, t)
	case n == 1 && r.Method == http.MethodGet:
		t, err := h.Store.GetTimeline(ctx, id)
		if err != nil {
			mmoWriteError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, t)
	case n == 1 && r.Method == http.MethodPut:
		var body struct {
			Name string   `json:"name"`
			EDL  nock.EDL `json:"edl"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			mmoWriteError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		t, err := h.Store.UpdateTimeline(ctx, id, body.Name, body.EDL)
		if err != nil {
			mmoWriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, t)
	case n == 1 && r.Method == http.MethodDelete:
		if err := h.Store.DeleteTimeline(ctx, id); err != nil {
			mmoWriteError(w, http.StatusConflict, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case n == 2 && verb == "render" && r.Method == http.MethodPost:
		t, err := h.Store.StartRender(ctx, id)
		if err != nil {
			mmoWriteError(w, http.StatusConflict, err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, t)
	case n == 2 && verb == "output" && r.Method == http.MethodGet:
		path, t, err := h.Store.RenderFile(ctx, id)
		if err != nil {
			mmoWriteError(w, http.StatusNotFound, err.Error())
			return
		}
		serveMediaFile(w, r, path, "video/mp4", t.Name+".mp4", r.URL.Query().Get("download") == "1")
	default:
		http.NotFound(w, r)
	}
}

// serveMediaFile streams a file with Range support (needed for <video> seeking on iOS Safari
// especially, which refuses to play media from a server that ignores Range).
func serveMediaFile(w http.ResponseWriter, r *http.Request, path, contentType, filename string, attachment bool) {
	f, err := os.Open(path)
	if err != nil {
		mmoWriteError(w, http.StatusNotFound, "file missing on disk")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if attachment {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", sanitizeFilename(filename)))
	}
	http.ServeContent(w, r, "", st.ModTime(), f)
}

func sanitizeFilename(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == '"' || r == '\\' || r == '/' || r > 0x7e {
			return '_'
		}
		return r
	}, s)
	if s == "" {
		return "video.mp4"
	}
	return s
}

// streamVideoUpload reads a multipart body WITHOUT buffering the file in memory or a temp file
// (net/http's ParseMultipartForm would spill a multi-GB phone video to /tmp first). An optional
// "name" text field must come before the "file" part to be honored.
func streamVideoUpload(w http.ResponseWriter, r *http.Request, maxBytes int64, save func(name, filename string, body io.Reader) (*nock.Video, error)) (*nock.Video, error) {
	if maxBytes > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes+1<<20) // + room for multipart framing
	}
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, errBadUpload{"expected a multipart/form-data upload"}
	}
	name := ""
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return nil, errBadUpload{"no \"file\" part in upload"}
		}
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				return nil, nock.ErrTooLarge
			}
			return nil, errBadUpload{"malformed multipart body"}
		}
		switch part.FormName() {
		case "name":
			b, _ := io.ReadAll(io.LimitReader(part, 256))
			name = strings.TrimSpace(string(b))
		case "file":
			v, err := save(name, part.FileName(), part)
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				return nil, nock.ErrTooLarge
			}
			return v, err
		}
		_ = drainPart(part)
	}
}

func drainPart(p *multipart.Part) error {
	_, err := io.Copy(io.Discard, io.LimitReader(p, 1<<20))
	return err
}

type errBadUpload struct{ msg string }

func (e errBadUpload) Error() string { return e.msg }

func writeUploadError(w http.ResponseWriter, err error) {
	var bad errBadUpload
	switch {
	case errors.As(err, &bad):
		mmoWriteError(w, http.StatusBadRequest, bad.msg)
	case errors.Is(err, nock.ErrTooLarge):
		mmoWriteError(w, http.StatusRequestEntityTooLarge, err.Error())
	case errors.Is(err, nock.ErrNotVideo):
		mmoWriteError(w, http.StatusUnsupportedMediaType, err.Error())
	case errors.Is(err, nock.ErrUploadLinkInvalid):
		mmoWriteError(w, http.StatusGone, err.Error())
	default:
		mmoWriteError(w, http.StatusInternalServerError, err.Error())
	}
}

// ---------------------------------------------------------------------------------------------
// Public phone upload page
// ---------------------------------------------------------------------------------------------

// NockPhoneUploadHandler is the public, login-free phone upload surface.
type NockPhoneUploadHandler struct {
	Store    *nock.VideoStore
	MaxBytes int64
}

func (h *NockPhoneUploadHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := strings.Trim(strings.TrimPrefix(r.URL.Path, "/nock/upload/"), "/")
	if token == "" || strings.Contains(token, "/") {
		http.NotFound(w, r)
		return
	}
	// The token is a credential in the URL: never leak it to another origin via Referer, never
	// cache the page.
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	switch r.Method {
	case http.MethodGet:
		link, err := h.Store.LookupUploadLink(r.Context(), token)
		h.page(w, link, err)
	case http.MethodPost:
		v, err := streamVideoUpload(w, r, h.MaxBytes, func(_, filename string, body io.Reader) (*nock.Video, error) {
			return h.Store.SavePhoneUpload(r.Context(), token, body, nock.UploadOpts{OriginalFilename: filename, MaxBytes: h.MaxBytes})
		})
		if err != nil {
			writeUploadError(w, err)
			return
		}
		// Deliberately minimal: the phone learns its upload landed, nothing about the library.
		writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "name": v.Name, "duration_ms": v.DurationMS})
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *NockPhoneUploadHandler) page(w http.ResponseWriter, link *nock.UploadLink, lookupErr error) {
	nonceBytes := make([]byte, 16)
	_, _ = rand.Read(nonceBytes)
	nonce := base64.StdEncoding.EncodeToString(nonceBytes)
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; style-src 'nonce-"+nonce+"'; script-src 'nonce-"+nonce+"'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	data := phonePageData{Nonce: nonce, MaxMB: h.MaxBytes >> 20}
	if lookupErr != nil || link == nil {
		w.WriteHeader(http.StatusGone)
		data.Invalid = true
	} else {
		data.Label = link.Label
		if exp, err := time.Parse(time.RFC3339, link.ExpiresAt); err == nil {
			data.ExpiresIn = time.Until(exp).Round(time.Minute).String()
		}
		if link.MaxUploads > 0 {
			data.Remaining = link.MaxUploads - link.UploadCount
		}
	}
	_ = phoneUploadTmpl.Execute(w, data)
}

type phonePageData struct {
	Nonce     string
	Invalid   bool
	Label     string
	ExpiresIn string
	Remaining int
	MaxMB     int64
}

var phoneUploadTmpl = template.Must(template.New("phone").Parse(`<!doctype html>
<html lang="en"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<meta name="robots" content="noindex">
<title>NOCK — send video</title>
<style nonce="{{.Nonce}}">
:root{--bg:#f7f5ff;--fg:#1d1b2e;--muted:#6b6880;--card:#fff;--accent:#5b4bdb;--ok:#1a7f4b;--err:#b3261e;--bar:#e4e0fb}
@media (prefers-color-scheme:dark){:root{--bg:#141320;--fg:#ecebf5;--muted:#a09db5;--card:#1f1d30;--accent:#9d8fff;--ok:#5fd39a;--err:#ff8a80;--bar:#2e2b45}}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.45 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;padding:24px 16px calc(24px + env(safe-area-inset-bottom))}
main{max-width:480px;margin:0 auto}
h1{font-size:22px;margin:0 0 4px;letter-spacing:.04em}
.sub{color:var(--muted);margin:0 0 20px;font-size:14px}
.pick{display:block;width:100%;padding:22px 16px;border:0;border-radius:16px;background:var(--accent);color:#fff;font-size:18px;font-weight:600;text-align:center;cursor:pointer}
.pick:active{transform:scale(.99)}
input[type=file]{position:absolute;left:-9999px}
ul{list-style:none;padding:0;margin:20px 0 0}
li{background:var(--card);border-radius:12px;padding:12px 14px;margin-bottom:10px}
.row{display:flex;justify-content:space-between;gap:12px;font-size:14px}
.fname{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.bar{height:6px;background:var(--bar);border-radius:3px;margin-top:8px;overflow:hidden}
.bar>div{height:100%;width:0;background:var(--accent);transition:width .2s}
.ok{color:var(--ok)}.err{color:var(--err)}
.note{color:var(--muted);font-size:13px;margin-top:18px}
</style></head><body><main>
<h1>NOCK</h1>
{{if .Invalid}}
<p class="sub">This upload link is expired, used up, or was revoked. Ask for a new QR code in NOCK.</p>
{{else}}
<p class="sub">Send videos straight into the NOCK video editor{{if .Label}} — <strong>{{.Label}}</strong>{{end}}.
{{if .ExpiresIn}}Link expires in {{.ExpiresIn}}.{{end}}{{if .Remaining}} {{.Remaining}} upload(s) left.{{end}}</p>
<label class="pick" for="f">Choose videos</label>
<input id="f" type="file" accept="video/*" multiple>
<ul id="list"></ul>
<p class="note">Keep this page open until every upload says “Sent”. Up to {{.MaxMB}} MB per video. Nothing else on your phone is shared.</p>
<script nonce="{{.Nonce}}">
(function(){
  var input=document.getElementById('f'), list=document.getElementById('list'), queue=[], busy=false;
  function mb(n){return (n/1048576).toFixed(1)+' MB'}
  function row(file){
    var li=document.createElement('li');
    li.innerHTML='<div class="row"><span class="fname"></span><span class="st">Waiting…</span></div><div class="bar"><div></div></div>';
    li.querySelector('.fname').textContent=file.name+' · '+mb(file.size);
    list.appendChild(li); return li;
  }
  function next(){
    if(busy||!queue.length) return;
    busy=true;
    var job=queue.shift(), st=job.li.querySelector('.st'), bar=job.li.querySelector('.bar>div');
    var fd=new FormData(); fd.append('file',job.file,job.file.name);
    var x=new XMLHttpRequest(); x.open('POST',location.pathname);
    x.upload.onprogress=function(e){ if(e.lengthComputable){var p=Math.round(e.loaded/e.total*100); bar.style.width=p+'%'; st.textContent=p<100?p+'%':'Processing…';} };
    x.onload=function(){
      if(x.status===201){ st.textContent='Sent ✓'; st.className='st ok'; bar.style.width='100%'; }
      else { var m='Failed'; try{m=JSON.parse(x.responseText).error||m}catch(e){} st.textContent=m; st.className='st err'; }
      busy=false; next();
    };
    x.onerror=function(){ st.textContent='Network error — try again'; st.className='st err'; busy=false; next(); };
    x.send(fd);
  }
  input.addEventListener('change',function(){
    for(var i=0;i<input.files.length;i++){ var f=input.files[i]; queue.push({file:f,li:row(f)}); }
    input.value=''; next();
  });
})();
</script>
{{end}}
</main></body></html>
`))
