// Package deckstats aggregates DEADWEIGHT's draft deck log (dw_server --match-log DIR/decks.ndjson) into per-deck and
// per-card win rates for WOTAN's public deck browser, and picks the strongest decks for the constructed-mode bots. The
// log is append-only ndjson with two record kinds:
//
//	{"event":"draft","deck_id":N,"t":unix,"player":"..","kind":0|1,"cards":[23 ids],"names":[..]}
//	{"event":"match","deck_id":N,"match_id":M,"player":"..","kind":0|1,"result":"win|loss|draw","rounds":R}
//
// The log is big and keeps growing: the standing bot pool drafts ~46,000 decks and plays ~110,000 deck-games a day, so the
// file was 882 MiB after 26 days. The first version of this package read whatever it had not seen with io.ReadAll and kept
// every deck in memory, which turned the first request after any IDUNA start into a 900 MB allocation and an OOM kill
// (the pod limit is 768 MiB) -- the "WOTAN draft decks tracker isn't working" bug. This version never holds more than one
// batch of lines: it tails the file in bounded batches into a SQLite read model (deck + card_stat tables) and remembers
// how far it got, so a restart resumes where it stopped instead of re-reading the whole file, and every query is an
// indexed read. It resets (and re-reads) if the log shrinks or its first line changes -- a rotated or replaced log.
package deckstats

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

// Deck is one drafted deck and its results so far.
type Deck struct {
	ID      uint32  `json:"deck_id"`
	Player  string  `json:"player"`
	Kind    int     `json:"kind"` // 0 human, 1 bot
	Created int64   `json:"created"`
	Cards   []int   `json:"cards"` // 23 card ids in pick order (duplicates = copies)
	Games   int     `json:"games"`
	Wins    int     `json:"wins"`
	Losses  int     `json:"losses"`
	Draws   int     `json:"draws"`
	WinRate float64 `json:"win_rate"` // wins / games (draws are not wins); 0 with no games
	Score   float64 `json:"score"`    // 95% Wilson lower bound of win_rate: ranks a 9-1 deck above a 2-0 deck
}

// wilson is the lower bound of the 95% Wilson score interval for wins out of games.
func wilson(wins, games int) float64 {
	if games == 0 {
		return 0
	}
	const z = 1.96
	n, p := float64(games), float64(wins)/float64(games)
	return (p + z*z/(2*n) - z*math.Sqrt((p*(1-p)+z*z/(4*n))/n)) / (1 + z*z/n)
}

// CardStat is a card's record across every deck that ran it.
type CardStat struct {
	Card    int     `json:"card"`
	Decks   int     `json:"decks"`  // decks containing the card
	Copies  int     `json:"copies"` // total copies across those decks
	Games   int     `json:"games"`  // games played by decks containing it
	Wins    int     `json:"wins"`
	Losses  int     `json:"losses"`
	Draws   int     `json:"draws"`
	WinRate float64 `json:"win_rate"`
	Score   float64 `json:"score"` // Wilson lower bound across all games with this card
}

// Summary is the headline numbers.
type Summary struct {
	Decks      int   `json:"decks"`
	HumanDecks int   `json:"human_decks"`
	BotDecks   int   `json:"bot_decks"`
	Matches    int   `json:"matches"`
	Updated    int64 `json:"updated"`
	// Warming is true while the store is still working through a large backlog of the log (a first start, or a rotated
	// log): the numbers are correct for what has been read so far, and grow until LogBytes == IndexedBytes.
	Warming      bool  `json:"warming,omitempty"`
	IndexedBytes int64 `json:"indexed_bytes"`
	LogBytes     int64 `json:"log_bytes"`
}

// Query filters and orders the deck list.
type Query struct {
	Sort     string // winrate (default), score (sample-size adjusted), games, recent
	MinGames int
	Kind     string // "", "bot", "human"
	Player   string // case-insensitive substring
	Card     int    // -1 = any; else only decks containing this card id
	Limit    int
	Offset   int
}

const (
	schemaVersion = 1
	// A log line is ~700 bytes (a draft with names); anything past this is junk and skipped, not buffered.
	maxLine = 256 << 10
	// While the store is more than this far behind the file it reports Warming.
	warmBytes = 16 << 20
)

// A batch is committed after this many bytes / lines: it bounds memory (the batch's decks live in a map) and how long
// the write lock is held. Variables so tests can force many small batches.
var (
	batchBytes int64 = 24 << 20
	batchLines       = 60000
)

// Store tails one decks.ndjson into SQLite.
type Store struct {
	Path string
	// DBPath is the SQLite file. Empty = a private in-memory database (tests, throwaway tools).
	DBPath string
	// MinRefresh throttles the file check done by reads when no background loop runs (default 2s).
	MinRefresh time.Duration

	initOnce sync.Once
	initErr  error
	db       *sql.DB

	mu          sync.Mutex // one ingester at a time
	lastRefresh atomic.Int64
	bg          atomic.Bool
	pids        map[string]int64  // player name -> player.id (the ingester's cache of the player table)
	names       map[string]string // canonical name strings, so parsing a known player allocates nothing
	gate        chan struct{}     // bounds concurrent list queries

	cacheMu   sync.Mutex
	cache     map[string]cached
	tierCache map[string]cachedTier
}

type cached struct {
	at    time.Time
	decks []Deck
	total int
}

var memDBSeq atomic.Int64

// Err reports why the store cannot be used (the SQLite file could not be opened), or nil.
func (s *Store) Err() error {
	s.open()
	return s.initErr
}

func (s *Store) open() {
	s.initOnce.Do(func() {
		var dsn string
		maxConns := 4
		if s.DBPath == "" {
			dsn = fmt.Sprintf("file:deckstats_mem_%d?mode=memory&cache=shared&_pragma=busy_timeout(5000)", memDBSeq.Add(1))
			maxConns = 1 // a shared in-memory database vanishes with its last connection, and shared-cache tables lock
		} else {
			dsn = "file:" + s.DBPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=cache_size(-8192)&_pragma=temp_store(MEMORY)"
		}
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			s.initErr = err
			return
		}
		db.SetMaxOpenConns(maxConns)
		db.SetMaxIdleConns(maxConns)
		db.SetConnMaxLifetime(0)
		if err := migrate(db); err != nil {
			db.Close()
			s.initErr = err
			return
		}
		s.db = db
		s.pids = map[string]int64{}
		s.names = map[string]string{}
		s.gate = make(chan struct{}, 3)
		s.cache = map[string]cached{}
		s.tierCache = map[string]cachedTier{}
		if rows, err := db.Query(`SELECT id, name FROM player`); err == nil {
			for rows.Next() {
				var id int64
				var name string
				if rows.Scan(&id, &name) == nil {
					s.pids[name] = id
				}
			}
			rows.Close()
		}
	})
}

// Close releases the database.
func (s *Store) Close() error {
	s.open()
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

func migrate(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS meta (k TEXT PRIMARY KEY, v)`,
		// Player names are few (the bot pool plus a handful of people) and repeat 1.2 million times, so a deck stores a small
		// integer and the name filter becomes an integer test the indexes below can answer without reading the table.
		`CREATE TABLE IF NOT EXISTS player (id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, lname TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS deck (
			id INTEGER PRIMARY KEY,
			created INTEGER NOT NULL,
			player TEXT NOT NULL,
			pid INTEGER NOT NULL,
			kind INTEGER NOT NULL,
			cards BLOB NOT NULL,
			games INTEGER NOT NULL DEFAULT 0,
			wins INTEGER NOT NULL DEFAULT 0,
			losses INTEGER NOT NULL DEFAULT 0,
			draws INTEGER NOT NULL DEFAULT 0,
			win_rate REAL NOT NULL DEFAULT 0,
			score REAL NOT NULL DEFAULT 0)`,
		// One index per sort order. They also carry games, kind and pid, so the min-games / drafted-by / player filters are
		// decided from the index alone: a "players only" query over 1.2M decks walks 30 MB of index in ~0.1 s instead of
		// reading the 150 MB table (3 s).
		`CREATE INDEX IF NOT EXISTS deck_score ON deck (score DESC, id DESC, games, kind, pid)`,
		`CREATE INDEX IF NOT EXISTS deck_winrate ON deck (win_rate DESC, games DESC, id DESC, kind, pid)`,
		`CREATE INDEX IF NOT EXISTS deck_games ON deck (games DESC, id, kind, pid)`,
		`CREATE INDEX IF NOT EXISTS deck_recent ON deck (id DESC, games, kind, pid)`,
		`CREATE TABLE IF NOT EXISTS card_stat (
			card INTEGER PRIMARY KEY,
			decks INTEGER NOT NULL DEFAULT 0,
			copies INTEGER NOT NULL DEFAULT 0,
			games INTEGER NOT NULL DEFAULT 0,
			wins INTEGER NOT NULL DEFAULT 0,
			losses INTEGER NOT NULL DEFAULT 0,
			draws INTEGER NOT NULL DEFAULT 0)`,
	}
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			return err
		}
	}
	var v int
	err := db.QueryRow(`SELECT v FROM meta WHERE k='schema'`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = db.Exec(`INSERT OR REPLACE INTO meta (k, v) VALUES ('schema', ?)`, schemaVersion)
	}
	return err
}

// ---- reading the log ------------------------------------------------------------------------------------------

type meta struct {
	offset       int64
	fp           string
	drafts       int
	humanDecks   int
	botDecks     int
	matchRecords int
}

func (s *Store) readMeta(q interface {
	Query(string, ...any) (*sql.Rows, error)
}) (m meta, err error) {
	rows, err := q.Query(`SELECT k, v FROM meta`)
	if err != nil {
		return m, err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var v any
		if err := rows.Scan(&k, &v); err != nil {
			return m, err
		}
		n := func() int64 {
			switch x := v.(type) {
			case int64:
				return x
			case float64:
				return int64(x)
			case string:
				i, _ := strconv.ParseInt(x, 10, 64)
				return i
			}
			return 0
		}
		switch k {
		case "offset":
			m.offset = n()
		case "fp":
			if x, ok := v.(string); ok {
				m.fp = x
			}
		case "drafts":
			m.drafts = int(n())
		case "human_decks":
			m.humanDecks = int(n())
		case "bot_decks":
			m.botDecks = int(n())
		case "match_records":
			m.matchRecords = int(n())
		}
	}
	return m, rows.Err()
}

// firstLineFP identifies the log by its first complete line: a rotated or replaced log starts with different bytes even
// when it has already grown past the old offset. "" when the file has no complete first line yet.
func firstLineFP(f *os.File) (string, error) {
	buf := make([]byte, 4096)
	n, err := f.ReadAt(buf, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	buf = buf[:n]
	nl := -1
	for i, c := range buf {
		if c == '\n' {
			nl = i
			break
		}
	}
	if nl < 0 {
		return "", nil
	}
	h := fnv.New64a()
	h.Write(buf[:nl])
	return fmt.Sprintf("%d:%x", nl, h.Sum64()), nil
}

func fpMatches(f *os.File, fp string) bool {
	var n int
	var sum string
	if i := strings.IndexByte(fp, ':'); i > 0 {
		n, _ = strconv.Atoi(fp[:i])
		sum = fp[i+1:]
	}
	if n <= 0 {
		return true
	}
	buf := make([]byte, n+1)
	if _, err := f.ReadAt(buf, 0); err != nil || buf[n] != '\n' {
		return false
	}
	h := fnv.New64a()
	h.Write(buf[:n])
	return fmt.Sprintf("%x", h.Sum64()) == sum
}

// deckRow is a deck held while its batch is open.
type deckRow struct {
	id                         uint32
	created                    int64
	player                     string
	kind                       int
	cards                      [maxCards]byte
	n                          int
	games, wins, losses, draws int
}

type delta struct{ games, wins, losses, draws int }

func (d *delta) add(res byte) {
	d.games++
	switch res {
	case 'w':
		d.wins++
	case 'l':
		d.losses++
	default:
		d.draws++
	}
}

type cardAgg struct{ decks, copies, games, wins, losses, draws int }

type batch struct {
	newDecks     map[uint32]*deckRow
	deltas       map[uint32]*delta
	matchRecords int
	lines        int
}

func newBatch() *batch {
	return &batch{newDecks: map[uint32]*deckRow{}, deltas: map[uint32]*delta{}}
}

// playerName returns the player's name as a string without allocating for names already seen (a handful of names repeat
// a million times); only a new name is copied out of the line buffer.
func (s *Store) playerName(b []byte) string {
	if v, ok := s.names[string(b)]; ok {
		return v
	}
	v := string(b)
	if len(s.names) < 4096 {
		s.names[v] = v
	}
	return v
}

func (b *batch) apply(s *Store, ev *event) {
	switch ev.typ {
	case 'd':
		if ev.ncards == 0 || ev.badCards {
			return
		}
		if _, dup := b.newDecks[ev.deckID]; dup {
			return
		}
		d := &deckRow{id: ev.deckID, created: ev.t, player: s.playerName(ev.player), kind: ev.kind, n: ev.ncards}
		copy(d.cards[:], ev.cards[:ev.ncards])
		b.newDecks[ev.deckID] = d
	case 'm':
		b.matchRecords++
		if d := b.newDecks[ev.deckID]; d != nil {
			d.games++
			switch ev.result {
			case 'w':
				d.wins++
			case 'l':
				d.losses++
			default:
				d.draws++
			}
			return
		}
		dl := b.deltas[ev.deckID]
		if dl == nil {
			dl = &delta{}
			b.deltas[ev.deckID] = dl
		}
		dl.add(ev.result)
	}
}

// refreshLocked reads whatever the log gained since the stored offset, in bounded batches, until it reaches the end of
// the file or the budget (0 = none) runs out. It reports whether more remains. The caller holds s.mu.
func (s *Store) refreshLocked(budget time.Duration, throttle bool) (more bool) {
	s.open()
	if s.db == nil {
		return false
	}
	s.lastRefresh.Store(time.Now().UnixNano())
	f, err := os.Open(s.Path)
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return false
	}
	m, err := s.readMeta(s.db)
	if err != nil {
		return false
	}
	// A log that shrank, or whose first line changed, is a different log: forget what was read from the old one.
	if m.offset > 0 && (st.Size() < m.offset || (m.fp != "" && !fpMatches(f, m.fp))) {
		if err := s.reset(); err != nil {
			return false
		}
		m = meta{}
	}
	deadline := time.Time{}
	if budget > 0 {
		deadline = time.Now().Add(budget)
	}
	for m.offset < st.Size() {
		start := time.Now()
		consumed, err := s.ingestBatch(f, st.Size(), &m)
		if err != nil || consumed == 0 {
			return false
		}
		if m.offset >= st.Size() {
			break
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return true
		}
		if throttle {
			// Leave the CPU to the API: sleep as long as the batch took (about a 50% duty cycle while catching up).
			time.Sleep(time.Since(start))
		}
	}
	return false
}

// ingestBatch parses up to one batch of complete lines starting at m.offset, writes them in one transaction together with
// the new offset, and advances m. It returns how many bytes it consumed (0 = nothing complete to read).
func (s *Store) ingestBatch(f *os.File, size int64, m *meta) (int64, error) {
	sr := io.NewSectionReader(f, m.offset, size-m.offset)
	br := bufio.NewReaderSize(sr, 1<<20)
	b := newBatch()
	var ev event
	var consumed int64
	for consumed < batchBytes && b.lines < batchLines {
		line, err := br.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			// An absurdly long line: skip it without buffering the rest.
			n := int64(len(line))
			for errors.Is(err, bufio.ErrBufferFull) {
				var more []byte
				more, err = br.ReadSlice('\n')
				n += int64(len(more))
			}
			if err != nil { // no newline before EOF: incomplete, stop before it
				break
			}
			consumed += n
			continue
		}
		if err != nil { // EOF with no trailing newline: a partial line stays unread until it is complete
			break
		}
		consumed += int64(len(line))
		b.lines++
		if len(line) > maxLine {
			continue
		}
		body := line[:len(line)-1]
		if len(body) > 0 && body[len(body)-1] == '\r' {
			body = body[:len(body)-1]
		}
		if len(body) == 0 {
			continue
		}
		if !parseLine(body, &ev) {
			continue
		}
		b.apply(s, &ev)
	}
	if consumed == 0 {
		return 0, nil
	}
	fp := m.fp
	if m.offset == 0 && fp == "" {
		if fp, _ = firstLineFP(f); fp == "" {
			fp = m.fp
		}
	}
	if err := s.flush(b, m, m.offset+consumed, fp); err != nil {
		return 0, err
	}
	return consumed, nil
}

func (s *Store) flush(b *batch, m *meta, newOffset int64, fp string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	cards := map[int]*cardAgg{}
	agg := func(c int) *cardAgg {
		a := cards[c]
		if a == nil {
			a = &cardAgg{}
			cards[c] = a
		}
		return a
	}
	// addGames credits a deck's games to every distinct card it runs.
	addGames := func(cardBytes []byte, d delta) {
		var seen [256]bool
		for _, c := range cardBytes {
			if seen[c] {
				continue
			}
			seen[c] = true
			a := agg(int(c))
			a.games += d.games
			a.wins += d.wins
			a.losses += d.losses
			a.draws += d.draws
		}
	}

	ins, err := tx.Prepare(`INSERT OR IGNORE INTO deck (id, created, player, pid, kind, cards, games, wins, losses, draws, win_rate, score)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer ins.Close()
	newPids := map[string]int64{} // players first seen in this batch: cached only once the batch commits
	pidOf := func(name string) (int64, error) {
		if id, ok := s.pids[name]; ok {
			return id, nil
		}
		if id, ok := newPids[name]; ok {
			return id, nil
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO player (name, lname) VALUES (?, ?)`, name, strings.ToLower(name)); err != nil {
			return 0, err
		}
		var id int64
		if err := tx.QueryRow(`SELECT id FROM player WHERE name = ?`, name).Scan(&id); err != nil {
			return 0, err
		}
		newPids[name] = id
		return id, nil
	}

	ids := make([]uint32, 0, len(b.newDecks))
	for id := range b.newDecks {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	humans, bots, drafts := 0, 0, 0
	for _, id := range ids {
		d := b.newDecks[id]
		wr := 0.0
		if d.games > 0 {
			wr = float64(d.wins) / float64(d.games)
		}
		pid, err := pidOf(d.player)
		if err != nil {
			return err
		}
		res, err := ins.Exec(d.id, d.created, d.player, pid, d.kind, d.cards[:d.n], d.games, d.wins, d.losses, d.draws, wr, wilson(d.wins, d.games))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			// The deck is already stored (a repeated draft record): its games in this batch still count.
			dl := b.deltas[id]
			if dl == nil {
				dl = &delta{}
				b.deltas[id] = dl
			}
			dl.games += d.games
			dl.wins += d.wins
			dl.losses += d.losses
			dl.draws += d.draws
			continue
		}
		drafts++
		if d.kind == 1 {
			bots++
		} else {
			humans++
		}
		var count [256]int
		for _, c := range d.cards[:d.n] {
			count[c]++
		}
		for c, n := range count {
			if n > 0 {
				a := agg(c)
				a.decks++
				a.copies += n
			}
		}
		addGames(d.cards[:d.n], delta{d.games, d.wins, d.losses, d.draws})
	}

	// Matches for decks that were drafted in an earlier batch: read-modify-write the stored row.
	if len(b.deltas) > 0 {
		dids := make([]uint32, 0, len(b.deltas))
		for id := range b.deltas {
			dids = append(dids, id)
		}
		slices.Sort(dids)
		sel, err := tx.Prepare(`SELECT cards, games, wins, losses, draws FROM deck WHERE id = ?`)
		if err != nil {
			return err
		}
		defer sel.Close()
		upd, err := tx.Prepare(`UPDATE deck SET games=?, wins=?, losses=?, draws=?, win_rate=?, score=? WHERE id=?`)
		if err != nil {
			return err
		}
		defer upd.Close()
		for _, id := range dids {
			dl := b.deltas[id]
			var cardBlob []byte
			var g, w, l, dr int
			if err := sel.QueryRow(id).Scan(&cardBlob, &g, &w, &l, &dr); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					continue // a match for a deck we never saw drafted
				}
				return err
			}
			g += dl.games
			w += dl.wins
			l += dl.losses
			dr += dl.draws
			if _, err := upd.Exec(g, w, l, dr, float64(w)/float64(g), wilson(w, g), id); err != nil {
				return err
			}
			addGames(cardBlob, *dl)
		}
	}

	if len(cards) > 0 {
		up, err := tx.Prepare(`INSERT INTO card_stat (card, decks, copies, games, wins, losses, draws) VALUES (?,?,?,?,?,?,?)
			ON CONFLICT(card) DO UPDATE SET decks=decks+excluded.decks, copies=copies+excluded.copies, games=games+excluded.games,
			wins=wins+excluded.wins, losses=losses+excluded.losses, draws=draws+excluded.draws`)
		if err != nil {
			return err
		}
		defer up.Close()
		for c, a := range cards {
			if _, err := up.Exec(c, a.decks, a.copies, a.games, a.wins, a.losses, a.draws); err != nil {
				return err
			}
		}
	}

	m.offset = newOffset
	m.fp = fp
	m.drafts += drafts
	m.humanDecks += humans
	m.botDecks += bots
	m.matchRecords += b.matchRecords
	for k, v := range map[string]any{
		"offset": m.offset, "fp": m.fp, "drafts": m.drafts, "human_decks": m.humanDecks, "bot_decks": m.botDecks,
		"match_records": m.matchRecords,
	} {
		if _, err := tx.Exec(`INSERT OR REPLACE INTO meta (k, v) VALUES (?, ?)`, k, v); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for name, id := range newPids {
		s.pids[name] = id
	}
	if !s.bg.Load() { // with a background loop the 5 s TTL alone bounds staleness; the loop flushes every 2 s
		s.cacheMu.Lock()
		s.cache = map[string]cached{}
		s.cacheMu.Unlock()
	}
	return nil
}

func (s *Store) reset() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{`DELETE FROM deck`, `DELETE FROM card_stat`, `DELETE FROM meta WHERE k <> 'schema'`} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.cacheMu.Lock()
	s.cache = map[string]cached{}
	s.tierCache = map[string]cachedTier{}
	s.cacheMu.Unlock()
	return nil
}

// Refresh reads whatever the log gained, now, however much that is (tests and one-shot tools).
func (s *Store) Refresh() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked(0, false)
}

// Start runs the ingester in the background: it checks the log every two seconds and, while it is far behind (the first
// start reads the whole history), works through it in throttled batches. Reads then never touch the file. Stop it with ctx.
func (s *Store) Start(ctx context.Context) {
	if !s.bg.CompareAndSwap(false, true) {
		return
	}
	go func() {
		for {
			s.mu.Lock()
			func() {
				defer func() { _ = recover() }() // a bad line must never take IDUNA down
				s.refreshLocked(0, true)
			}()
			s.mu.Unlock()
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
		}
	}()
}

// maybeRefresh is the read-time refresh used when no background loop runs.
func (s *Store) maybeRefresh() {
	s.open()
	if s.bg.Load() || s.db == nil {
		return
	}
	min := s.MinRefresh
	if min == 0 {
		min = 2 * time.Second
	}
	if time.Since(time.Unix(0, s.lastRefresh.Load())) < min {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Another reader may have refreshed while this one waited for the lock.
	if time.Since(time.Unix(0, s.lastRefresh.Load())) < min {
		return
	}
	s.refreshLocked(0, false)
}

// ---- queries --------------------------------------------------------------------------------------------------

// Summary returns the headline counts.
func (s *Store) Summary() Summary {
	s.maybeRefresh()
	if s.db == nil {
		return Summary{}
	}
	m, err := s.readMeta(s.db)
	if err != nil {
		return Summary{}
	}
	sum := Summary{Decks: m.drafts, HumanDecks: m.humanDecks, BotDecks: m.botDecks, Matches: m.matchRecords / 2,
		Updated: time.Unix(0, s.lastRefresh.Load()).Unix(), IndexedBytes: m.offset}
	if st, err := os.Stat(s.Path); err == nil {
		sum.LogBytes = st.Size()
		sum.Warming = st.Size()-m.offset > warmBytes
	}
	return sum
}

const deckCols = `id, created, player, kind, cards, games, wins, losses, draws, win_rate, score`

type scanner interface{ Scan(...any) error }

func scanDeck(r scanner) (Deck, error) {
	var d Deck
	var blob []byte
	var id int64
	if err := r.Scan(&id, &d.Created, &d.Player, &d.Kind, &blob, &d.Games, &d.Wins, &d.Losses, &d.Draws, &d.WinRate, &d.Score); err != nil {
		return d, err
	}
	d.ID = uint32(id)
	d.Cards = make([]int, len(blob))
	for i, c := range blob {
		d.Cards[i] = int(c)
	}
	return d, nil
}

// Deck returns one deck by id.
func (s *Store) Deck(id uint32) (Deck, bool) {
	s.maybeRefresh()
	if s.db == nil {
		return Deck{}, false
	}
	d, err := scanDeck(s.db.QueryRow(`SELECT `+deckCols+` FROM deck WHERE id = ?`, int64(id)))
	if err != nil {
		return Deck{}, false
	}
	return d, true
}

// ErrBusy means too many list queries were already running; try again shortly.
var ErrBusy = errors.New("deckstats: busy")

// ErrBadTier is a tier request with impossible numbers (tier outside 1..tiers, tiers outside 1..10, n outside 1..100).
var ErrBadTier = errors.New("deckstats: bad tier request")

// where builds the filter shared by the page and the count. pids are the players the name filter resolved to.
func (q Query) where(pids []int64) (string, []any) {
	conds := []string{"games >= ?"}
	args := []any{q.MinGames}
	switch q.Kind {
	case "bot":
		conds = append(conds, "kind = 1")
	case "human":
		conds = append(conds, "kind = 0")
	}
	if strings.TrimSpace(q.Player) != "" {
		ph := make([]string, len(pids))
		for i, id := range pids {
			ph[i] = "?"
			args = append(args, id)
		}
		conds = append(conds, "pid IN ("+strings.Join(ph, ",")+")")
	}
	if q.Card >= 0 {
		if q.Card > 255 {
			conds = append(conds, "0")
		} else {
			conds = append(conds, "instr(cards, ?) > 0")
			args = append(args, []byte{byte(q.Card)})
		}
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

func (q Query) order() (index, orderBy string) {
	switch q.Sort {
	case "games":
		return "deck_games", "games DESC, id ASC"
	case "recent":
		return "deck_recent", "id DESC"
	case "score":
		return "deck_score", "score DESC, id DESC"
	default: // winrate, ties broken by more games, then newest
		return "deck_winrate", "win_rate DESC, games DESC, id DESC"
	}
}

// matchPlayers resolves a name filter to player ids.
func (s *Store) matchPlayers(ctx context.Context, needle string) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM player WHERE instr(lname, ?) > 0 ORDER BY id LIMIT 500`, strings.ToLower(strings.TrimSpace(needle)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// List returns the filtered, ordered page and the total number of matches before paging. An error (busy, timed out, no
// database) reads as an empty page; the HTTP handler uses ListCtx to tell the caller why.
func (s *Store) List(q Query) ([]Deck, int) {
	out, total, _ := s.ListCtx(context.Background(), q)
	return out, total
}

// ListCtx is List with the reason for a failure. A page is bounded by a 4 s deadline, and at most three run at once: the
// store serves a public page, so a pathological filter must cost a few seconds of one query, not the whole pod.
func (s *Store) ListCtx(ctx context.Context, q Query) ([]Deck, int, error) {
	s.maybeRefresh()
	if s.db == nil {
		return nil, 0, s.initErr
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	key := fmt.Sprintf("%+v", q)
	s.cacheMu.Lock()
	if c, ok := s.cache[key]; ok && time.Since(c.at) < 5*time.Second {
		s.cacheMu.Unlock()
		return append([]Deck(nil), c.decks...), c.total, nil
	}
	s.cacheMu.Unlock()

	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	case <-time.After(2 * time.Second):
		return nil, 0, ErrBusy
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	var pids []int64
	if strings.TrimSpace(q.Player) != "" {
		var err error
		if pids, err = s.matchPlayers(ctx, q.Player); err != nil {
			return nil, 0, err
		}
		if len(pids) == 0 {
			return []Deck{}, 0, nil
		}
	}
	where, args := q.where(pids)
	index, orderBy := q.order()
	sqlText := `SELECT ` + deckCols + ` FROM deck INDEXED BY ` + index + where + ` ORDER BY ` + orderBy
	pageArgs := append([]any(nil), args...)
	if q.Limit > 0 {
		sqlText += ` LIMIT ? OFFSET ?`
		pageArgs = append(pageArgs, q.Limit, q.Offset)
	} else if q.Offset > 0 {
		sqlText += ` LIMIT -1 OFFSET ?`
		pageArgs = append(pageArgs, q.Offset)
	}
	rows, err := s.db.QueryContext(ctx, sqlText, pageArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Deck{}
	for rows.Next() {
		d, err := scanDeck(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	rows.Close()
	var total int
	if (len(out) > 0 || q.Offset == 0) && (q.Limit <= 0 || len(out) < q.Limit) {
		// The page ran off the end of the result set (an empty page at offset 0 is the whole, empty, set), so it tells the
		// total: no second pass over the index. An empty page past the offset says nothing, and is counted below.
		total = q.Offset + len(out)
	} else {
		countFrom := "deck INDEXED BY deck_games" // answered from the index alone, unless a card filter needs the row
		if q.Card >= 0 {
			countFrom = "deck"
		}
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+countFrom+where, args...).Scan(&total); err != nil {
			return nil, 0, err
		}
	}
	s.cacheMu.Lock()
	if len(s.cache) > 256 {
		s.cache = map[string]cached{}
	}
	s.cache[key] = cached{at: time.Now(), decks: append([]Deck(nil), out...), total: total}
	s.cacheMu.Unlock()
	return out, total, nil
}

// CardStats returns per-card records, ordered by card id.
func (s *Store) CardStats() []CardStat {
	s.maybeRefresh()
	out := []CardStat{}
	if s.db == nil {
		return out
	}
	rows, err := s.db.Query(`SELECT card, decks, copies, games, wins, losses, draws FROM card_stat ORDER BY card`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var cs CardStat
		if err := rows.Scan(&cs.Card, &cs.Decks, &cs.Copies, &cs.Games, &cs.Wins, &cs.Losses, &cs.Draws); err != nil {
			return out
		}
		if cs.Games > 0 {
			cs.WinRate = float64(cs.Wins) / float64(cs.Games)
			cs.Score = wilson(cs.Wins, cs.Games)
		}
		out = append(out, cs)
	}
	return out
}

// ---- strength tiers (the constructed-mode bots' deck source) ---------------------------------------------------

// TierResult is one strength tier of the draft-deck pool.
type TierResult struct {
	Tier     int     `json:"tier"`  // 1 = weakest ... Tiers = strongest
	Tiers    int     `json:"tiers"` // how many tiers the pool is cut into
	MinGames int     `json:"min_games"`
	Eligible int     `json:"eligible"` // decks with at least MinGames games
	From     float64 `json:"from"`     // the band, as a fraction of the eligible decks ranked weakest (0) to strongest (1)
	To       float64 `json:"to"`
	ScoreMin float64 `json:"score_min"` // the weakest and strongest Wilson score actually in the band
	ScoreMax float64 `json:"score_max"`
	Decks    []Deck  `json:"decks"` // up to n decks spread evenly across the band, strongest first
}

// TierBand is where tier t of tiers sits among the eligible decks, as fractions of the ranking (weakest 0 ... strongest 1).
// The cut points are 1 - (1 - k/tiers)^2, so the bands get narrower toward the top: with five tiers the strongest is the top
// 4% (the champions), then 12%, 20%, 28%, and the weakest tier is the bottom 36%. A flat split would make the top tier as
// broad as the bottom one, and "the best decks" is the part of the ranking that carries the signal.
func TierBand(tier, tiers int) (from, to float64) {
	cut := func(k int) float64 { x := 1 - float64(k)/float64(tiers); return 1 - x*x }
	return cut(tier - 1), cut(tier)
}

// TierDecks returns up to n decks from strength tier `tier` of `tiers`, ranked by the Wilson score among the decks with at
// least minGames games. A deck's record in this log is mostly a win streak (a deck plays until its first loss), so this is
// "how long did it keep winning, adjusted for sample size". The pick is spread evenly across the band rather than taken from
// its edge, so a bot that draws one at random plays the whole band.
func (s *Store) TierDecks(ctx context.Context, tier, tiers, minGames, n int) (TierResult, error) {
	res := TierResult{Tier: tier, Tiers: tiers, MinGames: minGames, Decks: []Deck{}}
	if tiers < 1 || tiers > 10 || tier < 1 || tier > tiers || minGames < 1 || n < 1 || n > 100 {
		return res, ErrBadTier
	}
	s.maybeRefresh()
	if s.db == nil {
		return res, s.initErr
	}
	key := fmt.Sprintf("tier/%d/%d/%d/%d", tier, tiers, minGames, n)
	s.cacheMu.Lock()
	if c, ok := s.tierCache[key]; ok && time.Since(c.at) < 5*time.Minute {
		s.cacheMu.Unlock()
		return c.res, nil
	}
	s.cacheMu.Unlock()

	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	case <-time.After(2 * time.Second):
		return res, ErrBusy
	case <-ctx.Done():
		return res, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	var eligible int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deck INDEXED BY deck_games WHERE games >= ?`, minGames).Scan(&eligible); err != nil {
		return res, err
	}
	res.Eligible = eligible
	res.From, res.To = TierBand(tier, tiers)
	if eligible == 0 {
		return res, nil
	}
	// Ranks, weakest first: the band is [lo, hi). Both edges come from one rounding of the same cut points, so neighbouring
	// tiers partition the ranking exactly. (A pool too small for a tier to own a rank still returns one deck.)
	edge := func(k int) int {
		if k <= 0 {
			return 0
		}
		if k >= tiers {
			return eligible
		}
		_, to := TierBand(k, tiers)
		return int(math.Round(float64(eligible) * to))
	}
	lo, hi := edge(tier-1), edge(tier)
	if hi <= lo {
		if lo >= eligible {
			lo = eligible - 1
		}
		hi = lo + 1
	}
	// counted from the strongest deck, the band is [eligible-hi, eligible-lo)
	top, span := eligible-hi, hi-lo
	rows, err := s.db.QueryContext(ctx, `SELECT id, score FROM deck INDEXED BY deck_score WHERE games >= ? ORDER BY score DESC, id DESC LIMIT ? OFFSET ?`, minGames, span, top)
	if err != nil {
		return res, err
	}
	ids := make([]int64, 0, span)
	scores := make([]float64, 0, span)
	for rows.Next() {
		var id int64
		var sc float64
		if err := rows.Scan(&id, &sc); err != nil {
			rows.Close()
			return res, err
		}
		ids = append(ids, id)
		scores = append(scores, sc)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return res, err
	}
	rows.Close()
	if len(ids) == 0 {
		return res, nil
	}
	res.ScoreMax, res.ScoreMin = scores[0], scores[len(scores)-1]
	m := n
	if m > len(ids) {
		m = len(ids)
	}
	pick := make([]any, 0, m)
	placeholders := make([]string, 0, m)
	for j := 0; j < m; j++ {
		i := int((float64(j) + 0.5) * float64(len(ids)) / float64(m))
		pick = append(pick, ids[i])
		placeholders = append(placeholders, "?")
	}
	drows, err := s.db.QueryContext(ctx, `SELECT `+deckCols+` FROM deck WHERE id IN (`+strings.Join(placeholders, ",")+`) ORDER BY score DESC, id DESC`, pick...)
	if err != nil {
		return res, err
	}
	defer drows.Close()
	for drows.Next() {
		d, err := scanDeck(drows)
		if err != nil {
			return res, err
		}
		res.Decks = append(res.Decks, d)
	}
	if err := drows.Err(); err != nil {
		return res, err
	}
	s.cacheMu.Lock()
	if len(s.tierCache) > 64 {
		s.tierCache = map[string]cachedTier{}
	}
	s.tierCache[key] = cachedTier{at: time.Now(), res: res}
	s.cacheMu.Unlock()
	return res, nil
}

type cachedTier struct {
	at  time.Time
	res TierResult
}
