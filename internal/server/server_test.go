package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"search-engine/internal/config"
	"search-engine/internal/index"
)

const testKey = "test-admin-key-0123456789"

func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	idx := index.NewIndex()
	idx.AddDocument("a", "https://go.dev", "Go", "Go is a programming language")
	return New(config.Config{DataDir: t.TempDir(), AdminKey: testKey}, idx).Handler()
}

func do(h http.Handler, method, path, body, key string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = "198.51.100.1:1234"
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestAdminEndpointsRequireKey(t *testing.T) {
	h := newTestServer(t)
	cases := []struct{ method, path, body string }{
		{"POST", "/crawl", `{"url":"https://example.com"}`},
		{"GET", "/crawl/jobs", ""},
		{"GET", "/crawl/status/abc", ""},
		{"GET", "/search/stats", ""},
		{"POST", "/eval", `{"judgments":[{"query":"go","relevant_urls":[]}]}`},
	}
	for _, c := range cases {
		if w := do(h, c.method, c.path, c.body, ""); w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without key: %d, want 401", c.method, c.path, w.Code)
		}
		if w := do(h, c.method, c.path, c.body, "wrong-key-wrong-key"); w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s wrong key: %d, want 401", c.method, c.path, w.Code)
		}
	}
	// The failures above used up the admin burst — itself the brute-force
	// protection. A valid key works from a fresh limiter.
	if w := do(h, "GET", "/search/stats", "", testKey); w.Code != http.StatusTooManyRequests {
		t.Errorf("after 10 failed attempts: %d, want 429", w.Code)
	}
	h = newTestServer(t)
	if w := do(h, "GET", "/search/stats", "", testKey); w.Code != http.StatusOK {
		t.Errorf("valid key: %d, want 200", w.Code)
	}
	if w := do(h, "GET", "/search?q=go", "", ""); w.Code != http.StatusOK {
		t.Errorf("public search: %d, want 200", w.Code)
	}
}

func TestCrawlRejectsUnsafeRequests(t *testing.T) {
	h := newTestServer(t)
	cases := map[string]string{
		"metadata":    `{"url":"http://169.254.169.254/metadata/instance"}`,
		"loopback":    `{"url":"http://127.0.0.1:8080/crawl"}`,
		"output_path": `{"url":"https://example.com","output_path":"/app/gosearch"}`,
		"max_pages":   `{"url":"https://8.8.8.8","max_pages":100000}`,
		"workers":     `{"url":"https://8.8.8.8","workers":500}`,
		"depth":       `{"url":"https://8.8.8.8","depth":10}`,
		"trailing":    `{"url":"https://8.8.8.8"} {}`,
	}
	for name, body := range cases {
		if w := do(h, "POST", "/crawl", body, testKey); w.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d (%s), want 400", name, w.Code, w.Body.String())
		}
	}

	big := `{"url":"https://8.8.8.8","pad":"` + strings.Repeat("x", 20<<10) + `"}`
	if w := do(h, "POST", "/crawl", big, testKey); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "too large") {
		t.Errorf("oversized body: %d %s", w.Code, w.Body.String())
	}
}

func TestSearchLimits(t *testing.T) {
	h := newTestServer(t)
	for _, path := range []string{
		"/search?q=go&k=1000",
		"/search?q=go&k=0",
		"/search?q=go&k=abc",
		"/search?q=" + strings.Repeat("a", 600),
		"/search?q=" + strings.Repeat("word+", 40),
	} {
		if w := do(h, "GET", path, "", ""); w.Code != http.StatusBadRequest {
			t.Errorf("%.60s: got %d, want 400", path, w.Code)
		}
	}
}

func TestSearchStatsHasNoQueryText(t *testing.T) {
	h := newTestServer(t)
	do(h, "GET", "/search?q=alice%40example.com+diagnosis", "", "")
	w := do(h, "GET", "/search/stats", "", testKey)
	if strings.Contains(w.Body.String(), "alice") || strings.Contains(w.Body.String(), "diagnosis") {
		t.Fatalf("stats leak query text: %s", w.Body.String())
	}
}

func TestSearchRateLimited(t *testing.T) {
	h := newTestServer(t)
	limited := false
	for i := 0; i < 40; i++ {
		if w := do(h, "GET", "/search?q=go", "", ""); w.Code == http.StatusTooManyRequests {
			limited = true
			if w.Header().Get("Retry-After") == "" {
				t.Error("429 without Retry-After")
			}
			break
		}
	}
	if !limited {
		t.Fatal("expected 429 after exceeding burst")
	}
}
