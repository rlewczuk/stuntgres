package functions

// porterStem ports the historical Porter algorithm shipped in PostgreSQL
// REL_18_6 src/backend/snowball/libstemmer/stem_UTF_8_porter.c, from Snowball
// d19326ac6c1b9a417fc872f7c2f845265a5e9ece algorithms/porter.sbl.
// See snowball_LICENSE.txt. Only neutral englishWord buffer mechanics are shared;
// no English (Porter2) phase, exception or short-word rule participates.
func porterStem(input string) string {
	w := englishWord{letters: []rune(input)}
	marked := false
	for i, c := range w.letters {
		if c == 'y' && (i == 0 || englishVowel(w.letters[i-1])) {
			w.letters[i] = 'Y'
			marked = true
		}
	}
	w.p1 = w.region(0)
	w.p2 = w.region(w.p1)
	porterStep1a(&w)
	porterStep1b(&w)
	porterStep1c(&w)
	porterStep2(&w)
	porterStep3(&w)
	porterStep4(&w)
	porterStep5a(&w)
	porterStep5b(&w)
	if marked {
		for i, c := range w.letters {
			if c == 'Y' {
				w.letters[i] = 'y'
			}
		}
	}
	// Reuse the original immutable string when all edits were neutral.
	i := 0
	for _, c := range input {
		if i == len(w.letters) || c != w.letters[i] {
			return string(w.letters)
		}
		i++
	}
	if i == len(w.letters) {
		return input
	}
	return string(w.letters)
}

func porterShort(w *englishWord, end int) bool {
	if end < 3 {
		return false
	}
	a, b, c := w.letters[end-3], w.letters[end-2], w.letters[end-1]
	return !englishVowel(a) && englishVowel(b) && !englishVowel(c) && c != 'w' && c != 'x' && c != 'Y'
}

func porterStep1a(w *englishWord) {
	switch s := w.suffix("sses", "ies", "ss", "s"); s {
	case "sses":
		w.replace(s, "ss")
	case "ies":
		w.replace(s, "i")
	case "s":
		w.replace(s, "")
	}
}

func porterStep1b(w *englishWord) {
	switch s := w.suffix("eed", "ing", "ed"); s {
	case "eed":
		if len(w.letters)-len(s) >= w.p1 {
			w.replace(s, "ee")
		}
	case "ed", "ing":
		if !w.hasVowel(len(w.letters) - len(s)) {
			return
		}
		w.replace(s, "")
		if w.suffix("at", "bl", "iz") != "" {
			w.letters = append(w.letters, 'e')
		} else if w.suffix("bb", "dd", "ff", "gg", "mm", "nn", "pp", "rr", "tt") != "" {
			w.letters = w.letters[:len(w.letters)-1]
		} else if len(w.letters) == w.p1 && porterShort(w, len(w.letters)) {
			w.letters = append(w.letters, 'e')
		}
	}
}

func porterStep1c(w *englishWord) {
	if s := w.suffix("y", "Y"); s != "" && w.hasVowel(len(w.letters)-1) {
		w.replace(s, "i")
	}
}

func porterStep2(w *englishWord) {
	s := w.suffix("ization", "ational", "fulness", "ousness", "iveness", "tional", "biliti", "entli", "ation", "alism", "aliti", "ousli", "iviti", "enci", "anci", "abli", "izer", "ator", "alli", "eli")
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
	case "eli":
		replacement = "e"
	case "izer", "ization":
		replacement = "ize"
	case "ational", "ation", "ator":
		replacement = "ate"
	case "alli", "alism", "aliti":
		replacement = "al"
	case "fulness":
		replacement = "ful"
	case "ousli", "ousness":
		replacement = "ous"
	case "iveness", "iviti":
		replacement = "ive"
	case "biliti":
		replacement = "ble"
	}
	w.replace(s, replacement)
}

func porterStep3(w *englishWord) {
	s := w.suffix("alize", "icate", "iciti", "ative", "ical", "ness", "ful")
	if s == "" || len(w.letters)-len(s) < w.p1 {
		return
	}
	replacement := ""
	switch s {
	case "alize":
		replacement = "al"
	case "icate", "iciti", "ical":
		replacement = "ic"
	}
	w.replace(s, replacement)
}

func porterStep4(w *englishWord) {
	s := w.suffix("ement", "ance", "ence", "able", "ible", "ment", "ant", "ent", "ism", "ate", "iti", "ous", "ive", "ize", "ion", "al", "er", "ic", "ou")
	n := len(w.letters) - len(s)
	if s == "" || n < w.p2 {
		return
	}
	if s != "ion" || n > 0 && (w.letters[n-1] == 's' || w.letters[n-1] == 't') {
		w.replace(s, "")
	}
}

func porterStep5a(w *englishWord) {
	if w.suffix("e") != "" {
		n := len(w.letters) - 1
		if n >= w.p2 || n >= w.p1 && !porterShort(w, n) {
			w.replace("e", "")
		}
	}
}

func porterStep5b(w *englishWord) {
	if w.suffix("ll") != "" && len(w.letters)-1 >= w.p2 {
		w.letters = w.letters[:len(w.letters)-1]
	}
}
