package functions

import "unicode/utf8"

// Direct port of PostgreSQL REL_18_6 stem_UTF_8_french.c and Snowball
// d19326ac6c1b9a417fc872f7c2f845265a5e9ece algorithms/french.sbl.
// BSD license: see snowball_LICENSE.txt. Case, stopwords and the byte guard
// belong to SnowballDictionary, not this invocation-local kernel.
func frenchVowel(c rune) bool {
	switch c {
	case 'a', 'e', 'i', 'o', 'u', 'y', 'â', 'à', 'ë', 'é', 'ê', 'è', 'ï', 'î', 'ô', 'û', 'ù':
		return true
	}
	return false
}

// frenchSuffix returns a codepoint offset without allocating a rune slice for
// each candidate. All regions and edits refer to the one invocation buffer.
func frenchSuffix(w []rune, suffix string) int {
	end := len(w)
	for i := len(suffix); i > 0; {
		c, size := utf8.DecodeLastRuneInString(suffix[:i])
		i -= size
		end--
		if end < 0 || w[end] != c {
			return -1
		}
	}
	return end
}

// Select the longest match before testing its action's region/predecessor.
// Verb tables instead have a real RV lower bound, as in setlimit tomark pV.
func frenchLongest(w []rune, suffixes []string, lower int) (string, int) {
	best, start := "", len(w)+1
	for _, suffix := range suffixes {
		if p := frenchSuffix(w, suffix); p >= lower && p < start {
			best, start = suffix, p
		}
	}
	return best, start
}

func frenchRegion(w []rune, from int) int {
	for i := from + 1; i < len(w); i++ {
		if frenchVowel(w[i-1]) && !frenchVowel(w[i]) {
			return i + 1
		}
	}
	return len(w)
}

func frenchRegions(w []rune) (rv, r1, r2 int) {
	rv = len(w)
	if len(w) >= 3 && (frenchVowel(w[0]) && frenchVowel(w[1]) ||
		w[0] == 'p' && w[1] == 'a' && w[2] == 'r' ||
		w[0] == 'c' && w[1] == 'o' && w[2] == 'l' ||
		w[0] == 't' && w[1] == 'a' && w[2] == 'p') {
		rv = 3
	} else {
		for i := 1; i < len(w); i++ {
			if frenchVowel(w[i]) {
				rv = i + 1
				break
			}
		}
	}
	r1 = frenchRegion(w, 0)
	r2 = frenchRegion(w, r1)
	return
}

// Marking observes the still-unexpanded next input vowel (a i ë protects i).
// The preceding output character observes earlier markings and He/Hi expansion
// (ë i a protects i). This is the released prelude's left-to-right goto order.
func frenchPrelude(w []rune, word string) []rune {
	for at := 0; at < len(word); {
		c, size := utf8.DecodeRuneInString(word[at:])
		at += size
		var next, previous rune
		if at < len(word) {
			next, _ = utf8.DecodeRuneInString(word[at:])
		}
		if len(w) > 0 {
			previous = w[len(w)-1]
		}
		switch c {
		case 'u':
			if previous == 'q' || frenchVowel(previous) && frenchVowel(next) {
				c = 'U'
			}
		case 'i':
			if frenchVowel(previous) && frenchVowel(next) {
				c = 'I'
			}
		case 'y':
			if frenchVowel(previous) || frenchVowel(next) {
				c = 'Y'
			}
		case 'ë':
			w = append(w, 'H', 'e')
			continue
		case 'ï':
			w = append(w, 'H', 'i')
			continue
		}
		w = append(w, c)
	}
	return w
}

func frenchPostlude(w []rune) []rune {
	n := 0
	for i := 0; i < len(w); i++ {
		c := w[i]
		switch c {
		case 'I':
			c = 'i'
		case 'U':
			c = 'u'
		case 'Y':
			c = 'y'
		case 'H':
			if i+1 == len(w) || w[i+1] != 'e' && w[i+1] != 'i' {
				continue
			}
			i++
			c = 'ë'
			if w[i] == 'i' {
				c = 'ï'
			}
		}
		w[n] = c
		n++
	}
	return w[:n]
}

func frenchStem(word string) string {
	// Exactly one ASCII elision, and never an empty remainder. z' and curly
	// apostrophes are deliberately not part of this released algorithm.
	if len(word) > 2 && word[1] == '\'' {
		switch word[0] {
		case 'c', 'd', 'j', 'l', 'm', 'n', 's', 't':
			word = word[2:]
		}
	} else if len(word) > 3 && word[:3] == "qu'" {
		word = word[3:]
	}
	// UTF8 byte length bounds the expanded rune count, including ë/ï -> He/Hi.
	// Common short words use stack scratch; no mutable state survives the call.
	var scratch [64]rune
	w := scratch[:0]
	if len(word) > len(scratch) {
		w = make([]rune, 0, len(word))
	}
	w = frenchPrelude(w, word)
	rv, r1, r2 := frenchRegions(w)
	w, success := frenchStandard(w, rv, r1, r2)
	if !success {
		w, success = frenchIVerb(w, rv)
	}
	if !success {
		w, success = frenchVerb(w, rv, r2)
	}
	if success {
		if n := len(w); n > 0 {
			if w[n-1] == 'Y' {
				w[n-1] = 'i'
			} else if w[n-1] == 'ç' {
				w[n-1] = 'c'
			}
		}
	} else {
		w = frenchResidual(w, rv, r2)
	}
	for _, suffix := range [...]string{"enn", "onn", "ett", "ell", "eill"} {
		if frenchSuffix(w, suffix) >= 0 {
			w = w[:len(w)-1]
			break
		}
	}
	// The last vowel must have at least one following consonant. This is not
	// general accent folding: final é and all other accented vowels survive.
	for i := len(w) - 1; i >= 0; i-- {
		if frenchVowel(w[i]) {
			if i < len(w)-1 && (w[i] == 'é' || w[i] == 'è') {
				w[i] = 'e'
			}
			break
		}
	}
	w = frenchPostlude(w)
	i := 0
	for _, c := range word {
		if i == len(w) || w[i] != c {
			return string(w)
		}
		i++
	}
	if i == len(w) {
		return word
	}
	return string(w)
}
