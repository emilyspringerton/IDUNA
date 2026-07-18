package statuspage

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// UDPPortBound reports whether any local process currently has a socket
// bound to the given UDP port, by reading /proc/net/udp (and /proc/net/udp6).
//
// This is a real, honest liveness signal for a raw UDP service running on
// this same box — not a full application-level protocol ping (that would
// need to speak SHANKPIT's wire format), but genuinely stronger than "the
// process exists" since it confirms the socket is actually bound, not just
// that a PID is alive. Linux-specific; only meaningful because this checker
// always runs on the same host as the services it checks (see package doc).
func UDPPortBound(port int) bool {
	return udpPortBoundIn("/proc/net/udp", port) || udpPortBoundIn("/proc/net/udp6", port)
}

func udpPortBoundIn(path string, port int) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	wantHex := strings.ToUpper(fmt.Sprintf("%04X", port))
	scanner := bufio.NewScanner(f)
	scanner.Scan() // header line
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		// fields[1] is "local_address" formatted as IP:PORT in hex, e.g. "00000000:1B39".
		parts := strings.Split(fields[1], ":")
		if len(parts) != 2 {
			continue
		}
		if strings.EqualFold(parts[1], wantHex) {
			return true
		}
	}
	return false
}
