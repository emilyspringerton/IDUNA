package nock

// video_store.go — NOCK's video editor backend (founder real-time, 2026-09-27: "blue ocean we
// need a nock video editor that can take uploads from any phone via nock").
//
// Three real pieces, all in one store because they share one on-disk root:
//
//  1. Clips (`nock_videos`): an uploaded video file on disk plus a metadata row. Unlike
//     textures/animations (BLOB-in-SQLite), video is far too big for a BLOB column — the row
//     holds a path relative to Dir, never an absolute one, so Dir can move. Every upload is
//     probed with ffprobe and REJECTED (file deleted) unless it really contains a video stream:
//     the upload path is reachable by an unauthenticated phone, so "it's a video" is checked,
//     not trusted from the filename or Content-Type.
//  2. Phone upload links (`nock_video_upload_links`): the "from any phone" half. An admin mints a
//     short-lived, capability-style token; the phone opens BASE_URL/nock/upload/<token> (shown as
//     a QR code in NOCK) and uploads straight from its camera roll — no app, no IDUNA login on
//     the phone. A link has an expiry, an optional upload cap, and can be revoked.
//  3. Timelines (`nock_video_timelines`): an edit decision list (ordered clip segments with
//     in/out points) plus an ffmpeg render of it to one H.264/AAC MP4.
//
// Phones produce wildly different media (iPhone HEVC .mov with rotation metadata, Android H.264
// .mp4, variable frame rate, sometimes no audio track). Two things absorb that: every clip gets
// a browser-safe H.264 720p "proxy" + a thumbnail (generated in the background after upload, so
// the upload request returns immediately), and every render normalizes each segment to the
// timeline's own size/fps/codec before concatenating, so mixed sources concat cleanly.
//
// Every pixel/sample operation shells out to the system ffmpeg/ffprobe (same "shell out to the
// real tool" shape imagemagick.go already uses for stills). Real, named v0 limits: cuts only (no
// transitions, titles, audio mixing, or multi-track); one render at a time per process.

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrUploadLinkInvalid covers every "this phone link can't be used" case (unknown, expired,
// revoked, used up) — deliberately one error so the public page can't be used to probe which.
var ErrUploadLinkInvalid = errors.New("nock: upload link is invalid, expired, or used up")

// ErrNotVideo is returned when an upload has no decodable video stream.
var ErrNotVideo = errors.New("nock: uploaded file is not a readable video")

// Video is one row of nock_videos.
type Video struct {
	ID               int64   `json:"id"`
	Name             string  `json:"name"`
	OriginalFilename string  `json:"original_filename"`
	SizeBytes        int64   `json:"size_bytes"`
	DurationMS       int64   `json:"duration_ms"`
	Width            int     `json:"width"`
	Height           int     `json:"height"`
	VideoCodec       string  `json:"video_codec"`
	HasAudio         bool    `json:"has_audio"`
	FPS              float64 `json:"fps"`
	ProxyStatus      string  `json:"proxy_status"` // pending | ready | failed
	ProxyError       string  `json:"proxy_error,omitempty"`
	Source           string  `json:"source"` // admin | phone
	UploadLinkID     *int64  `json:"upload_link_id,omitempty"`
	UploadedBy       string  `json:"uploaded_by,omitempty"`
	CreatedAt        string  `json:"created_at"`

	filePath  string
	proxyPath string
	thumbPath string
}

// HasThumb reports whether a thumbnail file exists for this clip.
func (v *Video) HasThumb() bool { return v.thumbPath != "" }

// UploadLink is one row of nock_video_upload_links.
type UploadLink struct {
	ID          int64  `json:"id"`
	Token       string `json:"token"`
	Label       string `json:"label"`
	CreatedBy   string `json:"created_by,omitempty"`
	ExpiresAt   string `json:"expires_at"`
	MaxUploads  int    `json:"max_uploads"` // 0 = unlimited until expiry
	UploadCount int    `json:"upload_count"`
	Revoked     bool   `json:"revoked"`
	CreatedAt   string `json:"created_at"`
}

// Active reports whether the link can still accept an upload at time now.
func (l *UploadLink) Active(now time.Time) bool {
	exp, err := time.Parse(time.RFC3339, l.ExpiresAt)
	if err != nil || !now.Before(exp) || l.Revoked {
		return false
	}
	return l.MaxUploads == 0 || l.UploadCount < l.MaxUploads
}

// Segment is one cut on a timeline: [InMS, OutMS) of clip ClipID.
type Segment struct {
	ClipID int64 `json:"clip_id"`
	InMS   int64 `json:"in_ms"`
	OutMS  int64 `json:"out_ms"`
}

// EDL is a timeline's edit decision list plus its output format.
type EDL struct {
	Width    int       `json:"width"`
	Height   int       `json:"height"`
	FPS      int       `json:"fps"`
	Segments []Segment `json:"segments"`
}

// Timeline is one row of nock_video_timelines.
type Timeline struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	EDL          EDL    `json:"edl"`
	RenderStatus string `json:"render_status"` // none | rendering | ready | failed | stale (edited since last render)
	RenderError  string `json:"render_error,omitempty"`
	RenderedAt   string `json:"rendered_at,omitempty"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`

	renderPath string
}

// VideoStore is the SQLite + filesystem backed video layer.
type VideoStore struct {
	DB  *sql.DB
	Dir string // root for clips/, proxies/, thumbs/, renders/, tmp/

	// FFmpeg/FFprobe default to "ffmpeg"/"ffprobe" on PATH.
	FFmpeg  string
	FFprobe string

	// Now is overridable for tests.
	Now func() time.Time

	renderMu sync.Mutex // one render at a time: an ffmpeg encode already saturates the box
	bgWG     sync.WaitGroup
}

func (s *VideoStore) ffmpeg() string {
	if s.FFmpeg != "" {
		return s.FFmpeg
	}
	return "ffmpeg"
}

func (s *VideoStore) ffprobe() string {
	if s.FFprobe != "" {
		return s.FFprobe
	}
	return "ffprobe"
}

func (s *VideoStore) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// Wait blocks until every background proxy/render job started by this store finishes. Used by
// tests and graceful shutdown.
func (s *VideoStore) Wait() { s.bgWG.Wait() }

// Init creates the on-disk layout. Safe to call repeatedly.
func (s *VideoStore) Init() error {
	for _, sub := range []string{"clips", "proxies", "thumbs", "renders", "tmp"} {
		if err := os.MkdirAll(filepath.Join(s.Dir, sub), 0o755); err != nil {
			return fmt.Errorf("nock: video dir: %w", err)
		}
	}
	return nil
}

// Recover fixes up jobs a process restart interrupted: a render left 'rendering' is marked
// failed (its goroutine is gone), and any clip whose proxy was still 'pending' is re-queued.
// Call once at startup, after Init.
func (s *VideoStore) Recover(ctx context.Context) error {
	if _, err := s.DB.ExecContext(ctx, `UPDATE nock_video_timelines SET render_status = 'failed',
		render_error = 'interrupted by a server restart — render again' WHERE render_status = 'rendering'`); err != nil {
		return err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM nock_videos WHERE proxy_status = 'pending'`)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		s.startProxy(id)
	}
	return nil
}

func (s *VideoStore) abs(rel string) string {
	if rel == "" {
		return ""
	}
	return filepath.Join(s.Dir, rel)
}

func randToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// allowedVideoExt maps a lowercased upload extension to the one stored on disk. Anything else is
// stored as ".bin" — the ffprobe check, not the extension, decides whether it's accepted.
var allowedVideoExt = map[string]bool{
	".mp4": true, ".mov": true, ".m4v": true, ".webm": true, ".mkv": true, ".3gp": true, ".avi": true,
}

// ---------------------------------------------------------------------------------------------
// Upload links
// ---------------------------------------------------------------------------------------------

// CreateUploadLink mints a new phone upload link valid for ttl.
func (s *VideoStore) CreateUploadLink(ctx context.Context, label string, ttl time.Duration, maxUploads int, createdBy string) (*UploadLink, error) {
	if ttl <= 0 || ttl > 7*24*time.Hour {
		return nil, fmt.Errorf("nock: upload link ttl must be between 1s and 7 days")
	}
	if maxUploads < 0 {
		return nil, fmt.Errorf("nock: max_uploads must be >= 0")
	}
	label = strings.TrimSpace(label)
	if len(label) > 200 {
		return nil, fmt.Errorf("nock: label too long")
	}
	tok, err := randToken(24)
	if err != nil {
		return nil, err
	}
	now := s.now()
	exp := now.Add(ttl).Format(time.RFC3339)
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO nock_video_upload_links (token, label, created_by, expires_at, max_uploads, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		tok, label, createdBy, exp, maxUploads, now.Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("nock: create upload link: %w", err)
	}
	id, _ := res.LastInsertId()
	return s.GetUploadLink(ctx, id)
}

const uploadLinkCols = `id, token, label, COALESCE(created_by, ''), expires_at, max_uploads, upload_count, revoked, created_at`

func scanUploadLink(row interface{ Scan(...any) error }) (*UploadLink, error) {
	var l UploadLink
	var revoked int
	if err := row.Scan(&l.ID, &l.Token, &l.Label, &l.CreatedBy, &l.ExpiresAt, &l.MaxUploads, &l.UploadCount, &revoked, &l.CreatedAt); err != nil {
		return nil, err
	}
	l.Revoked = revoked != 0
	return &l, nil
}

// GetUploadLink fetches one link by id.
func (s *VideoStore) GetUploadLink(ctx context.Context, id int64) (*UploadLink, error) {
	l, err := scanUploadLink(s.DB.QueryRowContext(ctx, `SELECT `+uploadLinkCols+` FROM nock_video_upload_links WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("nock: upload link %d not found", id)
	}
	return l, err
}

// LookupUploadLink resolves a token to an ACTIVE link, or ErrUploadLinkInvalid.
func (s *VideoStore) LookupUploadLink(ctx context.Context, token string) (*UploadLink, error) {
	if token == "" || len(token) > 128 {
		return nil, ErrUploadLinkInvalid
	}
	l, err := scanUploadLink(s.DB.QueryRowContext(ctx, `SELECT `+uploadLinkCols+` FROM nock_video_upload_links WHERE token = ?`, token))
	if err != nil {
		return nil, ErrUploadLinkInvalid
	}
	if !l.Active(s.now()) {
		return nil, ErrUploadLinkInvalid
	}
	return l, nil
}

// ListUploadLinks returns every link, newest first.
func (s *VideoStore) ListUploadLinks(ctx context.Context) ([]UploadLink, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+uploadLinkCols+` FROM nock_video_upload_links ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UploadLink{}
	for rows.Next() {
		l, err := scanUploadLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *l)
	}
	return out, rows.Err()
}

// RevokeUploadLink permanently disables a link.
func (s *VideoStore) RevokeUploadLink(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE nock_video_upload_links SET revoked = 1 WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("nock: upload link %d not found", id)
	}
	return nil
}

// reserveUpload atomically claims one upload slot on an active link. Claiming BEFORE the body is
// streamed means two phones racing on a max_uploads=1 link can't both get in.
func (s *VideoStore) reserveUpload(ctx context.Context, token string) (*UploadLink, error) {
	now := s.now().Format(time.RFC3339)
	res, err := s.DB.ExecContext(ctx, `UPDATE nock_video_upload_links SET upload_count = upload_count + 1
		WHERE token = ? AND revoked = 0 AND expires_at > ? AND (max_uploads = 0 OR upload_count < max_uploads)`, token, now)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return nil, ErrUploadLinkInvalid
	}
	l, err := scanUploadLink(s.DB.QueryRowContext(ctx, `SELECT `+uploadLinkCols+` FROM nock_video_upload_links WHERE token = ?`, token))
	if err != nil {
		return nil, ErrUploadLinkInvalid
	}
	return l, nil
}

func (s *VideoStore) releaseUpload(id int64) {
	_, _ = s.DB.Exec(`UPDATE nock_video_upload_links SET upload_count = upload_count - 1 WHERE id = ? AND upload_count > 0`, id)
}

// ---------------------------------------------------------------------------------------------
// Clips
// ---------------------------------------------------------------------------------------------

// UploadOpts describes where an upload came from.
type UploadOpts struct {
	Name             string // display name; defaults to the original filename's stem
	OriginalFilename string
	MaxBytes         int64 // 0 = unlimited
	UploadedBy       string
}

// ErrTooLarge is returned when an upload exceeds MaxBytes.
var ErrTooLarge = errors.New("nock: upload exceeds the size limit")

// SaveUpload streams r to disk, probes it, and inserts a clip row (admin-sourced).
func (s *VideoStore) SaveUpload(ctx context.Context, r io.Reader, opts UploadOpts) (*Video, error) {
	return s.saveUpload(ctx, r, opts, "admin", nil)
}

// SavePhoneUpload does the same through a phone upload link token, consuming one slot.
func (s *VideoStore) SavePhoneUpload(ctx context.Context, token string, r io.Reader, opts UploadOpts) (*Video, error) {
	link, err := s.reserveUpload(ctx, token)
	if err != nil {
		return nil, err
	}
	if opts.UploadedBy == "" {
		opts.UploadedBy = "phone:" + link.Label
	}
	v, err := s.saveUpload(ctx, r, opts, "phone", &link.ID)
	if err != nil {
		s.releaseUpload(link.ID)
		return nil, err
	}
	return v, nil
}

func (s *VideoStore) saveUpload(ctx context.Context, r io.Reader, opts UploadOpts, source string, linkID *int64) (*Video, error) {
	ext := strings.ToLower(filepath.Ext(opts.OriginalFilename))
	if !allowedVideoExt[ext] {
		ext = ".bin"
	}
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(opts.OriginalFilename), filepath.Ext(opts.OriginalFilename))
	}
	if name == "" || name == "." {
		name = "clip"
	}
	if len(name) > 200 {
		name = name[:200]
	}
	origName := filepath.Base(opts.OriginalFilename)
	if len(origName) > 255 {
		origName = origName[:255]
	}

	key, err := randToken(12)
	if err != nil {
		return nil, err
	}
	rel := filepath.Join("clips", key+ext)
	dst := s.abs(rel)
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("nock: create clip file: %w", err)
	}
	src := r
	if opts.MaxBytes > 0 {
		src = io.LimitReader(r, opts.MaxBytes+1)
	}
	n, copyErr := io.Copy(f, src)
	closeErr := f.Close()
	fail := func(e error) (*Video, error) {
		_ = os.Remove(dst)
		return nil, e
	}
	if copyErr != nil {
		return fail(fmt.Errorf("nock: write clip: %w", copyErr))
	}
	if closeErr != nil {
		return fail(closeErr)
	}
	if opts.MaxBytes > 0 && n > opts.MaxBytes {
		return fail(ErrTooLarge)
	}
	if n == 0 {
		return fail(ErrNotVideo)
	}

	info, err := s.Probe(ctx, dst)
	if err != nil || info.Width == 0 {
		return fail(ErrNotVideo)
	}

	res, err := s.DB.ExecContext(ctx, `INSERT INTO nock_videos
		(name, original_filename, file_path, size_bytes, duration_ms, width, height, video_codec, has_audio, fps,
		 proxy_status, source, upload_link_id, uploaded_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?, ?)`,
		name, origName, rel, n, info.DurationMS, info.Width, info.Height, info.VideoCodec, boolInt(info.HasAudio), info.FPS,
		source, linkID, nullIfEmpty(opts.UploadedBy), s.now().Format(time.RFC3339))
	if err != nil {
		return fail(fmt.Errorf("nock: insert clip: %w", err))
	}
	id, _ := res.LastInsertId()
	s.startProxy(id)
	return s.GetVideo(ctx, id)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

const videoCols = `id, name, original_filename, file_path, size_bytes, duration_ms, width, height, video_codec, has_audio, fps,
	proxy_status, COALESCE(proxy_error, ''), COALESCE(proxy_path, ''), COALESCE(thumb_path, ''), source, upload_link_id,
	COALESCE(uploaded_by, ''), created_at`

func scanVideo(row interface{ Scan(...any) error }) (*Video, error) {
	var v Video
	var hasAudio int
	var linkID sql.NullInt64
	if err := row.Scan(&v.ID, &v.Name, &v.OriginalFilename, &v.filePath, &v.SizeBytes, &v.DurationMS, &v.Width, &v.Height,
		&v.VideoCodec, &hasAudio, &v.FPS, &v.ProxyStatus, &v.ProxyError, &v.proxyPath, &v.thumbPath, &v.Source, &linkID,
		&v.UploadedBy, &v.CreatedAt); err != nil {
		return nil, err
	}
	v.HasAudio = hasAudio != 0
	if linkID.Valid {
		id := linkID.Int64
		v.UploadLinkID = &id
	}
	return &v, nil
}

// GetVideo fetches one clip.
func (s *VideoStore) GetVideo(ctx context.Context, id int64) (*Video, error) {
	v, err := scanVideo(s.DB.QueryRowContext(ctx, `SELECT `+videoCols+` FROM nock_videos WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("nock: video %d not found", id)
	}
	return v, err
}

// ListVideos returns every clip, newest first.
func (s *VideoStore) ListVideos(ctx context.Context) ([]Video, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+videoCols+` FROM nock_videos ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Video{}
	for rows.Next() {
		v, err := scanVideo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

// RenameVideo changes a clip's display name.
func (s *VideoStore) RenameVideo(ctx context.Context, id int64, name string) (*Video, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 {
		return nil, fmt.Errorf("nock: name must be 1-200 characters")
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE nock_videos SET name = ? WHERE id = ?`, name, id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("nock: video %d not found", id)
	}
	return s.GetVideo(ctx, id)
}

// DeleteVideo removes a clip and its files. A clip still referenced by a timeline is refused, so
// a saved edit never silently loses footage.
func (s *VideoStore) DeleteVideo(ctx context.Context, id int64) error {
	v, err := s.GetVideo(ctx, id)
	if err != nil {
		return err
	}
	tls, err := s.ListTimelines(ctx)
	if err != nil {
		return err
	}
	for _, t := range tls {
		for _, seg := range t.EDL.Segments {
			if seg.ClipID == id {
				return fmt.Errorf("nock: clip %d is used by timeline %q — remove it there first", id, t.Name)
			}
		}
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM nock_videos WHERE id = ?`, id); err != nil {
		return err
	}
	for _, p := range []string{v.filePath, v.proxyPath, v.thumbPath} {
		if p != "" {
			_ = os.Remove(s.abs(p))
		}
	}
	return nil
}

// VideoFile returns the absolute path of a clip's original file.
func (s *VideoStore) VideoFile(ctx context.Context, id int64) (string, *Video, error) {
	v, err := s.GetVideo(ctx, id)
	if err != nil {
		return "", nil, err
	}
	return s.abs(v.filePath), v, nil
}

// ProxyFile returns the absolute path of a clip's browser-safe proxy, if ready.
func (s *VideoStore) ProxyFile(ctx context.Context, id int64) (string, error) {
	v, err := s.GetVideo(ctx, id)
	if err != nil {
		return "", err
	}
	if v.ProxyStatus != "ready" || v.proxyPath == "" {
		return "", fmt.Errorf("nock: proxy for video %d is %s", id, v.ProxyStatus)
	}
	return s.abs(v.proxyPath), nil
}

// ThumbFile returns the absolute path of a clip's thumbnail, if present.
func (s *VideoStore) ThumbFile(ctx context.Context, id int64) (string, error) {
	v, err := s.GetVideo(ctx, id)
	if err != nil {
		return "", err
	}
	if v.thumbPath == "" {
		return "", fmt.Errorf("nock: no thumbnail for video %d", id)
	}
	return s.abs(v.thumbPath), nil
}

// RegenerateProxy re-runs proxy/thumbnail generation for a clip (e.g. after installing ffmpeg).
func (s *VideoStore) RegenerateProxy(ctx context.Context, id int64) error {
	if _, err := s.GetVideo(ctx, id); err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE nock_videos SET proxy_status = 'pending', proxy_error = NULL WHERE id = ?`, id); err != nil {
		return err
	}
	s.startProxy(id)
	return nil
}

func (s *VideoStore) startProxy(id int64) {
	s.bgWG.Add(1)
	go func() {
		defer s.bgWG.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		s.makeProxy(ctx, id)
	}()
}

// makeProxy writes a 720p H.264/AAC faststart MP4 and a JPEG thumbnail. ffmpeg auto-applies a
// phone clip's rotation metadata while transcoding, so portrait footage comes out upright.
func (s *VideoStore) makeProxy(ctx context.Context, id int64) {
	v, err := s.GetVideo(ctx, id)
	if err != nil {
		return
	}
	src := s.abs(v.filePath)
	base := strings.TrimSuffix(filepath.Base(v.filePath), filepath.Ext(v.filePath))
	proxyRel := filepath.Join("proxies", base+".mp4")
	thumbRel := filepath.Join("thumbs", base+".jpg")

	// Thumbnail first (cheap): grab a frame ~1s in, or the first frame for very short clips.
	seek := "1"
	if v.DurationMS < 1500 {
		seek = "0"
	}
	thumbErr := s.run(ctx, "-y", "-ss", seek, "-i", src, "-frames:v", "1", "-vf", "scale=320:-2", "-q:v", "4", s.abs(thumbRel))
	if thumbErr != nil {
		thumbRel = ""
	}

	args := []string{"-y", "-i", src,
		"-vf", "scale=-2:'min(720,ih)'",
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "26", "-pix_fmt", "yuv420p"}
	if v.HasAudio {
		args = append(args, "-c:a", "aac", "-b:a", "128k", "-ac", "2")
	} else {
		args = append(args, "-an")
	}
	args = append(args, "-movflags", "+faststart", s.abs(proxyRel))
	if err := s.run(ctx, args...); err != nil {
		_ = os.Remove(s.abs(proxyRel))
		_, _ = s.DB.Exec(`UPDATE nock_videos SET proxy_status = 'failed', proxy_error = ?, thumb_path = ? WHERE id = ?`,
			truncErr(err), nullIfEmpty(thumbRel), id)
		return
	}
	_, _ = s.DB.Exec(`UPDATE nock_videos SET proxy_status = 'ready', proxy_error = NULL, proxy_path = ?, thumb_path = ? WHERE id = ?`,
		proxyRel, nullIfEmpty(thumbRel), id)
}

func truncErr(err error) string {
	msg := err.Error()
	if len(msg) > 2000 {
		msg = msg[len(msg)-2000:]
	}
	return msg
}

// run executes ffmpeg with the given args, returning stderr's tail on failure.
func (s *VideoStore) run(ctx context.Context, args ...string) error {
	full := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin"}, args...)
	cmd := exec.CommandContext(ctx, s.ffmpeg(), full...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ProbeInfo is the subset of ffprobe output NOCK uses.
type ProbeInfo struct {
	DurationMS int64
	Width      int // display width (rotation applied)
	Height     int // display height (rotation applied)
	VideoCodec string
	HasAudio   bool
	FPS        float64
}

// Probe runs ffprobe on path.
func (s *VideoStore) Probe(ctx context.Context, path string) (*ProbeInfo, error) {
	cmd := exec.CommandContext(ctx, s.ffprobe(), "-v", "error", "-print_format", "json", "-show_format", "-show_streams", path)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe: %w", err)
	}
	return parseProbe(out)
}

type ffprobeOut struct {
	Streams []struct {
		CodecType    string            `json:"codec_type"`
		CodecName    string            `json:"codec_name"`
		Width        int               `json:"width"`
		Height       int               `json:"height"`
		AvgFrameRate string            `json:"avg_frame_rate"`
		Duration     string            `json:"duration"`
		Tags         map[string]string `json:"tags"`
		Disposition  map[string]int    `json:"disposition"`
		SideDataList []struct {
			Rotation float64 `json:"rotation"`
		} `json:"side_data_list"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

func parseProbe(raw []byte) (*ProbeInfo, error) {
	var p ffprobeOut
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("ffprobe json: %w", err)
	}
	info := &ProbeInfo{}
	durSec, _ := strconv.ParseFloat(p.Format.Duration, 64)
	for _, st := range p.Streams {
		switch st.CodecType {
		case "video":
			// Skip embedded cover art (a single still attached to an audio file is not video).
			if info.Width != 0 || st.Disposition["attached_pic"] == 1 || st.Width == 0 {
				continue
			}
			info.Width, info.Height = st.Width, st.Height
			info.VideoCodec = st.CodecName
			info.FPS = parseRate(st.AvgFrameRate)
			rot := 0.0
			if r, ok := st.Tags["rotate"]; ok {
				rot, _ = strconv.ParseFloat(r, 64)
			}
			for _, sd := range st.SideDataList {
				if sd.Rotation != 0 {
					rot = sd.Rotation
				}
			}
			if r := int(math.Abs(math.Round(rot))) % 180; r == 90 {
				info.Width, info.Height = info.Height, info.Width
			}
			if durSec == 0 {
				durSec, _ = strconv.ParseFloat(st.Duration, 64)
			}
		case "audio":
			info.HasAudio = true
		}
	}
	info.DurationMS = int64(math.Round(durSec * 1000))
	return info, nil
}

func parseRate(r string) float64 {
	num, den, ok := strings.Cut(r, "/")
	if !ok {
		f, _ := strconv.ParseFloat(r, 64)
		return f
	}
	n, _ := strconv.ParseFloat(num, 64)
	d, _ := strconv.ParseFloat(den, 64)
	if d == 0 {
		return 0
	}
	return math.Round(n/d*1000) / 1000
}

// ---------------------------------------------------------------------------------------------
// Timelines
// ---------------------------------------------------------------------------------------------

// DefaultEDL is the output format a new timeline starts with: 1080p30 landscape.
func DefaultEDL() EDL { return EDL{Width: 1920, Height: 1080, FPS: 30, Segments: []Segment{}} }

// validateEDL checks output format and every segment against the clips it references.
func (s *VideoStore) validateEDL(ctx context.Context, e *EDL) error {
	if e.Width < 16 || e.Width > 3840 || e.Height < 16 || e.Height > 3840 || e.Width%2 != 0 || e.Height%2 != 0 {
		return fmt.Errorf("nock: output size must be even and between 16 and 3840 per side")
	}
	if e.FPS < 1 || e.FPS > 60 {
		return fmt.Errorf("nock: fps must be 1-60")
	}
	if len(e.Segments) > 500 {
		return fmt.Errorf("nock: at most 500 segments per timeline")
	}
	if e.Segments == nil {
		e.Segments = []Segment{}
	}
	for i, seg := range e.Segments {
		v, err := s.GetVideo(ctx, seg.ClipID)
		if err != nil {
			return fmt.Errorf("nock: segment %d: %w", i, err)
		}
		if seg.InMS < 0 || seg.OutMS <= seg.InMS {
			return fmt.Errorf("nock: segment %d: out must be after in", i)
		}
		if v.DurationMS > 0 && seg.OutMS > v.DurationMS {
			return fmt.Errorf("nock: segment %d: out (%dms) is past the end of clip %q (%dms)", i, seg.OutMS, v.Name, v.DurationMS)
		}
	}
	return nil
}

// CreateTimeline makes a new, empty (or pre-filled) timeline.
func (s *VideoStore) CreateTimeline(ctx context.Context, name string, edl *EDL) (*Timeline, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	e := DefaultEDL()
	if edl != nil {
		e = *edl
	}
	if err := s.validateEDL(ctx, &e); err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(e)
	now := s.now().Format(time.RFC3339)
	res, err := s.DB.ExecContext(ctx, `INSERT INTO nock_video_timelines (name, edl_json, render_status, created_at, updated_at)
		VALUES (?, ?, 'none', ?, ?)`, name, string(raw), now, now)
	if err != nil {
		return nil, fmt.Errorf("nock: create timeline: %w", err)
	}
	id, _ := res.LastInsertId()
	return s.GetTimeline(ctx, id)
}

const timelineCols = `id, name, edl_json, render_status, COALESCE(render_error, ''), COALESCE(render_path, ''),
	COALESCE(rendered_at, ''), created_at, updated_at`

func scanTimeline(row interface{ Scan(...any) error }) (*Timeline, error) {
	var t Timeline
	var raw string
	if err := row.Scan(&t.ID, &t.Name, &raw, &t.RenderStatus, &t.RenderError, &t.renderPath, &t.RenderedAt, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(raw), &t.EDL); err != nil {
		return nil, fmt.Errorf("nock: timeline %d edl: %w", t.ID, err)
	}
	if t.EDL.Segments == nil {
		t.EDL.Segments = []Segment{}
	}
	return &t, nil
}

// GetTimeline fetches one timeline.
func (s *VideoStore) GetTimeline(ctx context.Context, id int64) (*Timeline, error) {
	t, err := scanTimeline(s.DB.QueryRowContext(ctx, `SELECT `+timelineCols+` FROM nock_video_timelines WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("nock: timeline %d not found", id)
	}
	return t, err
}

// ListTimelines returns every timeline, newest first.
func (s *VideoStore) ListTimelines(ctx context.Context) ([]Timeline, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+timelineCols+` FROM nock_video_timelines ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Timeline{}
	for rows.Next() {
		t, err := scanTimeline(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// UpdateTimeline replaces a timeline's EDL (and optionally renames it). The previous render is
// kept on disk but marked stale by resetting render_status to 'none' — the output no longer
// matches the edit.
func (s *VideoStore) UpdateTimeline(ctx context.Context, id int64, name string, edl EDL) (*Timeline, error) {
	cur, err := s.GetTimeline(ctx, id)
	if err != nil {
		return nil, err
	}
	if cur.RenderStatus == "rendering" {
		return nil, fmt.Errorf("nock: timeline %d is rendering — wait for it to finish", id)
	}
	if name == "" {
		name = cur.Name
	}
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	if err := s.validateEDL(ctx, &edl); err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(edl)
	_, err = s.DB.ExecContext(ctx, `UPDATE nock_video_timelines SET name = ?, edl_json = ?, render_status = CASE WHEN render_path IS NULL THEN 'none' ELSE 'stale' END,
		render_error = NULL, updated_at = ? WHERE id = ?`, name, string(raw), s.now().Format(time.RFC3339), id)
	if err != nil {
		return nil, fmt.Errorf("nock: update timeline: %w", err)
	}
	return s.GetTimeline(ctx, id)
}

// DeleteTimeline removes a timeline and its render. Clips are untouched.
func (s *VideoStore) DeleteTimeline(ctx context.Context, id int64) error {
	t, err := s.GetTimeline(ctx, id)
	if err != nil {
		return err
	}
	if t.RenderStatus == "rendering" {
		return fmt.Errorf("nock: timeline %d is rendering — wait for it to finish", id)
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM nock_video_timelines WHERE id = ?`, id); err != nil {
		return err
	}
	if t.renderPath != "" {
		_ = os.Remove(s.abs(t.renderPath))
	}
	return nil
}

// RenderFile returns the absolute path of a timeline's last successful render.
func (s *VideoStore) RenderFile(ctx context.Context, id int64) (string, *Timeline, error) {
	t, err := s.GetTimeline(ctx, id)
	if err != nil {
		return "", nil, err
	}
	if t.renderPath == "" {
		return "", nil, fmt.Errorf("nock: timeline %d has no render yet", id)
	}
	return s.abs(t.renderPath), t, nil
}

// StartRender kicks off a background render. Returns immediately with status 'rendering'.
func (s *VideoStore) StartRender(ctx context.Context, id int64) (*Timeline, error) {
	t, err := s.GetTimeline(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(t.EDL.Segments) == 0 {
		return nil, fmt.Errorf("nock: timeline %q has no segments to render", t.Name)
	}
	if err := s.validateEDL(ctx, &t.EDL); err != nil {
		return nil, err
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE nock_video_timelines SET render_status = 'rendering', render_error = NULL
		WHERE id = ? AND render_status != 'rendering'`, id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("nock: timeline %d is already rendering", id)
	}
	s.bgWG.Add(1)
	go func() {
		defer s.bgWG.Done()
		rctx, cancel := context.WithTimeout(context.Background(), 4*time.Hour)
		defer cancel()
		s.renderMu.Lock()
		defer s.renderMu.Unlock()
		if err := s.render(rctx, t); err != nil {
			_, _ = s.DB.Exec(`UPDATE nock_video_timelines SET render_status = 'failed', render_error = ? WHERE id = ?`, truncErr(err), id)
		}
	}()
	return s.GetTimeline(ctx, id)
}

// render normalizes every segment to the timeline's format, then concatenates with stream copy.
// Normalizing first is what makes mixed phone sources (HEVC/H.264, portrait/landscape,
// 24/30/60fps, with/without audio) concat without artifacts: after this pass every piece has the
// same codec, size, pixel format, timebase and an audio track.
func (s *VideoStore) render(ctx context.Context, t *Timeline) error {
	work, err := os.MkdirTemp(s.abs("tmp"), fmt.Sprintf("render-%d-", t.ID))
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)

	e := t.EDL
	// Letterbox/pillarbox into the output frame rather than crop: a portrait phone clip on a
	// landscape timeline keeps all of its picture.
	vf := fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2:color=black,setsar=1,fps=%d,format=yuv420p",
		e.Width, e.Height, e.Width, e.Height, e.FPS)

	var list strings.Builder
	for i, seg := range e.Segments {
		v, err := s.GetVideo(ctx, seg.ClipID)
		if err != nil {
			return fmt.Errorf("segment %d: %w", i, err)
		}
		part := filepath.Join(work, fmt.Sprintf("part%04d.mp4", i))
		args := []string{"-y",
			"-ss", msToSec(seg.InMS), "-to", msToSec(seg.OutMS), "-i", s.abs(v.filePath)}
		if !v.HasAudio {
			args = append(args, "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo")
		}
		args = append(args, "-map", "0:v:0")
		if v.HasAudio {
			args = append(args, "-map", "0:a:0")
		} else {
			args = append(args, "-map", "1:a:0", "-shortest")
		}
		args = append(args,
			"-vf", vf,
			"-c:v", "libx264", "-preset", "medium", "-crf", "20", "-video_track_timescale", "90000",
			"-c:a", "aac", "-b:a", "192k", "-ar", "48000", "-ac", "2",
			part)
		if err := s.run(ctx, args...); err != nil {
			return fmt.Errorf("segment %d (%s): %w", i, v.Name, err)
		}
		fmt.Fprintf(&list, "file '%s'\n", filepath.Base(part))
	}
	listPath := filepath.Join(work, "list.txt")
	if err := os.WriteFile(listPath, []byte(list.String()), 0o644); err != nil {
		return err
	}
	outRel := filepath.Join("renders", fmt.Sprintf("timeline-%d.mp4", t.ID))
	tmpOut := filepath.Join(work, "out.mp4")
	if err := s.run(ctx, "-y", "-f", "concat", "-safe", "0", "-i", listPath, "-c", "copy", "-movflags", "+faststart", tmpOut); err != nil {
		return fmt.Errorf("concat: %w", err)
	}
	if err := os.Rename(tmpOut, s.abs(outRel)); err != nil {
		return err
	}
	_, err = s.DB.Exec(`UPDATE nock_video_timelines SET render_status = 'ready', render_error = NULL, render_path = ?, rendered_at = ? WHERE id = ?`,
		outRel, s.now().Format(time.RFC3339), t.ID)
	return err
}

func msToSec(ms int64) string {
	return strconv.FormatFloat(float64(ms)/1000, 'f', 3, 64)
}
