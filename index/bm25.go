package index

import "math"

// BM25Params holds tunable BM25 parameters.
//
// K1 controls term frequency saturation — how quickly repeated occurrences
// of a term stop mattering. Higher K1 = more weight to raw TF.
//
// B controls document length normalization — how much longer documents
// are penalized. B=0 means no length normalization; B=1 means full
// normalization relative to average document length.
type BM25Params struct {
	K1 float64 // term frequency saturation (default 1.2)
	B  float64 // document length normalization (default 0.75)
}

// DefaultBM25 returns the standard BM25 parameters used by Elasticsearch and Lucene.
//
// K1=1.2: After ~3-4 occurrences, additional mentions contribute very little.
// B=0.75: Documents 2× the average length are penalized, but not drastically.
func DefaultBM25() BM25Params {
	return BM25Params{K1: 1.2, B: 0.75}
}

// BM25IDF computes the BM25 inverse document frequency with Robertson-Walker
// smoothing.
//
//	IDF = ln((N - DF + 0.5) / (DF + 0.5) + 1)
//
// Unlike vanilla IDF (log(N/DF)), this never goes negative — even terms that
// appear in more than half the corpus get a small positive weight.
//
// The +1 inside the ln prevents negative values when DF > N/2.
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
//
// Where:
//   - tf:    raw term frequency from Posting.TF
//   - df:    document frequency from TermEntry.DF
//   - docLen:  document length from DocMeta.Length
//   - avgDocLen: average document length across the corpus
//   - docCount: total number of documents
//
// The numerator tf × (k1+1) ensures that a term appearing once gets a
// non-trivial score. The denominator adds saturation: going from TF=1→2
// is a big jump, but TF=10→11 is marginal.
func (p BM25Params) Score(tf uint32, df uint32, docLen int, avgDocLen float64, docCount int) float64 {
	if tf == 0 || df == 0 || docCount == 0 || avgDocLen == 0 {
		return 0
	}

	idf := BM25IDF(docCount, df)

	tfFloat := float64(tf)
	dl := float64(docLen)

	// Length normalization factor.
	// When B=0.75: a doc 2× avg length has normFactor ≈ 1.75, penalizing it.
	// When B=0:    normFactor = 1.0 (no length normalization).
	normFactor := 1 - p.B + p.B*(dl/avgDocLen)

	// TF saturation.
	// numerator = tf × (k1 + 1)
	// denominator = tf + k1 × normFactor
	numerator := tfFloat * (p.K1 + 1)
	denominator := tfFloat + p.K1*normFactor

	return idf * (numerator / denominator)
}
