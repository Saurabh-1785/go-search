package index

import "math"

// BM25Params holds tunable BM25 parameters.
type BM25Params struct {
	K1 float64
	B  float64
}

// DefaultBM25 returns the standard BM25 parameters.
func DefaultBM25() BM25Params {
	return BM25Params{K1: 1.2, B: 0.75}
}

// BM25IDF computes the BM25 inverse document frequency.
//
//	IDF = ln((N - DF + 0.5) / (DF + 0.5) + 1)
func BM25IDF(docCount int, df uint32) float64 {
	if df == 0 || docCount == 0 {
		return 0
	}
	n := float64(docCount)
	d := float64(df)
	return math.Log((n-d+0.5)/(d+0.5) + 1)
}

// Score computes the BM25 score for a single term in a single document.
//
//	score = IDF × (tf × (k1 + 1)) / (tf + k1 × (1 - b + b × |D| / avgdl))
func (p BM25Params) Score(tf uint32, df uint32, docLen int, avgDocLen float64, docCount int) float64 {
	if tf == 0 || df == 0 || docCount == 0 || avgDocLen == 0 {
		return 0
	}

	idf := BM25IDF(docCount, df)

	tfFloat := float64(tf)
	dl := float64(docLen)

	normFactor := 1 - p.B + p.B*(dl/avgDocLen)
	numerator := tfFloat * (p.K1 + 1)
	denominator := tfFloat + p.K1*normFactor

	return idf * (numerator / denominator)
}
