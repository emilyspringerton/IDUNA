package nock

// sound_store.go -- CRUD + SQLite persistence for NOCK's sound library and its shareable filter
// chains (founder real-time, 2026-09-27: "nock and shankpit engine need sound engineering
// primatives we need a way to upload and record in nock as well as pass filters around").
// Same BLOB-in-SQLite shape as texture_store.go/anim_store.go; schema and rationale in
// migrations/truestore/202609270001_nock_sounds.sql.
//
// The audio processing itself runs where the listener is: NOCK's browser UI applies a chain with
// TypeScript compiled from PARENA stdlib/audio/dsp.prn, and SHANKPIT applies the same chain with
// C compiled from the same file. This store therefore validates a chain's SHAPE and parameter
// ranges (so a chain saved here is one every consumer can run) but never renders audio itself.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// MaxSoundBytes caps one stored sound (a ~6 minute 48 kHz stereo 16-bit WAV).
const MaxSoundBytes = 64 << 20

// Sound is one nock_sounds row. AudioData is never inlined into JSON; it streams from
// .../sounds/:id/audio.
type Sound struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	MimeType    string `json:"mime_type"`
	Source      string `json:"source"`
	DurationMS  int64  `json:"duration_ms"`
	SampleRate  int64  `json:"sample_rate"`
	Channels    int64  `json:"channels"`
	SizeBytes   int64  `json:"size_bytes"`
	ContentHash string `json:"content_hash"`
	AudioData   []byte `json:"-"`
	ParentID    *int64 `json:"parent_id,omitempty"`
	FilterID    *int64 `json:"filter_id,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// SoundMeta is what a caller supplies alongside the raw audio bytes.
type SoundMeta struct {
	Name       string
	MimeType   string
	Source     string
	DurationMS int64
	SampleRate int64
	Channels   int64
	ParentID   *int64
	FilterID   *int64
}

// SoundFilter is one named, shareable filter chain.
type SoundFilter struct {
	ID          int64           `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Chain       json.RawMessage `json:"chain"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
}

// SoundStore is the SQLite-backed layer; DB must already carry the migration's tables.
type SoundStore struct {
	DB *sql.DB
}

var validSoundSources = map[string]bool{"upload": true, "record": true, "render": true}

func validAudioMime(m string) bool {
	m = strings.ToLower(strings.TrimSpace(strings.SplitN(m, ";", 2)[0]))
	switch m {
	case "audio/wav", "audio/x-wav", "audio/wave", "audio/vnd.wave", "audio/flac", "audio/x-flac",
		"audio/mpeg", "audio/mp3", "audio/ogg", "audio/opus", "audio/webm", "audio/mp4", "audio/aac",
		"video/webm": // Chrome's MediaRecorder labels audio-only recordings video/webm in some builds
		return true
	}
	return false
}

// ---- sounds ----

const soundCols = `id, name, mime_type, source, duration_ms, sample_rate, channels, size_bytes,
	content_hash, parent_id, filter_id, created_at, updated_at`

func scanSound(row interface{ Scan(...any) error }, withData bool) (*Sound, error) {
	s := &Sound{}
	var parent, filter sql.NullInt64
	dest := []any{&s.ID, &s.Name, &s.MimeType, &s.Source, &s.DurationMS, &s.SampleRate, &s.Channels,
		&s.SizeBytes, &s.ContentHash, &parent, &filter, &s.CreatedAt, &s.UpdatedAt}
	if withData {
		dest = append(dest, &s.AudioData)
	}
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	if parent.Valid {
		s.ParentID = &parent.Int64
	}
	if filter.Valid {
		s.FilterID = &filter.Int64
	}
	return s, nil
}

// CreateSound stores audio bytes exactly as given.
func (st *SoundStore) CreateSound(ctx context.Context, meta SoundMeta, data []byte) (*Sound, error) {
	if err := ValidateName(meta.Name); err != nil {
		return nil, err
	}
	if !validSoundSources[meta.Source] {
		return nil, fmt.Errorf("nock: invalid sound source %q (upload|record|render)", meta.Source)
	}
	if !validAudioMime(meta.MimeType) {
		return nil, fmt.Errorf("nock: unsupported audio type %q", meta.MimeType)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("nock: empty audio")
	}
	if len(data) > MaxSoundBytes {
		return nil, fmt.Errorf("nock: audio is %d bytes, max %d", len(data), MaxSoundBytes)
	}
	if meta.DurationMS < 0 || meta.SampleRate < 0 || meta.Channels < 0 {
		return nil, fmt.Errorf("nock: negative duration/sample_rate/channels")
	}
	sum := sha256.Sum256(data)
	res, err := st.DB.ExecContext(ctx, `INSERT INTO nock_sounds
		(name, mime_type, source, duration_ms, sample_rate, channels, size_bytes, content_hash, audio_data, parent_id, filter_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		meta.Name, meta.MimeType, meta.Source, meta.DurationMS, meta.SampleRate, meta.Channels,
		len(data), hex.EncodeToString(sum[:]), data, meta.ParentID, meta.FilterID)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, fmt.Errorf("nock: a sound named %q already exists", meta.Name)
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return st.GetSound(ctx, id)
}

// GetSound returns metadata only.
func (st *SoundStore) GetSound(ctx context.Context, id int64) (*Sound, error) {
	s, err := scanSound(st.DB.QueryRowContext(ctx, `SELECT `+soundCols+` FROM nock_sounds WHERE id = ?`, id), false)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("nock: sound %d not found", id)
	}
	return s, err
}

// GetSoundAudio returns metadata plus the audio bytes.
func (st *SoundStore) GetSoundAudio(ctx context.Context, id int64) (*Sound, error) {
	s, err := scanSound(st.DB.QueryRowContext(ctx, `SELECT `+soundCols+`, audio_data FROM nock_sounds WHERE id = ?`, id), true)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("nock: sound %d not found", id)
	}
	return s, err
}

func (st *SoundStore) ListSounds(ctx context.Context) ([]Sound, error) {
	rows, err := st.DB.QueryContext(ctx, `SELECT `+soundCols+` FROM nock_sounds ORDER BY updated_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Sound{}
	for rows.Next() {
		s, err := scanSound(rows, false)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

func (st *SoundStore) RenameSound(ctx context.Context, id int64, name string) (*Sound, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	res, err := st.DB.ExecContext(ctx, `UPDATE nock_sounds SET name = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, name, id)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, fmt.Errorf("nock: a sound named %q already exists", name)
		}
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("nock: sound %d not found", id)
	}
	return st.GetSound(ctx, id)
}

func (st *SoundStore) DeleteSound(ctx context.Context, id int64) error {
	res, err := st.DB.ExecContext(ctx, `DELETE FROM nock_sounds WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("nock: sound %d not found", id)
	}
	return nil
}

// ---- filter chains ----

// FilterChain is the portable chain format (version 1). Every stage's parameters map 1:1 onto
// PARENA stdlib/audio/dsp.prn functions; see frontend/nock/src/sound/chain.ts (TS runner) and
// SHANKPIT packages/audio_dsp (C runner).
type FilterChain struct {
	Version int           `json:"version"`
	Stages  []FilterStage `json:"stages"`
}

// FilterStage is a flat parameter bag; which fields matter depends on Type.
type FilterStage struct {
	Type        string   `json:"type"`
	Bypass      bool     `json:"bypass,omitempty"`
	Kind        *int     `json:"kind,omitempty"` // biquad kind id (dsp.prn): 0 LP .. 6 HIGHSHELF
	Freq        *float64 `json:"freq,omitempty"`
	Q           *float64 `json:"q,omitempty"`
	GainDB      *float64 `json:"gain_db,omitempty"`
	ThresholdDB *float64 `json:"threshold_db,omitempty"`
	Ratio       *float64 `json:"ratio,omitempty"`
	KneeDB      *float64 `json:"knee_db,omitempty"`
	RangeDB     *float64 `json:"range_db,omitempty"`
	AttackMS    *float64 `json:"attack_ms,omitempty"`
	ReleaseMS   *float64 `json:"release_ms,omitempty"`
	MakeupDB    *float64 `json:"makeup_db,omitempty"`
	Intensity   *float64 `json:"intensity,omitempty"`
	CeilingDB   *float64 `json:"ceiling_db,omitempty"`
	TargetLUFS  *float64 `json:"target_lufs,omitempty"`
	Mix         *float64 `json:"mix,omitempty"`
}

// MaxChainStages bounds a chain so a shared chain can't be a CPU bomb for a game server.
const MaxChainStages = 32

type rangeRule struct {
	name     string
	v        *float64
	lo, hi   float64
	required bool
}

func checkRanges(i int, typ string, rules ...rangeRule) error {
	for _, r := range rules {
		if r.v == nil {
			if r.required {
				return fmt.Errorf("stage %d (%s): %s is required", i, typ, r.name)
			}
			continue
		}
		if math.IsNaN(*r.v) || math.IsInf(*r.v, 0) || *r.v < r.lo || *r.v > r.hi {
			return fmt.Errorf("stage %d (%s): %s=%v out of range [%v, %v]", i, typ, r.name, *r.v, r.lo, r.hi)
		}
	}
	return nil
}

// ValidateChain parses and range-checks a chain, returning its canonical JSON.
func ValidateChain(raw []byte) ([]byte, error) {
	var c FilterChain
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("nock: invalid filter chain JSON: %w", err)
	}
	if c.Version != 1 {
		return nil, fmt.Errorf("nock: filter chain version must be 1")
	}
	if len(c.Stages) > MaxChainStages {
		return nil, fmt.Errorf("nock: filter chain has %d stages, max %d", len(c.Stages), MaxChainStages)
	}
	mix := func(s FilterStage) rangeRule { return rangeRule{"mix", s.Mix, 0, 1, false} }
	for i, s := range c.Stages {
		var err error
		switch s.Type {
		case "biquad":
			if s.Kind == nil || *s.Kind < 0 || *s.Kind > 6 {
				return nil, fmt.Errorf("nock: stage %d (biquad): kind must be 0..6", i)
			}
			err = checkRanges(i, s.Type, rangeRule{"freq", s.Freq, 10, 24000, true},
				rangeRule{"q", s.Q, 0.1, 40, false}, rangeRule{"gain_db", s.GainDB, -48, 24, false}, mix(s))
		case "compressor":
			err = checkRanges(i, s.Type, rangeRule{"threshold_db", s.ThresholdDB, -80, 0, true},
				rangeRule{"ratio", s.Ratio, 1, 40, true}, rangeRule{"knee_db", s.KneeDB, 0, 24, false},
				rangeRule{"attack_ms", s.AttackMS, 0, 500, false}, rangeRule{"release_ms", s.ReleaseMS, 1, 5000, false},
				rangeRule{"makeup_db", s.MakeupDB, -24, 24, false}, mix(s))
		case "expander":
			err = checkRanges(i, s.Type, rangeRule{"threshold_db", s.ThresholdDB, -100, 0, true},
				rangeRule{"ratio", s.Ratio, 1, 40, true}, rangeRule{"knee_db", s.KneeDB, 0, 24, false},
				rangeRule{"range_db", s.RangeDB, -120, 0, false}, rangeRule{"attack_ms", s.AttackMS, 0, 500, false},
				rangeRule{"release_ms", s.ReleaseMS, 1, 5000, false})
		case "deesser":
			err = checkRanges(i, s.Type, rangeRule{"freq", s.Freq, 2000, 16000, true},
				rangeRule{"intensity", s.Intensity, 0, 1, true}, rangeRule{"attack_ms", s.AttackMS, 0, 100, false},
				rangeRule{"release_ms", s.ReleaseMS, 1, 1000, false})
		case "limiter":
			err = checkRanges(i, s.Type, rangeRule{"ceiling_db", s.CeilingDB, -24, 0, true},
				rangeRule{"release_ms", s.ReleaseMS, 1, 2000, false})
		case "gain":
			err = checkRanges(i, s.Type, rangeRule{"gain_db", s.GainDB, -60, 24, true})
		case "normalize":
			err = checkRanges(i, s.Type, rangeRule{"target_lufs", s.TargetLUFS, -40, -5, true},
				rangeRule{"ceiling_db", s.CeilingDB, -12, 0, false})
		default:
			return nil, fmt.Errorf("nock: stage %d: unknown type %q (biquad|compressor|expander|deesser|limiter|gain|normalize)", i, s.Type)
		}
		if err != nil {
			return nil, fmt.Errorf("nock: %w", err)
		}
	}
	return json.Marshal(c)
}

func scanFilter(row interface{ Scan(...any) error }) (*SoundFilter, error) {
	f := &SoundFilter{}
	var chain string
	if err := row.Scan(&f.ID, &f.Name, &f.Description, &chain, &f.CreatedAt, &f.UpdatedAt); err != nil {
		return nil, err
	}
	f.Chain = json.RawMessage(chain)
	return f, nil
}

const filterCols = `id, name, description, chain_json, created_at, updated_at`

func (st *SoundStore) CreateFilter(ctx context.Context, name, description string, chain []byte) (*SoundFilter, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	canon, err := ValidateChain(chain)
	if err != nil {
		return nil, err
	}
	res, err := st.DB.ExecContext(ctx, `INSERT INTO nock_sound_filters (name, description, chain_json) VALUES (?, ?, ?)`,
		name, description, string(canon))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, fmt.Errorf("nock: a filter chain named %q already exists", name)
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return st.GetFilter(ctx, id)
}

func (st *SoundStore) GetFilter(ctx context.Context, id int64) (*SoundFilter, error) {
	f, err := scanFilter(st.DB.QueryRowContext(ctx, `SELECT `+filterCols+` FROM nock_sound_filters WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("nock: filter chain %d not found", id)
	}
	return f, err
}

func (st *SoundStore) GetFilterByName(ctx context.Context, name string) (*SoundFilter, error) {
	f, err := scanFilter(st.DB.QueryRowContext(ctx, `SELECT `+filterCols+` FROM nock_sound_filters WHERE name = ?`, name))
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("nock: filter chain %q not found", name)
	}
	return f, err
}

func (st *SoundStore) ListFilters(ctx context.Context) ([]SoundFilter, error) {
	rows, err := st.DB.QueryContext(ctx, `SELECT `+filterCols+` FROM nock_sound_filters ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SoundFilter{}
	for rows.Next() {
		f, err := scanFilter(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *f)
	}
	return out, rows.Err()
}

// UpdateFilter replaces any of name/description/chain that are non-nil.
func (st *SoundStore) UpdateFilter(ctx context.Context, id int64, name, description *string, chain []byte) (*SoundFilter, error) {
	cur, err := st.GetFilter(ctx, id)
	if err != nil {
		return nil, err
	}
	newName, newDesc, newChain := cur.Name, cur.Description, []byte(cur.Chain)
	if name != nil {
		if err := ValidateName(*name); err != nil {
			return nil, err
		}
		newName = *name
	}
	if description != nil {
		newDesc = *description
	}
	if chain != nil {
		if newChain, err = ValidateChain(chain); err != nil {
			return nil, err
		}
	}
	if _, err := st.DB.ExecContext(ctx, `UPDATE nock_sound_filters SET name = ?, description = ?, chain_json = ?,
		updated_at = CURRENT_TIMESTAMP WHERE id = ?`, newName, newDesc, string(newChain), id); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, fmt.Errorf("nock: a filter chain named %q already exists", newName)
		}
		return nil, err
	}
	return st.GetFilter(ctx, id)
}

func (st *SoundStore) CloneFilter(ctx context.Context, id int64, name string) (*SoundFilter, error) {
	cur, err := st.GetFilter(ctx, id)
	if err != nil {
		return nil, err
	}
	return st.CreateFilter(ctx, name, cur.Description, cur.Chain)
}

func (st *SoundStore) DeleteFilter(ctx context.Context, id int64) error {
	res, err := st.DB.ExecContext(ctx, `DELETE FROM nock_sound_filters WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("nock: filter chain %d not found", id)
	}
	return nil
}
