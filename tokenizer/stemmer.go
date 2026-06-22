package tokenizer

import "strings"

// Stem applies the Porter stemming algorithm to a single lowercase word.
// It returns the stemmed form of the word.
//
// The algorithm was published by Martin Porter in 1980 and operates in 5 steps,
// each removing or replacing suffixes based on the "measure" of the remaining stem.
//
// Reference: https://tartarus.org/martin/PorterStemmer/def.txt
//
// Note: the input MUST be lowercase. The function does not check or convert case.
func Stem(word string) string {
	if len(word) <= 2 {
		return word
	}

	word = strings.ToLower(word)

	w := []byte(word)
	w = step1a(w)
	w = step1b(w)
	w = step1c(w)
	w = step2(w)
	w = step3(w)
	w = step4(w)
	w = step5a(w)
	w = step5b(w)

	return string(w)
}

// --- Helper functions ---

// isConsonant returns true if w[i] is a consonant in the Porter sense.
// A consonant is any letter that is not a, e, i, o, u, and not y when
// preceded by a consonant.
func isConsonant(w []byte, i int) bool {
	switch w[i] {
	case 'a', 'e', 'i', 'o', 'u':
		return false
	case 'y':
		if i == 0 {
			return true
		}
		return !isConsonant(w, i-1)
	default:
		return true
	}
}

// measure returns the "measure" m of a word — the number of VC (vowel-consonant)
// sequences in the stem. For example:
//
//	TR        → 0  (no vowel)
//	EE        → 0  (no consonant after vowel)
//	TREE      → 0  (C, no VC pair)
//	TROUBLE   → 1  (C-V-C = 1 VC)
//	OATS      → 1  (V-C = 1 VC)
//	TREES     → 1  (C-V-C = 1 VC)
//	PRIVATE   → 2  (C-V-C-V-C = 2 VC)
func measure(w []byte) int {
	n := len(w)
	i := 0

	// Skip initial consonants
	for i < n && isConsonant(w, i) {
		i++
	}

	m := 0
	for i < n {
		// Skip vowels
		for i < n && !isConsonant(w, i) {
			i++
		}
		if i >= n {
			break
		}
		// Skip consonants — this is one VC pair
		for i < n && isConsonant(w, i) {
			i++
		}
		m++
	}
	return m
}

// hasSuffix checks if w ends with the given suffix.
func hasSuffix(w []byte, suffix string) bool {
	s := []byte(suffix)
	if len(w) < len(s) {
		return false
	}
	for i := range s {
		if w[len(w)-len(s)+i] != s[i] {
			return false
		}
	}
	return true
}

// replaceSuffix replaces the last len(suffix) bytes of w with replacement.
func replaceSuffix(w []byte, suffix, replacement string) []byte {
	stem := w[:len(w)-len(suffix)]
	return append(stem, []byte(replacement)...)
}

// containsVowel returns true if the stem w contains at least one vowel.
func containsVowel(w []byte) bool {
	for i := range w {
		if !isConsonant(w, i) {
			return true
		}
	}
	return false
}

// endsWithDouble returns true if w ends with a double consonant (e.g., "ll", "ss").
func endsWithDouble(w []byte) bool {
	n := len(w)
	if n < 2 {
		return false
	}
	return w[n-1] == w[n-2] && isConsonant(w, n-1)
}

// endsCVC returns true if w ends with consonant-vowel-consonant,
// where the last consonant is not w, x, or y.
// This is the *o condition in Porter's paper.
func endsCVC(w []byte) bool {
	n := len(w)
	if n < 3 {
		return false
	}
	c := w[n-1]
	if c == 'w' || c == 'x' || c == 'y' {
		return false
	}
	return isConsonant(w, n-1) && !isConsonant(w, n-2) && isConsonant(w, n-3)
}

// --- Step 1a: Plural suffixes ---
//
//	SSES → SS     caresses → caress
//	IES  → I      ponies   → poni
//	SS   → SS     caress   → caress  (no change)
//	S    →        cats     → cat
func step1a(w []byte) []byte {
	if hasSuffix(w, "sses") {
		return replaceSuffix(w, "sses", "ss")
	}
	if hasSuffix(w, "ies") {
		return replaceSuffix(w, "ies", "i")
	}
	if hasSuffix(w, "ss") {
		return w // no change
	}
	if hasSuffix(w, "s") {
		return w[:len(w)-1]
	}
	return w
}

// --- Step 1b: -ED, -ING verb suffixes ---
//
//	(m>0) EED → EE     agreed → agree, feed → feed
//	(*v*) ED  →        plastered → plaster, bled → bled
//	(*v*) ING →        motoring → motor, sing → sing
//
// After removing ED/ING (when stem has vowel), apply fixups:
//
//	AT → ATE    conflat(ed) → conflate
//	BL → BLE    troubl(ing) → trouble
//	IZ → IZE    siz(ing)    → size
//	(*d and not *L,*S,*Z) → single letter    hopp(ing) → hop
//	(m=1 and *o) → E       fil(ing) → file
func step1b(w []byte) []byte {
	if hasSuffix(w, "eed") {
		stem := w[:len(w)-3]
		if measure(stem) > 0 {
			return replaceSuffix(w, "eed", "ee")
		}
		return w
	}

	changed := false
	if hasSuffix(w, "ed") {
		stem := w[:len(w)-2]
		if containsVowel(stem) {
			w = stem
			changed = true
		}
	} else if hasSuffix(w, "ing") {
		stem := w[:len(w)-3]
		if containsVowel(stem) {
			w = stem
			changed = true
		}
	}

	if changed {
		// Apply fixups after removing -ed / -ing
		if hasSuffix(w, "at") {
			return append(w, 'e')
		}
		if hasSuffix(w, "bl") {
			return append(w, 'e')
		}
		if hasSuffix(w, "iz") {
			return append(w, 'e')
		}

		// Double consonant (but not l, s, z) → remove last letter
		if endsWithDouble(w) {
			last := w[len(w)-1]
			if last != 'l' && last != 's' && last != 'z' {
				return w[:len(w)-1]
			}
		}

		// Short word: measure=1 and ends CVC → add 'e'
		if measure(w) == 1 && endsCVC(w) {
			return append(w, 'e')
		}
	}

	return w
}

// --- Step 1c: Terminal Y ---
//
//	(*v*) Y → I    happy → happi, sky → sky
func step1c(w []byte) []byte {
	if len(w) > 1 && w[len(w)-1] == 'y' {
		stem := w[:len(w)-1]
		if containsVowel(stem) {
			w[len(w)-1] = 'i'
		}
	}
	return w
}

// --- Step 2: Double suffix normalization ---
//
// (m>0) suffix → replacement
func step2(w []byte) []byte {
	if len(w) < 4 {
		return w
	}

	// Step 2 rules, ordered by suffix. Longest suffixes checked first
	// where ambiguity exists (e.g., "ational" before "ation").
	type rule struct {
		suffix, replacement string
	}

	rules := []rule{
		{"ational", "ate"},
		{"tional", "tion"},
		{"enci", "ence"},
		{"anci", "ance"},
		{"izer", "ize"},
		{"abli", "able"},
		{"alli", "al"},
		{"entli", "ent"},
		{"eli", "e"},
		{"ousli", "ous"},
		{"ization", "ize"},
		{"ation", "ate"},
		{"ator", "ate"},
		{"alism", "al"},
		{"iveness", "ive"},
		{"fulness", "ful"},
		{"ousness", "ous"},
		{"aliti", "al"},
		{"iviti", "ive"},
		{"biliti", "ble"},
	}

	for _, r := range rules {
		if hasSuffix(w, r.suffix) {
			return step2Replace(w, r.suffix, r.replacement)
		}
	}

	return w
}


// step2Replace replaces suffix if measure of stem > 0.
func step2Replace(w []byte, suffix, replacement string) []byte {
	stem := w[:len(w)-len(suffix)]
	if measure(stem) > 0 {
		return append(stem, []byte(replacement)...)
	}
	return w
}

// --- Step 3: More suffix normalization ---
//
// (m>0) suffix → replacement
func step3(w []byte) []byte {
	if len(w) < 4 {
		return w
	}

	switch w[len(w)-1] {
	case 'e':
		if hasSuffix(w, "icate") {
			return step3Replace(w, "icate", "ic")
		}
		if hasSuffix(w, "ative") {
			return step3Replace(w, "ative", "")
		}
		if hasSuffix(w, "alize") {
			return step3Replace(w, "alize", "al")
		}
	case 'i':
		if hasSuffix(w, "iciti") {
			return step3Replace(w, "iciti", "ic")
		}
	case 'l':
		if hasSuffix(w, "ical") {
			return step3Replace(w, "ical", "ic")
		}
		if hasSuffix(w, "ful") {
			return step3Replace(w, "ful", "")
		}
	case 's':
		if hasSuffix(w, "ness") {
			return step3Replace(w, "ness", "")
		}
	}

	return w
}

// step3Replace replaces suffix if measure of stem > 0.
func step3Replace(w []byte, suffix, replacement string) []byte {
	stem := w[:len(w)-len(suffix)]
	if measure(stem) > 0 {
		return append(stem, []byte(replacement)...)
	}
	return w
}

// --- Step 4: Remove long suffixes ---
//
// (m>1) suffix → (delete)
func step4(w []byte) []byte {
	if len(w) < 4 {
		return w
	}

	// Try each suffix, grouped by second-to-last letter for efficiency.
	// All require m>1 on the stem after removal.
	switch w[len(w)-2] {
	case 'a':
		if hasSuffix(w, "al") {
			return step4Delete(w, "al")
		}
	case 'c':
		if hasSuffix(w, "ance") {
			return step4Delete(w, "ance")
		}
		if hasSuffix(w, "ence") {
			return step4Delete(w, "ence")
		}
	case 'e':
		if hasSuffix(w, "er") {
			return step4Delete(w, "er")
		}
	case 'i':
		if hasSuffix(w, "ic") {
			return step4Delete(w, "ic")
		}
	case 'l':
		if hasSuffix(w, "able") {
			return step4Delete(w, "able")
		}
		if hasSuffix(w, "ible") {
			return step4Delete(w, "ible")
		}
	case 'n':
		if hasSuffix(w, "ant") {
			return step4Delete(w, "ant")
		}
		if hasSuffix(w, "ement") {
			return step4Delete(w, "ement")
		}
		if hasSuffix(w, "ment") {
			return step4Delete(w, "ment")
		}
		if hasSuffix(w, "ent") {
			return step4Delete(w, "ent")
		}
	case 'o':
		// Special case: -ion only if preceded by 's' or 't'
		if hasSuffix(w, "ion") {
			stem := w[:len(w)-3]
			if len(stem) > 0 && (stem[len(stem)-1] == 's' || stem[len(stem)-1] == 't') {
				if measure(stem) > 1 {
					return stem
				}
			}
		}
		if hasSuffix(w, "ou") {
			return step4Delete(w, "ou")
		}
	case 's':
		if hasSuffix(w, "ism") {
			return step4Delete(w, "ism")
		}
	case 't':
		if hasSuffix(w, "ate") {
			return step4Delete(w, "ate")
		}
		if hasSuffix(w, "iti") {
			return step4Delete(w, "iti")
		}
	case 'u':
		if hasSuffix(w, "ous") {
			return step4Delete(w, "ous")
		}
	case 'v':
		if hasSuffix(w, "ive") {
			return step4Delete(w, "ive")
		}
	case 'z':
		if hasSuffix(w, "ize") {
			return step4Delete(w, "ize")
		}
	}

	return w
}

// step4Delete removes the suffix if the stem's measure > 1.
func step4Delete(w []byte, suffix string) []byte {
	stem := w[:len(w)-len(suffix)]
	if measure(stem) > 1 {
		return stem
	}
	return w
}

// --- Step 5a: Remove final 'e' ---
//
//	(m>1) E →          probate → probat
//	(m=1 and not *o) E →   (no change)
func step5a(w []byte) []byte {
	if len(w) < 2 {
		return w
	}
	if w[len(w)-1] != 'e' {
		return w
	}

	stem := w[:len(w)-1]
	m := measure(stem)

	if m > 1 {
		return stem
	}
	if m == 1 && !endsCVC(stem) {
		return stem
	}
	return w
}

// --- Step 5b: Remove double final consonant ---
//
//	(m>1 and *d and *L) → single letter    controll → control, roll → roll
func step5b(w []byte) []byte {
	n := len(w)
	if n < 2 {
		return w
	}
	if w[n-1] == 'l' && w[n-2] == 'l' && measure(w) > 1 {
		return w[:n-1]
	}
	return w
}
