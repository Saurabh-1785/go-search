package index

// RelevanceJudgment defines which URLs are relevant for a given query.
// This is the standard IR evaluation format — a human labels which
// documents should appear for each query.
type RelevanceJudgment struct {
	Query        string   `json:"query"`
	RelevantURLs []string `json:"relevant_urls"`
}

// EvalResult holds precision and recall for a single query.
type EvalResult struct {
	Query      string  `json:"query"`
	PrecisionK float64 `json:"precision_at_k"` // |relevant ∩ topK| / K
	Recall     float64 `json:"recall"`          // |relevant ∩ topK| / |relevant|
	K          int     `json:"k"`
	Hits       int     `json:"hits"`          // relevant docs found in top K
	TotalRel   int     `json:"total_relevant"` // total relevant docs in judgment
}

// EvalReport holds evaluation results across all queries.
type EvalReport struct {
	Results       []EvalResult `json:"results"`
	MeanPrecision float64      `json:"mean_precision_at_k"`
	MeanRecall    float64      `json:"mean_recall"`
	QueriesRun    int          `json:"queries_run"`
}

// Evaluate runs each query against the index and compares results against
// the provided relevance judgments.
func Evaluate(idx *Index, judgments []RelevanceJudgment, k int, params BM25Params) EvalReport {
	report := EvalReport{
		Results: make([]EvalResult, 0, len(judgments)),
	}

	if k <= 0 {
		k = 10
	}

	var sumPrecision, sumRecall float64

	for _, j := range judgments {
		// Run the search.
		resp := idx.Search(j.Query, k, "or")

		// Build a set of relevant URLs for fast lookup.
		relevantSet := make(map[string]bool, len(j.RelevantURLs))
		for _, url := range j.RelevantURLs {
			relevantSet[url] = true
		}

		// Count how many of the returned results are in the relevant set.
		hits := 0
		for _, result := range resp.Results {
			if relevantSet[result.URL] {
				hits++
			}
		}

		totalRel := len(j.RelevantURLs)

		// Precision@K = hits / K
		precisionK := 0.0
		if k > 0 {
			precisionK = float64(hits) / float64(k)
		}

		// Recall = hits / total relevant
		recall := 0.0
		if totalRel > 0 {
			recall = float64(hits) / float64(totalRel)
		}

		result := EvalResult{
			Query:      j.Query,
			PrecisionK: precisionK,
			Recall:     recall,
			K:          k,
			Hits:       hits,
			TotalRel:   totalRel,
		}

		report.Results = append(report.Results, result)
		sumPrecision += precisionK
		sumRecall += recall
	}

	report.QueriesRun = len(judgments)

	if report.QueriesRun > 0 {
		report.MeanPrecision = sumPrecision / float64(report.QueriesRun)
		report.MeanRecall = sumRecall / float64(report.QueriesRun)
	}

	return report
}
