package functions

import (
	"strings"
	"unicode/utf8"
)

// Direct port of the Serbian algorithm bundled in PostgreSQL REL_18_6:
// snowballstem/snowball d19326ac6c1b9a417fc872f7c2f845265a5e9ece,
// algorithms/serbian.sbl (Stefan Petkovic and Dragan Ivanovic).
// See snowball_LICENSE.txt. The dictionary wrapper owns ASCII case conversion.
func serbianCyrillic(r rune) string {
	switch r {
	case 'а': return "a"
	case 'б': return "b"
	case 'в': return "v"
	case 'г': return "g"
	case 'д': return "d"
	case 'ђ': return "đ"
	case 'е': return "e"
	case 'ж': return "ž"
	case 'з': return "z"
	case 'и': return "i"
	case 'ј': return "j"
	case 'к': return "k"
	case 'л': return "l"
	case 'љ': return "lj"
	case 'м': return "m"
	case 'н': return "n"
	case 'њ': return "nj"
	case 'о': return "o"
	case 'п': return "p"
	case 'р': return "r"
	case 'с': return "s"
	case 'т': return "t"
	case 'ћ': return "ć"
	case 'у': return "u"
	case 'ф': return "f"
	case 'х': return "h"
	case 'ц': return "c"
	case 'ч': return "č"
	case 'џ': return "dž"
	case 'ш': return "š"
	}
	return ""
}

func serbianTransliterate(s string) string {
	var b strings.Builder
	start := 0
	for i, r := range s {
		if replacement := serbianCyrillic(r); replacement != "" {
			// Every source letter is two bytes; dž is the largest result.
			if b.Cap() == 0 { b.Grow(len(s) + len(s)/2) }
			b.WriteString(s[start:i])
			b.WriteString(replacement)
			start = i + utf8.RuneLen(r)
		}
	}
	if start == 0 { return s }
	b.WriteString(s[start:])
	return b.String()
}

func serbianConsonant(r rune) bool { return strings.ContainsRune("bvgdzjklmnprstfhcčćžšđ", r) }

// goto restores its leading cursor after replacement, so the trailing
// consonant can also lead the next match. Keep all three passes separate.
func serbianPreludePass(s, from, to string, surrounded bool) string {
	var b strings.Builder
	start := 0
	for i := 0; i < len(s); {
		_, width := utf8.DecodeRuneInString(s[i:])
		if strings.HasPrefix(s[i:], from) {
			end := i + len(from)
			ok := true
			if surrounded {
				before, _ := utf8.DecodeLastRuneInString(s[:i])
				after, n := utf8.DecodeRuneInString(s[end:])
				ok = i > 0 && n > 0 && serbianConsonant(before) && serbianConsonant(after)
				if ok { width = len(from) }
			} else { width = len(from) }
			if ok {
				if b.Cap() == 0 { b.Grow(len(s)) }
				b.WriteString(s[start:i])
				b.WriteString(to)
				start = end
			}
		}
		i += width
	}
	if start == 0 { return s }
	b.WriteString(s[start:])
	return b.String()
}

func serbianPrelude(s string) string {
	s = serbianPreludePass(s, "ije", "e", true)
	s = serbianPreludePass(s, "je", "e", true)
	return serbianPreludePass(s, "dj", "đ", false)
}

func serbianRegions(s string) (p1 int, noDiacritics bool) {
	p1, noDiacritics = len(s), true
	for _, r := range s { if strings.ContainsRune("čćžšđ", r) { noDiacritics = false; break } }
	cursor := 0
	for cursor < len(s) {
		r, n := utf8.DecodeRuneInString(s[cursor:]); cursor += n
		if strings.ContainsRune("aeiou", r) {
			p1 = cursor
			if p1 < 2 {
				for cursor < len(s) {
					r, n = utf8.DecodeRuneInString(s[cursor:]); cursor += n
					if !strings.ContainsRune("aeiou", r) { p1 = cursor; break }
				}
			}
			break
		}
	}
	if i := strings.IndexByte(s, 'r'); i >= 0 {
		cursor = i + 1
		if cursor < 2 {
			for cursor < len(s) {
				r, n := utf8.DecodeRuneInString(s[cursor:]); cursor += n
				if r != 'r' { break }
			}
			// gopast non-rg must succeed, including at the end of the word.
			if cursor == len(s) && strings.Trim(s[i+1:], "r") == "" { return p1, noDiacritics }
		}
		if p1-cursor > 1 { p1 = cursor }
	}
	return p1, noDiacritics
}

type serbianRule struct {
	suffix, replacement string
	noDiacritics bool
}

// At most two suffix edits succeed: Step_1 and one of Step_2/Step_3.
// Keep a retained part of the first replacement separate from the second,
// avoiding an intermediate concatenation even when a match crosses segments.
// Tables are source-sorted; binary lookup reads this virtual word in place.
type serbianWord struct { prefix, retained, tail string }
func (w serbianWord) length() int { return len(w.prefix) + len(w.retained) + len(w.tail) }
func (w serbianWord) result() string { return w.prefix + w.retained + w.tail }
func (w serbianWord) at(i int) byte {
	if i < len(w.prefix) { return w.prefix[i] }
	i -= len(w.prefix)
	if i < len(w.retained) { return w.retained[i] }
	return w.tail[i-len(w.retained)]
}
func (w serbianWord) compareSuffix(start int, s string) int {
	for i := range len(s) {
		c := w.at(start+i)
		if c < s[i] { return -1 }; if c > s[i] { return 1 }
	}
	return 0
}
func (w *serbianWord) step(rules []serbianRule, maxLength, region int, noDiacritics bool) bool {
	length := w.length()
	if maxLength > length { maxLength = length }
	for n := maxLength; n > 0; n-- {
		start := length-n
		lo, hi := 0, len(rules)
		for lo < hi {
			mid := lo + (hi-lo)/2
			// Compare different lengths lexically, not merely their prefix.
			r := rules[mid].suffix
			common := n; if len(r) < common { common = len(r) }
			cmp := w.compareSuffix(start, r[:common])
			if cmp == 0 { cmp = n-len(r) }
			if cmp > 0 { lo = mid+1 } else { hi = mid }
		}
		if lo == len(rules) { continue }
		r := rules[lo]
		if len(r.suffix) != n || w.compareSuffix(start, r.suffix) != 0 { continue }
		// Snowball chooses globally before checking R1 or its predicate.
		if start < region || r.noDiacritics && !noDiacritics { return false }
		if start <= len(w.prefix) {
			w.prefix, w.retained, w.tail = w.prefix[:start], "", r.replacement
		} else {
			w.retained, w.tail = w.tail[:start-len(w.prefix)], r.replacement
		}
		return true
	}
	return false
}

func serbianStem(s string) string {
	s = serbianPrelude(serbianTransliterate(s))
	p1, noDiacritics := serbianRegions(s)
	w := serbianWord{prefix: s}
	w.step(serbianStep1[:], serbianStep1Max, 0, noDiacritics)
	if !w.step(serbianStep2[:], serbianStep2Max, p1, noDiacritics) {
		w.step(serbianStep3[:], serbianStep3Max, p1, noDiacritics)
	}
	return w.result()
}
