package nock

// anim_store.go — real CRUD + SQLite persistence for NOCK's animation repository (founder
// real-time, 2026-09-16: "need animation repository" — the storage/browse half of "let's start
// iterating towards nock tools modeler (blender) and golden band"). Same real BLOB-in-SQLite
// pattern texture_store.go already established for the texture library; see
// migrations/truestore/202609160001_nock_animations.sql's own header comment for the schema
// rationale, including why gskel_data/gmesh_data are nullable and manifest_json stays JSON
// rather than being split into columns.

import (
	"context"
	"database/sql"
	"fmt"
)

// Animation is one row of the nock_animations table -- despite the table's own historical name,
// a row does NOT require animation data (real, live-found gap, fixed 2026-09-17: "no animation
// found in this file" blocked importing a real rigged mesh with no baked animation yet). A row
// is really "a GOLDENBAND character asset" -- any real, non-empty combination of a mesh
// (GMeshData), a skeleton/rig (GSkelData), and/or animation (the TickRate/DurationTicks/
// NumChannels/ContentHash/GBandData/ManifestJSON group, all nil/empty together or all real
// together). HasAnimation reports which case a given row is.
type Animation struct {
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	TickRate       *int    `json:"tick_rate,omitempty"`
	DurationTicks  *int    `json:"duration_ticks,omitempty"`
	NumChannels    *int    `json:"num_channels,omitempty"`
	ContentHash    string  `json:"content_hash,omitempty"`
	GBandData      []byte  `json:"-"` // never inlined into a JSON response -- see AnimationSummary
	ManifestJSON   string  `json:"manifest_json,omitempty"`
	GSkelData      []byte  `json:"-"`
	GMeshData      []byte  `json:"-"`
	// SkeletonHash (2026-09-17) -- a real, hex-encoded sha256 of this row's own skeleton joint
	// data, present whenever GSkelData came from a real glTF import. The one real, checkable
	// signal AttachAnimation uses to verify a separately-uploaded (or already-in-library)
	// animation clip's own rig actually matches this row's rig before merging them.
	SkeletonHash   string  `json:"skeleton_hash,omitempty"`
	SourceLocation string  `json:"source_location,omitempty"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
}

// AnimationSummary is the real, lightweight shape a LIST returns -- everything about an
// Animation except its own potentially-large blobs and manifest text. HasSkel/HasMesh/
// HasAnimation are real booleans so a UI can show what a row actually carries (a bare mesh, a
// bare rig, a rigged mesh with no animation yet, or a full animated character) without fetching
// the full row.
type AnimationSummary struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	TickRate       *int   `json:"tick_rate,omitempty"`
	DurationTicks  *int   `json:"duration_ticks,omitempty"`
	NumChannels    *int   `json:"num_channels,omitempty"`
	HasSkel        bool   `json:"has_skel"`
	HasMesh        bool   `json:"has_mesh"`
	HasAnimation   bool   `json:"has_animation"`
	SkeletonHash   string `json:"skeleton_hash,omitempty"`
	SourceLocation string `json:"source_location,omitempty"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

// AnimStore is the real SQLite-backed CRUD layer. DB is expected to already have the
// nock_animations table (via a real migration in production, or a test's own inline CREATE
// TABLE -- see anim_store_test.go).
type AnimStore struct {
	DB *sql.DB
}

// CreateAnimation inserts a new, real, independent character-asset row. gbandData/manifestJSON
// (with tickRate/durationTicks/numChannels/contentHash) may ALL be empty/zero together -- a real,
// legitimate "no animation yet" row (a bare mesh, a bare rig, or a rigged mesh with no baked
// animation) -- but gskelData/gmeshData/gbandData can't ALL be empty at once (nothing real to
// store). gskelData/gmeshData may independently be nil.
func (s *AnimStore) CreateAnimation(ctx context.Context, name string, tickRate, durationTicks, numChannels int, contentHash string, gbandData []byte, manifestJSON string, gskelData, gmeshData []byte, skeletonHash, sourceLocation string) (*Animation, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	hasAnim := len(gbandData) > 0
	if !hasAnim && len(gskelData) == 0 && len(gmeshData) == 0 {
		return nil, fmt.Errorf("nock: at least one of mesh, skeleton, or animation data is required")
	}
	if hasAnim && (len(gbandData) < 4 || string(gbandData[0:4]) != "GBND") {
		return nil, fmt.Errorf("nock: gband data has bad magic, expected a real .gband file starting with \"GBND\"")
	}

	var tickRateVal, durationTicksVal, numChannelsVal any
	var gbandVal, manifestVal, contentHashVal any
	if hasAnim {
		tickRateVal, durationTicksVal, numChannelsVal = tickRate, durationTicks, numChannels
		gbandVal, manifestVal, contentHashVal = gbandData, manifestJSON, nullIfEmpty(contentHash)
	}

	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO nock_animations (name, tick_rate, duration_ticks, num_channels, content_hash, gband_data, manifest_json, gskel_data, gmesh_data, skeleton_hash, source_location)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		name, tickRateVal, durationTicksVal, numChannelsVal, contentHashVal, gbandVal, manifestVal, nullBytesIfEmpty(gskelData), nullBytesIfEmpty(gmeshData), nullIfEmpty(skeletonHash), nullIfEmpty(sourceLocation))
	if err != nil {
		return nil, fmt.Errorf("nock: create animation: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("nock: create animation: %w", err)
	}
	return s.GetAnimation(ctx, id)
}

// GetAnimation returns the full row, including its real blobs and manifest text.
func (s *AnimStore) GetAnimation(ctx context.Context, id int64) (*Animation, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, name, tick_rate, duration_ticks, num_channels, content_hash, gband_data, manifest_json, gskel_data, gmesh_data, skeleton_hash, source_location, created_at, updated_at
		 FROM nock_animations WHERE id = ?`, id)
	return scanAnimation(row)
}

func scanAnimation(row *sql.Row) (*Animation, error) {
	var a Animation
	var tickRate, durationTicks, numChannels sql.NullInt64
	var contentHash, manifestJSON, skeletonHash, sourceLocation sql.NullString
	var gband, gskel, gmesh []byte
	if err := row.Scan(&a.ID, &a.Name, &tickRate, &durationTicks, &numChannels, &contentHash,
		&gband, &manifestJSON, &gskel, &gmesh, &skeletonHash, &sourceLocation, &a.CreatedAt, &a.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("nock: animation not found")
		}
		return nil, fmt.Errorf("nock: get animation: %w", err)
	}
	if tickRate.Valid {
		v := int(tickRate.Int64)
		a.TickRate = &v
	}
	if durationTicks.Valid {
		v := int(durationTicks.Int64)
		a.DurationTicks = &v
	}
	if numChannels.Valid {
		v := int(numChannels.Int64)
		a.NumChannels = &v
	}
	a.ContentHash = contentHash.String
	a.ManifestJSON = manifestJSON.String
	a.GBandData = gband
	a.GSkelData = gskel
	a.GMeshData = gmesh
	a.SkeletonHash = skeletonHash.String
	a.SourceLocation = sourceLocation.String
	return &a, nil
}

// ListAnimations returns every character asset as a real, lightweight summary (no blobs/manifest
// text), newest first.
func (s *AnimStore) ListAnimations(ctx context.Context) ([]AnimationSummary, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, name, tick_rate, duration_ticks, num_channels, (gskel_data IS NOT NULL), (gmesh_data IS NOT NULL), (gband_data IS NOT NULL), skeleton_hash, source_location, created_at, updated_at
		 FROM nock_animations ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("nock: list animations: %w", err)
	}
	defer rows.Close()

	out := []AnimationSummary{}
	for rows.Next() {
		var s2 AnimationSummary
		var tickRate, durationTicks, numChannels sql.NullInt64
		var skeletonHash, sourceLocation sql.NullString
		if err := rows.Scan(&s2.ID, &s2.Name, &tickRate, &durationTicks, &numChannels, &s2.HasSkel, &s2.HasMesh, &s2.HasAnimation, &skeletonHash, &sourceLocation, &s2.CreatedAt, &s2.UpdatedAt); err != nil {
			return nil, fmt.Errorf("nock: list animations: %w", err)
		}
		if tickRate.Valid {
			v := int(tickRate.Int64)
			s2.TickRate = &v
		}
		if durationTicks.Valid {
			v := int(durationTicks.Int64)
			s2.DurationTicks = &v
		}
		if numChannels.Valid {
			v := int(numChannels.Int64)
			s2.NumChannels = &v
		}
		s2.SkeletonHash = skeletonHash.String
		s2.SourceLocation = sourceLocation.String
		out = append(out, s2)
	}
	return out, rows.Err()
}

// RenameAnimation updates an animation's own name in place.
func (s *AnimStore) RenameAnimation(ctx context.Context, id int64, newName string) (*Animation, error) {
	if err := ValidateName(newName); err != nil {
		return nil, err
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE nock_animations SET name = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, newName, id)
	if err != nil {
		return nil, fmt.Errorf("nock: rename animation: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("nock: animation %d not found", id)
	}
	return s.GetAnimation(ctx, id)
}

// CloneAnimation makes a real, full, independent copy of an existing animation under a new name
// -- same real bytes/manifest, a brand new id, no link back to the original (same "many
// independent masters, no parent_id" convention texture_store.go's own CloneTexture already
// uses).
func (s *AnimStore) CloneAnimation(ctx context.Context, id int64, newName string) (*Animation, error) {
	src, err := s.GetAnimation(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.CreateAnimation(ctx, newName, intOrZero(src.TickRate), intOrZero(src.DurationTicks), intOrZero(src.NumChannels), src.ContentHash, src.GBandData, src.ManifestJSON, src.GSkelData, src.GMeshData, src.SkeletonHash, src.SourceLocation)
}

// AttachAnimation (2026-09-17, founder real-time: "build fill in the gaps... you can add
// animations to it later, either by uploading a separate file with the same rig") merges real
// animation data onto an EXISTING row in place -- the real affordance NOCK's own animation-
// library copy already promised but never built. Real, deliberate design: this is a targeted
// UPDATE (id keeps its own identity, mesh/skeleton data untouched), not a clone -- attaching an
// animation to "the mannequin" should still BE the mannequin afterward, not spawn a new row.
//
// skeletonHash compatibility is checked when BOTH sides have one: an empty target or source
// skeleton_hash (a row created before this column existed, or a genuinely skeleton-less asset --
// see the migration's own "backfilled lazily, not retroactively" note) skips the check rather
// than refusing a real, honest attach just because older data predates this column existing.
func (s *AnimStore) AttachAnimation(ctx context.Context, id int64, gbandData []byte, manifestJSON string, tickRate, durationTicks, numChannels int, contentHash, skeletonHash string) (*Animation, error) {
	if len(gbandData) < 4 || string(gbandData[0:4]) != "GBND" {
		return nil, fmt.Errorf("nock: gband data has bad magic, expected a real .gband file starting with \"GBND\"")
	}
	target, err := s.GetAnimation(ctx, id)
	if err != nil {
		return nil, err
	}
	if target.SkeletonHash != "" && skeletonHash != "" && target.SkeletonHash != skeletonHash {
		return nil, fmt.Errorf("nock: this animation's rig doesn't match %q's own rig (skeleton_hash mismatch) -- attaching it would animate the wrong joints", target.Name)
	}
	newSkeletonHash := target.SkeletonHash
	if newSkeletonHash == "" {
		newSkeletonHash = skeletonHash
	}
	_, err = s.DB.ExecContext(ctx,
		`UPDATE nock_animations SET tick_rate = ?, duration_ticks = ?, num_channels = ?, content_hash = ?, gband_data = ?, manifest_json = ?, skeleton_hash = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		tickRate, durationTicks, numChannels, nullIfEmpty(contentHash), gbandData, manifestJSON, nullIfEmpty(newSkeletonHash), id)
	if err != nil {
		return nil, fmt.Errorf("nock: attach animation: %w", err)
	}
	return s.GetAnimation(ctx, id)
}

func intOrZero(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// DeleteAnimation permanently removes an animation row.
func (s *AnimStore) DeleteAnimation(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM nock_animations WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("nock: delete animation: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("nock: animation %d not found", id)
	}
	return nil
}

func nullBytesIfEmpty(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return b
}
