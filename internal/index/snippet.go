package index

import (
	"strings"
	"unicode"
)

// GenerateSnippet extracts a text snippet from content around the first
// occurrence of a query term and highlights all matched terms with <mark> tags.
//
// Algorithm:
//  1. Find the earliest occurrence of any query term in the content
//  2. Extract a window of windowSize characters centered on the match
//  3. Clamp to word boundaries (don't cut words in half)
//  4. Add "..." prefix/suffix if the snippet is truncated
//  5. Wrap all query term occurrences in <mark> tags (case-insensitive)
//
// queryTerms should be unstemmed/lowercased original forms (from
// TokenizeWithoutStemming) so users see natural-looking highlights
// like "programming" instead of "program".
func GenerateSnippet(content string, queryTerms []string, windowSize int) string {
	if content == "" || len(queryTerms) == 0 {
		return ""
	}

	if windowSize <= 0 {
		windowSize = 200
	}

	contentLower := strings.ToLower(content)

	// Find the earliest match position across all query terms.
	bestPos := -1
	for _, term := range queryTerms {
		termLower := strings.ToLower(term)
		pos := strings.Index(contentLower, termLower)
		if pos != -1 && (bestPos == -1 || pos < bestPos) {
			bestPos = pos
		}
	}

	// If no term found in content, fall back to the beginning.
	if bestPos == -1 {
		bestPos = 0
	}

	// Calculate window boundaries.
	start := bestPos - windowSize/2
	end := bestPos + windowSize/2

	// Clamp to content bounds.
	if start < 0 {
		start = 0
	}
	if end > len(content) {
		end = len(content)
	}

	// Expand start to the nearest word boundary (don't cut words).
	if start > 0 {
		// Move forward to the next space/punctuation boundary.
		for start < end && !unicode.IsSpace(rune(content[start])) {
			start++
		}
		// Skip the whitespace itself.
		for start < end && unicode.IsSpace(rune(content[start])) {
			start++
		}
	}

	// Shrink end to the nearest word boundary.
	if end < len(content) {
		for end > start && !unicode.IsSpace(rune(content[end-1])) {
			end--
		}
	}

	// Safeguard: if clamping collapsed the window, just take what we can.
	if start >= end {
		start = bestPos
		end = bestPos + windowSize
		if end > len(content) {
			end = len(content)
		}
	}

	snippet := content[start:end]

	// Add ellipsis if truncated.
	prefix := ""
	suffix := ""
	if start > 0 {
		prefix = "..."
	}
	if end < len(content) {
		suffix = "..."
	}

	// Highlight query terms with <mark> tags (case-insensitive).
	snippet = highlightTerms(snippet, queryTerms)

	return prefix + snippet + suffix
}

// highlightTerms wraps all occurrences of query terms in <mark> tags.
// Case-insensitive matching, preserves original casing in output.
func highlightTerms(text string, terms []string) string {
	if len(terms) == 0 {
		return text
	}

	// Process each term — replace all case-insensitive occurrences.
	result := text
	for _, term := range terms {
		if term == "" {
			continue
		}

		termLower := strings.ToLower(term)
		var builder strings.Builder
		remaining := result
		remainingLower := strings.ToLower(remaining)

		for {
			idx := strings.Index(remainingLower, termLower)
			if idx == -1 {
				builder.WriteString(remaining)
				break
			}

			// Write text before the match.
			builder.WriteString(remaining[:idx])

			// Write the highlighted match (preserving original case).
			builder.WriteString("<mark>")
			builder.WriteString(remaining[idx : idx+len(term)])
			builder.WriteString("</mark>")

			// Advance past the match.
			remaining = remaining[idx+len(term):]
			remainingLower = remainingLower[idx+len(term):]
		}

		result = builder.String()
	}

	return result
}
