package functions

import (
	"strings"
	"unicode/utf8"
)

// Russian is ported from PostgreSQL REL_18_6's stem_UTF_8_russian.c and
// russian.sbl at Snowball d19326ac6c1b9a417fc872f7c2f845265a5e9ece.
// BSD Snowball license: see snowball_LICENSE.txt. Regions are original UTF8
// byte boundaries; deletion and predecessor tests cannot cross RV.
type russianSuffix struct {
	ending      string
	conditional bool // requires а/я immediately before the ending, inside RV
}

var russianPerfective = [...]russianSuffix{
	{"в", true}, {"вши", true}, {"вшись", true},
	{"ив", false}, {"ивши", false}, {"ившись", false},
	{"ыв", false}, {"ывши", false}, {"ывшись", false},
}

var russianAdjective = [...]russianSuffix{
	{"ее", false}, {"ие", false}, {"ые", false}, {"ое", false},
	{"ими", false}, {"ыми", false}, {"ей", false}, {"ий", false},
	{"ый", false}, {"ой", false}, {"ем", false}, {"им", false},
	{"ым", false}, {"ом", false}, {"его", false}, {"ого", false},
	{"ему", false}, {"ому", false}, {"их", false}, {"ых", false},
	{"ую", false}, {"юю", false}, {"ая", false}, {"яя", false},
	{"ою", false}, {"ею", false},
}

var russianParticiple = [...]russianSuffix{
	{"ем", true}, {"нн", true}, {"вш", true}, {"ющ", true}, {"щ", true},
	{"ивш", false}, {"ывш", false}, {"ующ", false},
}

var russianReflexive = [...]russianSuffix{{"ся", false}, {"сь", false}}

var russianVerb = [...]russianSuffix{
	{"ла", true}, {"на", true}, {"ете", true}, {"йте", true},
	{"ли", true}, {"й", true}, {"л", true}, {"ем", true},
	{"н", true}, {"ло", true}, {"но", true}, {"ет", true},
	{"ют", true}, {"ны", true}, {"ть", true}, {"ешь", true}, {"нно", true},
	{"ила", false}, {"ыла", false}, {"ена", false}, {"ейте", false},
	{"уйте", false}, {"ите", false}, {"или", false}, {"ыли", false},
	{"ей", false}, {"уй", false}, {"ил", false}, {"ыл", false},
	{"им", false}, {"ым", false}, {"ен", false}, {"ило", false},
	{"ыло", false}, {"ено", false}, {"ят", false}, {"ует", false},
	{"уют", false}, {"ит", false}, {"ыт", false}, {"ены", false},
	{"ить", false}, {"ыть", false}, {"ишь", false}, {"ую", false}, {"ю", false},
}

var russianNoun = [...]russianSuffix{
	{"а", false}, {"ев", false}, {"ов", false}, {"ие", false},
	{"ье", false}, {"е", false}, {"иями", false}, {"ями", false},
	{"ами", false}, {"еи", false}, {"ии", false}, {"и", false},
	{"ией", false}, {"ей", false}, {"ой", false}, {"ий", false}, {"й", false},
	{"иям", false}, {"ям", false}, {"ием", false}, {"ем", false},
	{"ам", false}, {"ом", false}, {"о", false}, {"у", false},
	{"ах", false}, {"иях", false}, {"ях", false}, {"ы", false}, {"ь", false},
	{"ию", false}, {"ью", false}, {"ю", false}, {"ия", false}, {"ья", false}, {"я", false},
}

var russianDerivational = [...]russianSuffix{{"ост", false}, {"ость", false}}
var russianTidy = [...]russianSuffix{{"ейш", false}, {"ейше", false}, {"н", false}, {"ь", false}}

func russianVowel(r rune) bool {
	switch r {
	case 'а', 'е', 'и', 'о', 'у', 'ы', 'э', 'ю', 'я':
		return true
	}
	return false
}

func russianRegions(s string) (rv, r2 int) {
	rv, r2 = len(s), len(s)
	previousVowel := false
	transitions := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		vowel := russianVowel(r)
		if vowel && rv == len(s) {
			rv = i
		}
		if previousVowel && !vowel {
			transitions++
			if transitions == 2 {
				r2 = i
				break
			}
		}
		previousVowel = vowel
	}
	return rv, r2
}

// Select the longest ending before evaluating its action. Failed conditions
// do not retry shorter endings in this same table.
func russianMatch(s string, boundary int, table []russianSuffix) (int, bool) {
	best := -1
	for i := range table {
		rule := &table[i]
		if len(s)-len(rule.ending) >= boundary && strings.HasSuffix(s, rule.ending) && (best < 0 || len(rule.ending) > len(table[best].ending)) {
			best = i
		}
	}
	if best < 0 {
		return len(s), false
	}
	rule := &table[best]
	start := len(s) - len(rule.ending)
	if rule.conditional {
		r, size := utf8.DecodeLastRuneInString(s[:start])
		if start-size < boundary || r != 'а' && r != 'я' {
			return len(s), false
		}
	}
	return start, true
}

func russianStem(input string) string {
	// Only lowercase ё is normalized; C-locale case conversion and stopword
	// lookup are the shared dictionary wrapper's earlier responsibilities.
	s := strings.ReplaceAll(input, "ё", "е")
	rv, r2 := russianRegions(s)
	if end, ok := russianMatch(s, rv, russianPerfective[:]); ok {
		s = s[:end]
	} else {
		if end, ok := russianMatch(s, rv, russianReflexive[:]); ok {
			s = s[:end]
		}
		if end, ok := russianMatch(s, rv, russianAdjective[:]); ok {
			s = s[:end]
			if end, ok := russianMatch(s, rv, russianParticiple[:]); ok {
				s = s[:end]
			}
		} else if end, ok := russianMatch(s, rv, russianVerb[:]); ok {
			s = s[:end]
		} else if end, ok := russianMatch(s, rv, russianNoun[:]); ok {
			s = s[:end]
		}
	}
	if len(s)-len("и") >= rv && strings.HasSuffix(s, "и") {
		s = s[:len(s)-len("и")]
	}
	if end, ok := russianMatch(s, rv, russianDerivational[:]); ok && end >= r2 {
		s = s[:end]
	}
	if end, ok := russianMatch(s, rv, russianTidy[:]); ok {
		suffix := s[end:]
		switch suffix {
		case "ейш", "ейше":
			s = s[:end]
			if len(s)-len("нн") >= rv && strings.HasSuffix(s, "нн") {
				s = s[:len(s)-len("н")]
			}
		case "н":
			if end-len("н") >= rv && strings.HasSuffix(s[:end], "н") {
				s = s[:end]
			}
		case "ь":
			s = s[:end]
		}
	}
	return s
}
