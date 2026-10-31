package functions

import (
	"strings"
	"unicode/utf8"
)

// Direct port of PostgreSQL REL_18_6 stem_UTF_8_italian.c and Snowball
// d19326ac6c1b9a417fc872f7c2f845265a5e9ece/algorithms/italian.sbl.
// https://github.com/postgres/postgres/blob/REL_18_6/src/backend/snowball/libstemmer/stem_UTF_8_italian.c
// See snowball_LICENSE.txt. Case conversion belongs to the dictionary wrapper.
func italianVowel(r rune) bool { return strings.ContainsRune("aeiouàèìòù", r) }

// Prelude replacements preserve byte widths. Allocate scratch only at the first
// actual replacement, retaining original backing for ordinary deletion-only words.
func italianPrelude(s string) string {
	var changed []byte
	set := func(i int, replacement string) {
		if changed == nil {
			changed = []byte(s)
		}
		copy(changed[i:], replacement)
	}
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch r {
		case 'á':
			set(i, "à")
		case 'é':
			set(i, "è")
		case 'í':
			set(i, "ì")
		case 'ó':
			set(i, "ò")
		case 'ú':
			set(i, "ù")
		case 'q':
			if i+1 < len(s) && s[i+1] == 'u' {
				set(i+1, "U")
				i++
			}
		}
		i += n
	}
	// Read normalized scratch without materializing an intermediate string.
	at := func(i int) (rune, int) {
		if changed != nil {
			return utf8.DecodeRune(changed[i:])
		}
		return utf8.DecodeRuneInString(s[i:])
	}
	for i := 0; i < len(s); {
		r, n := at(i)
		i += n
		if !italianVowel(r) || i >= len(s) {
			continue
		}
		middle, width := at(i)
		if (middle != 'i' && middle != 'u') || i+width >= len(s) {
			continue
		}
		next, _ := at(i + width)
		if italianVowel(next) {
			if middle == 'i' {
				set(i, "I")
			} else {
				set(i, "U")
			}
		}
	}
	if changed == nil {
		return s
	}
	return string(changed)
}

func italianRegion(s string, start int) int {
	vowel := false
	for i := start; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		i += n
		if vowel && !italianVowel(r) {
			return i
		}
		vowel = italianVowel(r)
	}
	return len(s)
}

func italianRV(s string) int {
	first, n := utf8.DecodeRuneInString(s)
	if n == 0 || n == len(s) {
		return len(s)
	}
	second, m := utf8.DecodeRuneInString(s[n:])
	i := n + m
	if !italianVowel(first) && italianVowel(second) {
		if i < len(s) {
			_, width := utf8.DecodeRuneInString(s[i:])
			return i + width
		}
		return len(s)
	}
	// A consonantal second letter seeks a vowel; two initial vowels seek
	// a consonant. The region starts after that character.
	seekVowel := !italianVowel(second)
	for i < len(s) {
		r, width := utf8.DecodeRuneInString(s[i:])
		i += width
		if italianVowel(r) == seekVowel {
			return i
		}
	}
	return len(s)
}

// Tail edits never concatenate intermediate words. Fixed region offsets refer
// to the prelude output, including after replacements shorten the suffix.
type italianWord struct{ prefix, tail string }

func (w italianWord) length() int { return len(w.prefix) + len(w.tail) }
func (w italianWord) hasSuffix(s string) bool {
	if len(s) <= len(w.tail) {
		return strings.HasSuffix(w.tail, s)
	}
	n := len(s) - len(w.tail)
	return strings.HasSuffix(w.prefix, s[:n]) && s[n:] == w.tail
}
func (w *italianWord) truncate(n int) {
	if n <= len(w.prefix) {
		w.prefix, w.tail = w.prefix[:n], ""
	} else {
		w.tail = w.tail[:n-len(w.prefix)]
	}
}
func (w italianWord) suffix(endings string, region int) string {
	best := ""
	for s := range strings.FieldsSeq(endings) {
		if len(s) > len(best) && w.length()-len(s) >= region && w.hasSuffix(s) {
			best = s
		}
	}
	return best
}
func (w *italianWord) remove(endings string, region int) bool {
	s := w.suffix(endings, 0)
	if s == "" || w.length()-len(s) < region {
		return false
	}
	w.truncate(w.length() - len(s))
	return true
}

const italianPronouns = "ci gli la le li lo mi ne si ti vi sene gliela gliele glieli glielo gliene mela mele meli melo mene tela tele teli telo tene cela cele celi celo cene vela vele veli velo vene"
const italianStandard = "anza anze ico ici ica ice iche ichi ismo ismi abile abili ibile ibili ista iste isti istà istè istì oso osi osa ose mente atrice atrici ante anti azione azioni atore atori logia logie uzione uzioni usione usioni enza enze amento amenti imento imenti amente ità ivo ivi iva ive"
const italianVerbs = "ammo ando ano are arono asse assero assi assimo ata ate ati ato ava avamo avano avate avi avo emmo enda ende endi endo erà erai eranno ere erebbe erebbero erei eremmo eremo ereste eresti erete erò erono essero ete eva evamo evano evate evi evo Yamo iamo immo irà irai iranno ire irebbe irebbero irei iremmo iremo ireste iresti irete irò irono isca iscano isce isci isco iscono issero ita ite iti ito iva ivamo ivano ivate ivi ivo ono uta ute uti uto ar ir"

func (w *italianWord) pronoun(rv int) {
	s := w.suffix(italianPronouns, 0)
	if s == "" {
		return
	}
	base := *w
	base.truncate(base.length() - len(s))
	before := base.suffix("ando endo ar er ir", rv)
	if before == "" {
		return
	}
	*w = base
	if before == "ar" || before == "er" || before == "ir" {
		w.tail = "e"
	}
}

func (w *italianWord) standard(rv, r1, r2 int) bool {
	s := w.suffix(italianStandard, 0)
	if s == "" {
		return false
	}
	region := r2
	switch s {
	case "amento", "amenti", "imento", "imenti":
		region = rv
	case "amente":
		region = r1
	}
	if w.length()-len(s) < region {
		return false
	}
	w.truncate(w.length() - len(s))
	switch s {
	case "azione", "azioni", "atore", "atori":
		w.remove("ic", r2)
	case "logia", "logie":
		w.tail = "log"
	case "uzione", "uzioni", "usione", "usioni":
		w.tail = "u"
	case "enza", "enze":
		w.tail = "ente"
	case "amente":
		secondary := w.suffix("iv os ic abil", 0)
		if secondary != "" && w.remove(secondary, r2) && secondary == "iv" {
			w.remove("at", r2)
		}
	case "ità":
		w.remove("abil ic iv", r2)
	case "ivo", "ivi", "iva", "ive":
		if w.remove("at", r2) {
			w.remove("ic", r2)
		}
	}
	return true
}

func italianStem(s string) string {
	if s == "divano" {
		return "divan"
	}
	s = italianPrelude(s)
	rv, r1 := italianRV(s), italianRegion(s, 0)
	r2 := italianRegion(s, r1)
	w := italianWord{prefix: s}
	w.pronoun(rv)
	if !w.standard(rv, r1, r2) {
		// Unlike standard among, verb among is restricted to RV before lookup.
		if suffix := w.suffix(italianVerbs, rv); suffix != "" {
			w.truncate(w.length() - len(suffix))
		}
	}
	if w.remove("a e i o à è ì ò", rv) {
		w.remove("i", rv)
	}
	if w.hasSuffix("h") {
		base := w
		base.truncate(base.length() - 1)
		if (base.hasSuffix("c") || base.hasSuffix("g")) && base.length()-1 >= rv {
			w = base
		}
	}
	if !strings.ContainsAny(w.prefix, "IU") && !strings.ContainsAny(w.tail, "IU") {
		return w.prefix + w.tail
	}
	var post strings.Builder
	post.Grow(w.length())
	for _, fragment := range [...]string{w.prefix, w.tail} {
		for i := range len(fragment) {
			c := fragment[i]
			if c == 'I' {
				c = 'i'
			} else if c == 'U' {
				c = 'u'
			}
			post.WriteByte(c)
		}
	}
	return post.String()
}
