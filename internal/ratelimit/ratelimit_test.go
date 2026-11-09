package ratelimit

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiter(t *testing.T) {
	rl := New("test", 60, 3) // 1/s, burst 3
	now := time.Unix(1000, 0)
	for i := 0; i < 3; i++ {
		if ok, _ := rl.Allow("a", now); !ok {
			t.Fatalf("request %d within burst was refused", i)
		}
	}
	ok, wait := rl.Allow("a", now)
	if ok {
		t.Fatal("4th request should be refused")
	}
	if wait <= 0 || wait > time.Second {
		t.Errorf("retry wait = %v, want (0, 1s]", wait)
	}
	if ok, _ := rl.Allow("b", now); !ok {
		t.Error("other clients must have their own bucket")
	}
	if ok, _ := rl.Allow("a", now.Add(time.Second)); !ok {
		t.Error("token should refill after 1s")
	}
}

func TestClientKey(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.9:5555"
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 198.51.100.7")

	if got := ClientKey(r, false); got != "203.0.113.9" {
		t.Errorf("untrusted: got %s, want TCP peer", got)
	}
	if got := ClientKey(r, true); got != "198.51.100.7" {
		t.Errorf("trusted: got %s, want rightmost XFF entry", got)
	}

	r.RemoteAddr = "[2001:db8:1:2:aaaa::1]:443"
	r.Header.Del("X-Forwarded-For")
	if got := ClientKey(r, false); got != "2001:db8:1:2::/64" {
		t.Errorf("ipv6: got %s, want /64 prefix", got)
	}
}

// --- HTTP-level tests ---
