package functions

import (
	"strings"
	"unicode/utf8"
)

// Direct port of PostgreSQL REL_18_6 libstemmer/stem_UTF_8_danish.c
// (Snowball 2.2.0, commit d19326ac6c1b9a417fc872f7c2f845265a5e9ece).
// See snowball_LICENSE.txt. Regions are original UTF-8 byte boundaries; the
// three-character minimum counts codepoints. All edits shorten the original
// string, so invocations need no mutable buffer or shared cursor.
func danishStem(word string) string {
	r1 := danishRegion(word)
	longest := ""
	for _, suffix := range [...]string{
		"hed", "ethed", "ered", "e", "erede", "ende", "erende", "ene",
		"erne", "ere", "en", "heden", "eren", "er", "heder", "erer",
		"s", "heds", "es", "endes", "erendes", "enes", "ernes", "eres",
		"ens", "hedens", "erens", "ers", "ets", "erets", "et", "eret",
	} {
		if len(suffix) > len(longest) && len(word)-len(suffix) >= r1 && strings.HasSuffix(word, suffix) {
			longest = suffix
		}
	}
	if longest == "s" {
		previous, _ := utf8.DecodeLastRuneInString(word[:len(word)-1])
		if strings.ContainsRune("abcdfghjklmnoprtvyzå", previous) {
			word = word[:len(word)-1]
		}
	} else {
		word = word[:len(word)-len(longest)]
	}
	word = danishConsonantPair(word, r1)
	word = danishOtherSuffix(word, r1)
	if len(word) >= 2 && len(word)-1 >= r1 {
		last := word[len(word)-1]
		if last == word[len(word)-2] && strings.IndexByte("bcdfghjklmnpqrstvwxz", last) >= 0 {
			word = word[:len(word)-1]
		}
	}
	return word
}

func danishRegion(word string) int {
	minimum, count, region := len(word), 0, len(word)
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

func danishConsonantPair(word string, r1 int) string {
	if len(word)-2 >= r1 {
		switch word[len(word)-2:] {
		case "gd", "dt", "gt", "kt":
			return word[:len(word)-1]
		}
	}
	return word
}

func danishOtherSuffix(word string, r1 int) string {
	// This preliminary deletion is deliberately not restricted to R1.
	if strings.HasSuffix(word, "igst") {
		word = word[:len(word)-2]
	}
	for _, suffix := range [...]string{"elig", "lig", "els", "ig", "løst"} {
		if len(word)-len(suffix) >= r1 && strings.HasSuffix(word, suffix) {
			if suffix == "løst" {
				return word[:len(word)-1]
			}
			return danishConsonantPair(word[:len(word)-len(suffix)], r1)
		}
	}
	return word
}
