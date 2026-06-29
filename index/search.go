package index

import (
	"search-engine/tokenizer"
	"sort"
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
//  2. Compute average document length for BM25 normalization
//  3. Look up posting lists for each query term
//  4. Accumulate BM25 scores per document (TAAT — Term-At-A-Time)
//  5. Apply boolean filter (AND mode: only docs containing all terms)
//  6. Sort by score descending, take top-K
//  7. Generate snippets for top-K results only (lazy content read from disk)
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

	params := DefaultBM25()

	idx.mu.RLock()

	docCount := len(idx.docTable)
	if docCount == 0 {
		idx.mu.RUnlock()
		response.TimeTakenMs = float64(time.Since(start).Microseconds()) / 1000.0
		return response
	}

	// Compute average document length for BM25.
	// O(N) scan over docTable — just summing integers, very fast.
	var totalLen int64
	for _, meta := range idx.docTable {
		totalLen += int64(meta.Length)
	}
	avgDocLen := float64(totalLen) / float64(docCount)

	// Score accumulation: TAAT (Term-At-A-Time).
	// For each query term, iterate its posting list and add the BM25
	// contribution to each document's running score.
	scores := make(map[uint32]float64)
	termHits := make(map[uint32]int) // how many query terms each doc matched

	for _, term := range queryTerms {
		entry, exists := idx.dictionary[term]
		if !exists {
			continue
		}

		for _, p := range entry.Postings {
			docLen := 0
			if int(p.DocID) < len(idx.docTable) {
				docLen = idx.docTable[p.DocID].Length
			}

			scores[p.DocID] += params.Score(p.TF, entry.DF, docLen, avgDocLen, docCount)
			termHits[p.DocID]++
		}
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

	// Convert scores map to a sortable slice.
	type scoredDoc struct {
		DocID uint32
		Score float64
	}
	ranked := make([]scoredDoc, 0, len(scores))
	for docID, score := range scores {
		ranked = append(ranked, scoredDoc{DocID: docID, Score: score})
	}

	// Sort by score descending.
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Score == ranked[j].Score {
			return ranked[i].DocID < ranked[j].DocID // tie-break by DocID
		}
		return ranked[i].Score > ranked[j].Score
	})

	response.TotalHits = len(ranked)

	// Take top-K only.
	if len(ranked) > topK {
		ranked = ranked[:topK]
	}

	// Get the unstemmed query terms for snippet highlighting.
	// TokenizeWithoutStemming preserves word forms ("programming" not "program")
	// so highlights look natural to users.
	snippetTerms := tokenizer.TokenizeWithoutStemming(query)

	// Build results with snippets — content is loaded from disk only
	// for these top-K results (not all matching documents).
	results := make([]SearchResult, 0, len(ranked))
	for _, sd := range ranked {
		meta := idx.GetDocMeta(sd.DocID)
		if meta == nil {
			continue
		}

		// Load content from disk (or buffer) for snippet generation.
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

	response.Results = results
	response.TimeTakenMs = float64(time.Since(start).Microseconds()) / 1000.0

	return response
}
