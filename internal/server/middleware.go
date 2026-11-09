package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"search-engine/internal/ratelimit"
)

// requireAdmin allows the request only with "Authorization: Bearer <key>".
// Both sides are hashed before the constant-time compare, so neither the
// key's contents nor its length leak through response timing.
func requireAdmin(key string, next http.HandlerFunc) http.HandlerFunc {
	want := sha256.Sum256([]byte(key))
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		got := sha256.Sum256([]byte(token))
		if !ok || subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="go-search"`)
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"error": "missing or invalid API key (send Authorization: Bearer <ADMIN_API_KEY>)",
			})
			return
		}
		next(w, r)
	}
}

// rateLimit rejects the request with 429 when the caller is over l's limit.
func (s *Server) rateLimit(l *ratelimit.Limiter, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.allow(w, r, l) {
			return
		}
		next(w, r)
	}
}

// allow spends one token of l for the caller. If none is left it writes a
// 429 response with Retry-After and returns false.
func (s *Server) allow(w http.ResponseWriter, r *http.Request, l *ratelimit.Limiter) bool {
	ok, wait := l.Allow(ratelimit.ClientKey(r, s.cfg.TrustProxyHeaders), time.Now())
	if ok {
		return true
	}
	secs := strconv.Itoa(int(math.Ceil(wait.Seconds())))
	w.Header().Set("Retry-After", secs)
	writeJSON(w, http.StatusTooManyRequests, map[string]string{
		"error": "rate limit exceeded for " + l.Name() + ", retry in " + secs + "s",
	})
	return false
}

// limitBody caps the request body so a huge JSON payload cannot exhaust memory.
func limitBody(maxBytes int64, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		next(w, r)
	}
}

// method rejects requests with any other HTTP method.
func method(m string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != m {
			w.Header().Set("Allow", m)
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		next(w, r)
	}
}

// securityHeaders sets response headers that are safe for a JSON API.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

// logRequests logs method, path, status and duration. The query string is
// left out on purpose: it carries search terms.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if r.URL.Path != "/health" {
			log.Printf("[HTTP]  %s %s → %d (%s)", r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Microsecond))
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}
