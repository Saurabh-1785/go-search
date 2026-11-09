// Package netguard blocks server-side request forgery (SSRF): it stops the
// crawler from connecting to private, loopback, link-local or reserved
// addresses.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// Why this exists:
//
// POST /crawl makes the server fetch URLs chosen by the caller. Without a
// guard, a caller can point it at addresses only the server can reach:
// the cloud metadata service (169.254.169.254 — on Azure this hands out
// managed-identity tokens), localhost admin ports, or the private network.
//
// Checking the seed URL alone is not enough. The crawler follows redirects,
// and DNS can return a public IP at check time and a private one at fetch
// time (DNS rebinding). So the real check happens when the connection is
// dialed: the guarded transport's Control hook sees the exact IP being
// connected to, for the seed, every redirect and every followed link.

// blockedPrefixes are non-public ranges not covered by netip's Is* helpers.
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),        // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),    // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),     // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),     // TEST-NET-1
	netip.MustParsePrefix("198.18.0.0/15"),    // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"),  // TEST-NET-2
	netip.MustParsePrefix("203.0.113.0/24"),   // TEST-NET-3
	netip.MustParsePrefix("240.0.0.0/4"),      // reserved + broadcast
	netip.MustParsePrefix("168.63.129.16/32"), // Azure WireServer (public IP, platform-internal)
	netip.MustParsePrefix("64:ff9b::/96"),     // NAT64 — can embed private IPv4
	netip.MustParsePrefix("64:ff9b:1::/48"),   // local-use NAT64
	netip.MustParsePrefix("2001:db8::/32"),    // documentation
	netip.MustParsePrefix("2002::/16"),        // 6to4 — can embed private IPv4
	netip.MustParsePrefix("fec0::/10"),        // deprecated site-local
}

// IsPublicIP reports whether ip is a globally routable unicast address that
// the crawler may connect to.
func IsPublicIP(ip netip.Addr) bool {
	ip = ip.Unmap() // treat ::ffff:10.0.0.1 as 10.0.0.1
	if !ip.IsValid() ||
		ip.IsLoopback() ||
		ip.IsPrivate() || // 10/8, 172.16/12, 192.168/16, fc00::/7
		ip.IsLinkLocalUnicast() || // 169.254/16 (cloud metadata), fe80::/10
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified() {
		return false
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// ErrBlocked is returned when a connection to a non-public address is refused.
var ErrBlocked = errors.New("destination address is not allowed")

// control is a net.Dialer Control hook that rejects connections to
// non-public IPs. It runs after DNS resolution, right before connect().
func control(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrBlocked, address)
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !IsPublicIP(ip) {
		return fmt.Errorf("%w: %s", ErrBlocked, host)
	}
	return nil
}

// NewTransport returns an http.Transport that can only connect to
// public IPs. Proxies are disabled: through a proxy the dial check would see
// the proxy's address, not the real destination.
func NewTransport() *http.Transport {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   control,
	}
	return &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
	}
}

// Install replaces http.DefaultTransport with the guarded one.
//
// gocrawl's fetcher builds its own http.Client without a Transport, so it
// falls back to http.DefaultTransport — this is the one place every crawler
// request goes through. go-search makes no other outbound HTTP calls.
func Install() {
	http.DefaultTransport = NewTransport()
}

// ValidateURL checks a seed URL before a crawl starts, so callers get a
// clear 400 instead of a job that fails later. The dial-time guard remains
// the real enforcement.
func ValidateURL(ctx context.Context, raw string, allowPrivate bool) (*url.URL, error) {
	if len(raw) > 2048 {
		return nil, errors.New("url is too long (max 2048 characters)")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid url: %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("url must use http or https")
	}
	if u.User != nil {
		return nil, errors.New("url must not contain credentials")
	}
	host := u.Hostname()
	if host == "" {
		return nil, errors.New("url must include a host")
	}
	if allowPrivate {
		return u, nil
	}

	if ip, err := netip.ParseAddr(host); err == nil {
		if !IsPublicIP(ip) {
			return nil, errors.New("url points to a private or reserved address")
		}
		return u, nil
	}
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return nil, errors.New("url points to a private or reserved address")
	}

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(addrs) == 0 {
		return nil, fmt.Errorf("cannot resolve host %q", host)
	}
	for _, ip := range addrs {
		if !IsPublicIP(ip) {
			return nil, errors.New("url resolves to a private or reserved address")
		}
	}
	return u, nil
}
