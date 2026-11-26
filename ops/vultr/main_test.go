package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func baseEnv() map[string]string {
	return map[string]string{
		"IDUNA_VULTR_REGION": "ewr", "IDUNA_VULTR_PLAN": "vc2-1c-2gb", "IDUNA_VULTR_OS_ID": "2284",
		"IDUNA_ZONE": "example.com", "IDUNA_IMAGE": "ghcr.io/emilyspringerton/iduna:5243d7b",
		"IDUNA_VULTR_SSH_KEY_IDS": "k-1", "IDUNA_SSH_KEY_FILE": "/k", "IDUNA_WEBMASTER_FILE": "/w",
	}
}

func TestUnconfirmedFactsHaveNoDefault(t *testing.T) {
	_, missing := loadConfig(env(map[string]string{}))
	got := strings.Join(missing, ",")
	for _, k := range []string{"IDUNA_VULTR_REGION", "IDUNA_VULTR_PLAN", "IDUNA_VULTR_OS_ID", "IDUNA_ZONE", "IDUNA_IMAGE",
		"IDUNA_VULTR_SSH_KEY_IDS", "IDUNA_SSH_KEY_FILE", "IDUNA_WEBMASTER_FILE"} {
		if !strings.Contains(got, k) {
			t.Errorf("%s should be required, missing=%v", k, missing)
		}
	}
	c, missing := loadConfig(env(baseEnv()))
	if len(missing) != 0 {
		t.Errorf("base env should be complete, missing %v", missing)
	}
	if strings.Join(c.hosts(), ",") != "iam.example.com,example.com,www.example.com" {
		t.Errorf("hosts: %v", c.hosts())
	}
}

func TestValidateRejectsInjection(t *testing.T) {
	bad := map[string]string{
		"IDUNA_ZONE":            "example.com;rm -rf /",
		"IDUNA_IMAGE":           "ghcr.io/x/iduna:abc$(id)",
		"IDUNA_EDGE_GIT_REF":    "main'$(id)'",
		"IDUNA_EDGE_GIT_URL":    "git@github.com:x/y.git",
		"IDUNA_TUNNEL_HOST":     "tunnel.other.org",
		"IDUNA_DWLOGS_SRC":      "http://dw:8081/x y",
		"IDUNA_VULTR_REGION":    "ewr\nx",
		"IDUNA_OKEMILY_GIT_URL": "file:///etc",
	}
	for k, v := range bad {
		e := baseEnv()
		e[k] = v
		c, _ := loadConfig(env(e))
		if c.validate() == nil {
			t.Errorf("%s=%q should be rejected", k, v)
		}
	}
	e := baseEnv()
	e["IDUNA_IMAGE"] = "ghcr.io/emilyspringerton/iduna:latest"
	if c, _ := loadConfig(env(e)); c.validate() == nil {
		t.Error(":latest must be refused (a reboot must not change what runs)")
	}
	e = baseEnv()
	e["IDUNA_TUNNEL_HOST"] = "tunnel.example.com"
	e["IDUNA_DWLOGS_SRC"] = "http://10.0.0.5:8081"
	if c, _ := loadConfig(env(e)); c.validate() != nil {
		t.Errorf("good config rejected: %v", c.validate())
	}
}

func TestPriceMatches(t *testing.T) {
	for _, c := range []struct {
		in   string
		cost float64
		ok   bool
	}{{"10", 10, true}, {"$10.00", 10, true}, {"9.99", 10, false}, {"", 10, false}, {"x", 10, false}} {
		if priceMatches(c.in, c.cost) != c.ok {
			t.Errorf("priceMatches(%q,%v) want %v", c.in, c.cost, c.ok)
		}
	}
}

// between returns the heredoc body that follows marker in s.
func between(t *testing.T, s, marker string) string {
	i := strings.Index(s, marker)
	if i < 0 {
		t.Fatalf("user-data has no %q", marker)
	}
	rest := s[i+len(marker):]
	j := strings.Index(rest, "\nEOF\n")
	if j < 0 {
		t.Fatalf("unterminated heredoc after %q", marker)
	}
	return rest[:j+1]
}

func TestUserDataCarriesNoSecretsAndParses(t *testing.T) {
	e := baseEnv()
	e["IDUNA_DWLOGS_SRC"] = "http://10.0.0.5:8081"
	c, _ := loadConfig(env(e))
	ud, err := renderUserData(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"PRIVATE KEY", "BEGIN CERTIFICATE", `"password"`} {
		if strings.Contains(ud, bad) {
			t.Errorf("user-data must carry no secrets, found %q (Vultr keeps user-data readable from inside the box)", bad)
		}
	}
	for _, want := range []string{
		"image: ghcr.io/emilyspringerton/iduna:5243d7b", "BASE_URL: https://iam.example.com",
		"NOCK_DATA_DIR: /app/var/nock-projects", "BLOG_OUTPUT_DIR: /app/var/www/blog",
		"server_name iam.example.com;", "server_name example.com www.example.com;",
		"location = / { return 302 /api/v1/auth/sso/login; }", `command: ["server", "8443", "iduna", "8080", "/app/var/sc-tunnel.key"]`,
		"SRC: http://10.0.0.5:8081", "--exclude='blog'", "--exclude='prompt-o-verse'", "shred -u /srv/iduna/var/webmaster.json",
		"curl -sS -f -C - -o", // logsync.sh embedded
	} {
		if !strings.Contains(ud, want) {
			t.Errorf("user-data missing %q", want)
		}
	}
	// The whole first-boot script and every script it writes must be valid bash.
	dir := t.TempDir()
	scripts := map[string]string{"user-data.sh": ud,
		"iduna-up":       between(t, ud, "cat > /usr/local/bin/iduna-up <<'EOF'\n"),
		"okemily-deploy": between(t, ud, "cat > /usr/local/bin/okemily-deploy <<'EOF'\n"),
		"logsync.sh":     between(t, ud, "cat > /opt/iduna/logsync.sh <<'EOF'\n")}
	for name, body := range scripts {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(body), 0o600)
		if out, err := exec.Command("bash", "-n", p).CombinedOutput(); err != nil {
			t.Errorf("%s does not parse: %v %s", name, err, out)
		}
	}
	// The compose file must be YAML that names the four services (checked structurally: no YAML lib in this module).
	compose := between(t, ud, "cat > /opt/iduna/compose.yml <<'EOF'\n")
	for _, svc := range []string{"\n  iduna:\n", "\n  web:\n", "\n  sc-tunnel:\n", "\n  logsync:\n"} {
		if !strings.Contains(compose, svc) {
			t.Errorf("compose missing service %q", strings.TrimSpace(svc))
		}
	}
	if strings.Contains(compose, "\t") {
		t.Error("compose has a tab (YAML forbids tabs for indentation)")
	}
	c2, _ := loadConfig(env(baseEnv()))
	ud2, _ := renderUserData(c2)
	if !strings.Contains(ud2, "SRC: "+dwlogsPlaceholder) {
		t.Error("unset IDUNA_DWLOGS_SRC should poll the never-resolving placeholder")
	}
}

func TestCheckLocalFiles(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "id_ed25519")
	os.WriteFile(key, []byte("k"), 0o600)
	wm := filepath.Join(dir, "webmaster.json")
	c := config{SSHKeyFile: key, WebmasterFile: wm}
	for body, ok := range map[string]bool{
		`{"email":"a@b.c","password":"long-enough-pw","display_name":"Web"}`: true,
		`{"email":"a@b.c","password":"short","display_name":"Web"}`:          false,
		`{"email":"a@b.c","display_name":"Web"}`:                             false,
		`not json`:                                                           false,
	} {
		os.WriteFile(wm, []byte(body), 0o600)
		if err := checkLocalFiles(c); (err == nil) != ok {
			t.Errorf("webmaster %s: err=%v, want ok=%v", body, err, ok)
		}
	}
	c.SSHKeyFile = dir
	if checkLocalFiles(c) == nil {
		t.Error("a directory is not an SSH key")
	}
}

func TestCreatePayload(t *testing.T) {
	var got createReq
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/instances" && r.Method == "POST" && r.Header.Get("Authorization") == "Bearer vk" {
			json.NewDecoder(r.Body).Decode(&got)
			w.WriteHeader(202)
			io.WriteString(w, `{"instance":{"id":"abc-123","main_ip":"0.0.0.0","status":"pending","default_password":"SHOULD-NOT-SURFACE"}}`)
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()
	c, _ := loadConfig(env(baseEnv()))
	if _, err := createInstance(newAPI(srv.URL, "vk"), c, "#!/bin/bash\n"); err != nil {
		t.Fatal(err)
	}
	if got.Backups != "disabled" || got.Region != "ewr" || strings.Join(got.SSHKeyIDs, ",") != "k-1" ||
		strings.Join(got.Tags, ",") != "iduna,provisioned-by:iduna-vultr" {
		t.Errorf("create payload: %+v", got)
	}
	if ud, _ := base64.StdEncoding.DecodeString(got.UserData); string(ud) != "#!/bin/bash\n" {
		t.Error("user_data must be base64 of the script")
	}
}

type fakeCF struct {
	mu   sync.Mutex
	recs map[string]dnsRecord
	n    int
}

func (f *fakeCF) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ok := func(v interface{}) {
		b, _ := json.Marshal(v)
		io.WriteString(w, `{"success":true,"errors":[],"result":`+string(b)+`}`)
	}
	p := r.URL.Path
	switch {
	case p == "/zones":
		ok([]map[string]string{{"id": "z1", "name": "example.com"}})
	case p == "/zones/z1/dns_records" && r.Method == "GET":
		var out []dnsRecord
		for _, x := range f.recs {
			if x.Name == r.URL.Query().Get("name") {
				out = append(out, x)
			}
		}
		ok(out)
	case p == "/zones/z1/dns_records" && r.Method == "POST":
		var d dnsRecord
		json.NewDecoder(r.Body).Decode(&d)
		f.n++
		d.ID = "r" + string(rune('0'+f.n))
		f.recs[d.ID] = d
		ok(d)
	case strings.HasPrefix(p, "/zones/z1/dns_records/") && r.Method == "DELETE":
		delete(f.recs, strings.TrimPrefix(p, "/zones/z1/dns_records/"))
		ok(map[string]string{})
	default:
		w.WriteHeader(404)
	}
}

// Every name gets its own rollback file; the tunnel name is DNS-only; restore puts each name back.
func TestPointAndRestoreEveryName(t *testing.T) {
	f := &fakeCF{recs: map[string]dnsRecord{
		"o1": {ID: "o1", Type: "A", Name: "iam.example.com", Content: "136.68.242.3", Proxied: true, TTL: 1},
		"o2": {ID: "o2", Type: "AAAA", Name: "example.com", Content: "2600::1", Proxied: true, TTL: 1},
	}}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	defer srv.Close()
	cf := newAPI(srv.URL, "t")
	e := baseEnv()
	e["IDUNA_TUNNEL_HOST"] = "tunnel.example.com"
	c, _ := loadConfig(env(e))
	dir := t.TempDir()
	for _, n := range dnsNames(c) {
		if err := pointDNS(cf, c.Zone, "z1", n.name, "203.0.113.7", n.proxied, "abc-123", rollbackPath(dir, n.name)); err != nil {
			t.Fatal(err)
		}
	}
	proxied := map[string]bool{}
	for _, r := range f.recs {
		if r.Content != "203.0.113.7" || r.Type != "A" {
			t.Errorf("leftover record %+v", r)
		}
		proxied[r.Name] = r.Proxied
	}
	want := map[string]bool{"iam.example.com": true, "example.com": true, "www.example.com": true, "tunnel.example.com": false}
	for n, p := range want {
		if got, ok := proxied[n]; !ok || got != p {
			t.Errorf("%s: present=%v proxied=%v, want proxied=%v", n, ok, got, p)
		}
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(files) != 4 {
		t.Fatalf("want 4 rollback files, got %v", files)
	}
	for _, fpath := range files {
		rb, err := readRollback(fpath)
		if err != nil {
			t.Fatal(err)
		}
		if err := restoreDNS(cf, rb); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.recs) != 2 {
		t.Fatalf("after restore want the 2 original records, got %+v", f.recs)
	}
	for _, r := range f.recs {
		if r.Content != "136.68.242.3" && r.Content != "2600::1" {
			t.Errorf("unexpected record after restore: %+v", r)
		}
	}
}
