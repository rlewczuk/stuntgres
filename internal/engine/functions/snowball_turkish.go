package functions

import (
	"strings"
	"unicode/utf8"
)

// Direct port of the Turkish Snowball algorithm shipped by PostgreSQL REL_18_6
// (Snowball d19326ac6c1b9a417fc872f7c2f845265a5e9ece), by Evren Çilden.
// See snowball_LICENSE.txt. Cursors are UTF-8 byte boundaries. Backtracking
// restores the distance from the current end, never already committed edits.
type turkishState struct {
	word  string
	c     int
	nouns bool
}

func turkishVowel(r rune) bool { return strings.ContainsRune("aeıioöuü", r) }
func (s *turkishState) previous(c int) (rune, int) {
	if c == 0 {
		return 0, 0
	}
	r, n := utf8.DecodeLastRuneInString(s.word[:c])
	return r, c - n
}
func (s *turkishState) harmony() bool {
	c := s.c
	for c > 0 {
		r, p := s.previous(c)
		c = p
		if !turkishVowel(r) {
			continue
		}
		group := ""
		switch r {
		case 'a':
			group = "aıou"
		case 'e':
			group = "eiöü"
		case 'ı':
			group = "aı"
		case 'i':
			group = "ei"
		case 'o', 'u':
			group = "ou"
		case 'ö', 'ü':
			group = "öü"
		}
		for c > 0 {
			r, p = s.previous(c)
			c = p
			if strings.ContainsRune(group, r) {
				return true
			}
		}
		return false
	}
	return false
}
func (s *turkishState) optional(buffer rune) bool {
	r, p := s.previous(s.c)
	if r == buffer {
		before, _ := s.previous(p)
		if p > 0 && turkishVowel(before) {
			s.c = p
			return true
		}
		return false
	}
	_, p = s.previous(s.c)
	before, _ := s.previous(p)
	return p > 0 && turkishVowel(before)
}
func (s *turkishState) optionalU() bool {
	r, p := s.previous(s.c)
	if strings.ContainsRune("ıiuü", r) {
		before, _ := s.previous(p)
		if p > 0 && !turkishVowel(before) {
			s.c = p
			return true
		}
		return false
	}
	_, p = s.previous(s.c)
	before, _ := s.previous(p)
	return p > 0 && !turkishVowel(before)
}

type turkishMarker uint8

const (
	trPoss turkishMarker = iota
	trSU
	trLArI
	trYU
	trNU
	trNUn
	trYA
	trNA
	trDA
	trNdA
	trDAn
	trNdAn
	trYlA
	trKi
	trNcA
	trYUm
	trSUn
	trYUz
	trSUnUz
	trLAr
	trNUz
	trDUr
	trCAsInA
	trYDU
	trYsA
	trYmUs
	trYken
)

var turkishSuffixes = [...][]string{
	{"mız", "miz", "muz", "müz", "nız", "niz", "nuz", "nüz", "m", "n"},
	{"ı", "i", "u", "ü"}, {"leri", "ları"}, {"ı", "i", "u", "ü"}, {"nı", "ni", "nu", "nü"},
	{"ın", "in", "un", "ün"}, {"a", "e"}, {"na", "ne"}, {"da", "de", "ta", "te"}, {"nda", "nde"},
	{"dan", "den", "tan", "ten"}, {"ndan", "nden"}, {"la", "le"}, {"ki"}, {"ca", "ce"},
	{"ım", "im", "um", "üm"}, {"sın", "sin", "sun", "sün"}, {"ız", "iz", "uz", "üz"},
	{"sınız", "siniz", "sunuz", "sünüz"}, {"ler", "lar"}, {"nız", "niz", "nuz", "nüz"},
	{"tır", "tir", "tur", "tür", "dır", "dir", "dur", "dür"}, {"casına", "cesine"},
	{"tım", "tim", "tum", "tüm", "dım", "dim", "dum", "düm", "tın", "tin", "tun", "tün", "dın", "din", "dun", "dün", "tık", "tik", "tuk", "tük", "dık", "dik", "duk", "dük", "tı", "ti", "tu", "tü", "dı", "di", "du", "dü"},
	{"sam", "san", "sak", "sem", "sen", "sek", "sa", "se"}, {"mış", "miş", "muş", "müş"}, {"ken"},
}

func (s *turkishState) mark(m turkishMarker) bool {
	start := s.c
	switch m {
	case trPoss, trLArI, trKi, trSUnUz, trCAsInA, trYsA, trYken:
	default:
		if !s.harmony() {
			return false
		}
	}
	matched := false
	for _, suffix := range turkishSuffixes[m] {
		if strings.HasSuffix(s.word[:s.c], suffix) {
			s.c -= len(suffix)
			matched = true
			break
		}
	}
	if !matched {
		return false
	}
	ok := true
	switch m {
	case trPoss:
		ok = s.optionalU()
	case trSU:
		ok = s.optional('s')
	case trYU, trYA, trYlA, trYUm, trYUz, trYDU, trYsA, trYmUs, trYken:
		ok = s.optional('y')
	case trNUn, trNcA:
		ok = s.optional('n')
	}
	if !ok {
		s.c = start
	}
	return ok
}
func (s *turkishState) any(markers ...turkishMarker) bool {
	c := s.c
	for _, m := range markers {
		s.c = c
		if s.mark(m) {
			return true
		}
	}
	s.c = c
	return false
}
func (s *turkishState) distance() int { return len(s.word) - s.c }
func (s *turkishState) restore(d int) { s.c = len(s.word) - d }
func (s *turkishState) cut(end int) {
	if end == len(s.word) {
		s.word = s.word[:s.c]
		return
	}
	s.word = s.word[:s.c] + s.word[end:]
}
func (s *turkishState) delete(markers ...turkishMarker) bool {
	end := s.c
	if !s.any(markers...) {
		return false
	}
	s.cut(end)
	return true
}
func (s *turkishState) nominal() {
	s.nouns = true
	end := s.c
	if s.any(trYmUs, trYDU, trYsA, trYken) {
		s.cut(end)
		return
	}
	s.c = end
	if s.mark(trCAsInA) {
		s.any(trSUnUz, trLAr, trYUm, trSUn, trYUz)
		if s.mark(trYmUs) {
			s.cut(end)
			return
		}
	}
	s.c = end
	if s.delete(trLAr) {
		end = s.c
		d := s.distance()
		if !s.any(trDUr, trYDU, trYsA, trYmUs) {
			s.restore(d)
		}
		s.nouns = false
		s.cut(end)
		return
	}
	s.c = end
	if s.mark(trNUz) && s.any(trYDU, trYsA) {
		s.cut(end)
		return
	}
	s.c = end
	if s.delete(trSUnUz, trYUz, trSUn, trYUm) {
		s.delete(trYmUs)
		return
	}
	s.c = end
	if s.delete(trDUr) {
		end = s.c
		d := s.distance()
		s.any(trSUnUz, trLAr, trYUm, trSUn, trYUz)
		if !s.mark(trYmUs) {
			s.restore(d)
		}
		s.cut(end)
	}
}

// pluralKi implements try([lAr] delete chain): failure restores the cursor
// but deliberately retains the plural deletion.
func (s *turkishState) pluralKi() {
	d := s.distance()
	if !s.delete(trLAr) || !s.chain() {
		s.restore(d)
	}
}
func (s *turkishState) chain() bool {
	d := s.distance()
	end := s.c
	if !s.mark(trKi) {
		return false
	}
	afterKi := s.distance()
	if s.mark(trDA) {
		s.cut(end)
		d = s.distance()
		if s.delete(trLAr) {
			q := s.distance()
			if !s.chain() {
				s.restore(q)
			}
			return true
		}
		s.restore(d)
		if s.delete(trPoss) {
			s.pluralKi()
			return true
		}
		s.restore(d)
		return true
	}
	s.restore(afterKi)
	if s.mark(trNUn) {
		s.cut(end)
		d = s.distance()
		if s.delete(trLArI) {
			return true
		}
		s.restore(d)
		if s.delete(trPoss, trSU) {
			s.pluralKi()
			return true
		}
		s.restore(d)
		if !s.chain() {
			s.restore(d)
		}
		return true
	}
	s.restore(afterKi)
	if s.mark(trNdA) {
		if s.mark(trLArI) {
			s.cut(end)
			return true
		}
		// Alternatives restore to the point immediately before ndA.
		s.restore(afterKi)
		s.mark(trNdA)
		q := s.distance()
		if s.mark(trSU) {
			s.cut(end)
			s.pluralKi()
			return true
		}
		s.restore(q)
		if s.chain() {
			return true
		}
	}
	s.restore(d)
	return false
}
func (s *turkishState) nounsStage() {
	d := s.distance()
	if s.delete(trLAr) {
		q := s.distance()
		if !s.chain() {
			s.restore(q)
		}
		return
	}
	s.restore(d)
	if s.delete(trNcA) {
		q := s.distance()
		if s.delete(trLArI) {
			return
		}
		s.restore(q)
		if s.delete(trPoss, trSU) {
			s.pluralKi()
			return
		}
		s.restore(q)
		s.pluralKi()
		return
	}
	s.restore(d)
	end := s.c
	if s.any(trNdA, trNA) {
		q := s.distance()
		if s.mark(trLArI) {
			s.cut(end)
			return
		}
		s.restore(q)
		if s.mark(trSU) {
			s.cut(end)
			s.pluralKi()
			return
		}
		s.restore(q)
		if s.chain() {
			return
		}
	}
	s.restore(d)
	end = s.c
	if s.any(trNdAn, trNU) {
		q := s.distance()
		if s.mark(trSU) {
			s.cut(end)
			s.pluralKi()
			return
		}
		s.restore(q)
		if s.mark(trLArI) {
			return
		}
	}
	s.restore(d)
	if s.delete(trDAn) {
		q := s.distance()
		if s.delete(trPoss) {
			s.pluralKi()
			return
		}
		s.restore(q)
		if s.delete(trLAr) {
			r := s.distance()
			if !s.chain() {
				s.restore(r)
			}
			return
		}
		s.restore(q)
		if !s.chain() {
			s.restore(q)
		}
		return
	}
	s.restore(d)
	if s.delete(trNUn, trYlA) {
		q := s.distance()
		if s.delete(trLAr) && s.chain() {
			return
		}
		s.restore(q)
		if s.delete(trPoss, trSU) {
			s.pluralKi()
			return
		}
		s.restore(q)
		if !s.chain() {
			s.restore(q)
		}
		return
	}
	s.restore(d)
	if s.delete(trLArI) {
		return
	}
	s.restore(d)
	if s.chain() {
		return
	}
	s.restore(d)
	if s.delete(trDA, trYU, trYA) {
		q := s.distance()
		end = s.c
		if s.delete(trPoss) {
			end = s.c
			s.any(trLAr)
			s.cut(end)
			r := s.distance()
			if !s.chain() {
				s.restore(r)
			}
			return
		}
		s.restore(q)
		if s.mark(trLAr) {
			s.cut(end)
			r := s.distance()
			if !s.chain() {
				s.restore(r)
			}
			return
		}
		s.restore(q)
		return
	}
	s.restore(d)
	if s.delete(trPoss, trSU) {
		s.pluralKi()
	}
}
func turkishStem(word string) string {
	if i := strings.IndexByte(word, '\''); i >= 0 {
		word = word[:i]
	}
	vowels := 0
	for _, r := range word {
		if turkishVowel(r) {
			vowels++
			if vowels == 2 {
				break
			}
		}
	}
	if vowels < 2 {
		return word
	}
	s := turkishState{word: word, c: len(word)}
	s.nominal()
	if !s.nouns {
		return s.word
	}
	s.c = len(s.word)
	s.nounsStage()
	word = s.word
	if word == "ad" || word == "soyad" {
		return word
	}
	if strings.HasSuffix(word, "d") || strings.HasSuffix(word, "g") {
		for c := len(word) - 1; c > 0; {
			r, p := s.previous(c)
			c = p
			switch r {
			case 'a', 'ı':
				return word + "ı"
			case 'e', 'i':
				return word + "i"
			case 'o', 'u':
				return word + "u"
			case 'ö', 'ü':
				return word + "ü"
			}
		}
	}
	switch {
	case strings.HasSuffix(word, "b"):
		return word[:len(word)-1] + "p"
	case strings.HasSuffix(word, "c"):
		return word[:len(word)-1] + "ç"
	case strings.HasSuffix(word, "d"):
		return word[:len(word)-1] + "t"
	case strings.HasSuffix(word, "ğ"):
		return word[:len(word)-2] + "k"
	}
	return word
}
