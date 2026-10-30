package functions

import (
	"strings"
	"unicode/utf8"
)

// Estonian Snowball 1.3, Linda Freienthal (January 2019), as released in
// PostgreSQL REL_18_6, stem_UTF_8_estonian.c. The readable authority is
// snowballstem/snowball d19326ac6c1b9a417fc872f7c2f845265a5e9ece/algorithms/estonian.sbl.
// Distributed under the BSD notice in snowball_LICENSE.txt.
// Byte offsets always remain UTF-8 boundaries; R1 belongs to the original word.
func estonianStem(word string) string {
	if exception, ok := estonianException(word); ok {
		return exception
	}
	r1 := len(word)
	vowel := false
	for at, r := range word {
		if estonianVowel(r) {
			vowel = true
		} else if vowel {
			r1 = at + utf8.RuneLen(r)
			break
		}
	}

	if suffix := estonianSuffix(word, r1, "gi", "ki"); suffix != "" {
		prefix := word[:len(word)-len(suffix)]
		if estonianFour(prefix) {
			last, _ := utf8.DecodeLastRuneInString(prefix)
			if suffix == "gi" && strings.ContainsRune("cjlmnqrvwxaeiouõäöü", last) && !estonianLongVowel(prefix) || suffix == "ki" && strings.ContainsRune("kptgbdshfšzž", last) {
				word = prefix
			}
		}
	}

	verb := false
	if suffix := estonianSuffix(word, r1, "nuksin", "nuksime", "nuksid", "nuksite", "ksin", "ksid", "ksime", "ksite", "mata", "takse", "dakse", "taks", "daks", "akse", "sime", "site", "sin", "me", "da", "n", "b"); suffix != "" {
		prefix := word[:len(word)-len(suffix)]
		switch suffix {
		case "me", "da", "n", "b":
			last, _ := utf8.DecodeLastRuneInString(prefix)
			if estonianVowel(last) {
				word, verb = prefix, true
			}
		case "akse":
			word, verb = prefix+"a", true
		default:
			word, verb = prefix, true
		}
	}
	if !verb {
		// Each do in substantive resumes at the CURRENT end. A successful
		// later phase never retries an earlier one; R1 is never recomputed.
		if suffix := estonianSuffix(word, r1, "lasse", "last", "lane", "lasi", "misse", "mist", "mine", "misi", "lisse", "list", "line", "lisi"); suffix != "" {
			prefix := word[:len(word)-len(suffix)]
			switch suffix {
			case "lasse", "last", "lane", "lasi":
				word = prefix + "lase"
			case "misse", "mist", "mine", "misi":
				word = prefix + "mise"
			default:
				word = prefix + "lise"
			}
		}
		if suffix := estonianSuffix(word, r1, "sse", "st", "le", "lt", "ga", "ks", "ta", "t", "s", "l"); suffix != "" {
			prefix := word[:len(word)-len(suffix)]
			if suffix == "t" && estonianFour(prefix) || suffix != "t" && estonianCaseVowel(prefix) {
				word = prefix
			}
		}
		if suffix := estonianSuffix(word, r1, "ikkude", "ikke", "ike", "sid", "te", "de", "d"); suffix != "" {
			prefix := word[:len(word)-len(suffix)]
			switch suffix {
			case "ikkude", "ikke", "ike":
				word = prefix + "iku"
			case "sid":
				if !estonianLongVowel(prefix) {
					word = prefix
				}
			case "de", "d":
				if estonianCaseVowel(prefix) {
					word = prefix
				}
			case "te":
				if !estonianFour(prefix) {
					word = prefix + "t"
				} else if strings.HasSuffix(prefix, "mis") || strings.HasSuffix(prefix, "las") || strings.HasSuffix(prefix, "lis") {
					word = prefix + "e"
				} else if !strings.HasSuffix(prefix, "t") {
					word = prefix
				}
			}
		}
		if suffix := estonianSuffix(word, r1, "mai", "ma", "m"); suffix != "" {
			prefix := word[:len(word)-len(suffix)]
			if suffix == "ma" || estonianRV(prefix) {
				word = prefix
			}
		}
		if strings.HasSuffix(word, "i") && len(word)-1 >= r1 && estonianRV(word[:len(word)-1]) {
			word = word[:len(word)-1]
		}
		if suffix := estonianSuffix(word, r1, "nu", "tu", "du", "va"); suffix != "" {
			word = word[:len(word)-len(suffix)]
		}
	}
	last, size := utf8.DecodeLastRuneInString(word)
	if estonianVowel(last) && len(word)-size >= r1 {
		prefix := word[:len(word)-size]
		if strings.HasSuffix(prefix, "kk") || strings.HasSuffix(prefix, "pp") || strings.HasSuffix(prefix, "tt") {
			word = prefix[:len(prefix)-1] + word[len(prefix):]
		}
	}
	return word
}

// Match only inside original R1 BEFORE choosing the longest suffix. Predicates
// then inspect the unrestricted prefix; their failure never retries a shorter match.
func estonianSuffix(word string, r1 int, suffixes ...string) string {
	var longest string
	for _, suffix := range suffixes {
		if len(suffix) > len(longest) && len(word)-len(suffix) >= r1 && strings.HasSuffix(word, suffix) {
			longest = suffix
		}
	}
	return longest
}

func estonianVowel(r rune) bool {
	switch r {
	case 'a', 'e', 'i', 'o', 'u', 'õ', 'ä', 'ö', 'ü':
		return true
	}
	return false
}

func estonianRV(prefix string) bool {
	if len(prefix) == 0 {
		return false
	}
	switch prefix[len(prefix)-1] {
	case 'a', 'e', 'i', 'u', 'o':
		return true
	}
	return false
}

func estonianLongVowel(prefix string) bool {
	last, size := utf8.DecodeLastRuneInString(prefix)
	if !estonianVowel(last) {
		return false
	}
	previous, _ := utf8.DecodeLastRuneInString(prefix[:len(prefix)-size])
	return previous == last
}

func estonianCaseVowel(prefix string) bool {
	return estonianRV(prefix) || estonianLongVowel(prefix)
}

func estonianFour(prefix string) bool {
	for range 4 {
		if len(prefix) == 0 {
			return false
		}
		_, size := utf8.DecodeLastRuneInString(prefix)
		prefix = prefix[:len(prefix)-size]
	}
	return true
}
