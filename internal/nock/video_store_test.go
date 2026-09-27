package nock

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"iduna/internal/store"
)

// newVideoTestStore runs the REAL migrations (not an inline copy of the schema) so the test
// exercises exactly what production gets.
func newVideoTestStore(t *testing.T) *VideoStore {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1) // same as internal/store.OpenSQLite in production
	if err := store.RunSQLiteMigrations(db, "../../migrations/truestore"); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	s := &VideoStore{DB: db, Dir: t.TempDir()}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Wait)
	return s
}

func requireFFmpeg(t *testing.T) {
	t.Helper()
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
}

// makeClip synthesizes a real test clip with ffmpeg's lavfi sources — stands in for a phone.
func makeClip(t *testing.T, name string, w, h int, secs string, audio bool, extra ...string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), name)
	args := []string{"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=" + strconv.Itoa(w) + "x" + strconv.Itoa(h) + ":rate=30:duration=" + secs}
	if audio {
		args = append(args, "-f", "lavfi", "-i", "sine=frequency=440:duration="+secs)
	}
	args = append(args, "-c:v", "libx264", "-pix_fmt", "yuv420p", "-preset", "ultrafast")
	if audio {
		args = append(args, "-c:a", "aac")
	}
	args = append(args, extra...)
	args = append(args, out)
	if b, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("make clip: %v: %s", err, b)
	}
	return out
}

func uploadFile(t *testing.T, s *VideoStore, path string) *Video {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	v, err := s.SaveUpload(context.Background(), f, UploadOpts{OriginalFilename: filepath.Base(path), UploadedBy: "test"})
	if err != nil {
		t.Fatalf("upload %s: %v", path, err)
	}
	return v
}

func TestVideoUploadProbeAndProxy(t *testing.T) {
	requireFFmpeg(t)
	s := newVideoTestStore(t)
	ctx := context.Background()

	v := uploadFile(t, s, makeClip(t, "IMG_0001.mov", 640, 360, "2", true))
	if v.Name != "IMG_0001" || v.Width != 640 || v.Height != 360 || !v.HasAudio || v.VideoCodec != "h264" {
		t.Fatalf("unexpected probe: %+v", v)
	}
	if v.DurationMS < 1900 || v.DurationMS > 2200 {
		t.Fatalf("duration %d", v.DurationMS)
	}
	if v.Source != "admin" || v.ProxyStatus != "pending" {
		t.Fatalf("source/proxy: %+v", v)
	}
	s.Wait()
	v, _ = s.GetVideo(ctx, v.ID)
	if v.ProxyStatus != "ready" || !v.HasThumb() {
		t.Fatalf("proxy not ready: %+v", v)
	}
	p, err := s.ProxyFile(ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.Probe(ctx, p)
	if err != nil || info.VideoCodec != "h264" || info.Height != 360 {
		t.Fatalf("proxy probe: %+v %v", info, err)
	}
}

// A portrait phone clip stored landscape with rotation metadata must report portrait dims.
func TestVideoProbeHonorsRotation(t *testing.T) {
	requireFFmpeg(t)
	s := newVideoTestStore(t)
	src := makeClip(t, "raw.mp4", 640, 360, "1", false)
	rotated := filepath.Join(t.TempDir(), "portrait.mp4")
	if b, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-display_rotation", "90", "-i", src, "-c", "copy", rotated).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg lacks -display_rotation: %s", b)
	}
	v := uploadFile(t, s, rotated)
	if v.Width != 360 || v.Height != 640 {
		t.Fatalf("rotation not applied: %dx%d", v.Width, v.Height)
	}
	if v.HasAudio {
		t.Fatal("expected no audio")
	}
}

func TestVideoRejectsNonVideo(t *testing.T) {
	requireFFmpeg(t)
	s := newVideoTestStore(t)
	_, err := s.SaveUpload(context.Background(), strings.NewReader("#!/bin/sh\necho pwned\n"), UploadOpts{OriginalFilename: "evil.mp4"})
	if !errors.Is(err, ErrNotVideo) {
		t.Fatalf("want ErrNotVideo, got %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(s.Dir, "clips"))
	if len(entries) != 0 {
		t.Fatalf("rejected upload left %d files behind", len(entries))
	}
}

func TestVideoRejectsOversize(t *testing.T) {
	s := newVideoTestStore(t)
	_, err := s.SaveUpload(context.Background(), bytes.NewReader(make([]byte, 1000)), UploadOpts{OriginalFilename: "a.mp4", MaxBytes: 100})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}

func TestUploadLinkLifecycle(t *testing.T) {
	requireFFmpeg(t)
	s := newVideoTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	clip := makeClip(t, "phone.mp4", 320, 240, "1", true)

	l, err := s.CreateUploadLink(ctx, "Emily's phone", time.Hour, 1, "admin@x")
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Token) < 30 {
		t.Fatalf("token too short: %q", l.Token)
	}
	if _, err := s.LookupUploadLink(ctx, l.Token); err != nil {
		t.Fatal(err)
	}

	f, _ := os.Open(clip)
	v, err := s.SavePhoneUpload(ctx, l.Token, f, UploadOpts{OriginalFilename: "phone.mp4"})
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if v.Source != "phone" || v.UploadLinkID == nil || *v.UploadLinkID != l.ID || v.UploadedBy != "phone:Emily's phone" {
		t.Fatalf("phone provenance wrong: %+v", v)
	}

	// max_uploads=1 is now used up.
	f, _ = os.Open(clip)
	_, err = s.SavePhoneUpload(ctx, l.Token, f, UploadOpts{OriginalFilename: "phone.mp4"})
	f.Close()
	if !errors.Is(err, ErrUploadLinkInvalid) {
		t.Fatalf("want used-up link rejected, got %v", err)
	}

	// A failed (non-video) upload gives its slot back.
	l2, err := s.CreateUploadLink(ctx, "", time.Hour, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SavePhoneUpload(ctx, l2.Token, strings.NewReader("nope"), UploadOpts{OriginalFilename: "x.mp4"}); !errors.Is(err, ErrNotVideo) {
		t.Fatalf("want ErrNotVideo, got %v", err)
	}
	if _, err := s.LookupUploadLink(ctx, l2.Token); err != nil {
		t.Fatalf("failed upload should release its slot: %v", err)
	}

	// Expiry.
	now = now.Add(2 * time.Hour)
	if _, err := s.LookupUploadLink(ctx, l2.Token); !errors.Is(err, ErrUploadLinkInvalid) {
		t.Fatalf("want expired, got %v", err)
	}

	// Revoke.
	l3, _ := s.CreateUploadLink(ctx, "", time.Hour, 0, "")
	if err := s.RevokeUploadLink(ctx, l3.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LookupUploadLink(ctx, l3.Token); !errors.Is(err, ErrUploadLinkInvalid) {
		t.Fatalf("want revoked, got %v", err)
	}
	if _, err := s.LookupUploadLink(ctx, "not-a-token"); !errors.Is(err, ErrUploadLinkInvalid) {
		t.Fatal("unknown token accepted")
	}
}

// Two phones racing a max_uploads=1 link: exactly one slot is granted.
func TestUploadLinkReserveIsAtomic(t *testing.T) {
	s := newVideoTestStore(t)
	ctx := context.Background()
	l, _ := s.CreateUploadLink(ctx, "", time.Hour, 1, "")
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.reserveUpload(ctx, l.Token); err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if ok != 1 {
		t.Fatalf("want exactly 1 reservation, got %d", ok)
	}
}

// The core promise: mixed phone footage (landscape w/ audio, portrait w/o audio, different fps)
// cut and rendered into one clean MP4 of the timeline's size.
func TestTimelineRenderMixedSources(t *testing.T) {
	requireFFmpeg(t)
	s := newVideoTestStore(t)
	ctx := context.Background()

	a := uploadFile(t, s, makeClip(t, "land.mp4", 640, 360, "3", true))
	b := uploadFile(t, s, makeClip(t, "port.mov", 360, 640, "2", false, "-r", "24"))

	tl, err := s.CreateTimeline(ctx, "first_cut", nil)
	if err != nil {
		t.Fatal(err)
	}
	if tl.EDL.Width != 1920 || tl.RenderStatus != "none" {
		t.Fatalf("defaults: %+v", tl)
	}
	edl := EDL{Width: 640, Height: 360, FPS: 30, Segments: []Segment{
		{ClipID: a.ID, InMS: 500, OutMS: 1500},
		{ClipID: b.ID, InMS: 0, OutMS: 1000},
		{ClipID: a.ID, InMS: 2000, OutMS: 2500},
	}}
	if _, err := s.UpdateTimeline(ctx, tl.ID, "", edl); err != nil {
		t.Fatal(err)
	}

	// Validation: out past clip end / inverted segment.
	bad := edl
	bad.Segments = []Segment{{ClipID: a.ID, InMS: 0, OutMS: 99_000}}
	if _, err := s.UpdateTimeline(ctx, tl.ID, "", bad); err == nil {
		t.Fatal("segment past end accepted")
	}
	bad.Segments = []Segment{{ClipID: a.ID, InMS: 900, OutMS: 100}}
	if _, err := s.UpdateTimeline(ctx, tl.ID, "", bad); err == nil {
		t.Fatal("inverted segment accepted")
	}

	started, err := s.StartRender(ctx, tl.ID)
	if err != nil {
		t.Fatal(err)
	}
	if started.RenderStatus != "rendering" {
		t.Fatalf("status %s", started.RenderStatus)
	}
	if _, err := s.StartRender(ctx, tl.ID); err == nil {
		t.Fatal("double render accepted")
	}
	s.Wait()

	out, done, err := s.RenderFile(ctx, tl.ID)
	if err != nil {
		t.Fatalf("render: %v (status %+v)", err, mustTimeline(t, s, tl.ID))
	}
	if done.RenderStatus != "ready" {
		t.Fatalf("status %s: %s", done.RenderStatus, done.RenderError)
	}
	info, err := s.Probe(ctx, out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Width != 640 || info.Height != 360 || !info.HasAudio || info.VideoCodec != "h264" {
		t.Fatalf("render format: %+v", info)
	}
	if info.DurationMS < 2300 || info.DurationMS > 2800 {
		t.Fatalf("render duration %dms, want ~2500", info.DurationMS)
	}

	// Editing after a render marks it stale; deleting a used clip is refused.
	if tl2, _ := s.UpdateTimeline(ctx, tl.ID, "", edl); tl2.RenderStatus != "stale" {
		t.Fatalf("want stale, got %s", tl2.RenderStatus)
	}
	if err := s.DeleteVideo(ctx, a.ID); err == nil {
		t.Fatal("deleted a clip still on a timeline")
	}
	if err := s.DeleteTimeline(ctx, tl.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("render file not removed with timeline")
	}
	if err := s.DeleteVideo(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
}

func TestRecoverMarksInterruptedRenderFailed(t *testing.T) {
	s := newVideoTestStore(t)
	ctx := context.Background()
	tl, _ := s.CreateTimeline(ctx, "t", nil)
	_, _ = s.DB.Exec(`UPDATE nock_video_timelines SET render_status = 'rendering' WHERE id = ?`, tl.ID)
	if err := s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if got := mustTimeline(t, s, tl.ID); got.RenderStatus != "failed" {
		t.Fatalf("want failed, got %s", got.RenderStatus)
	}
}

func mustTimeline(t *testing.T, s *VideoStore, id int64) *Timeline {
	t.Helper()
	tl, err := s.GetTimeline(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return tl
}
