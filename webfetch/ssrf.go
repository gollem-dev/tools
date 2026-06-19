package webfetch

import (
	"net"
	"net/http"
	"net/netip"
	"syscall"

	"github.com/m-mizutani/goerr/v2"
)

// blockedPrefixes enumerates the IANA IPv4/IPv6 special-purpose ranges the
// webfetch tool must never reach. The method-based checks in isBlockedIP cover
// the dynamic categories (loopback, private, link-local, multicast,
// unspecified); this list adds the reserved/documentation/benchmark ranges that
// those methods miss (e.g. 0.0.0.0/8, TEST-NET, 240.0.0.0/4, CGNAT, 2001:db8::/32)
// so that "public global-unicast only" actually holds. Sourced from RFC 6890 and
// the IANA IPv4/IPv6 special-purpose-address registries.
var blockedPrefixes = []netip.Prefix{
	// IPv4
	netip.MustParsePrefix("0.0.0.0/8"),       // "this host on this network" (incl. 0.0.0.1)
	netip.MustParsePrefix("100.64.0.0/10"),   // CGNAT (RFC 6598); routed by overlay networks
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // TEST-NET-1 (documentation)
	netip.MustParsePrefix("192.88.99.0/24"),  // 6to4 relay anycast (deprecated)
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // TEST-NET-2 (documentation)
	netip.MustParsePrefix("203.0.113.0/24"),  // TEST-NET-3 (documentation)
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved for future use (incl. 255.255.255.255)
	// IPv6
	netip.MustParsePrefix("64:ff9b:1::/48"), // IPv4/IPv6 translation (local-use)
	netip.MustParsePrefix("100::/64"),       // discard-only
	netip.MustParsePrefix("2001::/23"),      // IETF protocol assignments
	netip.MustParsePrefix("2001:db8::/32"),  // documentation
}

// isBlockedIP reports whether ip falls in a range the webfetch tool must not reach.
// Only public, global-unicast addresses are allowed so the agent cannot be steered
// (via untrusted content) into hitting loopback, RFC1918/ULA private networks, the
// cloud metadata endpoint (169.254.169.254, link-local), or any IANA
// special-purpose/reserved range.
func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	// Normalize IPv4-mapped IPv6 (::ffff:a.b.c.d) to plain IPv4 so the checks and
	// IPv4 prefixes below apply to the address the kernel will actually route.
	addr = addr.Unmap()
	if !addr.IsValid() {
		return true
	}

	if addr.IsLoopback() ||
		addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() ||
		addr.IsInterfaceLocalMulticast() ||
		addr.IsMulticast() ||
		addr.IsUnspecified() {
		return true
	}
	for _, p := range blockedPrefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// safeDialControl is a net.Dialer.Control hook that rejects connections to blocked
// IP ranges. address is the already-resolved "ip:port" the dialer is about to connect
// to, so inspecting it here defeats DNS rebinding and covers every redirect hop.
func safeDialControl(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return goerr.Wrap(err, "failed to parse dial address", goerr.V("address", address))
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return goerr.New("dial address is not a literal IP", goerr.V("address", address))
	}
	if isBlockedIP(ip) {
		return goerr.New("connection to non-public IP is blocked by SSRF guard",
			goerr.V("network", network), goerr.V("ip", ip.String()))
	}
	return nil
}

// newGuardedClient builds the default HTTP client used when the caller does not
// inject one via WithHTTPClient. When allowPrivateIP is false (the default), the
// dialer's Control hook enforces the SSRF guard on the resolved IP of every
// connection, including each redirect hop. When true, the guard is disabled so
// loopback/test servers can be reached.
func newGuardedClient(allowPrivateIP bool) *http.Client {
	dialer := &net.Dialer{Timeout: defaultTimeout}
	if !allowPrivateIP {
		dialer.Control = safeDialControl
	}
	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   defaultTimeout,
		ResponseHeaderTimeout: defaultTimeout,
	}
	return &http.Client{
		Timeout:   defaultTimeout,
		Transport: transport,
	}
}
