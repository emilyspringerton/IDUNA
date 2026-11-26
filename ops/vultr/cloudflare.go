// Copied from WOTAN/ops/vultr/cloudflare.go @ WOTAN 5857564. Differences: pointDNS takes proxied (the sc-tunnel name
// must be DNS-only: Cloudflare's proxy carries HTTP, not a raw TCP port), and mintOriginCert's comment (the key travels
// over SSH here, never in user-data). Keep the rest in step.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/url"
	"os"
	"time"
)

type cfEnvelope struct {
	Success bool            `json:"success"`
	Errors  json.RawMessage `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

func cfCall(cf *api, method, path string, body, result interface{}) error {
	var env cfEnvelope
	if err := cf.do(method, path, body, &env); err != nil {
		return err
	}
	if !env.Success {
		return fmt.Errorf("cloudflare %s %s: success=false: %s", method, path, string(env.Errors))
	}
	if result != nil && len(env.Result) > 0 {
		return json.Unmarshal(env.Result, result)
	}
	return nil
}

func cfZoneID(cf *api, zone string) (string, error) {
	var zs []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := cfCall(cf, "GET", "/zones?name="+url.QueryEscape(zone), nil, &zs); err != nil {
		return "", err
	}
	if len(zs) != 1 {
		return "", fmt.Errorf("cloudflare: expected one zone named %q, got %d", zone, len(zs))
	}
	return zs[0].ID, nil
}

// cfSSLMode reads the zone's SSL/TLS mode. Read only: this tool never changes it.
func cfSSLMode(cf *api, zoneID string) (string, error) {
	var s struct {
		Value string `json:"value"`
	}
	err := cfCall(cf, "GET", "/zones/"+url.PathEscape(zoneID)+"/settings/ssl", nil, &s)
	return s.Value, err
}

type dnsRecord struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
	TTL     int    `json:"ttl"`
}

func cfRecords(cf *api, zoneID, name string) ([]dnsRecord, error) {
	var rs []dnsRecord
	err := cfCall(cf, "GET", "/zones/"+url.PathEscape(zoneID)+"/dns_records?per_page=100&name="+url.QueryEscape(name), nil, &rs)
	return rs, err
}

// rollback is written BEFORE any DNS change: everything needed to put the name back exactly as it was.
type rollback struct {
	Zone       string      `json:"zone"`
	ZoneID     string      `json:"zone_id"`
	Name       string      `json:"name"`
	Before     []dnsRecord `json:"before"`
	CreatedID  string      `json:"created_id,omitempty"`
	InstanceID string      `json:"instance_id,omitempty"`
	At         string      `json:"at"`
}

func writeJSON(path string, v interface{}) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

// pointDNS replaces whatever answers for name (A/AAAA/CNAME) with one A record to ip (proxied or DNS-only). The previous records go
// to rollbackPath first; if that write fails nothing is changed.
func pointDNS(cf *api, zone, zoneID, name, ip string, proxied bool, instanceID, rollbackPath string) error {
	before, err := cfRecords(cf, zoneID, name)
	if err != nil {
		return err
	}
	rb := rollback{Zone: zone, ZoneID: zoneID, Name: name, InstanceID: instanceID, At: time.Now().UTC().Format(time.RFC3339)}
	for _, r := range before {
		if r.Type == "A" || r.Type == "AAAA" || r.Type == "CNAME" {
			rb.Before = append(rb.Before, r)
		}
	}
	if err := writeJSON(rollbackPath, rb); err != nil {
		return fmt.Errorf("could not write the rollback file, DNS left untouched: %v", err)
	}
	for _, r := range rb.Before {
		if err := cfCall(cf, "DELETE", "/zones/"+url.PathEscape(zoneID)+"/dns_records/"+url.PathEscape(r.ID), nil, nil); err != nil {
			return fmt.Errorf("removing old %s record (rollback file %s has it): %v", r.Type, rollbackPath, err)
		}
	}
	var created dnsRecord
	if err := cfCall(cf, "POST", "/zones/"+url.PathEscape(zoneID)+"/dns_records",
		dnsRecord{Type: "A", Name: name, Content: ip, Proxied: proxied, TTL: 1}, &created); err != nil {
		return fmt.Errorf("creating the new A record (old records are in %s): %v", rollbackPath, err)
	}
	rb.CreatedID = created.ID
	return writeJSON(rollbackPath, rb)
}

func readRollback(path string) (rollback, error) {
	var rb rollback
	b, err := os.ReadFile(path)
	if err != nil {
		return rb, err
	}
	err = json.Unmarshal(b, &rb)
	return rb, err
}

// restoreDNS undoes pointDNS from its rollback file.
func restoreDNS(cf *api, rb rollback) error {
	if rb.CreatedID != "" {
		if err := cfCall(cf, "DELETE", "/zones/"+url.PathEscape(rb.ZoneID)+"/dns_records/"+url.PathEscape(rb.CreatedID), nil, nil); err != nil {
			return err
		}
	}
	for _, r := range rb.Before {
		r.ID = ""
		if err := cfCall(cf, "POST", "/zones/"+url.PathEscape(rb.ZoneID)+"/dns_records", r, nil); err != nil {
			return fmt.Errorf("re-creating %s %s -> %s: %v", r.Type, r.Name, r.Content, err)
		}
	}
	return nil
}

// mintOriginCert makes a Cloudflare Origin CA certificate (free) for the hostnames. The private key is generated here
// and never sent anywhere except to the new instance, over SSH.
func mintOriginCert(cf *api, hostnames []string) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: hostnames[0]}, DNSNames: hostnames,
	}, key)
	if err != nil {
		return nil, nil, err
	}
	var res struct {
		Certificate string `json:"certificate"`
	}
	if err := cfCall(cf, "POST", "/certificates", map[string]interface{}{
		"hostnames":          hostnames,
		"requested_validity": 5475,
		"request_type":       "origin-ecc",
		"csr":                string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})),
	}, &res); err != nil {
		return nil, nil, err
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return []byte(res.Certificate), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), nil
}
