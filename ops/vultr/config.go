package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Everything comes from the environment. As in wotan-vultr, facts the founder has not confirmed (region, plan, OS,
// zone, image, price) have NO default: the tool stops and names the missing variable. Defaults exist only for facts
// read from GitHub itself (EDGE.GAME is public, default branch main) and for the founder's own 2026-11-26 choices
// (hosts iam/apex/www proxied, first admin from a webmaster.json seed).
type config struct {
	Region, Plan string
	OSID         int
	Zone         string // e.g. okemily.com: serves <zone>, www.<zone>, iam.<zone>
	TunnelHost   string // optional DNS-only name for the sc-tunnel port (8443); "" = reach it by IP
	Image        string // IDUNA container image, e.g. ghcr.io/emilyspringerton/iduna:5243d7b
	Label        string
	SSHKeyIDs    []string
	SSHKeyFile   string // local private key matching one of SSHKeyIDs: secrets go in over SSH, never user-data

	WebmasterFile string // local webmaster.json (email/password/display_name): seeds IDUNA's uid 0 on first boot
	EnvFile       string // optional local env file for IDUNA (GOOGLE_CLIENT_ID, ...), copied to /etc/iduna/iduna.env
	DWLogsSrc     string // optional DEADWEIGHT logserve URL for the logsync sidecar

	EdgeGitURL, EdgeGitRef       string // EDGE.GAME, where tools/sc_tunnel.c lives (built on the box)
	OkemilyGitURL, OkemilyGitRef string // OKEMILY, the okemily.com site rsynced into var/www (okemily-deploy.sh's rules)

	ConfirmMonthlyUSD string

	VultrKey string
	CFToken  string

	OriginCertFile, OriginKeyFile string
	OriginOutDir                  string
	RollbackDir                   string // one rollback file per DNS name; the directory must be empty or absent

	VultrBase, CFBase string
}

var (
	reDomain = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
	reSlug   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	rePlan   = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,62}$`)
	reRef    = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,100}$`)
	reGitURL = regexp.MustCompile(`^https://[A-Za-z0-9.-]+/[A-Za-z0-9._/-]+$`)
	reID     = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
	reImage  = regexp.MustCompile(`^[a-z0-9.-]+(:[0-9]{1,5})?(/[a-z0-9._-]+)+(:[A-Za-z0-9._-]{1,128}|@sha256:[a-f0-9]{64})$`)
	reURL    = regexp.MustCompile(`^https?://[A-Za-z0-9.-]+(:[0-9]{1,5})?$`)
)

// secret reads NAME_FILE (preferred) or NAME. Never printed.
func secret(name string) (string, error) {
	if p := os.Getenv(name + "_FILE"); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			return "", fmt.Errorf("%s_FILE: %v", name, err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	return strings.TrimSpace(os.Getenv(name)), nil
}

func loadConfig(getenv func(string) string) (config, []string) {
	var c config
	var missing []string
	need := func(k string) string {
		v := strings.TrimSpace(getenv(k))
		if v == "" {
			missing = append(missing, k)
		}
		return v
	}
	opt := func(k string) string { return strings.TrimSpace(getenv(k)) }
	c.Region = need("IDUNA_VULTR_REGION")
	c.Plan = need("IDUNA_VULTR_PLAN")
	if s := need("IDUNA_VULTR_OS_ID"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 {
			missing = append(missing, "IDUNA_VULTR_OS_ID (must be a number from GET /v2/os)")
		}
		c.OSID = n
	}
	c.Zone = strings.ToLower(need("IDUNA_ZONE"))
	c.TunnelHost = strings.ToLower(opt("IDUNA_TUNNEL_HOST"))
	c.Image = need("IDUNA_IMAGE")
	c.Label = opt("IDUNA_VULTR_LABEL")
	if c.Label == "" {
		c.Label = "iduna"
	}
	for _, s := range strings.Split(getenv("IDUNA_VULTR_SSH_KEY_IDS"), ",") {
		if s = strings.TrimSpace(s); s != "" {
			c.SSHKeyIDs = append(c.SSHKeyIDs, s)
		}
	}
	if len(c.SSHKeyIDs) == 0 {
		missing = append(missing, "IDUNA_VULTR_SSH_KEY_IDS")
	}
	c.SSHKeyFile = need("IDUNA_SSH_KEY_FILE")
	c.WebmasterFile = need("IDUNA_WEBMASTER_FILE")
	c.EnvFile = opt("IDUNA_ENV_FILE")
	c.DWLogsSrc = strings.TrimRight(opt("IDUNA_DWLOGS_SRC"), "/")
	c.EdgeGitURL = opt("IDUNA_EDGE_GIT_URL")
	if c.EdgeGitURL == "" {
		c.EdgeGitURL = "https://github.com/emilyspringerton/EDGE.GAME.git" // public, checked 2026-11-26
	}
	c.EdgeGitRef = opt("IDUNA_EDGE_GIT_REF")
	if c.EdgeGitRef == "" {
		c.EdgeGitRef = "main"
	}
	c.OkemilyGitURL = opt("IDUNA_OKEMILY_GIT_URL")
	if c.OkemilyGitURL == "" {
		c.OkemilyGitURL = "https://github.com/emilyspringerton/OKEMILY.git" // public, checked 2026-11-26
	}
	c.OkemilyGitRef = opt("IDUNA_OKEMILY_GIT_REF")
	if c.OkemilyGitRef == "" {
		c.OkemilyGitRef = "main"
	}
	c.ConfirmMonthlyUSD = opt("IDUNA_CONFIRM_MONTHLY_USD")
	c.OriginCertFile = opt("IDUNA_ORIGIN_CERT_FILE")
	c.OriginKeyFile = opt("IDUNA_ORIGIN_KEY_FILE")
	c.OriginOutDir = opt("IDUNA_ORIGIN_OUT_DIR")
	c.RollbackDir = opt("IDUNA_ROLLBACK_DIR")
	c.VultrBase = opt("IDUNA_VULTR_API_BASE")
	if c.VultrBase == "" {
		c.VultrBase = "https://api.vultr.com/v2"
	}
	c.CFBase = opt("IDUNA_CF_API_BASE")
	if c.CFBase == "" {
		c.CFBase = "https://api.cloudflare.com/client/v4"
	}
	return c, missing
}

// hosts are the proxied names IDUNA serves (founder, 2026-11-26: iam + apex + www; console stays off for now).
func (c config) hosts() []string {
	if c.Zone == "" {
		return nil
	}
	return []string{"iam." + c.Zone, c.Zone, "www." + c.Zone}
}

// validate rejects anything that would be interpolated into the first-boot script or the compose file unless it
// matches a strict pattern.
func (c config) validate() error {
	checks := []struct {
		ok   bool
		what string
	}{
		{c.Region == "" || reSlug.MatchString(c.Region), "IDUNA_VULTR_REGION"},
		{c.Plan == "" || rePlan.MatchString(c.Plan), "IDUNA_VULTR_PLAN"},
		{c.Zone == "" || reDomain.MatchString(c.Zone), "IDUNA_ZONE"},
		{c.TunnelHost == "" || (reDomain.MatchString(c.TunnelHost) && strings.HasSuffix(c.TunnelHost, "."+c.Zone)), "IDUNA_TUNNEL_HOST (a name inside IDUNA_ZONE)"},
		{c.Image == "" || reImage.MatchString(c.Image), "IDUNA_IMAGE (registry/repo:tag, no 'latest' ambiguity checks here)"},
		{reSlug.MatchString(c.Label), "IDUNA_VULTR_LABEL"},
		{reGitURL.MatchString(c.EdgeGitURL), "IDUNA_EDGE_GIT_URL (https only)"},
		{reRef.MatchString(c.EdgeGitRef) && !strings.Contains(c.EdgeGitRef, ".."), "IDUNA_EDGE_GIT_REF"},
		{reGitURL.MatchString(c.OkemilyGitURL), "IDUNA_OKEMILY_GIT_URL (https only)"},
		{reRef.MatchString(c.OkemilyGitRef) && !strings.Contains(c.OkemilyGitRef, ".."), "IDUNA_OKEMILY_GIT_REF"},
		{c.DWLogsSrc == "" || reURL.MatchString(c.DWLogsSrc), "IDUNA_DWLOGS_SRC (http(s)://host[:port], no path)"},
	}
	for _, id := range c.SSHKeyIDs {
		checks = append(checks, struct {
			ok   bool
			what string
		}{reID.MatchString(id), "IDUNA_VULTR_SSH_KEY_IDS"})
	}
	for _, ch := range checks {
		if !ch.ok {
			return fmt.Errorf("invalid %s", ch.what)
		}
	}
	if c.Image != "" && strings.HasSuffix(c.Image, ":latest") {
		return fmt.Errorf("invalid IDUNA_IMAGE: pin a commit tag, not :latest (a reboot must not change what runs)")
	}
	return nil
}

// priceMatches compares the founder's confirmed figure with Vultr's monthly_cost to the cent.
func priceMatches(confirmed string, cost float64) bool {
	f, err := strconv.ParseFloat(strings.TrimPrefix(confirmed, "$"), 64)
	if err != nil {
		return false
	}
	return int64(f*100+0.5) == int64(cost*100+0.5)
}
