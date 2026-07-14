package daemon

import (
	"fmt"
	"net"
	"testing"
	"time"
)

// reserveDeadPort grabs a free loopback port and releases it, so tests get a
// port with nothing listening that they can later bind themselves.
func reserveDeadPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

func TestConnectEnsureBrowserLaunchesManagedBrowser(t *testing.T) {
	port := reserveDeadPort(t)
	c := NewCdpConnection("127.0.0.1", port, NewTabStateManager())

	launches := 0
	c.SetEnsureBrowser(func() error {
		launches++
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			return err
		}
		newFakeCDPWithListener(t, ln)
		return nil
	})

	if err := c.Connect(); err != nil {
		t.Fatalf("Connect with ensure hook: %v", err)
	}
	t.Cleanup(c.Disconnect)

	if launches != 1 {
		t.Fatalf("ensure hook called %d times, want 1", launches)
	}
	if !c.Connected() {
		t.Fatal("connection should be established after the ensure hook launched the browser")
	}
}

func TestConnectEnsureBrowserFailureIsRateLimited(t *testing.T) {
	port := reserveDeadPort(t)
	c := NewCdpConnection("127.0.0.1", port, NewTabStateManager())

	launches := 0
	c.SetEnsureBrowser(func() error {
		launches++
		return fmt.Errorf("no browser installed")
	})

	if err := c.Connect(); err == nil {
		t.Fatal("Connect should fail when the ensure hook cannot launch a browser")
	}
	if launches != 1 {
		t.Fatalf("ensure hook called %d times, want 1", launches)
	}

	// A reconnect within the rate-limit window must not relaunch.
	if err := c.Connect(); err == nil {
		t.Fatal("second Connect should still fail")
	}
	if launches != 1 {
		t.Fatalf("ensure hook called %d times within the rate-limit window, want 1", launches)
	}

	// Once the window has passed, the hook is tried again.
	c.lastEnsureAt = time.Now().Add(-ensureBrowserMinInterval - time.Second)
	if err := c.Connect(); err == nil {
		t.Fatal("third Connect should still fail")
	}
	if launches != 2 {
		t.Fatalf("ensure hook called %d times after the window passed, want 2", launches)
	}
}

func TestConnectWithoutEnsureBrowserDoesNotRetry(t *testing.T) {
	port := reserveDeadPort(t)
	c := NewCdpConnection("127.0.0.1", port, NewTabStateManager())
	if err := c.Connect(); err == nil {
		t.Fatal("Connect should fail against a dead endpoint with no ensure hook")
	}
}

func TestNewServerWiresEnsureBrowser(t *testing.T) {
	port := reserveDeadPort(t)
	called := false
	srv := NewServer(ServerOptions{
		CDPHost: "127.0.0.1",
		CDPPort: port,
		EnsureBrowser: func() error {
			called = true
			return fmt.Errorf("stub")
		},
	})
	if err := srv.cdp.Connect(); err == nil {
		t.Fatal("Connect should fail with a stub ensure hook")
	}
	if !called {
		t.Fatal("NewServer should wire ServerOptions.EnsureBrowser into the CDP connection")
	}
}
