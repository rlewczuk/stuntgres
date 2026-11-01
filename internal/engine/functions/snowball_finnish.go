package functions

import (
	"strings"
	"unicode/utf8"
)

// Direct port of PostgreSQL REL_18_6 stem_UTF_8_finnish.c, from Snowball
// d19326ac6c1b9a417fc872f7c2f845265a5e9ece algorithms/finnish.sbl.
// BSD license: see snowball_LICENSE.txt. All indices are codepoints; regions
// remain fixed through edits. Case conversion and stopwords belong to the wrapper.
func finnishVowel(c rune) bool     { return strings.ContainsRune("aeiouyäö", c) }
func finnishConsonant(c rune) bool { return strings.ContainsRune("bcdfghjklmnpqrstvwxz", c) }

func finnishRegions(w []rune) (int, int) {
	r1, r2 := len(w), len(w)
	for i := 1; i < len(w); i++ {
		if finnishVowel(w[i-1]) && !finnishVowel(w[i]) {
			r1 = i + 1
			break
		}
	}
	for i := r1 + 1; i < len(w); i++ {
		if finnishVowel(w[i-1]) && !finnishVowel(w[i]) {
			r2 = i + 1
			break
		}
	}
	return r1, r2
}

// Match without allocating a rune slice for each suffix. The backward limit
// applies to selection, not the postselection predecessor tests.
func finnishSuffix(w []rune, suffix string, limit int) int {
	i := len(w)
	for j := len(suffix); j > 0; {
		c, size := utf8.DecodeLastRuneInString(suffix[:j])
		j -= size
		i--
		if i < limit || w[i] != c {
			return -1
		}
	}
	return i
}

func finnishAmong(w []rune, limit int, suffixes ...string) (int, string) {
	start, selected := len(w), ""
	for _, suffix := range suffixes {
		if i := finnishSuffix(w, suffix, limit); i >= 0 && i < start {
			start, selected = i, suffix
		}
	}
	return start, selected
}

func finnishLong(w []rune, limit int) bool {
	n := len(w)
	return n-2 >= limit && w[n-1] == w[n-2] && strings.ContainsRune("aeiouäö", w[n-1])
}

func finnishParticle(w []rune, r1, r2 int) []rune {
	i, suffix := finnishAmong(w, r1, "kin", "kaan", "kään", "ko", "kö", "han", "hän", "pa", "pä", "sti")
	if suffix == "sti" {
		if i >= r2 {
			return w[:i]
		}
	} else if suffix != "" && i > 0 && (finnishVowel(w[i-1]) || w[i-1] == 'n' || w[i-1] == 't') {
		return w[:i]
	}
	return w
}

func finnishPossessive(w []rune, r1 int) []rune {
	i, suffix := finnishAmong(w, r1, "si", "ni", "nsa", "nsä", "mme", "nne", "an", "än", "en")
	switch suffix {
	case "":
		return w
	case "si":
		if i > 0 && w[i-1] == 'k' {
			return w
		}
	case "ni":
		w = w[:i]
		if finnishSuffix(w, "kse", 0) >= 0 {
			w[len(w)-1] = 'i'
		}
		return w
	case "an":
		_, s := finnishAmong(w[:i], 0, "ta", "ssa", "sta", "lla", "lta", "na")
		if s == "" {
			return w
		}
	case "än":
		_, s := finnishAmong(w[:i], 0, "tä", "ssä", "stä", "llä", "ltä", "nä")
		if s == "" {
			return w
		}
	case "en":
		_, s := finnishAmong(w[:i], 0, "lle", "ine")
		if s == "" {
			return w
		}
	}
	return w[:i]
}

func finnishCase(w []rune, r1 int) ([]rune, bool) {
	i, suffix := finnishAmong(w, r1, "han", "hen", "hin", "hon", "hän", "hön", "den", "tten", "siin", "seen", "a", "ä", "tta", "ttä", "ta", "tä", "ssa", "ssä", "sta", "stä", "lla", "llä", "lta", "ltä", "lle", "na", "nä", "ksi", "ine", "n")
	// Only these four among predicates can fall back to n. They execute
	// while the backward limit is R1; subsequent action predicates do not.
	switch suffix {
	case "den", "tten", "siin":
		if i-2 < r1 || w[i-1] != 'i' || !strings.ContainsRune("aeiouäö", w[i-2]) {
			i, suffix = len(w)-1, "n"
		}
	case "seen":
		if !finnishLong(w[:i], r1) {
			i, suffix = len(w)-1, "n"
		}
	}
	switch suffix {
	case "":
		return w, false
	case "han", "hen", "hin", "hon", "hän", "hön":
		v, _ := utf8.DecodeRuneInString(suffix[1:])
		if i == 0 || w[i-1] != v {
			return w, false
		}
	case "tta", "ttä":
		if i == 0 || w[i-1] != 'e' {
			return w, false
		}
	case "a", "ä":
		if i < 2 || !finnishVowel(w[i-1]) || !finnishConsonant(w[i-2]) {
			return w, false
		}
	case "n":
		if finnishLong(w[:i], 0) || finnishSuffix(w[:i], "ie", 0) >= 0 {
			i--
		}
	}
	return w[:i], true
}

func finnishOther(w []rune, r2 int) []rune {
	i, suffix := finnishAmong(w, r2, "mpi", "mpa", "mpä", "mmi", "mma", "mmä", "impi", "impa", "impä", "immi", "imma", "immä", "eja", "ejä")
	switch suffix {
	case "":
		return w
	case "mpi", "mpa", "mpä", "mmi", "mma", "mmä":
		if finnishSuffix(w[:i], "po", 0) >= 0 {
			return w
		}
	}
	return w[:i]
}

func finnishPlural(w []rune, r1, r2 int, endingRemoved bool) []rune {
	n := len(w)
	if endingRemoved {
		if n > r1 && (w[n-1] == 'i' || w[n-1] == 'j') {
			return w[:n-1]
		}
		return w
	}
	// Unlike particle/case action tests, this predecessor stays inside R1.
	if n-2 < r1 || w[n-1] != 't' || !finnishVowel(w[n-2]) {
		return w
	}
	w = w[:n-1]
	i, suffix := finnishAmong(w, r2, "mma", "imma")
	if suffix == "" || (suffix == "mma" && finnishSuffix(w[:i], "po", 0) >= 0) {
		return w
	}
	return w[:i]
}

func finnishTidy(w []rune, r1 int) []rune {
	if len(w) < r1 {
		return w
	}
	if finnishLong(w, r1) {
		w = w[:len(w)-1]
	}
	n := len(w)
	if n-2 >= r1 && strings.ContainsRune("aäei", w[n-1]) && finnishConsonant(w[n-2]) {
		w = w[:n-1]
	}
	n = len(w)
	if n-2 >= r1 && w[n-1] == 'j' && (w[n-2] == 'o' || w[n-2] == 'u') {
		w = w[:n-1]
	}
	n = len(w)
	if n-2 >= r1 && w[n-1] == 'o' && w[n-2] == 'j' {
		w = w[:n-1]
	}
	// Undoubling is outside the R1 limit, and may edit before trailing vowels.
	i := len(w) - 1
	for i >= 0 && finnishVowel(w[i]) {
		i--
	}
	if i > 0 && finnishConsonant(w[i]) && w[i] == w[i-1] {
		copy(w[i:], w[i+1:])
		w = w[:len(w)-1]
	}
	return w
}

func finnishStem(input string) string {
	w := []rune(input)
	originalLength := len(w)
	r1, r2 := finnishRegions(w)
	w = finnishParticle(w, r1, r2)
	w = finnishPossessive(w, r1)
	w, removed := finnishCase(w, r1)
	w = finnishOther(w, r2)
	w = finnishPlural(w, r1, r2, removed)
	w = finnishTidy(w, r1)
	if len(w) == originalLength {
		// Only kse -> ksi can replace without shortening, and it follows ni
		// deletion; unchanged length therefore means unchanged input.
		return input
	}
	return string(w)
}
