// Package apps -- the real, shared app-release registry (S528, founder real-time: "we need an
// app repository in IDUNA signed in github somehow with emily session new for the build before
// each build and we can use session tokens in the app binaries figure out how to sign them with
// gpg keys or something").
//
// Backs SHANKPIT/apps/lobby's new "Apps" page (S527): a real, network-reachable place a CI build
// publishes a signed binary to, so bundling/distribution has a real source of truth instead of
// each SHANKPIT release having to carry a stale copy of another game's binary by hand. Mirrors
// internal/shankpit.CheckpointStore's own real shape (SQLite metadata + on-disk blob) field-for-
// field -- same reasoning applies here as there.
//
// "session_tag" reuses emily.cli's own `emily session new` tag format (sess-YYYYMMDD-HHMM-8hex)
// as a lightweight, free, per-build provenance nonce. That tool's own original purpose is
// fingerprinting an LLM conversation session for Apple/commit-message traceability -- this is a
// deliberate, second, unrelated use of the exact same CLI command and tag SHAPE, not a claim that
// a CI build IS an LLM session. The tag's own real properties (cheap, legible, collision-resistant
// via its embedded hash, no external API calls) are exactly what a build-id needs too.
//
// "signed in github somehow" is satisfied two ways, not one: (1) github_commit_sha/github_run_url
// record exactly which GitHub Actions run produced this artifact -- real, checkable provenance,
// not just an assertion; (2) gpg_signature is a real, detached OpenPGP signature over the binary
// itself, produced in that same CI run from a private key held only as a GitHub Actions secret
// (see docs/APP_RELEASE_SIGNING.md for the real key-management story) -- a party who trusts the
// EINHORN_INDUSTRIAL App Releases public key can verify a binary wasn't tampered with after CI
// signed it, independent of whether they trust this HTTP endpoint at all.
package apps

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var validAppSlug = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
var validPlatform = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
var validSessionTag = regexp.MustCompile(`^sess-[0-9]{8}-[0-9]{4}-[0-9a-f]{8}$`)

// Release is one row of the app_releases table.
type Release struct {
	ID              int64  `json:"id"`
	AppSlug         string `json:"app_slug"`
	Platform        string `json:"platform"`
	Version         string `json:"version"`
	SessionTag      string `json:"session_tag"`
	Filename        string `json:"filename"`
	SHA256          string `json:"sha256"`
	SizeBytes       int64  `json:"size_bytes"`
	GPGSignature    string `json:"gpg_signature"`
	GPGKeyID        string `json:"gpg_key_id"`
	GithubCommitSHA string `json:"github_commit_sha"`
	GithubRunURL    string `json:"github_run_url"`
	IsLatest        bool   `json:"is_latest"`
	CreatedAt       string `json:"created_at"`
}

const releaseColumns = `id, app_slug, platform, version, session_tag, filename, sha256, size_bytes,
	gpg_signature, gpg_key_id, github_commit_sha, github_run_url, is_latest, created_at`

func scanReleaseRow(scan func(...any) error) (*Release, error) {
	var r Release
	if err := scan(&r.ID, &r.AppSlug, &r.Platform, &r.Version, &r.SessionTag, &r.Filename, &r.SHA256, &r.SizeBytes,
		&r.GPGSignature, &r.GPGKeyID, &r.GithubCommitSHA, &r.GithubRunURL, &r.IsLatest, &r.CreatedAt); err != nil {
		return nil, err
	}
	return &r, nil
}

// ReleaseStore is the real, SQLite-metadata + on-disk-blob backed registry.
type ReleaseStore struct {
	DB      *sql.DB
	BlobDir string // e.g. var/app-releases -- real binaries, named by this row's own id
}

func validateReleaseInput(appSlug, platform, version, sessionTag, filename string, data []byte) error {
	if !validAppSlug.MatchString(appSlug) {
		return fmt.Errorf("apps: invalid app_slug %q", appSlug)
	}
	if !validPlatform.MatchString(platform) {
		return fmt.Errorf("apps: invalid platform %q", platform)
	}
	if version == "" {
		return fmt.Errorf("apps: version is required")
	}
	if !validSessionTag.MatchString(sessionTag) {
		return fmt.Errorf("apps: invalid session_tag %q (expected emily session new's own sess-YYYYMMDD-HHMM-8hex shape)", sessionTag)
	}
	if filename == "" {
		return fmt.Errorf("apps: filename is required")
	}
	if len(data) == 0 {
		return fmt.Errorf("apps: binary is empty")
	}
	// Real, sane bound -- a native game client binary is typically a few hundred KB to low tens
	// of MB (DEADWEIGHT's own dw_gui is ~200KB uncompressed, its bundled .tar.gz/.zip a bit
	// more); matches CheckpointStore's own 200MB cap for the same "generous but not unbounded"
	// reasoning.
	const maxReleaseBytes = 200 * 1024 * 1024
	if len(data) > maxReleaseBytes {
		return fmt.Errorf("apps: binary too large (%d bytes, max %d)", len(data), maxReleaseBytes)
	}
	return nil
}

// Create validates, hashes, writes the real blob to disk, and inserts the metadata row -- in
// that order, so a failed DB insert never leaves an orphaned blob file with no matching row.
// Marks the new row is_latest for its own (app_slug, platform) pair and clears that flag on every
// prior row for the same pair, in one transaction -- same real single-selection invariant
// CheckpointStore.SetActiveOpponent already established, just automatic on every upload instead
// of a separate admin action (a CI publish IS the new latest, there's no real "upload but don't
// promote yet" state worth modeling here).
func (s *ReleaseStore) Create(ctx context.Context, appSlug, platform, version, sessionTag, filename string, data []byte, gpgSignature, gpgKeyID, githubCommitSHA, githubRunURL string) (*Release, error) {
	if err := validateReleaseInput(appSlug, platform, version, sessionTag, filename, data); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])

	if err := os.MkdirAll(s.BlobDir, 0o755); err != nil {
		return nil, fmt.Errorf("apps: create blob dir: %w", err)
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("apps: begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`UPDATE app_releases SET is_latest = 0 WHERE app_slug = ? AND platform = ?`, appSlug, platform); err != nil {
		return nil, fmt.Errorf("apps: clear prior latest: %w", err)
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO app_releases (app_slug, platform, version, session_tag, filename, sha256, size_bytes, blob_path,
			gpg_signature, gpg_key_id, github_commit_sha, github_run_url, is_latest)
		 VALUES (?, ?, ?, ?, ?, ?, ?, '', ?, ?, ?, ?, 1)`,
		appSlug, platform, version, sessionTag, filename, sha, len(data), gpgSignature, gpgKeyID, githubCommitSHA, githubRunURL)
	if err != nil {
		return nil, fmt.Errorf("apps: create release row: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("apps: create release row: %w", err)
	}

	blobPath := filepath.Join(s.BlobDir, fmt.Sprintf("%d_%s", id, filename))
	if err := os.WriteFile(blobPath, data, 0o644); err != nil {
		return nil, fmt.Errorf("apps: write release blob: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE app_releases SET blob_path = ? WHERE id = ?`, blobPath, id); err != nil {
		_ = os.Remove(blobPath)
		return nil, fmt.Errorf("apps: record blob path: %w", err)
	}

	if err := tx.Commit(); err != nil {
		_ = os.Remove(blobPath)
		return nil, fmt.Errorf("apps: commit: %w", err)
	}

	return s.Get(ctx, id)
}

func (s *ReleaseStore) Get(ctx context.Context, id int64) (*Release, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+releaseColumns+` FROM app_releases WHERE id = ?`, id)
	r, err := scanReleaseRow(row.Scan)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("apps: release not found")
		}
		return nil, fmt.Errorf("apps: get release: %w", err)
	}
	return r, nil
}

// GetLatest returns the current is_latest row for (appSlug, platform), or nil (not an error) if
// none has ever been published -- a real, expected, honest state for a fresh app_slug.
func (s *ReleaseStore) GetLatest(ctx context.Context, appSlug, platform string) (*Release, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+releaseColumns+` FROM app_releases WHERE app_slug = ? AND platform = ? AND is_latest = 1`, appSlug, platform)
	r, err := scanReleaseRow(row.Scan)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("apps: get latest release: %w", err)
	}
	return r, nil
}

// List returns every registered release for one app_slug (any platform), newest first.
func (s *ReleaseStore) List(ctx context.Context, appSlug string) ([]Release, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+releaseColumns+` FROM app_releases WHERE app_slug = ? ORDER BY created_at DESC`, appSlug)
	if err != nil {
		return nil, fmt.Errorf("apps: list releases: %w", err)
	}
	defer rows.Close()

	out := []Release{}
	for rows.Next() {
		r, err := scanReleaseRow(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("apps: list releases: %w", err)
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// ReadBlob returns the real binary bytes for one release row.
func (s *ReleaseStore) ReadBlob(ctx context.Context, id int64) (*Release, []byte, error) {
	r, err := s.Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	row := s.DB.QueryRowContext(ctx, `SELECT blob_path FROM app_releases WHERE id = ?`, id)
	var blobPath string
	if err := row.Scan(&blobPath); err != nil {
		return nil, nil, fmt.Errorf("apps: get blob path: %w", err)
	}
	data, err := os.ReadFile(blobPath)
	if err != nil {
		return nil, nil, fmt.Errorf("apps: read blob: %w", err)
	}
	return r, data, nil
}
