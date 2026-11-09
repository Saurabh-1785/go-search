// Package tokenizer provides text tokenization and normalization for search indexing.
//
// The pipeline: raw text → lowercase → split on non-alphanumeric → stopword filter → Porter stem.
package tokenizer

// stopwords is the classic IR English stopword list (~174 words).
// Stored as a map for O(1) lookup.
var stopwords = map[string]bool{
	// Articles
	"a": true, "an": true, "the": true,

	// Pronouns
	"i": true, "me": true, "my": true, "myself": true,
	"we": true, "our": true, "ours": true, "ourselves": true,
	"you": true, "your": true, "yours": true, "yourself": true, "yourselves": true,
	"he": true, "him": true, "his": true, "himself": true,
	"she": true, "her": true, "hers": true, "herself": true,
	"it": true, "its": true, "itself": true,
	"they": true, "them": true, "their": true, "theirs": true, "themselves": true,
	"what": true, "which": true, "who": true, "whom": true,
	"this": true, "that": true, "these": true, "those": true,

	// Verbs (be, have, do, will, etc.)
	"am": true, "is": true, "are": true, "was": true, "were": true,
	"be": true, "been": true, "being": true,
	"have": true, "has": true, "had": true, "having": true,
	"do": true, "does": true, "did": true, "doing": true,
	"will": true, "would": true, "shall": true, "should": true,
	"can": true, "could": true, "may": true, "might": true, "must": true,

	// Prepositions
	"about": true, "above": true, "after": true, "again": true, "against": true,
	"at": true, "before": true, "below": true, "between": true, "by": true,
	"down": true, "during": true, "for": true, "from": true, "in": true,
	"into": true, "of": true, "off": true, "on": true, "once": true,
	"out": true, "over": true, "through": true, "to": true, "under": true,
	"until": true, "up": true, "with": true,

	// Conjunctions
	"and": true, "but": true, "if": true, "or": true, "nor": true,
	"so": true, "yet": true, "both": true, "either": true, "neither": true,
	"not": true, "only": true, "own": true, "same": true,
	"than": true, "too": true, "very": true,

	// Other common function words
	"because": true, "as": true, "while": true, "although": true,
	"each": true, "every": true, "all": true, "any": true, "few": true,
	"more": true, "most": true, "other": true, "some": true, "such": true,
	"no": true, "just": true, "also": true, "then": true, "there": true,
	"here": true, "when": true, "where": true, "why": true, "how": true,
	"further": true, "now": true,

	// Contractions (after apostrophe splitting, these fragments appear)
	"s": true, "t": true, "d": true, "ll": true, "re": true, "ve": true, "m": true,

	// Additional high-frequency words with low information value
	"don": true, "didn": true, "doesn": true, "hadn": true, "hasn": true,
	"haven": true, "isn": true, "wasn": true, "weren": true, "won": true,
	"wouldn": true, "couldn": true, "shouldn": true, "mustn": true, "needn": true,
}

// IsStopword returns true if the word is in the stopword list.
// The word should already be lowercased.
func IsStopword(word string) bool {
	return stopwords[word]
}

// StopwordCount returns the number of words in the stopword list.
func StopwordCount() int {
	return len(stopwords)
}
