package statuspage

import (
	"net"
	"testing"
)

func TestUDPPortBound_TrueForRealListener(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket: %v", err)
	}
	defer conn.Close()

	port := conn.LocalAddr().(*net.UDPAddr).Port
	if !UDPPortBound(port) {
		t.Fatalf("expected UDPPortBound(%d) to be true for a real listener", port)
	}
}

func TestUDPPortBound_FalseForUnboundPort(t *testing.T) {
	// Port 1 is a low, reserved port essentially never bound by an
	// unprivileged UDP listener in a test environment.
	if UDPPortBound(1) {
		t.Fatal("expected UDPPortBound(1) to be false — nothing should be bound there")
	}
}

func TestChecker_UDPPortCheck(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket: %v", err)
	}
	defer conn.Close()
	port := conn.LocalAddr().(*net.UDPAddr).Port

	store, err := Open(t.TempDir() + "/status.db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	checker := NewChecker(store, []Target{
		{Name: "udpsvc", Label: "UDP Service", Type: CheckUDPPort, UDPPort: port},
	})
	checker.checkAll(nil, nil)

	up, found := store.LatestStatus("udpsvc")
	if !found || !up {
		t.Fatalf("expected udpsvc to be up, got up=%v found=%v", up, found)
	}
}
