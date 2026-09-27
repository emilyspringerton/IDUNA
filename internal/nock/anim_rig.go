package nock

// anim_rig.go — the animation-library side of the animator and the rig-remapping primitives:
// decode a stored row into Skeleton/Mesh/Clip, and the three store-level operations the HTTP API
// exposes (author a keyframe clip on a rig, remap a mesh onto another row's rig, retarget a clip
// onto another row's rig). Each produces a NEW row (or, for an authored clip, optionally
// replaces the one being edited), following the library's "many independent masters"
// convention, so nothing a person imported is ever modified by these tools.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// DecodeAsset decodes whatever a row carries. Each return is nil when the row lacks that part.
func DecodeAsset(a *Animation) (*Skeleton, *Mesh, *Clip, error) {
	var (
		skel *Skeleton
		mesh *Mesh
		clip *Clip
		err  error
	)
	if len(a.GSkelData) > 0 {
		if skel, err = DecodeGSkel(a.GSkelData); err != nil {
			return nil, nil, nil, fmt.Errorf("%s: %w", a.Name, err)
		}
	}
	if len(a.GMeshData) > 0 {
		if mesh, err = DecodeGMesh(a.GMeshData); err != nil {
			return nil, nil, nil, fmt.Errorf("%s: %w", a.Name, err)
		}
	}
	if len(a.GBandData) > 0 {
		var mf struct {
			Channels []string `json:"channels"`
		}
		if err := json.Unmarshal([]byte(a.ManifestJSON), &mf); err != nil {
			return nil, nil, nil, fmt.Errorf("%s: manifest: %w", a.Name, err)
		}
		if clip, err = DecodeGBand(a.GBandData, mf.Channels); err != nil {
			return nil, nil, nil, fmt.Errorf("%s: %w", a.Name, err)
		}
	}
	return skel, mesh, clip, nil
}

// SetKeyframes stores a row's keyframe source.
func (s *AnimStore) SetKeyframes(ctx context.Context, id int64, keyframesJSON string) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE nock_animations SET keyframes_json = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, nullIfEmpty(keyframesJSON), id)
	if err != nil {
		return fmt.Errorf("nock: set keyframes: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("nock: animation %d not found", id)
	}
	return nil
}

// GetKeyframes returns a row's keyframe source, or "" if it has none (an imported clip).
func (s *AnimStore) GetKeyframes(ctx context.Context, id int64) (string, error) {
	var v sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT keyframes_json FROM nock_animations WHERE id = ?`, id).Scan(&v)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("nock: animation %d not found", id)
	}
	if err != nil {
		return "", fmt.Errorf("nock: get keyframes: %w", err)
	}
	return v.String, nil
}

// EditableKeyframes returns the row's keyframe doc: its stored source if it was authored in NOCK
// ("stored"), else keys derived from its baked clip every `every` ticks ("derived"), else an
// empty doc at the given defaults for a rig with no animation yet ("empty").
func (s *AnimStore) EditableKeyframes(ctx context.Context, id int64, every int) (*KeyframeDoc, string, error) {
	a, err := s.GetAnimation(ctx, id)
	if err != nil {
		return nil, "", err
	}
	skel, _, clip, err := DecodeAsset(a)
	if err != nil {
		return nil, "", err
	}
	if skel == nil {
		return nil, "", fmt.Errorf("nock: %q has no rig to animate", a.Name)
	}
	raw, err := s.GetKeyframes(ctx, id)
	if err != nil {
		return nil, "", err
	}
	if raw != "" {
		doc, err := ParseKeyframeDoc([]byte(raw), skel)
		if err != nil {
			return nil, "", err
		}
		return doc, "stored", nil
	}
	if clip != nil {
		doc, err := KeyframesFromClip(clip, skel, every)
		if err == nil {
			return doc, "derived", nil
		}
	}
	return &KeyframeDoc{Version: 1, TickRate: 30, DurationTicks: 60, Tracks: []KeyTrack{}}, "empty", nil
}

// AuthorClip bakes doc against rig row rigID's skeleton. With replace, the baked clip and its
// keyframe source overwrite rigID's own animation in place; otherwise a new row named name is
// created carrying rigID's rig (and mesh, if any) plus the new clip.
func (s *AnimStore) AuthorClip(ctx context.Context, rigID int64, name string, doc *KeyframeDoc, replace bool) (*Animation, error) {
	src, err := s.GetAnimation(ctx, rigID)
	if err != nil {
		return nil, err
	}
	skel, _, _, err := DecodeAsset(src)
	if err != nil {
		return nil, err
	}
	if skel == nil {
		return nil, fmt.Errorf("nock: %q has no rig to animate", src.Name)
	}
	clip, err := doc.Bake(skel)
	if err != nil {
		return nil, err
	}
	skelHash := skel.TopologyHashHex()
	gband, manifest, contentHash, err := clip.Encode(skelHash, "human", "nock animator")
	if err != nil {
		return nil, err
	}
	docJSON, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	var out *Animation
	if replace {
		if _, err := s.AttachAnimation(ctx, rigID, gband, manifest, clip.TickRate, clip.DurationTicks, len(clip.Channels), contentHash, skelHash); err != nil {
			return nil, err
		}
		out = src
	} else {
		if out, err = s.CreateAnimation(ctx, name, clip.TickRate, clip.DurationTicks, len(clip.Channels), contentHash, gband, manifest, src.GSkelData, src.GMeshData, skelHash, "nock animator"); err != nil {
			return nil, err
		}
	}
	if err := s.SetKeyframes(ctx, out.ID, string(docJSON)); err != nil {
		return nil, err
	}
	return s.GetAnimation(ctx, out.ID)
}

// BoneMapBetween reports the automatic bone map from row srcID's rig to row dstID's rig.
func (s *AnimStore) BoneMapBetween(ctx context.Context, srcID, dstID int64, overrides BoneMap) (BoneMapReport, error) {
	src, dst, err := s.twoRigs(ctx, srcID, dstID)
	if err != nil {
		return BoneMapReport{}, err
	}
	return AutoBoneMap(src, dst, overrides), nil
}

func (s *AnimStore) twoRigs(ctx context.Context, srcID, dstID int64) (*Skeleton, *Skeleton, error) {
	var out [2]*Skeleton
	for i, id := range []int64{srcID, dstID} {
		a, err := s.GetAnimation(ctx, id)
		if err != nil {
			return nil, nil, err
		}
		sk, _, _, err := DecodeAsset(a)
		if err != nil {
			return nil, nil, err
		}
		if sk == nil {
			return nil, nil, fmt.Errorf("nock: %q has no rig", a.Name)
		}
		out[i] = sk
	}
	return out[0], out[1], nil
}

// RemapMeshOnto re-skins row meshID's mesh onto row rigID's rig and stores the result as a new
// row named name (rigID's rig + the remapped mesh, no animation). The mesh row's own rig, if it
// has one, is the source for name-based mapping.
func (s *AnimStore) RemapMeshOnto(ctx context.Context, meshID, rigID int64, name string, opts RemapOptions) (*Animation, RemapReport, error) {
	ma, err := s.GetAnimation(ctx, meshID)
	if err != nil {
		return nil, RemapReport{}, err
	}
	srcSkel, mesh, _, err := DecodeAsset(ma)
	if err != nil {
		return nil, RemapReport{}, err
	}
	if mesh == nil {
		return nil, RemapReport{}, fmt.Errorf("nock: %q has no mesh to remap", ma.Name)
	}
	ra, err := s.GetAnimation(ctx, rigID)
	if err != nil {
		return nil, RemapReport{}, err
	}
	dstSkel, _, _, err := DecodeAsset(ra)
	if err != nil {
		return nil, RemapReport{}, err
	}
	if dstSkel == nil {
		return nil, RemapReport{}, fmt.Errorf("nock: %q has no rig to remap onto", ra.Name)
	}
	if srcSkel == nil && opts.Mode != RemapProximity {
		opts.Mode = RemapProximity
	}
	out, rep, err := RemapMesh(mesh, srcSkel, dstSkel, opts)
	if err != nil {
		return nil, rep, err
	}
	meshBytes, err := out.Encode()
	if err != nil {
		return nil, rep, err
	}
	row, err := s.CreateAnimation(ctx, name, 0, 0, 0, "", nil, "", ra.GSkelData, meshBytes, dstSkel.TopologyHashHex(), fmt.Sprintf("nock remap: %s onto %s (%s)", ma.Name, ra.Name, rep.Mode))
	return row, rep, err
}

// RetargetOnto re-expresses row clipID's animation on row rigID's rig and stores it as a new row
// named name (rigID's rig and mesh + the retargeted clip).
func (s *AnimStore) RetargetOnto(ctx context.Context, clipID, rigID int64, name string, overrides BoneMap) (*Animation, RetargetReport, error) {
	ca, err := s.GetAnimation(ctx, clipID)
	if err != nil {
		return nil, RetargetReport{}, err
	}
	srcSkel, _, clip, err := DecodeAsset(ca)
	if err != nil {
		return nil, RetargetReport{}, err
	}
	if clip == nil || srcSkel == nil {
		return nil, RetargetReport{}, fmt.Errorf("nock: %q needs both a rig and a clip to retarget from", ca.Name)
	}
	ra, err := s.GetAnimation(ctx, rigID)
	if err != nil {
		return nil, RetargetReport{}, err
	}
	dstSkel, _, _, err := DecodeAsset(ra)
	if err != nil {
		return nil, RetargetReport{}, err
	}
	if dstSkel == nil {
		return nil, RetargetReport{}, fmt.Errorf("nock: %q has no rig to retarget onto", ra.Name)
	}
	out, rep, err := RetargetClip(clip, srcSkel, dstSkel, overrides)
	if err != nil {
		return nil, rep, err
	}
	skelHash := dstSkel.TopologyHashHex()
	gband, manifest, contentHash, err := out.Encode(skelHash, "retargeted", fmt.Sprintf("nock retarget from %s", ca.Name))
	if err != nil {
		return nil, rep, err
	}
	row, err := s.CreateAnimation(ctx, name, out.TickRate, out.DurationTicks, len(out.Channels), contentHash, gband, manifest, ra.GSkelData, ra.GMeshData, skelHash, fmt.Sprintf("nock retarget: %s onto %s", ca.Name, ra.Name))
	return row, rep, err
}
