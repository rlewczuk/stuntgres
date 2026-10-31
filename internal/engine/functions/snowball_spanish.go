package functions

import (
	"strings"
	"unicode/utf8"
)

// Direct port of the Snowball 2.2.0 Spanish kernel in PostgreSQL REL_18_6:
// https://github.com/postgres/postgres/blob/REL_18_6/src/backend/snowball/libstemmer/stem_UTF_8_spanish.c
// Matching program: snowball d19326ac6c1b9a417fc872f7c2f845265a5e9ece/algorithms/spanish.sbl.
// See snowball_LICENSE.txt. Case conversion belongs to the dictionary wrapper.
func spanishVowel(r rune) bool { return strings.ContainsRune("aeiouáéíóúü", r) }

func spanishRegion(s string, start int) int {
	vowel := false
	for i := start; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		i += n
		if vowel && !spanishVowel(r) {
			return i
		}
		vowel = spanishVowel(r)
	}
	return len(s)
}

func spanishRV(s string) int {
	first, n := utf8.DecodeRuneInString(s)
	if n == 0 || n == len(s) {
		return len(s)
	}
	second, m := utf8.DecodeRuneInString(s[n:])
	i := n + m
	if !spanishVowel(first) && spanishVowel(second) {
		if i < len(s) {
			_, width := utf8.DecodeRuneInString(s[i:])
			return i + width
		}
		return len(s)
	}
	seekVowel := !spanishVowel(second)
	for i < len(s) {
		r, width := utf8.DecodeRuneInString(s[i:])
		i += width
		if spanishVowel(r) == seekVowel {
			return i
		}
	}
	return len(s)
}

// Replacements retain the input prefix and a literal tail, avoiding intermediate
// strings. Region byte offsets always refer to the original input word.
type spanishWord struct{ prefix, tail string }

func (w spanishWord) length() int { return len(w.prefix) + len(w.tail) }
func (w spanishWord) hasSuffix(s string) bool {
	if len(s) <= len(w.tail) {
		return strings.HasSuffix(w.tail, s)
	}
	n := len(s) - len(w.tail)
	return strings.HasSuffix(w.prefix, s[:n]) && s[n:] == w.tail
}
func (w *spanishWord) truncate(n int) {
	if n <= len(w.prefix) {
		w.prefix, w.tail = w.prefix[:n], ""
	} else {
		w.tail = w.tail[:n-len(w.prefix)]
	}
}
func (w spanishWord) suffix(endings string, region int) string {
	best := ""
	for s := range strings.FieldsSeq(endings) {
		if len(s) > len(best) && w.length()-len(s) >= region && w.hasSuffix(s) {
			best = s
		}
	}
	return best
}
func (w *spanishWord) remove(endings string, region int) bool {
	s := w.suffix(endings, 0)
	if s == "" || w.length()-len(s) < region {
		return false
	}
	w.truncate(w.length() - len(s))
	return true
}

// Released tables a_6 (46), a_8 (96), a_7 (12). Unaccented acion/ucion
// are deliberately absent: those rules were introduced after this release.
const spanishStandard = "anza anzas ico ica icos icas ismo ismos able ables ible ibles ista istas oso osa osos osas amiento amientos imiento imientos adora ador ación adoras adores aciones ante antes ancia ancias logía logías ución uciones encia encias amente mente idad idades iva ivo ivas ivos"
const spanishVerbs = "en es éis emos arían arías arán arás aríais aría aréis aríamos aremos ará aré erían erías erán erás eríais ería eréis eríamos eremos erá eré irían irías irán irás iríais iría iréis iríamos iremos irá iré aba ada ida ía ara iera ad ed id ase iese aste iste an aban ían aran ieran asen iesen aron ieron ado ido ando iendo ió ar er ir as abas adas idas ías aras ieras ases ieses ís áis abais íais arais ierais aseis ieseis asteis isteis ados idos amos ábamos íamos imos áramos iéramos iésemos ásemos"
const spanishYVerbs = "ya ye yan yen yeron yendo yo yó yas yes yais yamos"

func (w *spanishWord) pronoun(rv int) bool {
	pronoun := w.suffix("me se sela selo selas selos la le lo las les los nos", 0)
	if pronoun == "" {
		return false
	}
	verb := spanishWord{prefix: w.prefix[:len(w.prefix)-len(pronoun)]}
	ending := verb.suffix("iéndo ándo ár ér ír ando iendo ar er ir yendo", 0)
	start := verb.length() - len(ending)
	if ending == "" || start < rv {
		return false
	}
	if ending == "yendo" && (start == 0 || verb.prefix[start-1] != 'u') {
		return false
	}
	switch ending {
	case "iéndo":
		w.truncate(start)
		w.tail = "iendo"
	case "ándo":
		w.truncate(start)
		w.tail = "ando"
	case "ár":
		w.truncate(start)
		w.tail = "ar"
	case "ér":
		w.truncate(start)
		w.tail = "er"
	case "ír":
		w.truncate(start)
		w.tail = "ir"
	default:
		w.truncate(verb.length())
	}
	return true
}

func (w *spanishWord) standard(r1, r2 int) bool {
	s := w.suffix(spanishStandard, 0)
	if s == "" {
		return false
	}
	start, region := w.length()-len(s), r2
	if s == "amente" {
		region = r1
	}
	if start < region {
		return false
	}
	w.truncate(start)
	switch s {
	case "adora", "ador", "ación", "adoras", "adores", "aciones", "ante", "antes", "ancia", "ancias":
		w.remove("ic", r2)
	case "logía", "logías":
		w.tail = "log"
	case "ución", "uciones":
		w.tail = "u"
	case "encia", "encias":
		w.tail = "ente"
	case "amente":
		secondary := w.suffix("iv os ic ad", 0)
		if secondary != "" && w.remove(secondary, r2) && secondary == "iv" {
			w.remove("at", r2)
		}
	case "mente":
		w.remove("ante able ible", r2)
	case "idad", "idades":
		w.remove("abil ic iv", r2)
	case "iva", "ivo", "ivas", "ivos":
		w.remove("at", r2)
	}
	return true
}
func (w *spanishWord) yVerb(rv int) bool {
	s := w.suffix(spanishYVerbs, rv)
	start := w.length() - len(s)
	if s == "" || start == 0 {
		return false
	}
	preceding := *w
	preceding.truncate(start)
	if !preceding.hasSuffix("u") {
		return false
	}
	w.truncate(start)
	return true
}
func (w *spanishWord) verb(rv int) bool {
	s := w.suffix(spanishVerbs, rv)
	if s == "" {
		return false
	}
	w.truncate(w.length() - len(s))
	switch s {
	case "en", "es", "éis", "emos":
		if w.hasSuffix("gu") {
			w.truncate(w.length() - 1)
		}
	}
	return true
}
func (w *spanishWord) residual(rv int) bool {
	s := w.suffix("os a o á í ó e é", 0)
	if s == "" || !w.remove(s, rv) {
		return false
	}
	if (s == "e" || s == "é") && w.hasSuffix("gu") {
		w.remove("u", rv)
	}
	return true
}
func spanishStem(s string) string {
	rv, r1 := spanishRV(s), spanishRegion(s, 0)
	r2 := spanishRegion(s, r1)
	w := spanishWord{prefix: s}
	w.pronoun(rv)
	if !w.standard(r1, r2) && !w.yVerb(rv) {
		w.verb(rv)
	}
	w.residual(rv)
	// Only acute lowercase vowels change. In particular ü and uppercase accents
	// are not normalized. Allocate once only when a replacement is necessary.
	if !strings.ContainsAny(w.prefix, "áéíóú") {
		return w.prefix + w.tail
	}
	var post strings.Builder
	post.Grow(w.length())
	for _, r := range w.prefix {
		switch r {
		case 'á':
			r = 'a'
		case 'é':
			r = 'e'
		case 'í':
			r = 'i'
		case 'ó':
			r = 'o'
		case 'ú':
			r = 'u'
		}
		post.WriteRune(r)
	}
	post.WriteString(w.tail)
	return post.String()
}
