package tokenizer

import (
	"reflect"
	"strings"
	"testing"
)

// ============================================================
// Tokenizer pipeline tests
// ============================================================

func TestTokenize_BasicSentence(t *testing.T) {
	got := Tokenize("The quick brown fox jumps over the lazy dog")
	want := []string{"quick", "brown", "fox", "jump", "lazi", "dog"}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tokenize basic sentence:\n  got  = %v\n  want = %v", got, want)
	}
}

func TestTokenize_PunctuationRemoval(t *testing.T) {
	got := Tokenize("Hello, world! How are you today?")
	// "how", "are", "you" are stopwords → filtered
	// "hello" → "hello", "world" → "world", "today" → "today"
	want := []string{"hello", "world", "todai"}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tokenize with punctuation:\n  got  = %v\n  want = %v", got, want)
	}
}

func TestTokenize_Apostrophes(t *testing.T) {
	got := Tokenize("don't you think it's working?")
	// "don't" splits to "don" (stopword) + "t" (stopword)
	// "you" stopword, "think" kept, "it" stopword, "s" stopword, "working" kept
	want := []string{"think", "work"}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tokenize with apostrophes:\n  got  = %v\n  want = %v", got, want)
	}
}

func TestTokenize_Hyphens(t *testing.T) {
	got := Tokenize("well-known state-of-the-art technology")
	// "well" → "well", "known" → "known"
	// "state" → "state", "of" stopword, "the" stopword, "art" → "art"
	// "technology" → stemmed
	want := []string{"well", "known", "state", "art", "technologi"}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tokenize with hyphens:\n  got  = %v\n  want = %v", got, want)
	}
}

func TestTokenize_NumbersKept(t *testing.T) {
	got := Tokenize("HTTP 404 error in version 2.0")
	// "in" is stopword
	want := []string{"http", "404", "error", "version", "2", "0"}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tokenize with numbers:\n  got  = %v\n  want = %v", got, want)
	}
}

func TestTokenize_EmptyInput(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"whitespace only", "   \t\n  "},
		{"punctuation only", "!@#$%^&*()"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Tokenize(tc.input)
			if len(got) != 0 {
				t.Errorf("Tokenize(%q) = %v, want empty slice", tc.input, got)
			}
		})
	}
}

func TestTokenize_AllStopwords(t *testing.T) {
	got := Tokenize("the is a an and or but")
	if len(got) != 0 {
		t.Errorf("All-stopword sentence should return empty, got %v", got)
	}
}

func TestTokenize_CaseFolding(t *testing.T) {
	got := Tokenize("GOLANG is GREAT for CONCURRENCY")
	want := []string{"golang", "great", "concurr"}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tokenize with mixed case:\n  got  = %v\n  want = %v", got, want)
	}
}

func TestTokenize_UnicodeText(t *testing.T) {
	// Accented characters should be kept as part of tokens
	got := Tokenize("café résumé naïve")
	if len(got) != 3 {
		t.Errorf("Unicode text should produce 3 tokens, got %d: %v", len(got), got)
	}
	// First token should contain the accented characters
	if got[0] != "café" {
		t.Errorf("Expected 'café', got %q", got[0])
	}
}

func TestTokenize_LongInput(t *testing.T) {
	// Performance sanity: should not hang or crash on large input
	words := strings.Repeat("the quick brown fox jumps over the lazy dog ", 1000)
	got := Tokenize(words)
	if len(got) == 0 {
		t.Error("Long input produced no tokens")
	}
}

// ============================================================
// TokenizeWithoutStemming tests
// ============================================================

func TestTokenizeWithoutStemming(t *testing.T) {
	got := TokenizeWithoutStemming("The dogs are running quickly")
	// "the" and "are" are stopwords
	want := []string{"dogs", "running", "quickly"}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("TokenizeWithoutStemming:\n  got  = %v\n  want = %v", got, want)
	}
}

// ============================================================
// Normalize tests
// ============================================================

func TestNormalize(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"Hello World", "hello world"},
		{"ALL CAPS", "all caps"},
		{"already lowercase", "already lowercase"},
		{"MiXeD CaSe", "mixed case"},
		{"", ""},
	}

	for _, tc := range tests {
		got := Normalize(tc.input)
		if got != tc.want {
			t.Errorf("Normalize(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// ============================================================
// Stopword tests
// ============================================================

func TestIsStopword(t *testing.T) {
	// Should be stopwords
	stopwordList := []string{"the", "is", "a", "an", "and", "or", "of", "in", "to", "for"}
	for _, w := range stopwordList {
		if !IsStopword(w) {
			t.Errorf("IsStopword(%q) = false, want true", w)
		}
	}

	// Should NOT be stopwords
	nonStopwords := []string{"golang", "search", "engine", "index", "token", "porter"}
	for _, w := range nonStopwords {
		if IsStopword(w) {
			t.Errorf("IsStopword(%q) = true, want false", w)
		}
	}
}

func TestIsStopword_ContractionFragments(t *testing.T) {
	// After apostrophe splitting, contraction fragments should be stopwords
	fragments := []string{"s", "t", "d", "ll", "re", "ve", "m"}
	for _, f := range fragments {
		if !IsStopword(f) {
			t.Errorf("IsStopword(%q) = false, want true (contraction fragment)", f)
		}
	}
}

func TestStopwordCount(t *testing.T) {
	count := StopwordCount()
	if count < 100 {
		t.Errorf("StopwordCount() = %d, expected at least 100", count)
	}
}

// ============================================================
// Porter Stemmer tests
// ============================================================

func TestStem_Step1a_Plurals(t *testing.T) {
	tests := []struct{ input, want string }{
		{"caresses", "caress"},
		{"ponies", "poni"},
		{"ties", "ti"},
		{"cats", "cat"},
		{"caress", "caress"},
		{"dogs", "dog"},
	}
	for _, tc := range tests {
		got := Stem(tc.input)
		if got != tc.want {
			t.Errorf("Stem(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestStem_Step1b_VerbSuffixes(t *testing.T) {
	tests := []struct{ input, want string }{
		{"agreed", "agre"},
		{"feed", "feed"},
		{"plastered", "plaster"},
		{"bled", "bled"},
		{"motoring", "motor"},
		{"sing", "sing"},
		{"conflated", "conflat"},
		{"troubled", "troubl"},
		{"sized", "size"},
		{"hopping", "hop"},
		{"tanning", "tan"},
		{"falling", "fall"},
		{"hissing", "hiss"},
		{"fizzing", "fizz"},
		{"filing", "file"},
	}
	for _, tc := range tests {
		got := Stem(tc.input)
		if got != tc.want {
			t.Errorf("Stem(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestStem_Step1c_TerminalY(t *testing.T) {
	tests := []struct{ input, want string }{
		{"happy", "happi"},
		{"sky", "sky"},
	}
	for _, tc := range tests {
		got := Stem(tc.input)
		if got != tc.want {
			t.Errorf("Stem(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestStem_Step2_DoubleSuffixes(t *testing.T) {
	tests := []struct{ input, want string }{
		{"relational", "relat"},
		{"conditional", "condit"},
		{"rational", "ration"},
		{"valenci", "valenc"},
		{"hesitanci", "hesit"},
		{"digitizer", "digit"},
		{"conformabli", "conform"},
		{"radicalli", "radic"},
		{"differentli", "differ"},
		{"vileli", "vile"},
		{"analogousli", "analog"},
		{"vietnamization", "vietnam"},
		{"predication", "predic"},
		{"operator", "oper"},
		{"feudalism", "feudal"},
		{"decisiveness", "decis"},
		{"hopefulness", "hope"},
		{"callousness", "callous"},
		{"formaliti", "formal"},
		{"sensitiviti", "sensit"},
		{"sensibiliti", "sensibl"},
	}
	for _, tc := range tests {
		got := Stem(tc.input)
		if got != tc.want {
			t.Errorf("Stem(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestStem_Step3(t *testing.T) {
	tests := []struct{ input, want string }{
		{"triplicate", "triplic"},
		{"formative", "form"},
		{"formalize", "formal"},
		{"electriciti", "electr"},
		{"electrical", "electr"},
		{"hopeful", "hope"},
		{"goodness", "good"},
	}
	for _, tc := range tests {
		got := Stem(tc.input)
		if got != tc.want {
			t.Errorf("Stem(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestStem_Step4_LongSuffixes(t *testing.T) {
	tests := []struct{ input, want string }{
		{"revival", "reviv"},
		{"allowance", "allow"},
		{"inference", "infer"},
		{"airliner", "airlin"},
		{"gyroscopic", "gyroscop"},
		{"adjustable", "adjust"},
		{"defensible", "defens"},
		{"irritant", "irrit"},
		{"replacement", "replac"},
		{"adjustment", "adjust"},
		{"dependent", "depend"},
		{"adoption", "adopt"},
		{"homologou", "homolog"},
		{"communism", "commun"},
		{"activate", "activ"},
		{"angulariti", "angular"},
		{"homologous", "homolog"},
		{"effective", "effect"},
		{"bowdlerize", "bowdler"},
	}
	for _, tc := range tests {
		got := Stem(tc.input)
		if got != tc.want {
			t.Errorf("Stem(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestStem_Step5(t *testing.T) {
	tests := []struct{ input, want string }{
		{"probate", "probat"},
		{"rate", "rate"},
		{"cease", "ceas"},
		{"controll", "control"},
		{"roll", "roll"},
	}
	for _, tc := range tests {
		got := Stem(tc.input)
		if got != tc.want {
			t.Errorf("Stem(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestStem_EdgeCases(t *testing.T) {
	tests := []struct{ input, want string }{
		{"", ""},
		{"a", "a"},
		{"go", "go"},
		{"the", "the"}, // Stemmer doesn't filter stopwords — that's the tokenizer's job
		{"running", "run"},
		{"generalization", "gener"},
	}
	for _, tc := range tests {
		got := Stem(tc.input)
		if got != tc.want {
			t.Errorf("Stem(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// ============================================================
// Measure helper tests
// ============================================================

func TestMeasure(t *testing.T) {
	tests := []struct {
		word string
		want int
	}{
		{"tr", 0},
		{"ee", 0},
		{"tree", 0},
		{"y", 0},
		{"by", 0},
		{"trouble", 1},
		{"oats", 1},
		{"trees", 1},
		{"ivy", 1},
		{"troubles", 2},
		{"private", 2},
		{"oaten", 2},
		{"orrery", 2},
	}
	for _, tc := range tests {
		got := measure([]byte(tc.word))
		if got != tc.want {
			t.Errorf("measure(%q) = %d, want %d", tc.word, got, tc.want)
		}
	}
}
