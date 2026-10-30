package functions

// Direct port of PostgreSQL REL_18_6 stem_UTF_8_dutch.c and Snowball
// d19326ac6c1b9a417fc872f7c2f845265a5e9ece algorithms/dutch.sbl.
// BSD license: see snowball_LICENSE.txt. Case and stopwords belong to the wrapper.
func dutchVowel(c rune) bool {
	switch c {
	case 'a', 'e', 'i', 'o', 'u', 'y', 'è':
		return true
	}
	return false
}

func dutchPrelude(w []rune) {
	for i, c := range w {
		switch c {
		case 'á', 'ä':
			w[i] = 'a'
		case 'é', 'ë':
			w[i] = 'e'
		case 'í', 'ï':
			w[i] = 'i'
		case 'ó', 'ö':
			w[i] = 'o'
		case 'ú', 'ü':
			w[i] = 'u'
		}
	}
	if len(w) > 0 && w[0] == 'y' {
		w[0] = 'Y'
	}
	for i := 1; i < len(w); i++ {
		if dutchVowel(w[i-1]) {
			if w[i] == 'y' {
				w[i] = 'Y'
			} else if w[i] == 'i' && i+1 < len(w) && dutchVowel(w[i+1]) {
				w[i] = 'I'
			}
		}
	}
}

func dutchRegions(w []rune) (int, int) {
	next := func(start int) int {
		for i := start + 1; i < len(w); i++ {
			if dutchVowel(w[i-1]) && !dutchVowel(w[i]) {
				return i + 1
			}
		}
		return len(w)
	}
	natural := next(0)
	r2 := next(natural)
	return max(min(3, len(w)), natural), r2
}

// Suffixes here are ASCII; indices and fixed regions are codepoints.
func dutchHas(w []rune, suffix string) bool {
	if len(w) < len(suffix) {
		return false
	}
	start := len(w) - len(suffix)
	for i := range len(suffix) {
		if w[start+i] != rune(suffix[i]) {
			return false
		}
	}
	return true
}

func dutchUndouble(w []rune) []rune {
	n := len(w)
	if n >= 2 && w[n-1] == w[n-2] && (w[n-1] == 'd' || w[n-1] == 'k' || w[n-1] == 't') {
		return w[:n-1]
	}
	return w
}

func dutchEn(w []rune, start, r1 int) []rune {
	if start >= r1 && start > 0 && !dutchVowel(w[start-1]) && !dutchHas(w[:start], "gem") {
		return dutchUndouble(w[:start])
	}
	return w
}

func dutchE(w []rune, r1 int) ([]rune, bool) {
	start := len(w) - 1
	if start >= r1 && start > 0 && w[start] == 'e' && !dutchVowel(w[start-1]) {
		return dutchUndouble(w[:start]), true
	}
	return w, false
}

func dutchStem(word string) string {
	w := []rune(word)
	dutchPrelude(w)
	r1, r2 := dutchRegions(w)
	for _, suffix := range [...]string{"heden", "ene", "en", "se", "s"} {
		if !dutchHas(w, suffix) {
			continue
		}
		start := len(w) - len(suffix)
		switch suffix {
		case "heden":
			if start >= r1 {
				w[start], w[start+1], w[start+2], w[start+3] = 'h', 'e', 'i', 'd'
				w = w[:start+4]
			}
		case "ene", "en":
			w = dutchEn(w, start, r1)
		case "se", "s":
			if start >= r1 && start > 0 && !dutchVowel(w[start-1]) && w[start-1] != 'j' {
				w = w[:start]
			}
		}
		break
	}
	w, eFound := dutchE(w, r1)
	if start := len(w) - 4; start >= r2 && dutchHas(w, "heid") && start > 0 && w[start-1] != 'c' {
		w = w[:start]
		if dutchHas(w, "en") {
			w = dutchEn(w, len(w)-2, r1)
		}
	}
	for _, suffix := range [...]string{"end", "ing", "lijk", "baar", "bar", "ig"} {
		if !dutchHas(w, suffix) {
			continue
		}
		start := len(w) - len(suffix)
		if start >= r2 {
			switch suffix {
			case "end", "ing":
				w = w[:start]
				if p := len(w) - 2; p >= r2 && dutchHas(w, "ig") && p > 0 && w[p-1] != 'e' {
					w = w[:p]
				} else {
					w = dutchUndouble(w)
				}
			case "ig":
				if start > 0 && w[start-1] != 'e' {
					w = w[:start]
				}
			case "lijk":
				w, _ = dutchE(w[:start], r1)
			case "baar":
				w = w[:start]
			case "bar":
				if eFound {
					w = w[:start]
				}
			}
		}
		break
	}
	n := len(w)
	if n >= 4 && !dutchVowel(w[n-1]) && w[n-1] != 'I' && !dutchVowel(w[n-4]) && w[n-3] == w[n-2] {
		switch w[n-2] {
		case 'a', 'e', 'o', 'u':
			w[n-2] = w[n-1]
			w = w[:n-1]
		}
	}
	for i, c := range w {
		if c == 'Y' {
			w[i] = 'y'
		} else if c == 'I' {
			w[i] = 'i'
		}
	}
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
