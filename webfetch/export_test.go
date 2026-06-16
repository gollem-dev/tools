package webfetch

// ExtractContent exposes the package-internal extractContent function for
// white-box testing from the external test package.
var ExtractContent = extractContent

// SafeClose exposes the package-internal safeClose helper for unit testing.
var SafeClose = safeClose

// IsBlockedIP exposes the package-internal SSRF range check for unit testing.
var IsBlockedIP = isBlockedIP

// SafeDialControl exposes the package-internal net.Dialer.Control hook for unit
// testing. net/http invokes this for every dial, including each redirect hop.
var SafeDialControl = safeDialControl
