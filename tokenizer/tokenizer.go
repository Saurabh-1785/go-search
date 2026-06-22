package tokenizer

import (
	"strings"
	"unicode"
)

// Tokenize runs the full text-processing pipeline on the input text
// and returns a slice of normalized, stemmed tokens.
//
// Pipeline:
//  1. Lowercase the entire input
//  2. Split on any non-letter, non-digit character (Unicode-aware)
//  3. Remove stopwords
//  4. Apply Porter stemming
//
// Example:
//
//	Tokenize("The quick brown Fox's running!")
//	→ ["quick", "brown", "fox", "run"]
func Tokenize(text string) []string {
	tokens := tokenizeRaw(text)

	result := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		if IsStopword(tok) {
			continue
		}
		result = append(result, Stem(tok))
	}

	return result
}

// TokenizeWithoutStemming runs the pipeline without the final stemming step.
// Useful for debugging, display, and highlighting search results.
//
// Pipeline: lowercase → split → stopword filter (no stemming)
func TokenizeWithoutStemming(text string) []string {
	tokens := tokenizeRaw(text)

	result := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		if IsStopword(tok) {
			continue
		}
		result = append(result, tok)
	}

	return result
}

// Normalize lowercases the input text. This is the first step of the pipeline,
// exposed separately for cases where only normalization is needed.
func Normalize(text string) string {
	return strings.ToLower(text)
}

// tokenizeRaw performs lowercase + split into raw tokens (no filtering or stemming).
// Splits on any rune that is not a letter or digit, which handles:
//   - whitespace
//   - punctuation (commas, periods, colons, etc.)
//   - apostrophes ("don't" → ["don", "t"])
//   - hyphens ("well-known" → ["well", "known"])
//   - Unicode characters (accented letters are kept, symbols are split points)
func tokenizeRaw(text string) []string {
	lower := strings.ToLower(text)
	return strings.FieldsFunc(lower, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}
