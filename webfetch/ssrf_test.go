package webfetch_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gollem-dev/tools/webfetch"
	"github.com/m-mizutani/gt"
)

func TestIsBlockedIP(t *testing.T) {
	cases := []struct {
		name    string
		ip      string
		blocked bool
	}{
		{"loopback v4", "127.0.0.1", true},
		{"rfc1918 10", "10.0.0.1", true},
		{"rfc1918 192.168", "192.168.1.1", true},
		{"rfc1918 172.16", "172.16.0.1", true},
		{"metadata endpoint", "169.254.169.254", true},
		{"link-local v4", "169.254.0.1", true},
		{"cgnat low", "100.64.0.1", true},
		{"cgnat high", "100.127.255.255", true},
		{"loopback v6", "::1", true},
		{"link-local v6", "fe80::1", true},
		{"unspecified v4", "0.0.0.0", true},
		{"unspecified v6", "::", true},
		{"multicast v4", "224.0.0.1", true},
		{"multicast v6", "ff02::1", true},
		{"ula v6", "fd00::1", true},
		{"this-host 0.0.0.1", "0.0.0.1", true},
		{"test-net-1", "192.0.2.1", true},
		{"test-net-2", "198.51.100.1", true},
		{"test-net-3", "203.0.113.1", true},
		{"ietf protocol v4", "192.0.0.1", true},
		{"6to4 anycast", "192.88.99.1", true},
		{"benchmarking", "198.18.0.1", true},
		{"reserved future", "240.0.0.1", true},
		{"limited broadcast", "255.255.255.255", true},
		{"doc v6", "2001:db8::1", true},
		{"ietf protocol v6", "2001::1", true},
		{"discard v6", "100::1", true},
		{"v4-mapped loopback", "::ffff:127.0.0.1", true},
		{"v4-mapped private", "::ffff:10.0.0.1", true},
		{"public dns", "8.8.8.8", false},
		{"public cloudflare", "1.1.1.1", false},
		{"public v6", "2606:4700:4700::1111", false},
		{"just below cgnat", "100.63.255.255", false},
		{"just above cgnat", "100.128.0.1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			gt.Value(t, ip).NotNil()
			gt.Value(t, webfetch.IsBlockedIP(ip)).Equal(tc.blocked)
		})
	}

	t.Run("nil is blocked", func(t *testing.T) {
		gt.Value(t, webfetch.IsBlockedIP(nil)).Equal(true)
	})
}

// TestSafeDialControl exercises the Control hook directly. net/http invokes this
// for every dial, so a passing hook here means each redirect hop is covered too.
func TestSafeDialControl(t *testing.T) {
	t.Run("blocks resolved loopback", func(t *testing.T) {
		err := webfetch.SafeDialControl("tcp", "127.0.0.1:80", nil)
		gt.Error(t, err).Contains("SSRF guard")
	})
	t.Run("blocks metadata endpoint", func(t *testing.T) {
		err := webfetch.SafeDialControl("tcp", "169.254.169.254:80", nil)
		gt.Error(t, err).Contains("SSRF guard")
	})
	t.Run("allows public ip", func(t *testing.T) {
		gt.NoError(t, webfetch.SafeDialControl("tcp", "8.8.8.8:443", nil))
	})
	t.Run("rejects non-ip address", func(t *testing.T) {
		err := webfetch.SafeDialControl("tcp", "not-an-ip", nil)
		gt.Error(t, err)
	})
}

// TestRunBlocksLoopbackByDefault verifies the guard is wired into the default
// client's transport: a fetch to a loopback httptest server is rejected at dial.
func TestRunBlocksLoopbackByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body>should not be reachable</body></html>"))
	}))
	t.Cleanup(srv.Close)

	// Default New() builds the guarded client; no WithHTTPClient injection.
	ts := gt.R1(webfetch.New()).NoError(t)

	_, err := ts.Run(context.Background(), "web_fetch", map[string]any{"url": srv.URL})
	gt.Error(t, err).Contains("SSRF guard")
}

// TestRunAllowsLoopbackWhenPrivateIPAllowed verifies the guard can be disabled
// via WithAllowPrivateIP so the built-in client reaches a loopback server.
func TestRunAllowsLoopbackWhenPrivateIPAllowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body><p>reachable</p></body></html>"))
	}))
	t.Cleanup(srv.Close)

	ts := gt.R1(webfetch.New(webfetch.WithAllowPrivateIP(true))).NoError(t)

	result := gt.R1(ts.Run(context.Background(), "web_fetch", map[string]any{"url": srv.URL})).NoError(t)
	gt.Value(t, result["status"]).Equal(http.StatusOK)
	gt.String(t, result["result"].(string)).Contains("reachable")
}
