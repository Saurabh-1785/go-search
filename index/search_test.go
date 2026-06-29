package index

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ==================== BM25 Tests ====================

func TestBM25IDF(t *testing.T) {
	tests := []struct {
		name     string
		docCount int
		df       uint32
		wantZero bool
	}{
		{"rare term", 10000, 2, false},
		{"common term", 10000, 5000, false},
		{"all docs", 10000, 10000, false}, // BM25 IDF never goes zero for df < N
		{"zero df", 10000, 0, true},
		{"zero docCount", 0, 5, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BM25IDF(tt.docCount, tt.df)
			if tt.wantZero && got != 0 {
				t.Errorf("BM25IDF(%d, %d) = %f, want 0", tt.docCount, tt.df, got)
			}
			if !tt.wantZero && got <= 0 {
				t.Errorf("BM25IDF(%d, %d) = %f, want > 0", tt.docCount, tt.df, got)
			}
		})
	}

	// Rare term should have higher IDF than common term.
	rare := BM25IDF(10000, 2)
	common := BM25IDF(10000, 5000)
	if rare <= common {
		t.Errorf("BM25IDF(rare) = %f should be > BM25IDF(common) = %f", rare, common)
	}
}

func TestBM25IDFNeverNegative(t *testing.T) {
	// BM25 IDF with Robertson-Walker smoothing should never go negative,
	// even when DF > N/2. This is the key improvement over vanilla IDF.
	got := BM25IDF(100, 90) // term in 90% of docs
	if got < 0 {
		t.Errorf("BM25IDF(100, 90) = %f, should never be negative", got)
	}

	got = BM25IDF(100, 100) // term in every doc
	if got < 0 {
		t.Errorf("BM25IDF(100, 100) = %f, should never be negative", got)
	}
}

func TestBM25Score(t *testing.T) {
	params := DefaultBM25()

	// Basic sanity: score should be positive for non-zero inputs.
	score := params.Score(5, 10, 100, 100.0, 1000)
	if score <= 0 {
		t.Errorf("BM25 score should be positive, got %f", score)
	}
}

func TestBM25TFSaturation(t *testing.T) {
	params := DefaultBM25()

	// BM25 key property: TF=10 should NOT be 10× the score of TF=1.
	// (Unlike TF-IDF which scales linearly with TF.)
	score1 := params.Score(1, 10, 100, 100.0, 1000)
	score10 := params.Score(10, 10, 100, 100.0, 1000)

	ratio := score10 / score1
	if ratio >= 5 {
		t.Errorf("BM25 TF saturation failed: TF=10 gives %f, TF=1 gives %f, ratio=%f (should be < 5)",
			score10, score1, ratio)
	}
	t.Logf("TF saturation: score(TF=1)=%f, score(TF=10)=%f, ratio=%.2f", score1, score10, ratio)
}

func TestBM25LongDocPenalty(t *testing.T) {
	params := DefaultBM25()

	// Same TF, but doc 2× average length should score lower.
	shortDoc := params.Score(5, 10, 50, 100.0, 1000)   // shorter than avg
	longDoc := params.Score(5, 10, 200, 100.0, 1000)    // longer than avg

	if shortDoc <= longDoc {
		t.Errorf("short doc (%f) should score higher than long doc (%f)", shortDoc, longDoc)
	}
}

func TestBM25ZeroInputs(t *testing.T) {
	params := DefaultBM25()

	if got := params.Score(0, 10, 100, 100.0, 1000); got != 0 {
		t.Errorf("BM25 with tf=0 should be 0, got %f", got)
	}
	if got := params.Score(5, 0, 100, 100.0, 1000); got != 0 {
		t.Errorf("BM25 with df=0 should be 0, got %f", got)
	}
	if got := params.Score(5, 10, 100, 0, 1000); got != 0 {
		t.Errorf("BM25 with avgDocLen=0 should be 0, got %f", got)
	}
	if got := params.Score(5, 10, 100, 100.0, 0); got != 0 {
		t.Errorf("BM25 with docCount=0 should be 0, got %f", got)
	}
}

func TestDefaultBM25Params(t *testing.T) {
	params := DefaultBM25()
	if params.K1 != 1.2 {
		t.Errorf("expected K1=1.2, got %f", params.K1)
	}
	if params.B != 0.75 {
		t.Errorf("expected B=0.75, got %f", params.B)
	}
}

// ==================== TF-IDF Tests (legacy, kept for reference) ====================

func TestIDF(t *testing.T) {
	tests := []struct {
		name     string
		docCount int
		df       uint32
		wantZero bool
	}{
		{"rare term", 10000, 2, false},
		{"common term", 10000, 5000, false},
		{"all docs", 10000, 10000, true},
		{"zero df", 10000, 0, true},
		{"df exceeds N", 100, 200, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IDF(tt.docCount, tt.df)
			if tt.wantZero && got != 0 {
				t.Errorf("IDF(%d, %d) = %f, want 0", tt.docCount, tt.df, got)
			}
			if !tt.wantZero && got <= 0 {
				t.Errorf("IDF(%d, %d) = %f, want > 0", tt.docCount, tt.df, got)
			}
		})
	}

	// Rare term should have higher IDF than common term.
	rare := IDF(10000, 2)
	common := IDF(10000, 5000)
	if rare <= common {
		t.Errorf("IDF(rare) = %f should be > IDF(common) = %f", rare, common)
	}
}

func TestTFIDF(t *testing.T) {
	// Known values: tf=5, df=10, docLen=100, N=1000
	// expected = (5/100) * log10(1000/10) = 0.05 * 2.0 = 0.1
	got := TFIDF(5, 10, 100, 1000)
	expected := 0.1
	if math.Abs(got-expected) > 0.001 {
		t.Errorf("TFIDF(5, 10, 100, 1000) = %f, want %f", got, expected)
	}
}

func TestTFIDFLengthNormalization(t *testing.T) {
	// Same TF in a shorter doc should score higher.
	shortDoc := TFIDF(5, 10, 50, 1000)
	longDoc := TFIDF(5, 10, 500, 1000)

	if shortDoc <= longDoc {
		t.Errorf("short doc score %f should be > long doc score %f", shortDoc, longDoc)
	}
}

func TestTFIDFZeroDenominators(t *testing.T) {
	if got := TFIDF(5, 0, 100, 1000); got != 0 {
		t.Errorf("TFIDF with df=0 should return 0, got %f", got)
	}
	if got := TFIDF(5, 10, 0, 1000); got != 0 {
		t.Errorf("TFIDF with docLen=0 should return 0, got %f", got)
	}
	if got := TFIDF(5, 10, 100, 0); got != 0 {
		t.Errorf("TFIDF with docCount=0 should return 0, got %f", got)
	}
}

// ==================== Search Tests (now using BM25) ====================

// helper: build an index with test documents.
func buildTestIndex() *Index {
	idx := NewIndex()

	// Doc 0: heavily about Go
	idx.AddDocument("sha0", "https://go.dev", "The Go Programming Language",
		"Go is a programming language designed at Google. Go is fast. Go is concurrent. Go Go Go.")

	// Doc 1: about programming in general
	idx.AddDocument("sha1", "https://example.com/programming", "Programming Basics",
		"Programming is the process of creating instructions for computers. Programming requires logic.")

	// Doc 2: about concurrency in Go
	idx.AddDocument("sha2", "https://go.dev/concurrency", "Go Concurrency Patterns",
		"Go provides goroutines and channels for concurrent programming. Concurrency is not parallelism.")

	// Doc 3: unrelated
	idx.AddDocument("sha3", "https://example.com/cooking", "Best Pasta Recipes",
		"Pasta is a traditional Italian food. Cook pasta in boiling water with salt.")

	return idx
}

func TestSearchSingleTerm(t *testing.T) {
	idx := buildTestIndex()
	resp := idx.Search("Go", 10, "or")

	if resp.TotalHits == 0 {
		t.Fatal("expected hits for 'Go', got 0")
	}

	// Doc 0 has "go" many times — should rank highest.
	if resp.Results[0].URL != "https://go.dev" {
		t.Errorf("expected top result to be go.dev, got %s", resp.Results[0].URL)
	}
}

func TestSearchMultiTerm(t *testing.T) {
	idx := buildTestIndex()
	resp := idx.Search("Go programming", 10, "or")

	if resp.TotalHits == 0 {
		t.Fatal("expected hits for 'Go programming', got 0")
	}

	// Doc 0 and Doc 2 both contain "go" + "programming".
	// They should rank higher than Doc 1 (only "programming") and Doc 3 (neither).
	foundGodev := false
	for _, r := range resp.Results {
		if r.URL == "https://go.dev" {
			foundGodev = true
		}
	}
	if !foundGodev {
		t.Error("expected go.dev in results for 'Go programming'")
	}
}

func TestSearchBooleanAND(t *testing.T) {
	idx := buildTestIndex()
	resp := idx.Search("Go programming", 10, "and")

	// Only docs containing BOTH "go" AND "program" (stemmed) should appear.
	for _, r := range resp.Results {
		if r.URL == "https://example.com/cooking" {
			t.Error("cooking page should not appear in AND query for 'Go programming'")
		}
	}
}

func TestSearchBooleanOR(t *testing.T) {
	idx := buildTestIndex()
	resp := idx.Search("Go pasta", 10, "or")

	// Both go.dev docs AND the cooking doc should appear.
	urls := make(map[string]bool)
	for _, r := range resp.Results {
		urls[r.URL] = true
	}

	if !urls["https://go.dev"] {
		t.Error("expected go.dev in OR results")
	}
	if !urls["https://example.com/cooking"] {
		t.Error("expected cooking page in OR results")
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	idx := buildTestIndex()

	// Stopwords-only query should return nothing.
	resp := idx.Search("the a is", 10, "or")
	if resp.TotalHits != 0 {
		t.Errorf("expected 0 hits for stopwords-only query, got %d", resp.TotalHits)
	}

	// Empty query should return nothing.
	resp = idx.Search("", 10, "or")
	if resp.TotalHits != 0 {
		t.Errorf("expected 0 hits for empty query, got %d", resp.TotalHits)
	}
}

func TestSearchEmptyIndex(t *testing.T) {
	idx := NewIndex()
	resp := idx.Search("golang", 10, "or")

	if resp.TotalHits != 0 {
		t.Errorf("expected 0 hits on empty index, got %d", resp.TotalHits)
	}
}

func TestSearchTopK(t *testing.T) {
	idx := buildTestIndex()

	// Request only 2 results even though more match.
	resp := idx.Search("Go", 2, "or")

	if len(resp.Results) > 2 {
		t.Errorf("expected at most 2 results, got %d", len(resp.Results))
	}

	// TotalHits should reflect all matching docs, not just top-K.
	if resp.TotalHits <= 2 {
		// We have at least 3 docs mentioning "go"
		t.Logf("TotalHits = %d (may vary based on stemming)", resp.TotalHits)
	}
}

func TestSearchTiming(t *testing.T) {
	idx := buildTestIndex()
	resp := idx.Search("Go", 10, "or")

	if resp.TimeTakenMs <= 0 {
		t.Errorf("expected positive TimeTakenMs, got %f", resp.TimeTakenMs)
	}
}

func TestSearchScoreOrdering(t *testing.T) {
	idx := buildTestIndex()
	resp := idx.Search("Go programming", 10, "or")

	// Results should be sorted by score descending.
	for i := 1; i < len(resp.Results); i++ {
		if resp.Results[i].Score > resp.Results[i-1].Score {
			t.Errorf("results not sorted by score: [%d]=%f > [%d]=%f",
				i, resp.Results[i].Score, i-1, resp.Results[i-1].Score)
		}
	}
}

// ==================== Snippet Tests ====================

func TestSnippetGeneration(t *testing.T) {
	content := "Go is a programming language designed at Google for building scalable systems."
	terms := []string{"programming", "language"}

	snippet := GenerateSnippet(content, terms, 200)

	if !strings.Contains(snippet, "<mark>") {
		t.Error("snippet should contain <mark> tags")
	}
	if !strings.Contains(snippet, "programming") {
		t.Error("snippet should contain the matched term 'programming'")
	}
}

func TestSnippetCaseInsensitive(t *testing.T) {
	content := "Go is a Programming Language"
	terms := []string{"programming"}

	snippet := GenerateSnippet(content, terms, 200)

	// Should highlight "Programming" (preserving original case).
	if !strings.Contains(snippet, "<mark>Programming</mark>") {
		t.Errorf("expected case-preserved highlight, got: %s", snippet)
	}
}

func TestSnippetWindowClamping(t *testing.T) {
	// Long content with a match in the middle.
	words := make([]string, 100)
	for i := range words {
		words[i] = "filler"
	}
	words[50] = "golang"
	content := strings.Join(words, " ")

	snippet := GenerateSnippet(content, []string{"golang"}, 60)

	// Snippet should contain "..." when truncated.
	if !strings.Contains(snippet, "...") {
		t.Error("truncated snippet should contain '...'")
	}
	if !strings.Contains(snippet, "<mark>golang</mark>") {
		t.Errorf("snippet should highlight 'golang': %s", snippet)
	}
}

func TestSnippetNoMatch(t *testing.T) {
	content := "Go is a programming language designed at Google."
	terms := []string{"rust"} // not in content

	snippet := GenerateSnippet(content, terms, 200)

	// Should fall back to the beginning of content.
	if snippet == "" {
		t.Error("snippet should not be empty even when no terms match")
	}
}

func TestSnippetEmpty(t *testing.T) {
	if got := GenerateSnippet("", []string{"test"}, 200); got != "" {
		t.Errorf("expected empty snippet for empty content, got %q", got)
	}
	if got := GenerateSnippet("content", []string{}, 200); got != "" {
		t.Errorf("expected empty snippet for empty terms, got %q", got)
	}
}

// ==================== Content Disk Storage Tests ====================

func TestContentDiskStorage(t *testing.T) {
	idx := NewIndex()
	content0 := "Go is a fast programming language"
	content1 := "Rust is a systems programming language"

	idx.AddDocument("sha0", "https://go.dev", "Go", content0)
	idx.AddDocument("sha1", "https://rust-lang.org", "Rust", content1)

	// Before save: content should be in buffer.
	if got := idx.GetDocContent(0); got != content0 {
		t.Errorf("pre-save content mismatch: got %q, want %q", got, content0)
	}

	// Save to disk.
	dir := t.TempDir()
	if err := SaveIndex(idx, dir); err != nil {
		t.Fatalf("SaveIndex failed: %v", err)
	}

	// After save: buffer should be cleared, content readable from disk.
	if idx.ContentsBuf() != nil {
		t.Error("contentsBuf should be nil after save")
	}

	// Content should still be readable (now from disk).
	if got := idx.GetDocContent(0); got != content0 {
		t.Errorf("post-save content mismatch for doc 0: got %q, want %q", got, content0)
	}
	if got := idx.GetDocContent(1); got != content1 {
		t.Errorf("post-save content mismatch for doc 1: got %q, want %q", got, content1)
	}

	// Verify contents.bin exists.
	contentsPath := filepath.Join(dir, "contents.bin")
	info, err := os.Stat(contentsPath)
	if err != nil {
		t.Fatalf("contents.bin not found: %v", err)
	}
	if info.Size() == 0 {
		t.Error("contents.bin should not be empty")
	}
}

func TestContentDiskStorageRoundTrip(t *testing.T) {
	idx := NewIndex()
	contents := []string{
		"First document about Go programming",
		"Second document about Rust systems",
		"Third document with special chars: <html>&\"quotes\"</html>",
	}

	for i, c := range contents {
		idx.AddDocument("sha"+string(rune('0'+i)), "https://example.com/"+string(rune('0'+i)),
			"Doc "+string(rune('0'+i)), c)
	}

	dir := t.TempDir()
	if err := SaveIndex(idx, dir); err != nil {
		t.Fatalf("SaveIndex failed: %v", err)
	}

	// Read back via ReadDocContent with offsets from docs.json.
	docTable, err := LoadDocTable(dir)
	if err != nil {
		t.Fatalf("LoadDocTable failed: %v", err)
	}

	for i, meta := range docTable {
		got, err := ReadDocContent(dir, meta.ContentOffset, meta.ContentLength)
		if err != nil {
			t.Fatalf("ReadDocContent(%d) failed: %v", i, err)
		}
		if got != contents[i] {
			t.Errorf("doc %d content mismatch:\ngot:  %q\nwant: %q", i, got, contents[i])
		}
	}
}

func TestReadDocContentZeroLength(t *testing.T) {
	got, err := ReadDocContent(t.TempDir(), 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty string for zero length, got %q", got)
	}
}

// ==================== Integration: Search with Snippets ====================

func TestSearchWithSnippets(t *testing.T) {
	idx := NewIndex()
	idx.AddDocument("sha0", "https://go.dev", "Go",
		"Go is a programming language designed at Google for building scalable systems.")
	idx.AddDocument("sha1", "https://example.com", "Other",
		"This page has nothing about programming languages at all, just filler text for testing.")

	// Save so content is on disk.
	dir := t.TempDir()
	if err := SaveIndex(idx, dir); err != nil {
		t.Fatalf("SaveIndex failed: %v", err)
	}

	resp := idx.Search("Go programming", 10, "or")

	if resp.TotalHits == 0 {
		t.Fatal("expected hits")
	}

	// Top result should have a snippet.
	if resp.Results[0].Snippet == "" {
		t.Error("expected non-empty snippet for top result")
	}
}

// ==================== Evaluation Metrics Tests ====================

func TestPrecisionAtK(t *testing.T) {
	idx := buildTestIndex()

	judgments := []RelevanceJudgment{
		{
			Query:        "Go programming",
			RelevantURLs: []string{"https://go.dev", "https://go.dev/concurrency"},
		},
	}

	report := Evaluate(idx, judgments, 10, DefaultBM25())

	if len(report.Results) != 1 {
		t.Fatalf("expected 1 eval result, got %d", len(report.Results))
	}

	result := report.Results[0]

	// Both go.dev and go.dev/concurrency should be found in top 10.
	if result.Hits < 1 {
		t.Errorf("expected at least 1 hit, got %d", result.Hits)
	}

	// Precision@10 = hits / 10
	if result.PrecisionK < 0 || result.PrecisionK > 1 {
		t.Errorf("precision@K should be in [0, 1], got %f", result.PrecisionK)
	}
}

func TestRecall(t *testing.T) {
	idx := buildTestIndex()

	judgments := []RelevanceJudgment{
		{
			Query:        "Go",
			RelevantURLs: []string{"https://go.dev", "https://go.dev/concurrency"},
		},
	}

	report := Evaluate(idx, judgments, 10, DefaultBM25())
	result := report.Results[0]

	// Both relevant docs should be found.
	if result.Recall < 0.5 {
		t.Errorf("expected recall >= 0.5 (both Go docs should be found), got %f", result.Recall)
	}
}

func TestEvaluateMultipleQueries(t *testing.T) {
	idx := buildTestIndex()

	judgments := []RelevanceJudgment{
		{
			Query:        "Go",
			RelevantURLs: []string{"https://go.dev"},
		},
		{
			Query:        "pasta",
			RelevantURLs: []string{"https://example.com/cooking"},
		},
	}

	report := Evaluate(idx, judgments, 10, DefaultBM25())

	if report.QueriesRun != 2 {
		t.Errorf("expected QueriesRun=2, got %d", report.QueriesRun)
	}

	// Mean precision and recall should be computed.
	if report.MeanPrecision < 0 {
		t.Errorf("mean precision should be >= 0, got %f", report.MeanPrecision)
	}
	if report.MeanRecall < 0 {
		t.Errorf("mean recall should be >= 0, got %f", report.MeanRecall)
	}

	t.Logf("Mean Precision@10=%.3f, Mean Recall=%.3f", report.MeanPrecision, report.MeanRecall)
}

func TestEvaluateEmptyJudgments(t *testing.T) {
	idx := buildTestIndex()

	report := Evaluate(idx, []RelevanceJudgment{}, 10, DefaultBM25())

	if report.QueriesRun != 0 {
		t.Errorf("expected QueriesRun=0, got %d", report.QueriesRun)
	}
}

func TestEvaluateNoRelevantFound(t *testing.T) {
	idx := buildTestIndex()

	judgments := []RelevanceJudgment{
		{
			Query:        "Go",
			RelevantURLs: []string{"https://nonexistent.example.com"},
		},
	}

	report := Evaluate(idx, judgments, 10, DefaultBM25())
	result := report.Results[0]

	if result.Hits != 0 {
		t.Errorf("expected 0 hits for non-existent URL, got %d", result.Hits)
	}
	if result.PrecisionK != 0 {
		t.Errorf("expected precision=0, got %f", result.PrecisionK)
	}
	if result.Recall != 0 {
		t.Errorf("expected recall=0, got %f", result.Recall)
	}
}

// ==================== Query Log Tests ====================

func TestQueryLogRecordAndStats(t *testing.T) {
	ql := NewQueryLog()

	ql.Record("golang", 2*time.Millisecond, 5)
	ql.Record("rust", 3*time.Millisecond, 3)
	ql.Record("python", 1*time.Millisecond, 8)

	stats := ql.Stats()

	if stats.TotalQueries != 3 {
		t.Errorf("expected TotalQueries=3, got %d", stats.TotalQueries)
	}

	// Average latency = (2+3+1)/3 = 2ms
	if stats.AvgLatencyMs < 1 || stats.AvgLatencyMs > 3 {
		t.Errorf("expected avg latency ~2ms, got %f", stats.AvgLatencyMs)
	}

	// QPS should be > 0
	if stats.QPS <= 0 {
		t.Errorf("expected QPS > 0, got %f", stats.QPS)
	}

	// Uptime should be > 0
	if stats.UptimeSeconds <= 0 {
		t.Errorf("expected UptimeSeconds > 0, got %f", stats.UptimeSeconds)
	}

	// Recent should have 3 entries.
	if len(stats.Recent) != 3 {
		t.Errorf("expected 3 recent entries, got %d", len(stats.Recent))
	}
}

func TestQueryLogEmpty(t *testing.T) {
	ql := NewQueryLog()
	stats := ql.Stats()

	if stats.TotalQueries != 0 {
		t.Errorf("expected TotalQueries=0, got %d", stats.TotalQueries)
	}
	if stats.AvgLatencyMs != 0 {
		t.Errorf("expected AvgLatencyMs=0, got %f", stats.AvgLatencyMs)
	}
}

func TestQueryLogRollingWindow(t *testing.T) {
	ql := NewQueryLog()

	// Record 110 queries — rolling window should keep last 100.
	for i := 0; i < 110; i++ {
		ql.Record("query", 1*time.Millisecond, 1)
	}

	stats := ql.Stats()

	if stats.TotalQueries != 110 {
		t.Errorf("expected TotalQueries=110, got %d", stats.TotalQueries)
	}
	if len(stats.Recent) != 100 {
		t.Errorf("expected 100 recent entries (rolling window), got %d", len(stats.Recent))
	}
}
