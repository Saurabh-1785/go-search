package netguard

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestIsPublicIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1",
		"169.254.169.254", // cloud metadata
		"168.63.129.16",   // Azure WireServer
		"100.64.0.1", "0.0.0.0", "255.255.255.255", "224.0.0.1",
		"::1", "::", "fe80::1", "fc00::1", "fd12:3456::1",
		"::ffff:127.0.0.1", "::ffff:169.254.169.254", // IPv4-mapped
		"64:ff9b::a00:1", // NAT64 of 10.0.0.1
	}
	for _, s := range blocked {
		if IsPublicIP(netip.MustParseAddr(s)) {
			t.Errorf("%s should be blocked", s)
		}
	}
	allowed := []string{"8.8.8.8", "1.1.1.1", "142.250.190.78", "2606:4700:4700::1111"}
	for _, s := range allowed {
		if !IsPublicIP(netip.MustParseAddr(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
}

func TestValidateURL(t *testing.T) {
	ctx := context.Background()
	bad := []string{
		"ftp://example.com",
		"file:///etc/passwd",
		"http://169.254.169.254/metadata/identity/oauth2/token",
		"http://127.0.0.1:8080/",
		"http://[::1]/",
		"http://localhost/",
		"http://foo.localhost/",
		"http://user:pass@example.com/",
		"http:///nohost",
		"http://" + strings.Repeat("a", 2100) + ".com",
	}
	for _, u := range bad {
		if _, err := ValidateURL(ctx, u, false); err == nil {
			t.Errorf("%q should be rejected", u)
		}
	}
	if _, err := ValidateURL(ctx, "http://8.8.8.8/", false); err != nil {
		t.Errorf("public IP literal should be allowed: %v", err)
	}
	if _, err := ValidateURL(ctx, "http://127.0.0.1/", true); err != nil {
		t.Errorf("allowPrivate should skip the address check: %v", err)
	}
}

func TestTransportBlocksPrivateDial(t *testing.T) {
	var hit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer srv.Close()

	client := &http.Client{Transport: NewTransport(), Timeout: 5 * time.Second}
	_, err := client.Get(srv.URL)
	if err == nil || !errors.Is(err, ErrBlocked) {
		t.Fatalf("expected blocked dial, got %v", err)
	}
	if hit {
		t.Fatal("request reached the loopback server")
	}
}

func TestRedirectToMetadataIsBlocked(t *testing.T) {
	// The redirecting server itself is on loopback, so use a transport that
	// allows exactly one hop: the first dial goes through, the redirect
	// target (another loopback server) must be refused by the dial hook.
	var internalHit bool
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { internalHit = true }))
	defer internal.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL+"/metadata", http.StatusFound)
	}))
	defer redirector.Close()

	tr := NewTransport()
	first := true
	plain := http.DefaultTransport.(*http.Transport).Clone()
	client := &http.Client{Timeout: 5 * time.Second, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if first {
			first = false
			return plain.RoundTrip(r) // pretend the seed is public
		}
		return tr.RoundTrip(r)
	})}

	_, err := client.Get(redirector.URL)
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("expected redirect target to be blocked, got %v", err)
	}
	if internalHit {
		t.Fatal("redirect reached the internal server")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
