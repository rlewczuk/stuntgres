package functions

// Direct port of PostgreSQL REL_18_6 stem_UTF_8_german.c and Snowball
// d19326ac6c1b9a417fc872f7c2f845265a5e9ece algorithms/german.sbl.
// BSD license: see snowball_LICENSE.txt. Case and stops belong to the wrapper.
// In particular, this release has neither the later -et nor apostrophe rules.
func germanVowel(c rune) bool {
	switch c {
	case 'a', 'e', 'i', 'o', 'u', 'y', 'ä', 'ö', 'ü':
		return true
	}
	return false
}

func germanHas(w []rune, suffix string) bool {
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

func germanRegions(w []rune) (int, int) {
	next := func(start int) int {
		for i := start + 1; i < len(w); i++ {
			if germanVowel(w[i-1]) && !germanVowel(w[i]) {
				return i + 1
			}
		}
		return len(w)
	}
	natural := next(0)
	r2 := next(natural)
	return max(min(3, len(w)), natural), r2
}

func germanStem(word string) string {
	n, expansion := 0, 0
	for _, c := range word {
		n++
		if c == 'ß' {
			expansion++
		}
	}
	// The wrapper admits at most 1000 input bytes; even expanded ß fits here.
	// Keep all scratch invocation-local, and avoid heap scratch for admitted input.
	var scratch [1000]rune
	var w []rune
	if n+expansion <= len(scratch) {
		w = scratch[:n+expansion]
	} else {
		w = make([]rune, n+expansion)
	}
	i := 0
	for _, c := range word {
		w[i] = c
		i++
	}
	for i := 1; i+1 < n; i++ {
		if germanVowel(w[i-1]) && germanVowel(w[i+1]) {
			if w[i] == 'u' {
				w[i] = 'U'
			} else if w[i] == 'y' {
				w[i] = 'Y'
			}
		}
	}
	// Expand backwards once, then compact ae/oe/ue forwards. Consume qu as a
	// pair so its u cannot start a transliteration at the next cursor position.
	if expansion != 0 {
		out := len(w)
		for i := n - 1; i >= 0; i-- {
			c := w[i]
			out--
			if c == 'ß' {
				w[out] = 's'
				out--
				c = 's'
			}
			w[out] = c
		}
	}
	out := 0
	for i := 0; i < len(w); i++ {
		c := w[i]
		if i+1 < len(w) {
			if c == 'q' && w[i+1] == 'u' {
				w[out], w[out+1] = 'q', 'u'
				out += 2
				i++
				continue
			}
			if w[i+1] == 'e' {
				switch c {
				case 'a':
					c = 'ä'
				case 'o':
					c = 'ö'
				case 'u':
					c = 'ü'
				}
				if c != w[i] {
					i++
				}
			}
		}
		w[out] = c
		out++
	}
	w = w[:out]
	r1, r2 := germanRegions(w)
	// Longest match is selected before checking its region/predicate. A failed
	// condition never retries a shorter suffix (notably lns versus s).
	for _, suffix := range [...]string{"erinnen", "erin", "ern", "lns", "em", "en", "er", "es", "ln", "e", "s"} {
		if !germanHas(w, suffix) {
			continue
		}
		start := len(w) - len(suffix)
		if start >= r1 {
			switch suffix {
			case "em":
				if !germanHas(w[:start], "syst") {
					w = w[:start]
				}
			case "e", "en", "es":
				w = w[:start]
				if germanHas(w, "niss") {
					w = w[:len(w)-1]
				}
			case "s":
				if start > 0 {
					switch w[start-1] {
					case 'b', 'd', 'f', 'g', 'h', 'k', 'l', 'm', 'n', 'r', 't':
						w = w[:start]
					}
				}
			case "ln", "lns":
				w[start] = 'l'
				w = w[:start+1]
			default:
				w = w[:start]
			}
		}
		break
	}
	for _, suffix := range [...]string{"est", "en", "er", "st"} {
		if !germanHas(w, suffix) {
			continue
		}
		start := len(w) - len(suffix)
		if start >= r1 {
			if suffix != "st" {
				w = w[:start]
			} else if start >= 4 {
				switch w[start-1] {
				case 'b', 'd', 'f', 'g', 'h', 'k', 'l', 'm', 'n', 't':
					w = w[:start]
				}
			}
		}
		break
	}
	for _, suffix := range [...]string{"lich", "isch", "heit", "keit", "end", "ung", "ig", "ik"} {
		if !germanHas(w, suffix) {
			continue
		}
		start := len(w) - len(suffix)
		if start >= r2 {
			switch suffix {
			case "end", "ung":
				w = w[:start]
				if p := len(w) - 2; p >= r2 && germanHas(w, "ig") && (p == 0 || w[p-1] != 'e') {
					w = w[:p]
				}
			case "ig", "ik", "isch":
				if start == 0 || w[start-1] != 'e' {
					w = w[:start]
				}
			case "lich", "heit":
				w = w[:start]
				if p := len(w) - 2; p >= r1 && (germanHas(w, "er") || germanHas(w, "en")) {
					w = w[:p]
				}
			case "keit":
				w = w[:start]
				for _, extra := range [...]string{"lich", "ig"} {
					if germanHas(w, extra) {
						if p := len(w) - len(extra); p >= r2 {
							w = w[:p]
						}
						break
					}
				}
			}
		}
		break
	}
	for i, c := range w {
		switch c {
		case 'U', 'ü':
			w[i] = 'u'
		case 'Y':
			w[i] = 'y'
		case 'ä':
			w[i] = 'a'
		case 'ö':
			w[i] = 'o'
		}
	}
	i = 0
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
