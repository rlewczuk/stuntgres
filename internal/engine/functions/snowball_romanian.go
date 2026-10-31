package functions

import (
	"strings"
	"unicode/utf8"
)

// Romanian from Snowball d19326ac6c1b9a417fc872f7c2f845265a5e9ece as
// bundled in PostgreSQL REL_18_6 (not the older upstream v2.2.0 algorithm).
// https://github.com/postgres/postgres/blob/REL_18_6/src/backend/snowball/libstemmer/stem_UTF_8_romanian.c
// See snowball_LICENSE.txt. The shared dictionary wrapper owns case and limits.
func romanianVowel(r rune) bool { return strings.ContainsRune("aeiouăâî", r) }

func romanianPrelude(s string) string {
	var changed []byte
	var previous rune
	for i, r := range s {
		replacement := r
		switch r {
		case 'ş':
			replacement = 'ș'
		case 'ţ':
			replacement = 'ț'
		case 'i', 'u':
			if romanianVowel(previous) {
				next, _ := utf8.DecodeRuneInString(s[i+1:])
				if romanianVowel(next) {
					replacement = r - ('a' - 'A')
				}
			}
		}
		if replacement != r {
			if changed == nil {
				changed = []byte(s)
			}
			utf8.EncodeRune(changed[i:], replacement)
		}
		previous = replacement
	}
	if changed == nil {
		return s
	}
	return string(changed)
}

func romanianRegion(s string, start int) int {
	vowel := false
	for i := start; i < len(s); {
		r, width := utf8.DecodeRuneInString(s[i:])
		i += width
		if vowel && !romanianVowel(r) {
			return i
		}
		vowel = romanianVowel(r)
	}
	return len(s)
}

func romanianRV(s string) int {
	first, n := utf8.DecodeRuneInString(s)
	if n == 0 || n == len(s) {
		return len(s)
	}
	second, m := utf8.DecodeRuneInString(s[n:])
	i := n + m
	if !romanianVowel(first) && romanianVowel(second) {
		_, width := utf8.DecodeRuneInString(s[i:])
		return i + width
	}
	seekVowel := !romanianVowel(second)
	for i < len(s) {
		r, width := utf8.DecodeRuneInString(s[i:])
		i += width
		if romanianVowel(r) == seekVowel {
			return i
		}
	}
	return len(s)
}

// Retain the input prefix and a literal replacement tail until final publication.
// Regions are byte offsets in the original normalized/prelude word.
type romanianWord struct{ prefix, tail string }

func (w romanianWord) length() int { return len(w.prefix) + len(w.tail) }
func (w romanianWord) hasSuffix(s string) bool {
	if len(s) <= len(w.tail) {
		return strings.HasSuffix(w.tail, s)
	}
	n := len(s) - len(w.tail)
	return strings.HasSuffix(w.prefix, s[:n]) && s[n:] == w.tail
}
func (w *romanianWord) truncate(n int) {
	if n <= len(w.prefix) {
		w.prefix, w.tail = w.prefix[:n], ""
	} else {
		w.tail = w.tail[:n-len(w.prefix)]
	}
}
func (w romanianWord) suffix(endings string, region int) string {
	best := ""
	for s := range strings.FieldsSeq(endings) {
		if len(s) > len(best) && w.length()-len(s) >= region && w.hasSuffix(s) {
			best = s
		}
	}
	return best
}
func (w romanianWord) before(end int) (rune, int) {
	if end <= len(w.prefix) {
		return utf8.DecodeLastRuneInString(w.prefix[:end])
	}
	return utf8.DecodeLastRuneInString(w.tail[:end-len(w.prefix)])
}

const romanianArticles = "ul ului aua ea ele elor ii iua iei iile iilor ilor ile atei ație ația"
const romanianCombining = "abilitate abilitati abilităi abilități ibilitate ivitate ivitati ivităi ivități icitate icitati icităi icități icator icatori iciv iciva icive icivi icivă ical icala icale icali icală ativ ativa ative ativi ativă ațiune atoare ator atori ătoare ător ători itiv itiva itive itivi itivă ițiune itoare itor itori"
const romanianStandard = "at ata ată ati ate ut uta ută uti ute it ita ită iti ite ic ica ice ici ică abil abila abile abili abilă ibil ibila ibile ibili ibilă oasa oasă oase os osi oși ant anta ante anti antă ator atori itate itati ităi ități iv iva ive ivi ivă iune iuni ism isme ist ista iste isti istă iști"
const romanianConditionalVerbs = "are ere ire âre ind ând indu ându eze ească ez ezi ează esc ești ește ăsc ăști ăște am ai au eam eai ea eați eau iam iai ia iați iau ui ași arăm arăți ară uși urăm urăți ură iși irăm irăți iră âi âși ârăm ârăți âră asem aseși ase aserăm aserăți aseră isem iseși ise iserăm iserăți iseră âsem âseși âse âserăm âserăți âseră usem useși use userăm userăți useră"
const romanianUnconditionalVerbs = "ăm ați em eți im iți âm âți seși serăm serăți seră sei se sesem seseși sese seserăm seserăți seseră"

func (w *romanianWord) article(r1 int) bool {
	s := w.suffix(romanianArticles, 0)
	start := w.length() - len(s)
	if s == "" || start < r1 {
		return false
	}
	replacement := ""
	switch s {
	case "aua":
		replacement = "a"
	case "ea", "ele", "elor":
		replacement = "e"
	case "ii", "iua", "iei", "iile", "iilor", "ilor":
		replacement = "i"
	case "ile":
		if start >= 2 && w.prefix[start-2:start] == "ab" {
			return false
		}
		replacement = "i"
	case "atei":
		replacement = "at"
	case "ație", "ația":
		replacement = "ați"
	}
	w.truncate(start)
	w.tail = replacement
	return true
}

func (w *romanianWord) combine(r1 int) bool {
	s := w.suffix(romanianCombining, 0)
	start := w.length() - len(s)
	if s == "" || start < r1 {
		return false
	}
	replacement := ""
	switch s {
	case "abilitate", "abilitati", "abilităi", "abilități":
		replacement = "abil"
	case "ibilitate":
		replacement = "ibil"
	case "ivitate", "ivitati", "ivităi", "ivități":
		replacement = "iv"
	case "icitate", "icitati", "icităi", "icități", "icator", "icatori", "iciv", "iciva", "icive", "icivi", "icivă", "ical", "icala", "icale", "icali", "icală":
		replacement = "ic"
	case "ativ", "ativa", "ative", "ativi", "ativă", "ațiune", "atoare", "ator", "atori", "ătoare", "ător", "ători":
		replacement = "at"
	case "itiv", "itiva", "itive", "itivi", "itivă", "ițiune", "itoare", "itor", "itori":
		replacement = "it"
	}
	w.truncate(start)
	w.tail = replacement
	return true
}

func (w *romanianWord) standard(r2 int) bool {
	s := w.suffix(romanianStandard, 0)
	start := w.length() - len(s)
	if s == "" || start < r2 {
		return false
	}
	replacement := ""
	switch s {
	case "iune", "iuni":
		r, width := w.before(start)
		if r != 'ț' {
			return false
		}
		start -= width
		replacement = "t"
	case "ism", "isme", "ist", "ista", "iste", "isti", "istă", "iști":
		replacement = "ist"
	}
	w.truncate(start)
	w.tail = replacement
	return true
}

func (w *romanianWord) verb(rv int) bool {
	conditional := w.suffix(romanianConditionalVerbs, rv)
	unconditional := w.suffix(romanianUnconditionalVerbs, rv)
	s := conditional
	if len(unconditional) > len(s) {
		s = unconditional
	}
	if s == "" {
		return false
	}
	start := w.length() - len(s)
	if s == conditional {
		r, width := w.before(start)
		if width == 0 || start-width < rv || (romanianVowel(r) && r != 'u') {
			return false
		}
	}
	w.truncate(start)
	return true
}

func (w *romanianWord) vowel(rv int) bool {
	s := w.suffix("a e i ie ă", 0)
	if s == "" || w.length()-len(s) < rv {
		return false
	}
	w.truncate(w.length() - len(s))
	return true
}

func romanianStem(s string) string {
	s = romanianPrelude(s)
	rv, r1 := romanianRV(s), romanianRegion(s, 0)
	r2 := romanianRegion(s, r1)
	w := romanianWord{prefix: s}
	w.article(r1)
	combined := false
	for w.combine(r1) {
		combined = true
	}
	standard := w.standard(r2)
	if !combined && !standard {
		w.verb(rv)
	}
	w.vowel(rv)
	if !strings.ContainsAny(w.prefix, "IU") {
		return w.prefix + w.tail
	}
	var post strings.Builder
	post.Grow(w.length())
	for i := range len(w.prefix) {
		c := w.prefix[i]
		if c == 'I' || c == 'U' {
			c += 'a' - 'A'
		}
		post.WriteByte(c)
	}
	post.WriteString(w.tail)
	return post.String()
}
