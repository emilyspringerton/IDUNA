// iduna-vultr: provision a Vultr instance that runs IDUNA (compose: iduna + okemily-web + sc-tunnel + logsync) behind
// Cloudflare. WOTAN's box is wotan-vultr (WOTAN/ops/vultr); this is its sibling, same guard rails.
//
//	plan [--dns]             read-only: checks the env and the local files, shows Vultr's price, spends nothing
//	apply [--dns]            buys ONE instance (only if IDUNA_CONFIRM_MONTHLY_USD equals the plan's price), pushes the
//	                         secrets in over SSH, starts IDUNA; --dns then points iam/<zone>/www (proxied) and
//	                         IDUNA_TUNNEL_HOST (DNS-only) at it, saving the old records first
//	status --instance ID     shows the instance's state
//	destroy --instance ID    deletes it (IDUNA_CONFIRM_DESTROY=ID required); --restore-dns puts DNS back first
//
// See README.md. Run with GOWORK=off from inside the monorepo (its go.work does not list this module).
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "plan":
		err = cmdPlan(os.Args[2:])
	case "apply":
		err = cmdApply(os.Args[2:])
	case "status":
		err = cmdStatus(os.Args[2:])
	case "destroy":
		err = cmdDestroy(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "STOPPED:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: iduna-vultr plan [--dns] | apply [--dns] | status --instance ID | destroy --instance ID [--restore-dns]")
}

type checked struct {
	cfg   config
	plan  *vPlan
	os    *vOS
	vultr *api
	cf    *api
}

func prepare(needKey bool) (*checked, error) {
	c, missing := loadConfig(os.Getenv)
	if err := c.validate(); err != nil {
		return nil, err
	}
	key, err := secret("VULTR_API_KEY")
	if err != nil {
		return nil, err
	}
	if needKey && key == "" {
		missing = append(missing, "VULTR_API_KEY_FILE (or VULTR_API_KEY)")
	}
	c.VultrKey = key
	if c.CFToken, err = secret("CLOUDFLARE_API_TOKEN"); err != nil {
		return nil, err
	}
	pub := newAPI(c.VultrBase, "")
	if len(missing) > 0 {
		hint(pub, c, missing)
		return nil, fmt.Errorf("missing or invalid: %s", strings.Join(missing, ", "))
	}
	if err := checkLocalFiles(c); err != nil {
		return nil, err
	}
	ch := &checked{cfg: c, vultr: newAPI(c.VultrBase, key)}
	if c.CFToken != "" {
		ch.cf = newAPI(c.CFBase, c.CFToken)
	}
	if ch.plan, err = findPlan(pub, c.Plan); err != nil {
		return nil, err
	}
	if ch.os, err = findOS(pub, c.OSID); err != nil {
		return nil, err
	}
	if f := strings.ToLower(ch.os.Family); f != "ubuntu" {
		return nil, fmt.Errorf("os %d (%s) is family %q: the first-boot script installs docker-compose-v2 from Ubuntu's archive", ch.os.ID, ch.os.Name, ch.os.Family)
	}
	ok, err := planAvailable(pub, c.Region, c.Plan)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("plan %s is not available in region %s right now", c.Plan, c.Region)
	}
	return ch, nil
}

// checkLocalFiles: the SSH key and the webmaster seed are read before anything is bought, so a typo costs nothing.
func checkLocalFiles(c config) error {
	if st, err := os.Stat(c.SSHKeyFile); err != nil || st.IsDir() {
		return fmt.Errorf("IDUNA_SSH_KEY_FILE %s: not a readable file", c.SSHKeyFile)
	}
	b, err := os.ReadFile(c.WebmasterFile)
	if err != nil {
		return fmt.Errorf("IDUNA_WEBMASTER_FILE: %v", err)
	}
	var w struct {
		Email       string `json:"email"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
	}
	if err := json.Unmarshal(b, &w); err != nil {
		return fmt.Errorf("IDUNA_WEBMASTER_FILE is not JSON: %v", err)
	}
	if w.Email == "" || w.Password == "" || w.DisplayName == "" {
		return errors.New("IDUNA_WEBMASTER_FILE needs email, password and display_name (IDUNA seeds uid 0 from it)")
	}
	if len(w.Password) < 12 {
		return errors.New("IDUNA_WEBMASTER_FILE: use a password of 12+ characters for the only admin on a public box")
	}
	if c.EnvFile != "" {
		if _, err := os.ReadFile(c.EnvFile); err != nil {
			return fmt.Errorf("IDUNA_ENV_FILE: %v", err)
		}
	}
	return nil
}

func hint(pub *api, c config, missing []string) {
	has := func(k string) bool {
		for _, m := range missing {
			if strings.HasPrefix(m, k) {
				return true
			}
		}
		return false
	}
	if has("IDUNA_VULTR_PLAN") {
		var r struct {
			Plans []vPlan `json:"plans"`
		}
		if pub.do("GET", "/plans?type=vc2&per_page=500", nil, &r) == nil {
			sort.Slice(r.Plans, func(i, j int) bool { return r.Plans[i].MonthlyCost < r.Plans[j].MonthlyCost })
			fmt.Println("Cheapest Vultr cloud-compute (vc2) plans (IDUNA + docker want 2 GB RAM or more):")
			n := 0
			for _, p := range r.Plans {
				if p.RAM < 2048 || n == 6 {
					continue
				}
				n++
				fmt.Printf("  %-18s %d vCPU  %5d MB RAM  %4d GB disk  $%.2f/month\n", p.ID, p.VCPU, p.RAM, p.Disk, p.MonthlyCost)
			}
		}
	}
	if has("IDUNA_VULTR_REGION") {
		var r struct {
			Regions []struct{ ID, City, Country string } `json:"regions"`
		}
		if pub.do("GET", "/regions?per_page=500", nil, &r) == nil {
			var ids []string
			for _, x := range r.Regions {
				ids = append(ids, x.ID+"="+x.City)
			}
			fmt.Println("Regions:", strings.Join(ids, ", "))
		}
	}
	if has("IDUNA_VULTR_OS_ID") {
		var r struct {
			OS []vOS `json:"os"`
		}
		if pub.do("GET", "/os?per_page=500", nil, &r) == nil {
			fmt.Println("Ubuntu images:")
			for _, o := range r.OS {
				if strings.ToLower(o.Family) == "ubuntu" && o.Arch == "x64" {
					fmt.Printf("  %5d  %s\n", o.ID, o.Name)
				}
			}
		}
	}
	if has("IDUNA_IMAGE") {
		fmt.Println("IDUNA_IMAGE: a commit tag of ghcr.io/emilyspringerton/iduna (IDUNA's release workflow pushes one per main commit),")
		fmt.Println("  e.g. ghcr.io/emilyspringerton/iduna:$(git -C IDUNA rev-parse --short HEAD) if that commit's release run passed.")
	}
}

func tlsSource(c config, cf *api) string {
	switch {
	case c.OriginCertFile != "" && c.OriginKeyFile != "":
		return "origin cert from IDUNA_ORIGIN_CERT_FILE / IDUNA_ORIGIN_KEY_FILE (must cover " + strings.Join(c.hosts(), ", ") + ")"
	case cf != nil && c.OriginOutDir != "":
		return "mint a free Cloudflare Origin CA cert for " + strings.Join(c.hosts(), ", ") + " (saved to IDUNA_ORIGIN_OUT_DIR)"
	}
	return ""
}

func dnsNames(c config) []struct {
	name    string
	proxied bool
} {
	var out []struct {
		name    string
		proxied bool
	}
	for _, h := range c.hosts() {
		out = append(out, struct {
			name    string
			proxied bool
		}{h, true})
	}
	if c.TunnelHost != "" {
		// sc-tunnel is raw TCP on 8443: Cloudflare's proxy only carries HTTP, so this one name is DNS-only.
		out = append(out, struct {
			name    string
			proxied bool
		}{c.TunnelHost, false})
	}
	return out
}

func cmdPlan(args []string) error {
	fs := flag.NewFlagSet("plan", flag.ExitOnError)
	dns := fs.Bool("dns", false, "also check the Cloudflare side of apply --dns")
	_ = fs.Parse(args)
	ch, err := prepare(false)
	if err != nil {
		return err
	}
	c := ch.cfg
	fmt.Printf("IDUNA on Vultr: plan only, nothing bought.\n")
	fmt.Printf("  instance   %s in %s, %s (%d vCPU, %d MB RAM, %d GB disk, %d GB transfer)\n",
		c.Plan, c.Region, ch.os.Name, ch.plan.VCPU, ch.plan.RAM, ch.plan.Disk, ch.plan.Bandwidth)
	fmt.Printf("  price      $%.2f/month (Vultr's list price, backups off)\n", ch.plan.MonthlyCost)
	fmt.Printf("  image      %s (+ okemily-web, sc-tunnel built from %s @ %s, logsync)\n", c.Image, c.EdgeGitURL, c.EdgeGitRef)
	fmt.Printf("  serves     %s (proxied)\n", strings.Join(c.hosts(), ", "))
	if c.TunnelHost != "" {
		fmt.Printf("  tunnel     %s:8443 (DNS-only)\n", c.TunnelHost)
	} else {
		fmt.Printf("  tunnel     <box IP>:8443 (IDUNA_TUNNEL_HOST unset: no DNS name)\n")
	}
	fmt.Printf("  admin      uid 0 seeded from %s, which is shredded on the box once IDUNA is up\n", c.WebmasterFile)
	if c.DWLogsSrc == "" {
		fmt.Printf("  logsync    no DEADWEIGHT yet (IDUNA_DWLOGS_SRC unset): retries every 30 s\n")
	}
	if src := tlsSource(c, ch.cf); src != "" {
		fmt.Printf("  TLS        %s\n", src)
	} else {
		fmt.Printf("  TLS        MISSING: set IDUNA_ORIGIN_CERT_FILE+IDUNA_ORIGIN_KEY_FILE, or CLOUDFLARE_API_TOKEN_FILE+IDUNA_ORIGIN_OUT_DIR\n")
	}
	if *dns {
		if err := checkCF(ch); err != nil {
			return err
		}
	}
	if c.VultrKey == "" {
		fmt.Println("  key        VULTR_API_KEY_FILE not set (apply needs it)")
	}
	fmt.Printf("\nTo buy it, the founder confirms the price by setting it:\n  IDUNA_CONFIRM_MONTHLY_USD=%.2f ... iduna-vultr apply%s\n",
		ch.plan.MonthlyCost, map[bool]string{true: " --dns", false: ""}[*dns])
	return nil
}

func checkCF(ch *checked) error {
	c := ch.cfg
	if ch.cf == nil || c.RollbackDir == "" {
		return errors.New("--dns needs CLOUDFLARE_API_TOKEN_FILE and IDUNA_ROLLBACK_DIR")
	}
	zid, err := cfZoneID(ch.cf, c.Zone)
	if err != nil {
		return err
	}
	mode, err := cfSSLMode(ch.cf, zid)
	if err != nil {
		return err
	}
	fmt.Printf("  cloudflare zone %s, SSL mode %q\n", c.Zone, mode)
	if mode != "full" && mode != "strict" {
		return fmt.Errorf("zone SSL mode is %q: this origin needs Full or Full (strict). Not changed here: the founder decides", mode)
	}
	for _, n := range dnsNames(c) {
		recs, err := cfRecords(ch.cf, zid, n.name)
		if err != nil {
			return err
		}
		for _, r := range recs {
			// pointDNS only replaces address records; MX/TXT/etc. at the same name (the apex's email routing) stay.
			if r.Type == "A" || r.Type == "AAAA" || r.Type == "CNAME" {
				fmt.Printf("  existing   %s %s -> %s (proxied=%v): would be replaced, saved first\n", r.Type, r.Name, r.Content, r.Proxied)
			} else {
				fmt.Printf("  kept       %s %s -> %s (not an address record: untouched)\n", r.Type, r.Name, r.Content)
			}
		}
	}
	if ents, err := os.ReadDir(c.RollbackDir); err == nil && len(ents) > 0 {
		return fmt.Errorf("IDUNA_ROLLBACK_DIR %s is not empty: refusing to overwrite an earlier rollback", c.RollbackDir)
	}
	return nil
}

func rollbackPath(dir, name string) string { return filepath.Join(dir, name+".json") }

func cmdApply(args []string) error {
	fs := flag.NewFlagSet("apply", flag.ExitOnError)
	dns := fs.Bool("dns", false, "point the IDUNA names at the new instance on Cloudflare")
	_ = fs.Parse(args)
	ch, err := prepare(true)
	if err != nil {
		return err
	}
	c := ch.cfg
	if c.ConfirmMonthlyUSD == "" || !priceMatches(c.ConfirmMonthlyUSD, ch.plan.MonthlyCost) {
		return fmt.Errorf("IDUNA_CONFIRM_MONTHLY_USD=%q does not match %s's price $%.2f/month: nothing bought",
			c.ConfirmMonthlyUSD, c.Plan, ch.plan.MonthlyCost)
	}
	if tlsSource(c, ch.cf) == "" {
		return errors.New("no TLS material (see plan): nothing bought")
	}
	if *dns {
		if err := checkCF(ch); err != nil {
			return fmt.Errorf("%v: nothing bought", err)
		}
	}

	// 1. TLS first: free, and failing here costs nothing.
	var certPEM, keyPEM []byte
	if c.OriginCertFile != "" {
		if certPEM, err = os.ReadFile(c.OriginCertFile); err != nil {
			return err
		}
		if keyPEM, err = os.ReadFile(c.OriginKeyFile); err != nil {
			return err
		}
	} else {
		if certPEM, keyPEM, err = mintOriginCert(ch.cf, c.hosts()); err != nil {
			return fmt.Errorf("minting the origin cert: %v: nothing bought", err)
		}
		if err := os.MkdirAll(c.OriginOutDir, 0o700); err != nil {
			return err
		}
		stamp := time.Now().UTC().Format("20060102T150405Z")
		for name, b := range map[string][]byte{"iduna-origin-" + stamp + ".pem": certPEM, "iduna-origin-" + stamp + ".key": keyPEM} {
			if err := os.WriteFile(filepath.Join(c.OriginOutDir, name), b, 0o600); err != nil {
				return err
			}
		}
		fmt.Printf("Origin CA cert minted for %s, saved in %s (iduna-origin-%s.*).\n", strings.Join(c.hosts(), ", "), c.OriginOutDir, stamp)
	}
	ud, err := renderUserData(c)
	if err != nil {
		return err
	}

	// 2. The one paid call.
	in, err := createInstance(ch.vultr, c, ud)
	if err != nil {
		return err
	}
	fmt.Printf("Bought: Vultr instance %s (%s, %s, $%.2f/month). Undo: iduna-vultr destroy --instance %s\n",
		in.ID, c.Plan, c.Region, ch.plan.MonthlyCost, in.ID)
	in, err = waitActive(ch.vultr, in.ID, 10*time.Minute, 10*time.Second)
	if err != nil {
		return err
	}
	fmt.Printf("Active: %s (IPv4 %s). First boot installs docker + nginx (a few minutes).\n", in.ID, in.MainIP)

	// 3. Secrets over SSH, then start the stack.
	s := sshTarget{host: in.MainIP, key: c.SSHKeyFile, knownHosts: filepath.Join(filepath.Dir(c.SSHKeyFile), "iduna-vultr-known_hosts")}
	if err := s.waitReady(15 * time.Minute); err != nil {
		return fmt.Errorf("box never finished first boot, nothing pushed (instance %s still billed): %v", in.ID, err)
	}
	if err := pushSecrets(s, c, certPEM, keyPEM); err != nil {
		return fmt.Errorf("pushing secrets (instance %s still billed): %v", in.ID, err)
	}
	fmt.Println("Secrets pushed over SSH. Starting IDUNA (pulls the image, builds sc-tunnel)...")
	if out, err := s.run("/usr/local/bin/iduna-up", nil); err != nil {
		return fmt.Errorf("iduna-up failed (instance %s still billed):\n%s\n%v", in.ID, out, err)
	} else {
		fmt.Print(out)
	}

	// 4. DNS, with the old records saved first.
	if !*dns {
		fmt.Printf("DNS not touched. To serve it: re-run with --dns, or point proxied A records for %s at %s.\n", strings.Join(c.hosts(), ", "), in.MainIP)
		return nil
	}
	zid, err := cfZoneID(ch.cf, c.Zone)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(c.RollbackDir, 0o700); err != nil {
		return err
	}
	for _, n := range dnsNames(c) {
		if err := pointDNS(ch.cf, c.Zone, zid, n.name, in.MainIP, n.proxied, in.ID, rollbackPath(c.RollbackDir, n.name)); err != nil {
			return err
		}
		fmt.Printf("DNS: %s -> %s (proxied=%v)\n", n.name, in.MainIP, n.proxied)
	}
	fmt.Printf("Rollback: iduna-vultr destroy --instance %s --restore-dns (files in %s)\n", in.ID, c.RollbackDir)
	if err := waitHTTP("https://iam."+c.Zone+"/health", http.StatusOK, 10*time.Minute); err != nil {
		return err
	}
	fmt.Printf("UP: https://iam.%s/health answers 200 through Cloudflare with TLS verified.\n", c.Zone)
	return nil
}

// sshTarget runs the system ssh client (Git Bash / OpenSSH both have it). Host keys: trust on first use, into a
// dedicated known_hosts next to the key, so a later run against the same IP with a different key fails loudly.
type sshTarget struct{ host, key, knownHosts string }

func (s sshTarget) args(extra ...string) []string {
	return append([]string{"-i", s.key, "-o", "BatchMode=yes", "-o", "ConnectTimeout=10",
		"-o", "StrictHostKeyChecking=accept-new", "-o", "UserKnownHostsFile=" + s.knownHosts,
		"root@" + s.host}, extra...)
}

func (s sshTarget) run(cmd string, stdin []byte) (string, error) {
	c := exec.Command("ssh", s.args(cmd)...)
	if stdin != nil {
		c.Stdin = strings.NewReader(string(stdin))
	}
	out, err := c.CombinedOutput()
	return string(out), err
}

// waitReady: SSH answers and cloud-init has finished the first-boot script.
func (s sshTarget) waitReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	last := ""
	for time.Now().Before(deadline) {
		out, err := s.run("cloud-init status --wait >/dev/null; test -x /usr/local/bin/iduna-up && echo ready", nil)
		if err == nil && strings.Contains(out, "ready") {
			return nil
		}
		last = strings.TrimSpace(out)
		time.Sleep(15 * time.Second)
	}
	return fmt.Errorf("not ready after %s (last: %s)", timeout, last)
}

// pushSecrets writes each file straight from stdin with its final mode: nothing lands world-readable, even briefly.
func pushSecrets(s sshTarget, c config, certPEM, keyPEM []byte) error {
	files := []struct {
		path string
		mode string
		data []byte
	}{
		{"/etc/ssl/iduna/origin.pem", "644", certPEM},
		{"/etc/ssl/iduna/origin.key", "600", keyPEM},
	}
	wm, err := os.ReadFile(c.WebmasterFile)
	if err != nil {
		return err
	}
	files = append(files, struct {
		path string
		mode string
		data []byte
	}{"/srv/iduna/var/webmaster.json", "600", wm})
	if c.EnvFile != "" {
		env, err := os.ReadFile(c.EnvFile)
		if err != nil {
			return err
		}
		files = append(files, struct {
			path string
			mode string
			data []byte
		}{"/etc/iduna/iduna.env", "600", env})
	}
	for _, f := range files {
		if out, err := s.run("install -m "+f.mode+" /dev/stdin "+f.path, f.data); err != nil {
			return fmt.Errorf("%s: %v %s", f.path, err, strings.TrimSpace(out))
		}
	}
	return nil
}

// waitHTTP polls url (TLS always verified, redirects not followed) until it answers want.
func waitHTTP(u string, want int, timeout time.Duration) error {
	hc := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	deadline := time.Now().Add(timeout)
	last := ""
	for time.Now().Before(deadline) {
		resp, err := hc.Get(u)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == want {
				return nil
			}
			last = resp.Status
		} else {
			last = err.Error()
		}
		time.Sleep(10 * time.Second)
	}
	return fmt.Errorf("%s did not answer %d within %s (last: %s)", u, want, timeout, last)
}

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	id := fs.String("instance", "", "Vultr instance id")
	_ = fs.Parse(args)
	key, err := secret("VULTR_API_KEY")
	if err != nil || key == "" || *id == "" {
		return errors.New("status needs --instance and VULTR_API_KEY_FILE")
	}
	c, _ := loadConfig(os.Getenv)
	in, err := getInstance(newAPI(c.VultrBase, key), *id)
	if err != nil {
		return err
	}
	fmt.Printf("%s %s %s/%s/%s ip=%s ipv6=%s plan=%s region=%s\n", in.ID, in.Label, in.Status, in.PowerStatus, in.ServerState, in.MainIP, in.V6MainIP, in.Plan, in.Region)
	return nil
}

func cmdDestroy(args []string) error {
	fs := flag.NewFlagSet("destroy", flag.ExitOnError)
	id := fs.String("instance", "", "Vultr instance id")
	restore := fs.Bool("restore-dns", false, "first put DNS back from IDUNA_ROLLBACK_DIR")
	_ = fs.Parse(args)
	if *id == "" || !reID.MatchString(*id) {
		return errors.New("destroy needs --instance ID")
	}
	if os.Getenv("IDUNA_CONFIRM_DESTROY") != *id {
		return fmt.Errorf("set IDUNA_CONFIRM_DESTROY=%s to delete that instance (its disk, and IDUNA's database on it, are gone for good)", *id)
	}
	c, _ := loadConfig(os.Getenv)
	key, err := secret("VULTR_API_KEY")
	if err != nil || key == "" {
		return errors.New("destroy needs VULTR_API_KEY_FILE")
	}
	if *restore {
		tok, err := secret("CLOUDFLARE_API_TOKEN")
		if err != nil || tok == "" || c.RollbackDir == "" {
			return errors.New("--restore-dns needs CLOUDFLARE_API_TOKEN_FILE and IDUNA_ROLLBACK_DIR")
		}
		files, _ := filepath.Glob(filepath.Join(c.RollbackDir, "*.json"))
		if len(files) == 0 {
			return fmt.Errorf("no rollback files in %s, instance NOT deleted", c.RollbackDir)
		}
		cf := newAPI(c.CFBase, tok)
		for _, f := range files {
			rb, err := readRollback(f)
			if err != nil {
				return err
			}
			if rb.InstanceID != "" && rb.InstanceID != *id {
				return fmt.Errorf("rollback file %s belongs to instance %s, not %s", f, rb.InstanceID, *id)
			}
			if err := restoreDNS(cf, rb); err != nil {
				return fmt.Errorf("DNS restore of %s failed, instance NOT deleted: %v", rb.Name, err)
			}
			fmt.Printf("DNS for %s restored to its %d earlier record(s).\n", rb.Name, len(rb.Before))
		}
	}
	if err := deleteInstance(newAPI(c.VultrBase, key), *id); err != nil {
		return err
	}
	fmt.Printf("Deleted Vultr instance %s (billing stops).\n", *id)
	return nil
}
