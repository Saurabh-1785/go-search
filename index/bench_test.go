package index

import (
	"fmt"
	"testing"
)

// ==================== BM25 Benchmarks ====================

func BenchmarkBM25Score(b *testing.B) {
	params := DefaultBM25()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		params.Score(5, 10, 100, 100.0, 1000)
	}
}

// ==================== Search Benchmarks ====================

// buildBenchIndex creates an index with N docs for benchmarking.
func buildBenchIndex(n int) *Index {
	idx := NewIndex()
	for i := 0; i < n; i++ {
		url := fmt.Sprintf("https://example.com/%d", i)
		// Vary content so BM25 length normalization is exercised.
		content := fmt.Sprintf("go programming language %d fast concurrent systems design patterns", i)
		if i%3 == 0 {
			content += " web server http handler middleware routing"
		}
		if i%5 == 0 {
			content += " database query optimization index search engine"
		}
		idx.AddDocument(fmt.Sprintf("sha%d", i), url, fmt.Sprintf("Doc %d", i), content)
	}
	return idx
}

func BenchmarkSearch1Term(b *testing.B) {
	idx := buildBenchIndex(1000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx.Search("go", 10, "or")
	}
}

func BenchmarkSearch3Terms(b *testing.B) {
	idx := buildBenchIndex(1000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx.Search("go programming language", 10, "or")
	}
}

func BenchmarkSearchCacheHit(b *testing.B) {
	idx := buildBenchIndex(1000)
	// Warm the cache with a first search.
	idx.Search("go programming", 10, "or")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx.Search("go programming", 10, "or")
	}
}

func BenchmarkSearchCacheMiss(b *testing.B) {
	idx := buildBenchIndex(1000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Different query each time → always a cache miss.
		idx.Search(fmt.Sprintf("query%d", i), 10, "or")
	}
}

// ==================== Compression Benchmarks ====================

func buildBenchPostings(n int) []Posting {
	postings := make([]Posting, n)
	for i := 0; i < n; i++ {
		postings[i] = Posting{DocID: uint32(i * 3), TF: uint32(i%5 + 1)}
	}
	return postings
}

func BenchmarkEncodePostings(b *testing.B) {
	postings := buildBenchPostings(10000)
	b.ResetTimer()
	b.SetBytes(int64(len(postings) * 8)) // report MB/s based on uncompressed size
	for i := 0; i < b.N; i++ {
		EncodePostings(postings)
	}
}

func BenchmarkDecodePostings(b *testing.B) {
	postings := buildBenchPostings(10000)
	encoded := EncodePostings(postings)
	b.ResetTimer()
	b.SetBytes(int64(len(postings) * 8))
	for i := 0; i < b.N; i++ {
		DecodePostings(encoded, len(postings))
	}
}

// ==================== Cache Benchmarks ====================

func BenchmarkLRUPut(b *testing.B) {
	cache := NewSearchCache(128)
	docs := []ScoredDoc{{DocID: 1, Score: 1.5}, {DocID: 2, Score: 0.8}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Put(fmt.Sprintf("query%d", i), docs, 2)
	}
}

func BenchmarkLRUGet(b *testing.B) {
	cache := NewSearchCache(128)
	docs := []ScoredDoc{{DocID: 1, Score: 1.5}}
	cache.Put("test", docs, 1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Get("test")
	}
}
