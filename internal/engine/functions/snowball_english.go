package functions

import "strings"

// englishStem ports the English rules shipped in PostgreSQL REL_18_6, from
// Snowball d19326ac6c1b9a417fc872f7c2f845265a5e9ece algorithms/english.sbl.
// See snowball_LICENSE.txt. Scratch belongs exclusively to this invocation.
func englishStem(input string) string {
	switch input {
	case "skis":
		return "ski"
	case "skies":
		return "sky"
	case "dying":
		return "die"
	case "lying":
		return "lie"
	case "tying":
		return "tie"
	case "idly":
		return "idl"
	case "gently":
		return "gentl"
	case "ugly":
		return "ugli"
	case "early":
		return "earli"
	case "only":
		return "onli"
	case "singly":
		return "singl"
	case "sky", "news", "howe", "atlas", "cosmos", "bias", "andes":
		return input
	}
	w := englishWord{letters: []rune(input)}
	if len(w.letters) < 3 {
		return input
	}
	if w.letters[0] == '\'' {
		w.letters = w.letters[1:]
		input = input[1:]
	}
	for i, c := range w.letters {
		if c == 'y' && (i == 0 || englishVowel(w.letters[i-1])) {
			w.letters[i] = 'Y'
		}
	}
	w.p1 = w.region(0)
	for _, prefix := range []string{"gener", "commun", "arsen"} {
		if strings.HasPrefix(input, prefix) {
			w.p1 = len(prefix)
			break
		}
	}
	w.p2 = w.region(w.p1)
	w.step1a()
	switch string(w.letters) {
	case "inning", "outing", "canning", "herring", "earring", "proceed", "exceed", "succeed":
	default:
		w.step1b()
		n := len(w.letters)
		if n > 2 && (w.letters[n-1] == 'y' || w.letters[n-1] == 'Y') && !englishVowel(w.letters[n-2]) {
			w.letters[n-1] = 'i'
		}
		w.step2()
		w.step3()
		w.step4()
		w.step5()
	}
	for i, c := range w.letters {
		if c == 'Y' {
			w.letters[i] = 'y'
		}
	}
	return string(w.letters)
}

// Regions are established before suffix edits and never recomputed.
type englishWord struct {
	letters []rune
	p1, p2  int
}

func englishVowel(c rune) bool {
	return c == 'a' || c == 'e' || c == 'i' || c == 'o' || c == 'u' || c == 'y'
}

func (w *englishWord) region(start int) int {
	for i := start + 1; i < len(w.letters); i++ {
		if englishVowel(w.letters[i-1]) && !englishVowel(w.letters[i]) {
			return i + 1
		}
	}
	return len(w.letters)
}

// Tables are longest-first ASCII suffixes. Once selected, a failed condition
// never falls through to a shorter suffix. Non-ASCII letters remain codepoints.
func (w *englishWord) suffix(choices ...string) string {
	for _, s := range choices {
		start := len(w.letters) - len(s)
		if start < 0 {
			continue
		}
		match := true
		for i := range len(s) {
			if w.letters[start+i] != rune(s[i]) {
				match = false
				break
			}
		}
		if match {
			return s
		}
	}
	return ""
}

func (w *englishWord) replace(s, value string) {
	w.letters = w.letters[:len(w.letters)-len(s)]
	for i := range len(value) {
		w.letters = append(w.letters, rune(value[i]))
	}
}

func (w *englishWord) hasVowel(end int) bool {
	for _, c := range w.letters[:end] {
		if englishVowel(c) {
			return true
		}
	}
	return false
}

func (w *englishWord) short(end int) bool {
	if end == 2 {
		return englishVowel(w.letters[0]) && !englishVowel(w.letters[1])
	}
	if end < 3 {
		return false
	}
	a, b, c := w.letters[end-3], w.letters[end-2], w.letters[end-1]
	return !englishVowel(a) && englishVowel(b) && !englishVowel(c) && c != 'w' && c != 'x' && c != 'Y'
}

func (w *englishWord) step1a() {
	if s := w.suffix("'s'", "'s", "'"); s != "" {
		w.replace(s, "")
	}
	switch s := w.suffix("sses", "ied", "ies", "us", "ss", "s"); s {
	case "sses":
		w.replace(s, "ss")
	case "ied", "ies":
		if len(w.letters)-len(s) > 1 {
			w.replace(s, "i")
		} else {
			w.replace(s, "ie")
		}
	case "s":
		if len(w.letters) > 2 && w.hasVowel(len(w.letters)-2) {
			w.replace(s, "")
		}
	}
}

func (w *englishWord) step1b() {
	switch s := w.suffix("eedly", "ingly", "edly", "eed", "ing", "ed"); s {
	case "eed", "eedly":
		if len(w.letters)-len(s) >= w.p1 {
			w.replace(s, "ee")
		}
	case "ed", "edly", "ing", "ingly":
		if !w.hasVowel(len(w.letters) - len(s)) {
			return
		}
		w.replace(s, "")
		if w.suffix("at", "bl", "iz") != "" {
			w.letters = append(w.letters, 'e')
		} else if w.suffix("bb", "dd", "ff", "gg", "mm", "nn", "pp", "rr", "tt") != "" {
			// The released algorithm protects exactly a/e/o before the double.
			if !(len(w.letters) == 3 && strings.ContainsRune("aeo", w.letters[0])) {
				w.letters = w.letters[:len(w.letters)-1]
			}
		} else if len(w.letters) == w.p1 && w.short(len(w.letters)) {
			w.letters = append(w.letters, 'e')
		}
	}
}

func (w *englishWord) step2() {
	s := w.suffix("ization", "ational", "fulness", "ousness", "iveness", "tional", "biliti", "lessli", "entli", "ation", "alism", "aliti", "ousli", "iviti", "fulli", "enci", "anci", "abli", "izer", "ator", "alli", "bli", "ogi", "li")
	if s == "" || len(w.letters)-len(s) < w.p1 {
		return
	}
	replacement := ""
	switch s {
	case "tional":
		replacement = "tion"
	case "enci":
		replacement = "ence"
	case "anci":
		replacement = "ance"
	case "abli":
		replacement = "able"
	case "entli":
		replacement = "ent"
	case "izer", "ization":
		replacement = "ize"
	case "ational", "ation", "ator":
		replacement = "ate"
	case "alism", "aliti", "alli":
		replacement = "al"
	case "fulness", "fulli":
		replacement = "ful"
	case "ousli", "ousness":
		replacement = "ous"
	case "iveness", "iviti":
		replacement = "ive"
	case "biliti", "bli":
		replacement = "ble"
	case "lessli":
		replacement = "less"
	case "ogi":
		n := len(w.letters) - len(s)
		if n == 0 || w.letters[n-1] != 'l' {
			return
		}
		replacement = "og"
	case "li":
		n := len(w.letters) - len(s)
		if n == 0 || !strings.ContainsRune("cdeghkmnrt", w.letters[n-1]) {
			return
		}
	}
	w.replace(s, replacement)
}

func (w *englishWord) step3() {
	s := w.suffix("ational", "tional", "alize", "icate", "iciti", "ative", "ical", "ness", "ful")
	if s == "" || len(w.letters)-len(s) < w.p1 {
		return
	}
	replacement := ""
	switch s {
	case "ational":
		replacement = "ate"
	case "tional":
		replacement = "tion"
	case "alize":
		replacement = "al"
	case "icate", "iciti", "ical":
		replacement = "ic"
	case "ative":
		if len(w.letters)-len(s) < w.p2 {
			return
		}
	}
	w.replace(s, replacement)
}

func (w *englishWord) step4() {
	s := w.suffix("ement", "ance", "ence", "able", "ible", "ment", "ant", "ent", "ism", "ate", "iti", "ous", "ive", "ize", "ion", "al", "er", "ic")
	if s == "" || len(w.letters)-len(s) < w.p2 {
		return
	}
	n := len(w.letters) - len(s)
	if s != "ion" || n > 0 && (w.letters[n-1] == 's' || w.letters[n-1] == 't') {
		w.replace(s, "")
	}
}

func (w *englishWord) step5() {
	switch s := w.suffix("e", "l"); s {
	case "e":
		n := len(w.letters) - 1
		if n >= w.p2 || n >= w.p1 && !w.short(n) {
			w.replace(s, "")
		}
	case "l":
		n := len(w.letters) - 1
		if n >= w.p2 && n > 0 && w.letters[n-1] == 'l' {
			w.replace(s, "")
		}
	}
}
