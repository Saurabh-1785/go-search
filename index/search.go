package index

import (
	"search-engine/tokenizer"
	"sort"
	"sync"
	"time"
)

// SearchResult represents a single ranked search result.
type SearchResult struct {
	DocID      uint32  `json:"doc_id"`
	ExternalID string  `json:"external_id"`
	URL        string  `json:"url"`
	Title      string  `json:"title"`
	Score      float64 `json:"score"`
	Snippet    string  `json:"snippet"`
}

// SearchResponse wraps the full search response with timing.
type SearchResponse struct {
	Query       string         `json:"query"`
	Results     []SearchResult `json:"results"`
	TotalHits   int            `json:"total_hits"`
	TimeTakenMs float64        `json:"time_taken_ms"`
	CacheHit    bool           `json:"cache_hit"`
}

// Search processes a query string against the index using BM25 scoring.
//
// Parameters:
//   - query: raw user query string (will be tokenized through the same pipeline)
//   - topK: maximum number of results to return
//   - mode: "or" (default, any term matches) or "and" (all terms must match)
//
// Flow:
//  1. Tokenize the query through the same pipeline as documents
//  2. Check cache for previously scored results
//  3. On cache miss: score with BM25 (parallel for multi-term queries)
//  4. Cache the scored doc list
//  5. Generate snippets for top-K results only (lazy content read from disk)
func (idx *Index) Search(query string, topK int, mode string) SearchResponse {
	start := time.Now()

	response := SearchResponse{
		Query:   query,
		Results: make([]SearchResult, 0),
	}

	// Tokenize the query through the same pipeline as documents.
	queryTerms := tokenizer.Tokenize(query)
	if len(queryTerms) == 0 {
		response.TimeTakenMs = float64(time.Since(start).Microseconds()) / 1000.0
		return response
	}

	if topK <= 0 {
		topK = 10
	}

	if mode == "" {
		mode = "or"
	}

	// --- Check cache ---
	cacheKey := CacheKey(query, topK, mode)
	if idx.cache != nil {
		if docs, totalHits, ok := idx.cache.Get(cacheKey); ok {
			response.CacheHit = true
			response.TotalHits = totalHits
			response.Results = idx.buildResults(docs, query)
			response.TimeTakenMs = float64(time.Since(start).Microseconds()) / 1000.0
			return response
		}
	}

	// --- Cache miss: compute scores ---
	ranked, totalHits := idx.scoreQuery(queryTerms, topK, mode)

	// Cache the scored docs (not the full response).
	if idx.cache != nil {
		idx.cache.Put(cacheKey, ranked, totalHits)
	}

	response.TotalHits = totalHits
	response.Results = idx.buildResults(ranked, query)
	response.TimeTakenMs = float64(time.Since(start).Microseconds()) / 1000.0

	return response
}

// scoreQuery runs BM25 scoring across all query terms.
// For multi-term queries (≥2 terms), each term is scored in its own goroutine.
// Returns the ranked top-K scored docs and total hit count.
func (idx *Index) scoreQuery(queryTerms []string, topK int, mode string) ([]ScoredDoc, int) {
	params := DefaultBM25()

	idx.mu.RLock()

	docCount := len(idx.docTable)
	if docCount == 0 {
		idx.mu.RUnlock()
		return nil, 0
	}

	// Compute average document length for BM25.
	var totalLen int64
	for _, meta := range idx.docTable {
		totalLen += int64(meta.Length)
	}
	avgDocLen := float64(totalLen) / float64(docCount)

	// --- Scoring: sequential for 1 term, parallel for 2+ ---
	var scores map[uint32]float64
	var termHits map[uint32]int

	if len(queryTerms) == 1 {
		// Single term: no goroutine overhead.
		scores, termHits = idx.scoreTerm(queryTerms[0], params, avgDocLen, docCount)
	} else {
		// Multi-term: parallel scoring with per-goroutine maps.
		scores, termHits = idx.scoreTermsParallel(queryTerms, params, avgDocLen, docCount)
	}

	idx.mu.RUnlock()

	// Boolean AND filter: only keep docs that matched ALL query terms.
	if mode == "and" {
		for docID := range scores {
			if termHits[docID] < len(queryTerms) {
				delete(scores, docID)
			}
		}
	}

	// Convert to sorted slice.
	ranked := make([]ScoredDoc, 0, len(scores))
	for docID, score := range scores {
		ranked = append(ranked, ScoredDoc{DocID: docID, Score: score})
	}

	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Score == ranked[j].Score {
			return ranked[i].DocID < ranked[j].DocID // tie-break by DocID
		}
		return ranked[i].Score > ranked[j].Score
	})

	totalHits := len(ranked)

	if len(ranked) > topK {
		ranked = ranked[:topK]
	}

	return ranked, totalHits
}

// scoreTerm scores a single term's posting list. No goroutines.
// Caller must hold idx.mu.RLock().
func (idx *Index) scoreTerm(term string, params BM25Params, avgDocLen float64, docCount int) (map[uint32]float64, map[uint32]int) {
	scores := make(map[uint32]float64)
	termHits := make(map[uint32]int)

	entry, exists := idx.dictionary[term]
	if !exists {
		return scores, termHits
	}

	for _, p := range entry.Postings {
		docLen := 0
		if int(p.DocID) < len(idx.docTable) {
			docLen = idx.docTable[p.DocID].Length
		}
		scores[p.DocID] += params.Score(p.TF, entry.DF, docLen, avgDocLen, docCount)
		termHits[p.DocID]++
	}

	return scores, termHits
}

// scoreTermsParallel scores multiple terms in parallel, one goroutine per term.
// Each goroutine produces a local score map. Results are merged after all finish.
// Caller must hold idx.mu.RLock().
func (idx *Index) scoreTermsParallel(queryTerms []string, params BM25Params, avgDocLen float64, docCount int) (map[uint32]float64, map[uint32]int) {
	type termResult struct {
		scores   map[uint32]float64
		termHits map[uint32]int
	}

	results := make([]termResult, len(queryTerms))
	var wg sync.WaitGroup

	for i, term := range queryTerms {
		wg.Add(1)
		go func(i int, term string) {
			defer wg.Done()

			localScores := make(map[uint32]float64)
			localHits := make(map[uint32]int)

			entry, exists := idx.dictionary[term]
			if !exists {
				results[i] = termResult{localScores, localHits}
				return
			}

			for _, p := range entry.Postings {
				docLen := 0
				if int(p.DocID) < len(idx.docTable) {
					docLen = idx.docTable[p.DocID].Length
				}
				localScores[p.DocID] = params.Score(p.TF, entry.DF, docLen, avgDocLen, docCount)
				localHits[p.DocID] = 1
			}

			results[i] = termResult{localScores, localHits}
		}(i, term)
	}

	wg.Wait()

	// Merge: single-threaded, no locks needed.
	merged := make(map[uint32]float64)
	mergedHits := make(map[uint32]int)

	for _, r := range results {
		for docID, score := range r.scores {
			merged[docID] += score
		}
		for docID, count := range r.termHits {
			mergedHits[docID] += count
		}
	}

	return merged, mergedHits
}

// buildResults converts scored docs into SearchResults with snippets.
// Loads content from disk only for the top-K docs.
func (idx *Index) buildResults(ranked []ScoredDoc, query string) []SearchResult {
	if len(ranked) == 0 {
		return nil
	}

	// Get the unstemmed query terms for snippet highlighting.
	snippetTerms := tokenizer.TokenizeWithoutStemming(query)

	results := make([]SearchResult, 0, len(ranked))
	for _, sd := range ranked {
		meta := idx.GetDocMeta(sd.DocID)
		if meta == nil {
			continue
		}

		content := idx.GetDocContent(sd.DocID)
		snippet := ""
		if content != "" {
			snippet = GenerateSnippet(content, snippetTerms, 200)
		}

		results = append(results, SearchResult{
			DocID:      sd.DocID,
			ExternalID: meta.ExternalID,
			URL:        meta.URL,
			Title:      meta.Title,
			Score:      sd.Score,
			Snippet:    snippet,
		})
	}

	return results
}
