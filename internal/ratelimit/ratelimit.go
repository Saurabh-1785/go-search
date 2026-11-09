// Package ratelimit implements a per-client token-bucket rate limiter.
package ratelimit

import (
	"math"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// Limiter is a per-client token bucket: each client gets `burst` tokens,
// refilled at a fixed rate; each request spends one.
//
// It is in-memory and per-process, which is correct while the app runs as a
// single replica (see deploy/azure-setup.sh).
type Limiter struct {
	name  string
	rate  float64 // tokens per second
	burst float64

	mu        sync.Mutex
	buckets   map[string]*bucket
	lastSweep time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// maxBuckets caps memory if an attacker rotates source addresses.
const maxBuckets = 100_000

// New returns a limiter allowing perMinute requests per client, with bursts
// of up to burst requests. name appears in error messages.
func New(name string, perMinute float64, burst int) *Limiter {
	return &Limiter{
		name:      name,
		rate:      perMinute / 60,
		burst:     float64(burst),
		buckets:   make(map[string]*bucket),
		lastSweep: time.Now(),
	}
}

// Name returns the limiter's name.
func (l *Limiter) Name() string { return l.name }

// Allow spends one token for key. When none is left it returns false and how
// long until the next token.
func (l *Limiter) Allow(key string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.sweep(now)

	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= maxBuckets {
			return false, time.Minute
		}
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}

	b.tokens = math.Min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
	return false, wait
}

// sweep drops buckets that have refilled completely (indistinguishable from
// a new client), at most once a minute. Caller holds l.mu.
func (l *Limiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < time.Minute {
		return
	}
	l.lastSweep = now
	full := time.Duration(l.burst / l.rate * float64(time.Second))
	for k, b := range l.buckets {
		if now.Sub(b.last) > full {
			delete(l.buckets, k)
		}
	}
}

// ClientKey identifies the caller for rate limiting.
//
// With trustProxy (set only when running behind an ingress that appends the
// client address, like Azure Container Apps), it uses the rightmost
// X-Forwarded-For entry — the one added by the ingress. Entries further left
// are client-supplied and can be forged. Otherwise it uses the TCP peer.
//
// IPv6 clients are grouped by /64, since one subscriber usually controls a
// whole /64 and could otherwise rotate addresses to dodge the limit.
func ClientKey(r *http.Request, trustProxy bool) string {
	raw := ""
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			raw = strings.TrimSpace(parts[len(parts)-1])
		}
	}
	if raw == "" {
		raw = r.RemoteAddr
		if host, _, err := net.SplitHostPort(raw); err == nil {
			raw = host
		}
	}
	ip, err := netip.ParseAddr(raw)
	if err != nil {
		return raw
	}
	ip = ip.Unmap()
	if ip.Is6() {
		p, _ := ip.Prefix(64)
		return p.String()
	}
	return ip.String()
}
