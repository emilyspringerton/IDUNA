// Copied from WOTAN/ops/vultr/vultr.go @ WOTAN 5857564; only createInstance's tags differ. Keep the two in step.
package main

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"time"
)

type vPlan struct {
	ID          string   `json:"id"`
	VCPU        int      `json:"vcpu_count"`
	RAM         int      `json:"ram"`
	Disk        int      `json:"disk"`
	Bandwidth   int      `json:"bandwidth"`
	MonthlyCost float64  `json:"monthly_cost"`
	Type        string   `json:"type"`
	Locations   []string `json:"locations"`
}

type vOS struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Arch   string `json:"arch"`
	Family string `json:"family"`
}

type vInstance struct {
	ID          string `json:"id"`
	MainIP      string `json:"main_ip"`
	V6MainIP    string `json:"v6_main_ip"`
	Status      string `json:"status"`
	PowerStatus string `json:"power_status"`
	ServerState string `json:"server_status"`
	Label       string `json:"label"`
	Region      string `json:"region"`
	Plan        string `json:"plan"`
	// default_password is deliberately NOT decoded: it is never held, printed or stored by this tool.
}

// findPlan looks the plan up in the public catalogue (no key needed) so the price shown is Vultr's own.
func findPlan(v *api, id string) (*vPlan, error) {
	var r struct {
		Plans []vPlan `json:"plans"`
	}
	if err := v.do("GET", "/plans?per_page=500", nil, &r); err != nil {
		return nil, err
	}
	for i := range r.Plans {
		if r.Plans[i].ID == id {
			return &r.Plans[i], nil
		}
	}
	return nil, fmt.Errorf("plan %q is not in Vultr's plan list", id)
}

func findOS(v *api, id int) (*vOS, error) {
	var r struct {
		OS []vOS `json:"os"`
	}
	if err := v.do("GET", "/os?per_page=500", nil, &r); err != nil {
		return nil, err
	}
	for i := range r.OS {
		if r.OS[i].ID == id {
			return &r.OS[i], nil
		}
	}
	return nil, fmt.Errorf("os id %d is not in Vultr's OS list", id)
}

func planAvailable(v *api, region, plan string) (bool, error) {
	var r struct {
		Available []string `json:"available_plans"`
	}
	if err := v.do("GET", "/regions/"+url.PathEscape(region)+"/availability", nil, &r); err != nil {
		return false, err
	}
	for _, p := range r.Available {
		if p == plan {
			return true, nil
		}
	}
	return false, nil
}

type createReq struct {
	Region     string   `json:"region"`
	Plan       string   `json:"plan"`
	OSID       int      `json:"os_id"`
	Label      string   `json:"label"`
	Hostname   string   `json:"hostname"`
	UserData   string   `json:"user_data"`
	SSHKeyIDs  []string `json:"sshkey_id,omitempty"`
	Backups    string   `json:"backups"`
	EnableIPv6 bool     `json:"enable_ipv6"`
	Tags       []string `json:"tags"`
}

func createInstance(v *api, c config, userData string) (*vInstance, error) {
	req := createReq{
		Region: c.Region, Plan: c.Plan, OSID: c.OSID,
		Label: c.Label, Hostname: c.Label,
		UserData:  base64.StdEncoding.EncodeToString([]byte(userData)),
		SSHKeyIDs: c.SSHKeyIDs,
		// Backups are a paid add-on; they stay off unless the founder adds them (not part of the confirmed price).
		Backups:    "disabled",
		EnableIPv6: true,
		Tags:       []string{"iduna", "provisioned-by:iduna-vultr"},
	}
	var r struct {
		Instance vInstance `json:"instance"`
	}
	if err := v.do("POST", "/instances", req, &r); err != nil {
		return nil, err
	}
	return &r.Instance, nil
}

func getInstance(v *api, id string) (*vInstance, error) {
	var r struct {
		Instance vInstance `json:"instance"`
	}
	if err := v.do("GET", "/instances/"+url.PathEscape(id), nil, &r); err != nil {
		return nil, err
	}
	return &r.Instance, nil
}

// waitActive polls until Vultr reports the instance active with a real IPv4 address.
func waitActive(v *api, id string, timeout, every time.Duration) (*vInstance, error) {
	deadline := time.Now().Add(timeout)
	for {
		in, err := getInstance(v, id)
		if err == nil && in.Status == "active" && in.MainIP != "" && in.MainIP != "0.0.0.0" {
			return in, nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return nil, err
			}
			return in, fmt.Errorf("instance %s not active after %s (status=%s ip=%s)", id, timeout, in.Status, in.MainIP)
		}
		time.Sleep(every)
	}
}

func deleteInstance(v *api, id string) error {
	return v.do("DELETE", "/instances/"+url.PathEscape(id), nil, nil)
}
