package statuspage

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// SystemdUnitActive reports whether a user-scope systemd unit is currently
// active, by asking systemd itself (`systemctl --user is-active <unit>`) —
// the actual process supervisor, not a guess from scanning /proc for a
// process name. This is the only reliable liveness signal available for a
// headless poller with no network endpoint of its own (secwatch, prwatch,
// processor, etc. — most FatBaby pipeline stages have no HTTP or UDP
// surface to probe, unlike newssite/signalapi/SHANKPIT). Only meaningful
// because this checker always runs on the same host, under the same user's
// systemd --user session, as the units it checks (see package doc).
func SystemdUnitActive(unit string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "--user", "is-active", unit).Output()
	if err != nil {
		// A non-active unit makes `systemctl is-active` exit non-zero — that's
		// the expected, common "down" case, not a tool failure.
		return false
	}
	return strings.TrimSpace(string(out)) == "active"
}
