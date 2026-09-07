package tenantprovision

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// Real, permanent test coverage for the pure/file-writing pieces of this package -- the actual
// live systemctl spin-up path (Provision's own end-to-end mechanism) was verified manually
// against this real box (see this package's own header comment) rather than checked in as an
// automated test, matching this codebase's own established convention for infra-touching code
// (e.g. internal/mailaccounts, internal/sshconn elsewhere in this monorepo): a CI run should
// never spin up real systemd services as a side effect of `go test`.

func TestValidateSubdomain(t *testing.T) {
	valid := []string{"a", "acme", "acme-corp", "a1b2", strings.Repeat("a", 63)}
	for _, s := range valid {
		if err := ValidateSubdomain(s); err != nil {
			t.Errorf("expected %q to be valid, got error: %v", s, err)
		}
	}
	invalid := []string{"", "-acme", "acme-", "Acme", "acme_corp", "acme.corp", strings.Repeat("a", 64)}
	for _, s := range invalid {
		if err := ValidateSubdomain(s); err == nil {
			t.Errorf("expected %q to be rejected, got no error", s)
		}
	}
}

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE tenants (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		org_name VARCHAR(255) NOT NULL,
		contact_email VARCHAR(255) NOT NULL,
		subdomain VARCHAR(100) NOT NULL UNIQUE,
		status VARCHAR(32) NOT NULL DEFAULT 'provisioning',
		port INTEGER,
		sqlite_path VARCHAR(500),
		jwt_secret VARCHAR(255),
		error_message TEXT,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("create tenants table: %v", err)
	}
	return db
}

func TestAllocatePort_SkipsPortsAlreadyRecordedForActiveTenants(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	cfg := Config{DB: db, PortRangeStart: 19100, PortRangeEnd: 19102}

	if _, err := db.Exec(`INSERT INTO tenants (org_name, contact_email, subdomain, status, port) VALUES ('a','a@a.com','a','active', 19100)`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	port, err := allocatePort(ctx, cfg)
	if err != nil {
		t.Fatalf("allocatePort: %v", err)
	}
	if port == 19100 {
		t.Fatalf("expected the already-recorded active port to be skipped, got %d", port)
	}
}

func TestAllocatePort_ReusesPortFromFailedTenant(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	cfg := Config{DB: db, PortRangeStart: 19200, PortRangeEnd: 19200} // exactly one candidate port

	if _, err := db.Exec(`INSERT INTO tenants (org_name, contact_email, subdomain, status, port) VALUES ('a','a@a.com','a','failed', 19200)`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	port, err := allocatePort(ctx, cfg)
	if err != nil {
		t.Fatalf("expected the sole port to be reusable from a failed tenant, got error: %v", err)
	}
	if port != 19200 {
		t.Fatalf("expected port 19200, got %d", port)
	}
}

func TestAllocatePort_NoFreePortInRange(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	cfg := Config{DB: db, PortRangeStart: 19300, PortRangeEnd: 19300}

	if _, err := db.Exec(`INSERT INTO tenants (org_name, contact_email, subdomain, status, port) VALUES ('a','a@a.com','a','active', 19300)`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := allocatePort(ctx, cfg); err == nil {
		t.Fatal("expected an error when every port in range is already claimed")
	}
}

func TestWriteEnvFile_ContainsExpectedRealValues(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{ConfigBaseDir: dir, BaseDomain: "console.example.com"}
	tenant := &Tenant{Subdomain: "acme", Port: 9123, JWTSecret: "deadbeef", SQLitePath: "/tmp/acme/iduna.db"}

	if err := writeEnvFile(cfg, tenant); err != nil {
		t.Fatalf("writeEnvFile: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "idunapro-tenant-acme", "env"))
	if err != nil {
		t.Fatalf("read generated env file: %v", err)
	}
	s := string(content)
	for _, want := range []string{
		"JWT_SECRET=deadbeef",
		"JWT_ISSUER=https://acme.console.example.com",
		"ADDR=:9123",
		"SQLITE_PATH=/tmp/acme/iduna.db",
		"BASE_URL=https://acme.console.example.com",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("expected generated env file to contain %q, got:\n%s", want, s)
		}
	}
}

func TestWriteSystemdUnit_ReferencesTheSharedBinaryAndCorrectPort(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		SystemdUserDir:     dir,
		ConfigBaseDir:      "/home/fatbaby/.config",
		IdunaProBinary:     "/home/fatbaby/.local/bin/idunapro",
		IdunaProWorkingDir: "/home/fatbaby/IDUNA_PRO",
	}
	tenant := &Tenant{Subdomain: "acme", OrgName: "Acme Corp", Port: 9123}

	if err := writeSystemdUnit(cfg, tenant); err != nil {
		t.Fatalf("writeSystemdUnit: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "idunapro-tenant-acme.service"))
	if err != nil {
		t.Fatalf("read generated unit file: %v", err)
	}
	s := string(content)
	for _, want := range []string{
		"ExecStart=/home/fatbaby/.local/bin/idunapro", // the ONE shared binary, no per-tenant rebuild
		"WorkingDirectory=/home/fatbaby/IDUNA_PRO",
		"EnvironmentFile=-/home/fatbaby/.config/idunapro-tenant-acme/env",
		"http://localhost:9123/health",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("expected generated unit file to contain %q, got:\n%s", want, s)
		}
	}
}

func TestListTenants_ReturnsNewestFirst(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO tenants (org_name, contact_email, subdomain, status) VALUES ('First','a@a.com','first','active')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO tenants (org_name, contact_email, subdomain, status) VALUES ('Second','b@b.com','second','provisioning')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tenants, err := ListTenants(ctx, db)
	if err != nil {
		t.Fatalf("ListTenants: %v", err)
	}
	if len(tenants) != 2 {
		t.Fatalf("expected 2 tenants, got %d", len(tenants))
	}
	if tenants[0].Subdomain != "second" {
		t.Errorf("expected newest (second) first, got %q", tenants[0].Subdomain)
	}
}
