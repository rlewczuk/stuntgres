package functions

import (
	"strings"
	"unicode/utf8"
)

// Direct port of PostgreSQL REL_18_6 stem_UTF_8_indonesian.c (Snowball 2.2.0),
// from snowballstem/snowball d19326ac6c1b9a417fc872f7c2f845265a5e9ece,
// algorithms/indonesian.sbl. See snowball_LICENSE.txt.
// The measure is adjusted once per operation, never recounted. All state is
// invocation-local; deletions retain substrings, only replacements allocate.
func indonesianStem(word string) string {
	measure := 0
	for i := range len(word) {
		if indonesianVowel(word[i]) {
			measure++
		}
	}
	if measure <= 2 {
		return word
	}
	for _, suffix := range [...]string{"kah", "lah", "pun"} {
		if strings.HasSuffix(word, suffix) {
			word = word[:len(word)-len(suffix)]
			measure--
			break
		}
	}
	if measure <= 2 {
		return word
	}
	for _, suffix := range [...]string{"ku", "mu", "nya"} {
		if strings.HasSuffix(word, suffix) {
			word = word[:len(word)-len(suffix)]
			measure--
			break
		}
	}
	if measure <= 2 {
		return word
	}
	prefix := 0
	var first bool
	word, prefix, first = indonesianFirstPrefix(word)
	if first {
		measure--
		if measure > 2 {
			var removed bool
			word, removed = indonesianSuffix(word, prefix)
			if removed {
				measure--
				if measure > 2 {
					word, _, _ = indonesianSecondPrefix(word)
				}
			}
		}
	} else {
		var removed bool
		word, prefix, removed = indonesianSecondPrefix(word)
		if removed {
			measure--
		}
		if measure > 2 {
			word, _ = indonesianSuffix(word, prefix)
		}
	}
	return word
}

func indonesianVowel(c byte) bool {
	return c == 'a' || c == 'e' || c == 'i' || c == 'o' || c == 'u'
}

func indonesianFirstPrefix(word string) (string, int, bool) {
	// Longest among matches first. A failed meny/peny vowel predicate falls
	// back to men/pen; mem also falls back to me in the released among table.
	for _, rule := range [...]struct {
		text  string
		class int
	}{
		{"meng", 1}, {"meny", 1}, {"peng", 3}, {"peny", 3},
		{"mem", 1}, {"men", 1}, {"pem", 3}, {"pen", 3},
		{"ter", 1}, {"di", 1}, {"ke", 3}, {"me", 1},
	} {
		if !strings.HasPrefix(word, rule.text) {
			continue
		}
		rest := word[len(rule.text):]
		vowel := len(rest) != 0 && indonesianVowel(rest[0])
		switch rule.text {
		case "meny", "peny":
			if !vowel {
				continue
			}
			return "s" + rest, rule.class, true
		case "mem", "pem":
			if vowel {
				return "p" + rest, rule.class, true
			}
		}
		return rest, rule.class, true
	}
	return word, 0, false
}

func indonesianSecondPrefix(word string) (string, int, bool) {
	switch {
	case strings.HasPrefix(word, "belajar"):
		return word[3:], 4, true
	case strings.HasPrefix(word, "pelajar"):
		return word[3:], 0, true
	case strings.HasPrefix(word, "ber"):
		return word[3:], 4, true
	case strings.HasPrefix(word, "per"):
		return word[3:], 2, true
	case strings.HasPrefix(word, "pe"):
		return word[2:], 2, true
	case strings.HasPrefix(word, "be") && len(word) > 2:
		// KER consumes one UTF8 nonvowel, not one byte, followed by er.
		_, width := utf8.DecodeRuneInString(word[2:])
		if !indonesianVowel(word[2]) && strings.HasPrefix(word[2+width:], "er") {
			return word[2:], 4, true
		}
	}
	return word, 0, false
}

func indonesianSuffix(word string, prefix int) (string, bool) {
	if strings.HasSuffix(word, "kan") && prefix != 3 && prefix != 2 {
		return word[:len(word)-3], true
	}
	// The kan among entry falls back to an when its predicate fails.
	if strings.HasSuffix(word, "an") && prefix != 1 {
		return word[:len(word)-2], true
	}
	if strings.HasSuffix(word, "i") && prefix <= 2 && !strings.HasSuffix(word, "si") {
		return word[:len(word)-1], true
	}
	return word, false
}
