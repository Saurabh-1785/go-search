package index

import (
	"html"
	"sort"
	"strings"
	"unicode"
)

// GenerateSnippet extracts a text snippet from content around the first
// occurrence of a query term and highlights all matched terms with <mark> tags.
// The result is HTML-escaped and safe to render as HTML.
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

// highlightTerms HTML-escapes text and wraps all occurrences of query terms
// in <mark> tags. Matching is case-insensitive and preserves original casing.
//
// Crawled page text is untrusted: it is escaped so the snippet is safe to
// render as HTML (only the <mark> tags added here are markup). Matches are
// found on the raw text in one pass, so a term can never match inside
// a <mark> tag or an HTML entity.
func highlightTerms(text string, terms []string) string {
	lower := strings.ToLower(text)
	if len(lower) != len(text) {
		// Lowercasing changed byte offsets (rare Unicode case) — skip
		// highlighting rather than risk misaligned spans.
		return html.EscapeString(text)
	}

	type span struct{ start, end int }
	var spans []span
	for _, term := range terms {
		t := strings.ToLower(term)
		if t == "" {
			continue
		}
		for from := 0; ; {
			i := strings.Index(lower[from:], t)
			if i == -1 {
				break
			}
			start := from + i
			spans = append(spans, span{start, start + len(t)})
			from = start + len(t)
		}
	}
	if len(spans) == 0 {
		return html.EscapeString(text)
	}

	// Merge overlapping spans (e.g. terms "go" and "golang").
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	merged := spans[:1]
	for _, s := range spans[1:] {
		last := &merged[len(merged)-1]
		if s.start <= last.end {
			if s.end > last.end {
				last.end = s.end
			}
			continue
		}
		merged = append(merged, s)
	}

	var b strings.Builder
	prev := 0
	for _, s := range merged {
		b.WriteString(html.EscapeString(text[prev:s.start]))
		b.WriteString("<mark>")
		b.WriteString(html.EscapeString(text[s.start:s.end]))
		b.WriteString("</mark>")
		prev = s.end
	}
	b.WriteString(html.EscapeString(text[prev:]))
	return b.String()
}
