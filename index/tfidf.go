package index

import "math"

// IDF computes the inverse document frequency for a term.
//
// IDF = log10(N / DF)
//
// A rare term (low DF) gets a high IDF → it's discriminating.
// A common term (high DF) gets a low IDF → it's noise.
// Returns 0 if DF is 0 or DF >= N (term is in every doc or invalid).
func IDF(docCount int, df uint32) float64 {
	if df == 0 || int(df) >= docCount {
		return 0
	}
	return math.Log10(float64(docCount) / float64(df))
}

// TFIDF computes the length-normalized TF-IDF score for a single term
// in a single document.
//
//	score = (tf / docLen) × log10(N / df)
//
// Length normalization prevents long documents from dominating just
// because they contain more words. A term appearing 5 times in a
// 100-word doc is more significant than 5 times in a 10,000-word doc.
//
// Returns 0 if any denominator is 0.
func TFIDF(tf uint32, df uint32, docLen int, docCount int) float64 {
	if docLen == 0 || df == 0 || docCount == 0 {
		return 0
	}

	normalizedTF := float64(tf) / float64(docLen)
	idf := IDF(docCount, df)

	return normalizedTF * idf
}
