package functions

import (
	"strings"
	"unicode/utf8"
)

// Direct port of PostgreSQL REL_18_6 libstemmer/stem_UTF_8_norwegian.c,
// imported from Snowball d19326ac6c1b9a417fc872f7c2f845265a5e9ece.
// https://github.com/postgres/postgres/blob/REL_18_6/src/backend/snowball/libstemmer/stem_UTF_8_norwegian.c
// See snowball_LICENSE.txt. R1 is an original UTF-8 byte boundary with a
// three-codepoint minimum. Each phase executes once, without mutable scratch.
func norwegianStem(word string) string {
	r1 := norwegianRegion(word)
	longest := ""
	for _, suffix := range [...]string{
		"a", "e", "ede", "ande", "ende", "ane", "ene", "hetene", "en", "heten",
		"ar", "er", "heter", "as", "es", "edes", "endes", "enes", "hetenes",
		"ens", "hetens", "ers", "ets", "et", "het", "ast", "s", "ert", "erte",
	} {
		if len(suffix) > len(longest) && len(word)-len(suffix) >= r1 && strings.HasSuffix(word, suffix) {
			longest = suffix
		}
	}
	switch longest {
	case "s":
		prefix := word[:len(word)-1]
		previous, size := utf8.DecodeLastRuneInString(prefix)
		allowed := strings.ContainsRune("bcdfghjlmnoprtvyz", previous)
		if previous == 'k' && len(prefix) > size {
			before, _ := utf8.DecodeLastRuneInString(prefix[:len(prefix)-size])
			allowed = !strings.ContainsRune("aeiouyæåø", before)
		}
		if allowed {
			word = prefix
		}
	case "ert", "erte":
		// Both replacements are existing prefixes: no allocation is needed.
		word = word[:len(word)-len(longest)+2]
	default:
		word = word[:len(word)-len(longest)]
	}
	if len(word)-2 >= r1 && (strings.HasSuffix(word, "dt") || strings.HasSuffix(word, "vt")) {
		word = word[:len(word)-1]
	}
	longest = ""
	for _, suffix := range [...]string{"leg", "eleg", "ig", "eig", "lig", "elig", "els", "lov", "elov", "slov", "hetslov"} {
		if len(suffix) > len(longest) && len(word)-len(suffix) >= r1 && strings.HasSuffix(word, suffix) {
			longest = suffix
		}
	}
	return word[:len(word)-len(longest)]
}

func norwegianRegion(word string) int {
	minimum, region, count := len(word), len(word), 0
	previousVowel := false
	for offset, char := range word {
		count++
		end := offset + utf8.RuneLen(char)
		if count == 3 {
			minimum = end
		}
		vowel := strings.ContainsRune("aeiouyæåø", char)
		if previousVowel && !vowel && region == len(word) {
			region = end
		}
		previousVowel = vowel
		if count >= 3 && region != len(word) {
			return max(minimum, region)
		}
	}
	return max(minimum, region)
}
