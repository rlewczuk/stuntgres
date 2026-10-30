package functions

import "strings"

// Arabic Snowball, translated from PostgreSQL REL_18_6 stem_UTF_8_arabic.c
// and Snowball d19326ac6c1b9a417fc872f7c2f845265a5e9ece arabic.sbl.
// Authors: Assem Chelli and Abdelkrim Aries. See snowball_LICENSE.txt.
// Cursors count codepoints. Backtracking restores cursors, never mutations.
type arabicState struct {
	word    []rune
	cursor  int
	defined bool
}

type arabicAffix struct {
	text    string
	minimum int
}

func (s *arabicState) suffix(rules ...arabicAffix) bool {
	best, size := -1, 0
	for i, rule := range rules {
		n := 0
		for range rule.text {
			n++
		}
		if n <= size || n > s.cursor {
			continue
		}
		j, match := s.cursor-n, true
		for _, r := range rule.text {
			if s.word[j] != r {
				match = false
				break
			}
			j++
		}
		if match {
			best, size = i, n
		}
	}
	if best < 0 || len(s.word) < rules[best].minimum {
		return false
	}
	start := s.cursor - size
	copy(s.word[start:], s.word[s.cursor:])
	s.word = s.word[:len(s.word)-size]
	s.cursor = start
	return true
}

func (s *arabicState) verb1() bool {
	return s.suffix(arabicAffix{"ه", 4}, arabicAffix{"ك", 4}, arabicAffix{"ني", 5}, arabicAffix{"نا", 5}, arabicAffix{"ها", 5}, arabicAffix{"هم", 5}, arabicAffix{"هن", 5}, arabicAffix{"كم", 5}, arabicAffix{"كن", 5}, arabicAffix{"هما", 6}, arabicAffix{"كما", 6}, arabicAffix{"كمو", 6})
}
func (s *arabicState) verb2a() bool {
	return s.suffix(arabicAffix{"ت", 4}, arabicAffix{"ا", 4}, arabicAffix{"ن", 4}, arabicAffix{"ي", 4}, arabicAffix{"نا", 5}, arabicAffix{"تا", 5}, arabicAffix{"تن", 5}, arabicAffix{"ان", 6}, arabicAffix{"ون", 6}, arabicAffix{"ين", 6}, arabicAffix{"تما", 6})
}
func (s *arabicState) verb2c() bool { return s.suffix(arabicAffix{"و", 4}, arabicAffix{"تمو", 6}) }
func (s *arabicState) noun2() bool {
	return s.suffix(arabicAffix{"ا", 5}, arabicAffix{"ي", 5}, arabicAffix{"و", 5}) || s.suffix(arabicAffix{"ات", 5}) || s.suffix(arabicAffix{"ت", 4})
}
func (s *arabicState) backward() {
	// Alternatives reset the distance from the current end, including after
	// mutations made by a failing branch.
	reset := func() { s.cursor = len(s.word) }
	if !s.defined {
		if s.verb1() {
			for s.verb1() {
			}
			if s.verb2a() || s.verb2c() || s.cursor > 0 {
				return
			}
		}
		reset()
		if s.suffix(arabicAffix{"وا", 5}, arabicAffix{"تم", 5}) {
			return
		}
		reset()
		if s.verb2a() {
			return
		}
		reset()
	}
	// try(noun alternatives), then Noun_Step3. A successful fallback next
	// moves left without deleting, so Step3 can remove an interior yeh.
	if s.suffix(arabicAffix{"ة", 4}) {
	} else if !s.defined && s.suffix(arabicAffix{"ي", 4}, arabicAffix{"ك", 4}, arabicAffix{"ه", 4}, arabicAffix{"نا", 5}, arabicAffix{"كم", 5}, arabicAffix{"ها", 5}, arabicAffix{"هن", 5}, arabicAffix{"هم", 5}, arabicAffix{"كما", 6}, arabicAffix{"هما", 6}) {
		if !s.noun2() && s.cursor > 0 {
			s.cursor--
		}
	} else {
		reset()
		if s.suffix(arabicAffix{"ن", 6}) && s.noun2() {
		} else {
			reset()
			if !s.defined && s.suffix(arabicAffix{"ا", 5}, arabicAffix{"ي", 5}, arabicAffix{"و", 5}) {
			} else if !s.suffix(arabicAffix{"ات", 5}) {
				reset()
			}
		}
	}
	if s.suffix(arabicAffix{"ي", 3}) {
		return
	}
	reset()
	if s.cursor > 0 && s.word[s.cursor-1] == 'ى' {
		s.word[s.cursor-1] = 'ي'
	}
}

func (s *arabicState) prefix(text string, minimum int, replacement string) bool {
	start, n := s.cursor, s.cursor
	for _, r := range text {
		if n >= len(s.word) || s.word[n] != r {
			return false
		}
		n++
	}
	if len(s.word) < minimum {
		return false
	}
	if text == replacement {
		s.cursor = n
		return true
	}
	m := start
	for _, r := range replacement {
		s.word[m] = r
		m++
	}
	copy(s.word[m:], s.word[n:])
	s.word = s.word[:len(s.word)-(n-m)]
	s.cursor = m
	return true
}

func arabicStem(token string) string {
	// ASCII has no normalization or affix rules and needs no scratch buffer.
	if !strings.ContainsFunc(token, func(r rune) bool { return r >= 128 }) {
		return token
	}
	s := arabicState{word: make([]rune, 0, len(token))}
	for _, r := range token {
		s.word = append(s.word, r)
	}
	for _, p := range []string{"بال", "كال", "لل", "ال"} {
		minimum := 4
		if p == "بال" || p == "كال" {
			minimum = 5
		}
		if strings.HasPrefix(token, p) && len(s.word) >= minimum {
			s.defined = true
			break
		}
	}
	s.normalizePre()
	s.cursor = len(s.word)
	s.backward()
	s.cursor = 0
	for _, p := range []struct{ from, to string }{{"أأ", "أ"}, {"أآ", "آ"}, {"أؤ", "أ"}, {"أا", "ا"}, {"أإ", "إ"}} {
		if s.prefix(p.from, 4, p.to) {
			break
		}
	}
	if len(s.word) > 3 && s.cursor+1 < len(s.word) && (s.word[s.cursor] == 'ف' || s.word[s.cursor] == 'و') && s.word[s.cursor+1] != 'ا' {
		copy(s.word[s.cursor:], s.word[s.cursor+1:])
		s.word = s.word[:len(s.word)-1]
	}
	if !(s.prefix("بال", 6, "") || s.prefix("كال", 6, "") || s.prefix("لل", 5, "") || s.prefix("ال", 5, "")) {
		// Longest-match با succeeds unchanged; it blocks shorter ب.
		if !s.prefix("با", 0, "با") && !s.prefix("بب", 4, "ب") && !s.prefix("ب", 4, "") && !s.prefix("كك", 4, "ك") && !s.defined {
			for _, p := range []struct{ from, to string }{{"سي", "ي"}, {"ست", "ت"}, {"سن", "ن"}, {"سأ", "أ"}} {
				if s.prefix(p.from, 5, p.to) {
					break
				}
			}
			for _, p := range []string{"يست", "نست", "تست"} {
				if s.prefix(p, 5, "است") {
					break
				}
			}
		}
	}
	for i, r := range s.word {
		if i == len(s.word)-1 {
			switch r {
			case 'أ', 'إ', 'آ', 'ؤ', 'ئ':
				s.word[i] = 'ء'
			}
		} else {
			switch r {
			case 'أ', 'إ', 'آ':
				s.word[i] = 'ا'
			case 'ؤ':
				s.word[i] = 'و'
			case 'ئ':
				s.word[i] = 'ي'
			}
		}
	}
	return string(s.word)
}

// The presentation forms are a finite release table, not Unicode NFKC.
const arabicPresentation = "ءآآأأؤؤإإئئئئااببببةةتتتتثثثثججججححححخخخخددذذررززسسسسششششصصصصضضضضططططظظظظععععغغغغففففققققككككللللممممننننههههووىىيييي"

var arabicPresentationRunes = []rune(arabicPresentation)

func (s *arabicState) normalizePre() {
	// Byte-sized initial capacity accommodates every ligature expansion.
	extra := 0
	for _, r := range s.word {
		if r >= 0xfef5 && r <= 0xfefc {
			extra++
		}
	}
	// Expand right-to-left so unread input is never overwritten.
	if extra > 0 {
		n := len(s.word)
		s.word = s.word[:n+extra]
		j := len(s.word)
		for i := n - 1; i >= 0; i-- {
			r := s.word[i]
			if r >= 0xfef5 && r <= 0xfefc {
				a := 'ا'
				switch {
				case r <= 0xfef6:
					a = 'آ'
				case r <= 0xfef8:
					a = 'أ'
				case r <= 0xfefa:
					a = 'إ'
				}
				j--
				s.word[j] = a
				j--
				s.word[j] = 'ل'
			} else {
				j--
				s.word[j] = r
			}
		}
	}
	n := 0
	for _, r := range s.word {
		switch {
		case r == 0x640 || r >= 0x64b && r <= 0x652:
			continue
		case r >= 0x660 && r <= 0x669:
			r = '0' + r - 0x660
		case r >= 0xfe80 && r <= 0xfef4:
			r = arabicPresentationRunes[r-0xfe80]
		}
		s.word[n] = r
		n++
	}
	s.word = s.word[:n]
}
