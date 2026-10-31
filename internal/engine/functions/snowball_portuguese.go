package functions

import (
	"strings"
	"unicode/utf8"
)

// Direct port of Snowball 2.2.0 as released by PostgreSQL REL_18_6:
// https://github.com/postgres/postgres/blob/REL_18_6/src/backend/snowball/libstemmer/stem_UTF_8_portuguese.c
// See snowball_LICENSE.txt. The dictionary wrapper owns case conversion.
func portugueseVowel(r rune) bool { return strings.ContainsRune("aeiouáéíóúâêô", r) }

// Both marker substitutions preserve byte widths, so region offsets remain
// valid. Ordinary words retain their backing; scratch is allocated on change.
func portuguesePrelude(s string) string {
	var changed []byte
	for i, r := range s {
		if r != 'ã' && r != 'õ' {
			continue
		}
		if changed == nil {
			changed = []byte(s)
		}
		if r == 'ã' {
			copy(changed[i:], "a~")
		} else {
			copy(changed[i:], "o~")
		}
	}
	if changed == nil {
		return s
	}
	return string(changed)
}

func portugueseRegion(s string, start int) int {
	vowel := false
	for i := start; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		i += n
		if vowel && !portugueseVowel(r) {
			return i
		}
		vowel = portugueseVowel(r)
	}
	return len(s)
}

func portugueseRV(s string) int {
	first, n := utf8.DecodeRuneInString(s)
	if n == 0 || n == len(s) {
		return len(s)
	}
	second, m := utf8.DecodeRuneInString(s[n:])
	i := n + m
	if !portugueseVowel(first) && portugueseVowel(second) {
		if i < len(s) {
			_, width := utf8.DecodeRuneInString(s[i:])
			return i + width
		}
		return len(s)
	}
	seekVowel := !portugueseVowel(second)
	for i < len(s) {
		r, width := utf8.DecodeRuneInString(s[i:])
		i += width
		if portugueseVowel(r) == seekVowel {
			return i
		}
	}
	return len(s)
}

// Keep replacements separate from the retained input prefix until publication.
// All regions refer to the prelude word, never to a newly computed region.
type portugueseWord struct{ prefix, tail string }

func (w portugueseWord) length() int { return len(w.prefix) + len(w.tail) }
func (w portugueseWord) hasSuffix(s string) bool {
	if len(s) <= len(w.tail) {
		return strings.HasSuffix(w.tail, s)
	}
	n := len(s) - len(w.tail)
	return strings.HasSuffix(w.prefix, s[:n]) && s[n:] == w.tail
}
func (w *portugueseWord) truncate(n int) {
	if n <= len(w.prefix) {
		w.prefix, w.tail = w.prefix[:n], ""
	} else {
		w.tail = w.tail[:n-len(w.prefix)]
	}
}
func (w portugueseWord) suffix(endings string, region int) string {
	best := ""
	for s := range strings.FieldsSeq(endings) {
		if len(s) > len(best) && w.length()-len(s) >= region && w.hasSuffix(s) {
			best = s
		}
	}
	return best
}
func (w *portugueseWord) remove(endings string, region int) bool {
	// Standard/residual among chooses globally before testing the region.
	s := w.suffix(endings, 0)
	if s == "" || w.length()-len(s) < region {
		return false
	}
	w.truncate(w.length() - len(s))
	return true
}

// Released a_5 (45 entries) and a_6 (120 entries), in prelude spelling.
const portugueseStandard = "ica ância ência logia ira adora osa ista iva eza idade ante mente amente ável ível ico ismo oso amento imento ivo aça~o uça~o ador icas ências logias iras adoras osas istas ivas ezas idades adores antes aço~es uço~es icos ismos osos amentos imentos ivos"
const portugueseVerbs = "ada ida ia aria eria iria ara era ira ava asse esse isse aste este iste ei arei erei irei am iam ariam eriam iriam aram eram iram avam em arem erem irem assem essem issem ado ido ando endo indo ara~o era~o ira~o ar er ir as adas idas ias arias erias irias aras eras iras avas es ardes erdes irdes ares eres ires asses esses isses astes estes istes is ais eis areis ereis ireis áreis éreis íreis ásseis ésseis ísseis áveis íeis aríeis eríeis iríeis ados idos amos áramos éramos íramos ávamos íamos aríamos eríamos iríamos emos aremos eremos iremos ássemos êssemos íssemos imos armos ermos irmos ámos arás erás irás eu iu ou ará erá irá"

func (w *portugueseWord) standard(rv, r1, r2 int) bool {
	s := w.suffix(portugueseStandard, 0)
	if s == "" {
		return false
	}
	start, region := w.length()-len(s), r2
	switch s {
	case "amente":
		region = r1
	case "ira", "iras":
		region = rv
		if start == 0 || w.prefix[start-1] != 'e' {
			return false
		}
	}
	if start < region {
		return false
	}
	w.truncate(start)
	switch s {
	case "logia", "logias":
		w.tail = "log"
	case "uça~o", "uço~es":
		w.tail = "u"
	case "ência", "ências":
		w.tail = "ente"
	case "amente":
		secondary := w.suffix("ic ad os iv", 0)
		if secondary != "" && w.remove(secondary, r2) && secondary == "iv" {
			w.remove("at", r2)
		}
	case "mente":
		w.remove("ante avel ível", r2)
	case "idade", "idades":
		w.remove("ic abil iv", r2)
	case "iva", "ivo", "ivas", "ivos":
		w.remove("at", r2)
	case "ira", "iras":
		w.tail = "ir"
	}
	return true
}

func portugueseStem(s string) string {
	s = portuguesePrelude(s)
	rv, r1 := portugueseRV(s), portugueseRegion(s, 0)
	r2 := portugueseRegion(s, r1)
	w := portugueseWord{prefix: s}
	success := w.standard(rv, r1, r2)
	if !success {
		// Verb among is restricted to RV before choosing the longest match.
		if suffix := w.suffix(portugueseVerbs, rv); suffix != "" {
			w.truncate(w.length() - len(suffix))
			success = true
		}
	}
	if success {
		if w.hasSuffix("ci") {
			w.remove("i", rv)
		}
	} else {
		w.remove("os a i o á í ó", rv)
	}
	if w.remove("e é ê", rv) {
		if w.hasSuffix("gu") {
			w.remove("u", rv)
		} else if w.hasSuffix("ci") {
			w.remove("i", rv)
		}
	} else if w.hasSuffix("ç") {
		w.truncate(w.length() - len("ç"))
		w.tail = "c"
	}
	// Postlude also interprets literal input marker pairs. Avoid materializing
	// the intermediate concatenation when restoring nasal vowels.
	if !strings.Contains(w.prefix, "a~") && !strings.Contains(w.prefix, "o~") {
		return w.prefix + w.tail
	}
	var post strings.Builder
	post.Grow(w.length())
	for i := 0; i < len(w.prefix); i++ {
		c := w.prefix[i]
		if (c == 'a' || c == 'o') && i+1 < len(w.prefix) && w.prefix[i+1] == '~' {
			if c == 'a' {
				post.WriteString("ã")
			} else {
				post.WriteString("õ")
			}
			i++
		} else {
			post.WriteByte(c)
		}
	}
	post.WriteString(w.tail)
	return post.String()
}
