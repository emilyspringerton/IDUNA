package nock

// robot_store.go — NOCK's robot registry (founder real-time, 2026-09-27: "upgrade shankpit and
// nock to formal rigid body physics we need to get goldenband rigged up with real robot data
// from industrial data sheets"). Stores GOLDEN BAND datasheet robot rigs -- see
// GOLDENBAND/format/GROBOT_FORMAT.md for both file formats and
// migrations/truestore/202609270001_nock_robots.sql for the schema.
//
// The registry enforces provenance, not just storage:
//   - the compiled .grobot's own embedded spec_hash must equal sha256(spec JSON bytes), so the
//     physics rig in a row is provably the compilation of the cited spec in that same row;
//   - the spec must declare at least one source with a URL, and every link and joint must cite
//     a declared source id -- "real robot data from industrial data sheets", checked;
//   - joint names/count in the binary must match the spec, and every joint must carry a positive
//     datasheet speed and torque limit.
// ParseGRobot is a Go port of GOLDENBAND/src/grobot.c's reader (same fixed layout), kept here so
// IDUNA stays a standalone module with no GOLDENBAND dependency.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	grobotHeaderSize = 104
	grobotRecordSize = 280
	grobotMaxJoints  = 64
)

// RobotJoint is one decoded .grobot record (SI units: m, kg, kg*m^2, rad, rad/s, N*m).
type RobotJoint struct {
	Name             string     `json:"name"`
	ChildLink        string     `json:"child_link"`
	Parent           int        `json:"parent"`
	Continuous       bool       `json:"continuous"`
	OriginXYZ        [3]float64 `json:"origin_xyz"`
	Axis             [3]float64 `json:"axis"`
	Lower            float64    `json:"lower"`
	Upper            float64    `json:"upper"`
	Velocity         float64    `json:"velocity"`
	Effort           float64    `json:"effort"`
	Damping          float64    `json:"damping"`
	Mass             float64    `json:"mass"`
	COM              [3]float64 `json:"com"`
	PrincipalMoments [3]float64 `json:"principal_moments"`
}

// GRobotInfo is a decoded .grobot file.
type GRobotInfo struct {
	Name     string       `json:"name"`
	TCPJoint int          `json:"tcp_joint"`
	SpecHash string       `json:"spec_hash"`
	Joints   []RobotJoint `json:"joints"`
}

func f64(b []byte) float64 { return math.Float64frombits(binary.LittleEndian.Uint64(b)) }

func cstr(b []byte) string {
	if i := strings.IndexByte(string(b), 0); i >= 0 {
		return string(b[:i])
	}
	return string(b)
}

// ParseGRobot decodes and structurally validates a compiled .grobot file.
func ParseGRobot(data []byte) (*GRobotInfo, error) {
	if len(data) < grobotHeaderSize || string(data[0:4]) != "GRBT" {
		return nil, fmt.Errorf("nock: not a .grobot file (bad magic)")
	}
	le := binary.LittleEndian
	if v := le.Uint32(data[4:8]); v != 1 {
		return nil, fmt.Errorf("nock: .grobot version %d unsupported", v)
	}
	n := int(le.Uint32(data[8:12]))
	if n < 1 || n > grobotMaxJoints {
		return nil, fmt.Errorf("nock: .grobot joint_count %d out of range 1..%d", n, grobotMaxJoints)
	}
	if len(data) != grobotHeaderSize+n*grobotRecordSize {
		return nil, fmt.Errorf("nock: .grobot size %d does not match %d joints", len(data), n)
	}
	info := &GRobotInfo{
		Name:     cstr(data[40:72]),
		TCPJoint: int(int32(le.Uint32(data[12:16]))),
		SpecHash: hex.EncodeToString(data[72:104]),
	}
	for i := 0; i < n; i++ {
		r := data[grobotHeaderSize+i*grobotRecordSize : grobotHeaderSize+(i+1)*grobotRecordSize]
		j := RobotJoint{
			Name:       cstr(r[0:32]),
			ChildLink:  cstr(r[32:64]),
			Parent:     int(int32(le.Uint32(r[64:68]))),
			Continuous: le.Uint32(r[68:72]) == 2,
		}
		for k := 0; k < 3; k++ {
			j.OriginXYZ[k] = f64(r[72+8*k:])
			j.Axis[k] = f64(r[128+8*k:])
			j.COM[k] = f64(r[200+8*k:])
			j.PrincipalMoments[k] = f64(r[256+8*k:])
		}
		j.Lower, j.Upper, j.Velocity = f64(r[152:]), f64(r[160:]), f64(r[168:])
		j.Effort, j.Damping, j.Mass = f64(r[176:]), f64(r[184:]), f64(r[192:])
		if j.Parent < -1 || j.Parent >= i {
			return nil, fmt.Errorf("nock: .grobot joint %d (%s) has parent %d, must be -1..%d", i, j.Name, j.Parent, i-1)
		}
		if !(j.Mass > 0) || !(j.Velocity > 0) || !(j.Effort > 0) {
			return nil, fmt.Errorf("nock: .grobot joint %s needs positive mass, datasheet speed and torque limits", j.Name)
		}
		info.Joints = append(info.Joints, j)
	}
	if info.TCPJoint < -1 || info.TCPJoint >= n {
		return nil, fmt.Errorf("nock: .grobot tcp_joint %d out of range", info.TCPJoint)
	}
	return info, nil
}

// robotSpecCheck is the subset of a .grobot.json spec the registry validates.
type robotSpecCheck struct {
	GRobotVersion int    `json:"grobot_version"`
	Name          string `json:"name"`
	Manufacturer  string `json:"manufacturer"`
	Model         string `json:"model"`
	Sources       []struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	} `json:"sources"`
	Links []struct {
		Name   string `json:"name"`
		Source string `json:"source"`
	} `json:"links"`
	Joints []struct {
		Name   string `json:"name"`
		Source string `json:"source"`
	} `json:"joints"`
}

// ValidateRobotUpload checks a spec + compiled rig pair; returns the decoded rig and the parsed
// spec header on success.
func ValidateRobotUpload(specJSON, grobot []byte) (*GRobotInfo, *robotSpecCheck, error) {
	var spec robotSpecCheck
	if err := json.Unmarshal(specJSON, &spec); err != nil {
		return nil, nil, fmt.Errorf("nock: robot spec is not valid JSON: %w", err)
	}
	if spec.GRobotVersion != 1 || spec.Name == "" || spec.Manufacturer == "" || spec.Model == "" {
		return nil, nil, fmt.Errorf("nock: robot spec needs grobot_version 1, name, manufacturer and model")
	}
	sources := map[string]bool{}
	for _, s := range spec.Sources {
		if s.ID == "" || !(strings.HasPrefix(s.URL, "https://") || strings.HasPrefix(s.URL, "http://")) {
			return nil, nil, fmt.Errorf("nock: every robot spec source needs an id and a URL (got %q)", s.ID)
		}
		sources[s.ID] = true
	}
	if len(sources) == 0 {
		return nil, nil, fmt.Errorf("nock: robot spec cites no sources -- datasheet provenance is required")
	}
	for _, l := range spec.Links {
		if !sources[l.Source] {
			return nil, nil, fmt.Errorf("nock: link %q cites undeclared source %q", l.Name, l.Source)
		}
	}
	for _, j := range spec.Joints {
		if !sources[j.Source] {
			return nil, nil, fmt.Errorf("nock: joint %q cites undeclared source %q", j.Name, j.Source)
		}
	}
	info, err := ParseGRobot(grobot)
	if err != nil {
		return nil, nil, err
	}
	sum := sha256.Sum256(specJSON)
	if info.SpecHash != hex.EncodeToString(sum[:]) {
		return nil, nil, fmt.Errorf("nock: .grobot was not compiled from this spec (embedded spec_hash %s..., spec sha256 %s...) -- recompile with `gbtool robot compile`", info.SpecHash[:12], hex.EncodeToString(sum[:])[:12])
	}
	if len(info.Joints) != len(spec.Joints) {
		return nil, nil, fmt.Errorf("nock: .grobot has %d joints, spec has %d", len(info.Joints), len(spec.Joints))
	}
	for i, j := range info.Joints {
		if j.Name != spec.Joints[i].Name {
			return nil, nil, fmt.Errorf("nock: .grobot joint %d is %q, spec says %q", i, j.Name, spec.Joints[i].Name)
		}
	}
	return info, &spec, nil
}

// Robot is one nock_robots row.
type Robot struct {
	ID             int64       `json:"id"`
	Name           string      `json:"name"`
	Manufacturer   string      `json:"manufacturer"`
	Model          string      `json:"model"`
	JointCount     int         `json:"joint_count"`
	MovingMassKg   float64     `json:"moving_mass_kg"`
	SpecHash       string      `json:"spec_hash"`
	SpecJSON       string      `json:"spec_json,omitempty"`
	GRobotData     []byte      `json:"-"`
	GSkelData      []byte      `json:"-"`
	HasSkel        bool        `json:"has_skel"`
	Rig            *GRobotInfo `json:"rig,omitempty"` // decoded on GET, never stored separately
	SourceLocation string      `json:"source_location,omitempty"`
	CreatedAt      string      `json:"created_at"`
	UpdatedAt      string      `json:"updated_at"`
}

type RobotStore struct {
	DB *sql.DB
}

// CreateRobot validates and inserts a robot rig. gskel may be nil.
func (s *RobotStore) CreateRobot(ctx context.Context, specJSON, grobot, gskel []byte, sourceLocation string) (*Robot, error) {
	info, spec, err := ValidateRobotUpload(specJSON, grobot)
	if err != nil {
		return nil, err
	}
	if err := ValidateName(spec.Name); err != nil {
		return nil, err
	}
	if len(gskel) > 0 && (len(gskel) < 4 || string(gskel[0:4]) != "GSKL") {
		return nil, fmt.Errorf("nock: gskel data has bad magic, expected \"GSKL\"")
	}
	mass := 0.0
	for _, j := range info.Joints {
		mass += j.Mass
	}
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO nock_robots (name, manufacturer, model, joint_count, moving_mass_kg, spec_hash, spec_json, grobot_data, gskel_data, source_location)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		spec.Name, spec.Manufacturer, spec.Model, len(info.Joints), mass, info.SpecHash, string(specJSON), grobot,
		nullBytesIfEmpty(gskel), nullIfEmpty(sourceLocation))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, fmt.Errorf("nock: a robot named %q already exists", spec.Name)
		}
		return nil, fmt.Errorf("nock: create robot: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("nock: create robot: %w", err)
	}
	return s.GetRobot(ctx, id)
}

const robotCols = `id, name, manufacturer, model, joint_count, moving_mass_kg, spec_hash, spec_json, grobot_data, gskel_data, source_location, created_at, updated_at`

func scanRobot(sc interface{ Scan(...any) error }) (*Robot, error) {
	var r Robot
	var src sql.NullString
	if err := sc.Scan(&r.ID, &r.Name, &r.Manufacturer, &r.Model, &r.JointCount, &r.MovingMassKg, &r.SpecHash,
		&r.SpecJSON, &r.GRobotData, &r.GSkelData, &src, &r.CreatedAt, &r.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("nock: robot not found")
		}
		return nil, fmt.Errorf("nock: get robot: %w", err)
	}
	r.SourceLocation = src.String
	r.HasSkel = len(r.GSkelData) > 0
	return &r, nil
}

// GetRobot returns the full row with its decoded rig attached.
func (s *RobotStore) GetRobot(ctx context.Context, id int64) (*Robot, error) {
	r, err := scanRobot(s.DB.QueryRowContext(ctx, `SELECT `+robotCols+` FROM nock_robots WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	if r.Rig, err = ParseGRobot(r.GRobotData); err != nil {
		return nil, err
	}
	return r, nil
}

// ListRobots returns lightweight rows (no spec text, no blobs, no decoded rig).
func (s *RobotStore) ListRobots(ctx context.Context) ([]Robot, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+robotCols+` FROM nock_robots ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("nock: list robots: %w", err)
	}
	defer rows.Close()
	out := []Robot{}
	for rows.Next() {
		r, err := scanRobot(rows)
		if err != nil {
			return nil, err
		}
		r.SpecJSON, r.GRobotData, r.GSkelData = "", nil, nil
		out = append(out, *r)
	}
	return out, rows.Err()
}

func (s *RobotStore) DeleteRobot(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM nock_robots WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("nock: delete robot: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("nock: robot not found")
	}
	return nil
}

// ---------------------------------------------------------------- git -> registry sync

// Founder real-time, 2026-09-27: "build in the affordances that slurp it into the database". The
// robot data's source of truth is git (GOLDENBAND: robots/<name>.grobot.json specs, compiled
// assets/robots/<name>.grobot + .gskel); this pulls a GOLDENBAND working tree into nock_robots.
// Called from three places that share this one function: IDUNA startup (automatic), POST
// /admin/nock/api/robots/sync (NOCK's "Sync from git" button), and `nock robots-sync` (CLI).
//
// Rules: a robot is (re)imported only if it passes the same provenance checks as an upload.
// Rows the sync created are marked source_location "git:..." and are updated in place when git
// changes; a row with the same name that was uploaded by hand is never overwritten (reported as
// skipped). Robots removed from git are left in the database (reported, not deleted). The sync
// only reads the working tree -- it never pulls, fetches or modifies the git checkout.

type RobotSyncItem struct {
	Name   string `json:"name"`
	Action string `json:"action"` // created | updated | unchanged | skipped | error
	Detail string `json:"detail,omitempty"`
}

type RobotSyncReport struct {
	Dir      string          `json:"dir"`
	Revision string          `json:"revision,omitempty"`
	Items    []RobotSyncItem `json:"items"`
	NotInGit []string        `json:"not_in_git,omitempty"` // git-sourced rows whose spec is gone from the tree
}

// gitRevision returns the checkout's HEAD commit, read straight from .git (no git binary needed).
func gitRevision(dir string) string {
	head, err := os.ReadFile(filepath.Join(dir, ".git", "HEAD"))
	if err != nil {
		return ""
	}
	h := strings.TrimSpace(string(head))
	if !strings.HasPrefix(h, "ref: ") {
		return h
	}
	ref := strings.TrimPrefix(h, "ref: ")
	if b, err := os.ReadFile(filepath.Join(dir, ".git", filepath.FromSlash(ref))); err == nil {
		return strings.TrimSpace(string(b))
	}
	if packed, err := os.ReadFile(filepath.Join(dir, ".git", "packed-refs")); err == nil {
		for _, line := range strings.Split(string(packed), "\n") {
			if f := strings.Fields(line); len(f) == 2 && f[1] == ref {
				return f[0]
			}
		}
	}
	return ""
}

func (s *RobotStore) updateRobot(ctx context.Context, id int64, specJSON, grobot, gskel []byte, sourceLocation string) error {
	info, spec, err := ValidateRobotUpload(specJSON, grobot)
	if err != nil {
		return err
	}
	mass := 0.0
	for _, j := range info.Joints {
		mass += j.Mass
	}
	_, err = s.DB.ExecContext(ctx,
		`UPDATE nock_robots SET manufacturer = ?, model = ?, joint_count = ?, moving_mass_kg = ?, spec_hash = ?, spec_json = ?,
		 grobot_data = ?, gskel_data = ?, source_location = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		spec.Manufacturer, spec.Model, len(info.Joints), mass, info.SpecHash, string(specJSON), grobot,
		nullBytesIfEmpty(gskel), nullIfEmpty(sourceLocation), id)
	if err != nil {
		return fmt.Errorf("nock: update robot: %w", err)
	}
	return nil
}

// SyncFromGit imports every robot in a GOLDENBAND working tree at dir.
func (s *RobotStore) SyncFromGit(ctx context.Context, dir string) (*RobotSyncReport, error) {
	specs, err := filepath.Glob(filepath.Join(dir, "robots", "*.grobot.json"))
	if err != nil {
		return nil, err
	}
	if len(specs) == 0 {
		return nil, fmt.Errorf("nock: no robots/*.grobot.json under %s -- is this a GOLDENBAND checkout (with the robot data merged)?", dir)
	}
	sort.Strings(specs)
	rep := &RobotSyncReport{Dir: dir, Revision: gitRevision(dir)}
	source := "git:GOLDENBAND"
	if rep.Revision != "" {
		source += "@" + rep.Revision[:min(12, len(rep.Revision))]
	}
	existing, err := s.ListRobots(ctx)
	if err != nil {
		return nil, err
	}
	byName := map[string]Robot{}
	for _, r := range existing {
		byName[r.Name] = r
	}
	inGit := map[string]bool{}
	for _, specPath := range specs {
		name := strings.TrimSuffix(filepath.Base(specPath), ".grobot.json")
		inGit[name] = true
		item := RobotSyncItem{Name: name}
		spec, err1 := os.ReadFile(specPath)
		grobot, err2 := os.ReadFile(filepath.Join(dir, "assets", "robots", name+".grobot"))
		gskel, _ := os.ReadFile(filepath.Join(dir, "assets", "robots", name+".gskel")) // optional
		switch {
		case err1 != nil:
			item.Action, item.Detail = "error", err1.Error()
		case err2 != nil:
			item.Action, item.Detail = "error", "no compiled assets/robots/"+name+".grobot (run GOLDENBAND scripts/robot_assets.sh)"
		default:
			row, have := byName[name]
			switch {
			case !have:
				if _, err := s.CreateRobot(ctx, spec, grobot, gskel, source); err != nil {
					item.Action, item.Detail = "error", err.Error()
				} else {
					item.Action = "created"
				}
			case !strings.HasPrefix(row.SourceLocation, "git:"):
				item.Action, item.Detail = "skipped", "a hand-uploaded robot already uses this name; git never overwrites uploads"
			default:
				full, err := s.GetRobot(ctx, row.ID)
				if err != nil {
					item.Action, item.Detail = "error", err.Error()
					break
				}
				if full.SpecJSON == string(spec) && bytes.Equal(full.GRobotData, grobot) && bytes.Equal(full.GSkelData, gskel) {
					item.Action = "unchanged"
					break
				}
				if err := s.updateRobot(ctx, row.ID, spec, grobot, gskel, source); err != nil {
					item.Action, item.Detail = "error", err.Error()
				} else {
					item.Action = "updated"
				}
			}
		}
		rep.Items = append(rep.Items, item)
	}
	for _, r := range existing {
		if strings.HasPrefix(r.SourceLocation, "git:") && !inGit[r.Name] {
			rep.NotInGit = append(rep.NotInGit, r.Name)
		}
	}
	return rep, nil
}
