// Package statuspage implements a real, self-reported status page for the
// public EINHORN_INDUSTRIAL services reachable from this box.
//
// Honest scope, deliberately: only services with a real, currently-reachable
// public-facing endpoint are checked. emily-agent (daemon mode has no HTTP
// server) and SHANKPIT (pre-launch, no public server yet) are excluded
// rather than shown as permanently "down" — that would misrepresent a
// structural fact (no endpoint exists) as an outage.
//
// Known limitation, disclosed on the page itself rather than hidden: this
// checker runs on the same box as everything it checks. If the box itself
// goes down, the status page goes down with it — it cannot report "the
// whole system is down" during a real host-level outage. A page like this
// is a self-report, not independent third-party monitoring.
package statuspage

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"time"

	_ "modernc.org/sqlite"
)

// CheckType selects how a Target's liveness is verified. Defaults to
// CheckHTTP (empty string) for backward compatibility with existing targets.
type CheckType string

const (
	CheckHTTP        CheckType = ""             // GET CheckURL, 2xx = up
	CheckUDPPort     CheckType = "udp_port"     // is anything bound to UDPPort locally?
	CheckSystemdUnit CheckType = "systemd_unit" // is the named user-scope unit active?
)

type Target struct {
	Name     string // internal key, e.g. "iduna"
	Label    string // public-facing label, e.g. "Trust & Identity API"
	CheckURL string
	Type     CheckType
	UDPPort  int    // used when Type == CheckUDPPort
	Unit     string // used when Type == CheckSystemdUnit, e.g. "fatbaby-secwatch.service"
}

// DefaultTargets returns the services with a real, currently-reachable
// liveness signal, verified live 2026-07-18.
//
// FatBaby's headless pipeline pollers (secwatch, prwatch, prwatch-body,
// processor, eps-reconciler) have no HTTP or UDP surface of their own — the
// only real, honest liveness signal available for them is systemd itself,
// since all five now run under real, currently-enabled user-scope units
// (ops/systemd/fatbaby-*.service in PRRJECT_FATBABY).
//
// Deliberately NOT included, per this package's own scope discipline (see
// package doc): entity-graph and eps-processor. Both currently run as
// unsupervised `go run` processes — entity-graph's unit
// (fatbaby-entity-graph.service) exists but is disabled pending its Phase 2
// checkpoint work (EMILY/BACKLOG.md SECTION 1), and eps-processor has no
// unit at all yet. Checking their (inactive) units would report them "down"
// while they're actually running — the exact misrepresentation this
// package's doc warns against. Add them once real supervision lands.
func DefaultTargets() []Target {
	return []Target{
		{Name: "iduna", Label: "Trust & Identity API", CheckURL: "http://localhost:8080/health"},
		{Name: "newssite", Label: "FatBaby News", CheckURL: "http://localhost:8082/healthz"},
		{Name: "signalapi", Label: "FatBaby Signal API", CheckURL: "http://localhost:9091/v1/governance-signals?limit=1"},
		{Name: "secwatch", Label: "FatBaby SEC Filing Poller", Type: CheckSystemdUnit, Unit: "fatbaby-secwatch.service"},
		{Name: "prwatch", Label: "FatBaby PR Newswire Poller", Type: CheckSystemdUnit, Unit: "fatbaby-prwatch.service"},
		{Name: "prwatch-body", Label: "FatBaby PR Body Fetcher", Type: CheckSystemdUnit, Unit: "fatbaby-prwatch-body.service"},
		{Name: "processor", Label: "FatBaby Signal Processor", Type: CheckSystemdUnit, Unit: "fatbaby-processor.service"},
		{Name: "eps-reconciler", Label: "FatBaby EPS Reconciler", Type: CheckSystemdUnit, Unit: "fatbaby-eps-reconciler.service"},
		{Name: "shankpit460", Label: "SHANKPIT-460 Game Server", Type: CheckUDPPort, UDPPort: 6969},
		{Name: "shankpit460-emily-bot", Label: "SHANKPIT-460 Fill Bot", Type: CheckSystemdUnit, Unit: "shankpit460-emily-bot.service"},
	}
}

type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS checks (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	target     TEXT     NOT NULL,
	up         INTEGER  NOT NULL,
	latency_ms INTEGER  NOT NULL,
	checked_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_checks_target_time ON checks(target, checked_at);
`

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open statuspage db: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate statuspage db: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) record(target string, up bool, latencyMS int64) error {
	upInt := 0
	if up {
		upInt = 1
	}
	_, err := s.db.Exec(
		`INSERT INTO checks (target, up, latency_ms, checked_at) VALUES (?, ?, ?, ?)`,
		target, upInt, latencyMS, time.Now().UTC(),
	)
	return err
}

// LatestStatus returns the most recent check result for a target, or
// (false, false) if no check has ever run.
func (s *Store) LatestStatus(target string) (up bool, found bool) {
	var upInt int
	err := s.db.QueryRow(
		`SELECT up FROM checks WHERE target = ? ORDER BY checked_at DESC LIMIT 1`, target,
	).Scan(&upInt)
	if err != nil {
		return false, false
	}
	return upInt == 1, true
}

// LatestCheckedAt returns when a target was last checked.
func (s *Store) LatestCheckedAt(target string) (time.Time, bool) {
	var t time.Time
	err := s.db.QueryRow(
		`SELECT checked_at FROM checks WHERE target = ? ORDER BY checked_at DESC LIMIT 1`, target,
	).Scan(&t)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// UptimePercent computes real uptime over the given window from stored
// check history — the actual "history" data source (see package doc): every
// check ever performed is retained, so this is a live-computed percentage,
// not a placeholder.
func (s *Store) UptimePercent(target string, since time.Time) (pct float64, sampleCount int) {
	var total, up int
	err := s.db.QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(up), 0) FROM checks WHERE target = ? AND checked_at >= ?`,
		target, since,
	).Scan(&total, &up)
	if err != nil || total == 0 {
		return 0, 0
	}
	return float64(up) / float64(total) * 100.0, total
}

// Checker periodically pings every target and records the result.
type Checker struct {
	Store   *Store
	Targets []Target
	Client  *http.Client
}

func NewChecker(store *Store, targets []Target) *Checker {
	return &Checker{
		Store:   store,
		Targets: targets,
		Client:  &http.Client{Timeout: 5 * time.Second},
	}
}

// Run polls every target every interval until ctx is done. Errors recording
// a check are logged by the caller-supplied onError, if any — never fatal,
// this is a monitoring loop, not a critical path.
//
// The first check is delayed by startupGrace rather than fired immediately:
// this checker is started (via `go`) before IDUNA's own http.ListenAndServe
// is actually accepting connections, so an immediate self-check races IDUNA's
// own startup and spuriously records itself as down. Found live — the very
// first deploy of this feature recorded IDUNA as "down" against its own
// /health endpoint, seconds after a manual curl to the same URL succeeded.
func (c *Checker) Run(ctx context.Context, interval time.Duration, onError func(error)) {
	c.runDelayed(ctx, interval, 3*time.Second, onError)
}

func (c *Checker) runDelayed(ctx context.Context, interval, startupGrace time.Duration, onError func(error)) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(startupGrace):
	}
	c.checkAll(ctx, onError)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.checkAll(ctx, onError)
		}
	}
}

func (c *Checker) checkAll(ctx context.Context, onError func(error)) {
	for _, t := range c.Targets {
		up, latency := c.checkOne(ctx, t)
		if err := c.Store.record(t.Name, up, latency.Milliseconds()); err != nil && onError != nil {
			onError(fmt.Errorf("statuspage: record %s: %w", t.Name, err))
		}
	}
}

func (c *Checker) checkOne(ctx context.Context, t Target) (up bool, latency time.Duration) {
	switch t.Type {
	case CheckUDPPort:
		start := time.Now()
		up := UDPPortBound(t.UDPPort)
		return up, time.Since(start)
	case CheckSystemdUnit:
		start := time.Now()
		up := SystemdUnitActive(t.Unit)
		return up, time.Since(start)
	default:
		return c.checkHTTP(ctx, t)
	}
}

func (c *Checker) checkHTTP(ctx context.Context, t Target) (up bool, latency time.Duration) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.CheckURL, nil)
	if err != nil {
		return false, 0
	}
	start := time.Now()
	resp, err := c.Client.Do(req)
	latency = time.Since(start)
	if err != nil {
		return false, latency
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300, latency
}
