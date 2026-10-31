package functions

import (
	"strings"
	"unicode/utf8"
)

// Direct port of PostgreSQL REL_18_6 libstemmer/stem_UTF_8_swedish.c,
// imported from Snowball d19326ac6c1b9a417fc872f7c2f845265a5e9ece.
// https://github.com/postgres/postgres/blob/REL_18_6/src/backend/snowball/libstemmer/stem_UTF_8_swedish.c
// See snowball_LICENSE.txt. R1 retains its original UTF-8 boundary; every
// replacement is a prefix of the input, requiring no mutable scratch or copy.
func swedishStem(word string) string {
	r1 := swedishRegion(word)
	longest := ""
	for _, suffix := range [...]string{
		"a", "arna", "erna", "heterna", "orna", "ad", "e", "ade", "ande", "arne", "are", "aste",
		"en", "anden", "aren", "heten", "ern", "ar", "er", "heter", "or", "as", "arnas", "ernas",
		"ornas", "es", "ades", "andes", "ens", "arens", "hetens", "erns", "at", "andet", "het", "ast", "s",
	} {
		if len(suffix) > len(longest) && len(word)-len(suffix) >= r1 && strings.HasSuffix(word, suffix) {
			longest = suffix
		}
	}
	if longest == "s" {
		prefix := word[:len(word)-1]
		previous, _ := utf8.DecodeLastRuneInString(prefix)
		if strings.ContainsRune("bcdfghjklmnoprtvy", previous) {
			word = prefix
		}
	} else {
		word = word[:len(word)-len(longest)]
	}
	if len(word)-2 >= r1 {
		for _, pair := range [...]string{"dd", "gd", "nn", "dt", "gt", "kt", "tt"} {
			if strings.HasSuffix(word, pair) {
				word = word[:len(word)-1]
				break
			}
		}
	}
	longest = ""
	for _, suffix := range [...]string{"lig", "ig", "els", "öst", "fullt"} {
		if len(suffix) > len(longest) && len(word)-len(suffix) >= r1 && strings.HasSuffix(word, suffix) {
			longest = suffix
		}
	}
	switch longest {
	case "öst":
		previous, _ := utf8.DecodeLastRuneInString(word[:len(word)-len(longest)])
		if strings.ContainsRune("iklnprtuv", previous) {
			word = word[:len(word)-1]
		}
	case "fullt":
		word = word[:len(word)-1]
	default:
		word = word[:len(word)-len(longest)]
	}
	return word
}

func swedishRegion(word string) int {
	minimum, region, count := len(word), len(word), 0
	previousVowel := false
	for offset, char := range word {
		count++
		end := offset + utf8.RuneLen(char)
		if count == 3 {
			minimum = end
		}
		vowel := strings.ContainsRune("aeiouyäåö", char)
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
