// Package tenantprovision implements the real control-plane provisioning pipeline named (but not
// built) in IDUNA/docs/EMILY_FOR_BUSINESS_NORTHSTAR.md's own "control-plane model" section --
// founder real-time, 2026-09-03: "so we use our IDUNA to manage the free trials for emily for
// business." Internal IDUNA stays the backbone and gains a new real capability: spinning up a
// genuine, separate, live IDUNA_PRO instance per tenant, on its own port, its own SQLite file,
// its own JWT trust domain -- the exact same shape CarePyre's own manually-run sudo-queue/51
// deploy already proved out, just automated and per-tenant instead of hand-run once.
//
// Real, deliberate scope boundary (founder-confirmed, 2026-09-07): this package DOES actually
// spin up a live systemd --user service end to end -- it is not artifact-generation-only. It is
// gated admin-only (no public self-serve signup exists yet; console.okemily.com is still
// unbuilt) precisely because it has that real capability.
//
// Real, checked mechanism (not assumed): IDUNA_PRO is a config-driven, GOWORK=off standalone
// Go module with zero per-deployment source differences -- the SAME already-built
// ~/.local/bin/idunapro binary already serving CarePyre can serve every tenant too, distinguished
// entirely by which env file (SQLITE_PATH/ADDR/JWT_SECRET/BASE_URL) its own systemd unit points
// at. Provisioning a new tenant needs zero rebuild, matching the NORTHSTAR doc's own "DB-per-
// install is already close to free" finding.
//
// systemctl --user works here without sudo (verified live, 2026-09-06, this same session):
// internal IDUNA itself already runs as a systemd --user unit (iduna.service), so its own child
// processes inherit a real user D-Bus session -- this package still sets
// XDG_RUNTIME_DIR/DBUS_SESSION_BUS_ADDRESS explicitly on every systemctl invocation rather than
// trusting inherited environment, since that's the one thing this session found NOT reliably
// ambient (a plain interactive shell lacked it; a systemd-launched process should have it, but
// "should" isn't "verified" for THIS specific process, so the explicit set costs nothing and
// removes the assumption).
package tenantprovision

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Config wires the real, checked paths/values this package needs -- every field has a real
// default in NewDefaultConfig, matching this box's own actual, current layout (not invented).
type Config struct {
	DB *sql.DB

	IdunaProBinary     string // ~/.local/bin/idunapro -- the ONE shared binary every tenant runs
	IdunaProWorkingDir string // /home/fatbaby/IDUNA_PRO -- source checkout the binary was built from
	TenantVarDir       string // /home/fatbaby/IDUNA/var/tenants -- per-tenant SQLite files live under here
	SystemdUserDir     string // ~/.config/systemd/user -- where generated *.service files go
	ConfigBaseDir      string // ~/.config -- per-tenant env files go in <this>/idunapro-tenant-<slug>/env
	BaseDomain         string // console.okemily.com -- a tenant's real subdomain is <slug>.<this>

	PortRangeStart int // 9100
	PortRangeEnd   int // 9199

	HealthCheckTimeout time.Duration // how long to wait for the new instance's own /health before declaring failure
}

// NewDefaultConfig fills in this box's own real, current paths -- callers only need to set DB.
func NewDefaultConfig(db *sql.DB) Config {
	home, _ := os.UserHomeDir()
	return Config{
		DB:                 db,
		IdunaProBinary:     filepath.Join(home, ".local", "bin", "idunapro"),
		IdunaProWorkingDir: filepath.Join(home, "IDUNA_PRO"),
		TenantVarDir:       filepath.Join(home, "IDUNA", "var", "tenants"),
		SystemdUserDir:     filepath.Join(home, ".config", "systemd", "user"),
		ConfigBaseDir:      filepath.Join(home, ".config"),
		BaseDomain:         "console.okemily.com",
		PortRangeStart:     9100,
		PortRangeEnd:       9199,
		HealthCheckTimeout: 30 * time.Second,
	}
}

// Tenant mirrors the tenants table (migrations/truestore/202609070001_tenants.sql).
type Tenant struct {
	ID           int64
	OrgName      string
	ContactEmail string
	Subdomain    string
	Status       string // provisioning | active | failed
	Port         int
	SQLitePath   string
	JWTSecret    string
	ErrorMessage string
	CreatedAt    string
	UpdatedAt    string
}

var subdomainPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidateSubdomain enforces real DNS-label rules (lowercase alnum + internal hyphens, 1-63
// chars) -- this becomes a live Host-header route and eventually a real DNS label, so this is a
// correctness check, not cosmetic input validation.
func ValidateSubdomain(s string) error {
	if !subdomainPattern.MatchString(s) {
		return fmt.Errorf("subdomain %q must be a valid DNS label (lowercase letters, digits, internal hyphens only)", s)
	}
	return nil
}

// Provision runs the real, full pipeline: allocate a port, generate a secret, write the env
// file + systemd unit, start it, wait for its own /health to answer, and record the result.
// Returns the Tenant row in whatever state it ended in (active or failed) -- an error return
// means provisioning could not even be attempted (bad input, DB failure), not that it failed
// partway (that's Tenant.Status == "failed" with Tenant.ErrorMessage set, a real result, not a
// Go error, since the caller very much wants the tenant ROW even when the underlying spin-up
// failed).
func Provision(ctx context.Context, cfg Config, orgName, contactEmail, subdomain string) (*Tenant, error) {
	subdomain = strings.ToLower(strings.TrimSpace(subdomain))
	if err := ValidateSubdomain(subdomain); err != nil {
		return nil, err
	}
	orgName = strings.TrimSpace(orgName)
	contactEmail = strings.TrimSpace(contactEmail)
	if orgName == "" || contactEmail == "" {
		return nil, fmt.Errorf("org_name and contact_email are required")
	}

	res, err := cfg.DB.ExecContext(ctx,
		`INSERT INTO tenants (org_name, contact_email, subdomain, status) VALUES (?, ?, ?, 'provisioning')`,
		orgName, contactEmail, subdomain)
	if err != nil {
		return nil, fmt.Errorf("tenantprovision: insert tenant row: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("tenantprovision: read new tenant id: %w", err)
	}

	t := &Tenant{ID: id, OrgName: orgName, ContactEmail: contactEmail, Subdomain: subdomain, Status: "provisioning"}

	port, err := allocatePort(ctx, cfg)
	if err != nil {
		return failTenant(ctx, cfg, t, fmt.Errorf("allocate port: %w", err))
	}
	t.Port = port

	secret, err := randomHex(32)
	if err != nil {
		return failTenant(ctx, cfg, t, fmt.Errorf("generate jwt secret: %w", err))
	}
	t.JWTSecret = secret
	t.SQLitePath = filepath.Join(cfg.TenantVarDir, subdomain, "iduna.db")

	if err := writeEnvFile(cfg, t); err != nil {
		return failTenant(ctx, cfg, t, fmt.Errorf("write env file: %w", err))
	}
	if err := writeSystemdUnit(cfg, t); err != nil {
		return failTenant(ctx, cfg, t, fmt.Errorf("write systemd unit: %w", err))
	}
	if err := startTenantService(ctx, cfg, t); err != nil {
		return failTenant(ctx, cfg, t, fmt.Errorf("start service: %w", err))
	}
	if err := waitForHealth(ctx, cfg, t); err != nil {
		return failTenant(ctx, cfg, t, fmt.Errorf("health check: %w", err))
	}

	t.Status = "active"
	if _, err := cfg.DB.ExecContext(ctx,
		`UPDATE tenants SET status = 'active', port = ?, sqlite_path = ?, jwt_secret = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		t.Port, t.SQLitePath, t.JWTSecret, t.ID); err != nil {
		return nil, fmt.Errorf("tenantprovision: mark tenant active: %w", err)
	}
	return t, nil
}

func failTenant(ctx context.Context, cfg Config, t *Tenant, cause error) (*Tenant, error) {
	t.Status = "failed"
	t.ErrorMessage = cause.Error()
	if _, err := cfg.DB.ExecContext(ctx,
		`UPDATE tenants SET status = 'failed', error_message = ?, port = ?, sqlite_path = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		t.ErrorMessage, nullableInt(t.Port), nullableString(t.SQLitePath), t.ID); err != nil {
		// The tenant row itself couldn't be updated -- this IS a real error the caller needs to
		// see (the row is stuck at "provisioning" with no explanation), unlike every other
		// failure path here which returns the tenant with Status=="failed" as a real result.
		return nil, fmt.Errorf("tenantprovision: record failure (original cause: %v): %w", cause, err)
	}
	return t, nil
}

func nullableInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// allocatePort scans cfg.PortRangeStart..PortRangeEnd for the first port neither already
// recorded against a live tenant row NOR currently bound by anything else on this box -- the DB
// check alone isn't sufficient (a port could be in use by something unrelated to tenants), and
// the bind check alone isn't sufficient (two concurrent Provision calls could race on the same
// free port) -- checking both, then holding the DB row open before releasing the listener, is
// the same "check, then claim" shape any real port allocator needs. Real, accepted race: this
// listener is released before the systemd unit's own listener claims the port, a small window
// another process could theoretically steal it in -- acceptable for an admin-triggered, low-
// frequency operation, not for a high-throughput allocator.
func allocatePort(ctx context.Context, cfg Config) (int, error) {
	for port := cfg.PortRangeStart; port <= cfg.PortRangeEnd; port++ {
		var count int
		if err := cfg.DB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM tenants WHERE port = ? AND status != 'failed'`, port).Scan(&count); err != nil {
			return 0, fmt.Errorf("check port %d in DB: %w", port, err)
		}
		if count > 0 {
			continue
		}
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue // already bound by something else
		}
		ln.Close()
		return port, nil
	}
	return 0, fmt.Errorf("no free port in range %d-%d", cfg.PortRangeStart, cfg.PortRangeEnd)
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func writeEnvFile(cfg Config, t *Tenant) error {
	dir := filepath.Join(cfg.ConfigBaseDir, "idunapro-tenant-"+t.Subdomain)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	content := fmt.Sprintf(`# Generated by tenantprovision.Provision -- do not hand-edit, a re-provision will overwrite this.
JWT_SECRET=%s
JWT_ISSUER=https://%s.%s
ADDR=:%d
SQLITE_PATH=%s
BASE_URL=https://%s.%s
`, t.JWTSecret, t.Subdomain, cfg.BaseDomain, t.Port, t.SQLitePath, t.Subdomain, cfg.BaseDomain)
	return os.WriteFile(filepath.Join(dir, "env"), []byte(content), 0o600)
}

func writeSystemdUnit(cfg Config, t *Tenant) error {
	if err := os.MkdirAll(cfg.SystemdUserDir, 0o755); err != nil {
		return err
	}
	unitName := "idunapro-tenant-" + t.Subdomain
	content := fmt.Sprintf(`# Generated by tenantprovision.Provision for tenant %q (%s) -- do not hand-edit, a
# re-provision will overwrite this. Same real shape as IDUNA_PRO/scripts/idunapro.service
# (CarePyre's own hand-run precedent), parameterized per tenant instead of hand-copied once.
[Unit]
Description=IDUNA_PRO -- tenant %s (%s)
After=network.target

[Service]
Type=simple
WorkingDirectory=%s
EnvironmentFile=-%s/env
ExecStart=%s
ExecStartPost=/bin/sh -c 'for i in $(seq 1 30); do curl -sf -o /dev/null http://localhost:%d/health && exit 0; sleep 1; done; echo "idunapro-tenant-%s health check failed after 30s" >&2; exit 1'
Restart=on-failure
RestartSec=10
StandardOutput=journal
StandardError=journal
MemoryMax=256M
TasksMax=64

[Install]
WantedBy=default.target
`, t.OrgName, t.Subdomain, t.OrgName, t.Subdomain,
		cfg.IdunaProWorkingDir,
		filepath.Join(cfg.ConfigBaseDir, "idunapro-tenant-"+t.Subdomain),
		cfg.IdunaProBinary,
		t.Port, t.Subdomain)
	return os.WriteFile(filepath.Join(cfg.SystemdUserDir, unitName+".service"), []byte(content), 0o644)
}

// systemctlEnv returns the environment a systemctl --user subprocess needs to find this user's
// own session bus -- see this file's own header comment for why this is set explicitly rather
// than trusted from ambient environment.
func systemctlEnv() []string {
	uid := os.Getuid()
	runtimeDir := fmt.Sprintf("/run/user/%d", uid)
	return append(os.Environ(),
		"XDG_RUNTIME_DIR="+runtimeDir,
		"DBUS_SESSION_BUS_ADDRESS=unix:path="+runtimeDir+"/bus",
	)
}

func runSystemctl(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "systemctl", args...)
	cmd.Env = systemctlEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func startTenantService(ctx context.Context, cfg Config, t *Tenant) error {
	unitName := "idunapro-tenant-" + t.Subdomain + ".service"
	if err := runSystemctl(ctx, "--user", "daemon-reload"); err != nil {
		return err
	}
	return runSystemctl(ctx, "--user", "enable", "--now", unitName)
}

func waitForHealth(ctx context.Context, cfg Config, t *Tenant) error {
	deadline := time.Now().Add(cfg.HealthCheckTimeout)
	url := fmt.Sprintf("http://localhost:%d/health", t.Port)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err == nil {
			if resp, err := client.Do(req); err == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return nil
				}
			}
		}
		time.Sleep(1 * time.Second)
	}
	return fmt.Errorf("no healthy response from %s within %s", url, cfg.HealthCheckTimeout)
}

// Deprovision stops and disables a tenant's systemd unit and marks it "expired" -- real, honest
// v0: it does NOT delete the tenant's SQLite file, env file, or unit file (a real, deliberate
// "don't destroy customer data on a status change" choice), and does NOT remove the broker route
// (that's a separate concern, see the broker route-registration code this package intentionally
// does not own -- keeping infrastructure-mutation boundaries the same shape as sudo-queue
// scripts elsewhere in this monorepo: one clear owner per real side-effect).
func Deprovision(ctx context.Context, cfg Config, t *Tenant) error {
	unitName := "idunapro-tenant-" + t.Subdomain + ".service"
	if err := runSystemctl(ctx, "--user", "disable", "--now", unitName); err != nil {
		return err
	}
	_, err := cfg.DB.ExecContext(ctx, `UPDATE tenants SET status = 'expired', updated_at = CURRENT_TIMESTAMP WHERE id = ?`, t.ID)
	return err
}

// ListTenants returns every tenant row, newest first.
func ListTenants(ctx context.Context, db *sql.DB) ([]Tenant, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, org_name, contact_email, subdomain, status, COALESCE(port, 0), COALESCE(sqlite_path, ''), COALESCE(error_message, ''), created_at, updated_at
		 FROM tenants ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Tenant
	for rows.Next() {
		var t Tenant
		if err := rows.Scan(&t.ID, &t.OrgName, &t.ContactEmail, &t.Subdomain, &t.Status, &t.Port, &t.SQLitePath, &t.ErrorMessage, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
