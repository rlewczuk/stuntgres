package functions

import (
	"strings"
	"unicode/utf8"
)

// Hungarian noun inflections, ported from PostgreSQL REL_18_6's Snowball
// stem_UTF_8_hungarian.c and upstream d19326ac6c1b9a417fc872f7c2f845265a5e9ece.
// https://github.com/postgres/postgres/blob/REL_18_6/src/backend/snowball/libstemmer/stem_UTF_8_hungarian.c
// See snowball_LICENSE.txt. Region offsets are original UTF8 byte offsets;
// shortening never recomputes them. ASCII lowering belongs to the dictionary.
func hungarianVowel(r rune) bool { return strings.ContainsRune("aeiouáéíóöőúüű", r) }

func hungarianRegion(s string) int {
	first, size := utf8.DecodeRuneInString(s)
	if size == 0 {
		return len(s)
	}
	if hungarianVowel(first) {
		i := size
		for i < len(s) {
			r, n := utf8.DecodeRuneInString(s[i:])
			if !hungarianVowel(r) {
				for _, unit := range [...]string{"dzs", "cs", "gy", "ly", "ny", "sz", "ty", "zs"} {
					if strings.HasPrefix(s[i:], unit) {
						return i + len(unit)
					}
				}
				return i + n
			}
			i += n
		}
	} else {
		for i := size; i < len(s); {
			r, n := utf8.DecodeRuneInString(s[i:])
			i += n
			if hungarianVowel(r) {
				return i
			}
		}
	}
	return len(s)
}

// Every edit is a tail deletion/replacement, except undoubling which preserves
// the last codepoint. Keep that single tail separately, so intermediate edits
// never allocate or copy a word. Only a surviving replacement needs one final
// concatenation; deletion-only and unchanged results reuse the input backing.
type hungarianWord struct{ prefix, tail string }

func (w hungarianWord) length() int { return len(w.prefix) + len(w.tail) }

func (w hungarianWord) hasSuffix(suffix string) bool {
	if len(suffix) <= len(w.tail) {
		return strings.HasSuffix(w.tail, suffix)
	}
	split := len(suffix) - len(w.tail)
	return strings.HasSuffix(w.prefix, suffix[:split]) && suffix[split:] == w.tail
}

func (w *hungarianWord) truncate(n int) {
	if n <= len(w.prefix) {
		w.prefix, w.tail = w.prefix[:n], ""
	} else {
		w.tail = w.tail[:n-len(w.prefix)]
	}
}

func (w hungarianWord) text() string { return w.prefix + w.tail }

// Select the longest suffix before testing the region, never retrying a shorter
// match after a predicate fails. Tables are immutable, and lookup allocates nothing.
func (w hungarianWord) suffix(endings string) string {
	best := ""
	for suffix := range strings.FieldsSeq(endings) {
		if len(suffix) > len(best) && w.hasSuffix(suffix) {
			best = suffix
		}
	}
	return best
}

func (w *hungarianWord) replace(p1 int, endings, replacement string) {
	suffix := w.suffix(endings)
	if suffix == "" || w.length()-len(suffix) < p1 {
		return
	}
	w.truncate(w.length() - len(suffix))
	w.tail = replacement
}

func (w hungarianWord) double() bool {
	return w.suffix("bb cc ccs dd ff gg ggy jj kk ll lly mm nn nny pp rr ss ssz tt tty vv zz zzs") != ""
}

func (w *hungarianWord) undouble() {
	last := w.tail
	if last == "" {
		_, n := utf8.DecodeLastRuneInString(w.prefix)
		last = w.prefix[len(w.prefix)-n:]
	}
	w.truncate(w.length() - len(last))
	_, previous := utf8.DecodeLastRuneInString(w.prefix)
	w.truncate(w.length() - previous)
	w.tail = last
}

func (w *hungarianWord) assimilation(p1 int, endings string) {
	suffix := w.suffix(endings)
	if suffix == "" || w.length()-len(suffix) < p1 {
		return
	}
	base := *w
	base.truncate(base.length() - len(suffix))
	if !base.double() {
		return
	}
	base.undouble()
	*w = base
}

const hungarianCases = "ban ben ba be ra re nak nek val vel tól től ról ről ból ből hoz hez höz nál nél ig at et ot öt ért képp képpen kor ul ül vá vé onként enként anként ként en on an ön n t"

// Replacement families belong to a single among operation: choose across all
// families first, then apply the selected action (rather than successive retries).
type hungarianRule struct{ endings, replacement string }

var hungarianOther = [...]hungarianRule{
	{"astul estül stul stül", ""}, {"ástul", "a"}, {"éstül", "e"},
}
var hungarianOwned = [...]hungarianRule{
	{"oké öké aké eké ké éi é", ""}, {"éké ééi éé", "e"}, {"áké áéi", "a"},
}
var hungarianSingular = [...]hungarianRule{
	{"ünk unk nk juk jük uk ük em om am m od ed ad öd d ja je a e o", ""},
	{"ánk ájuk ám ád á", "a"}, {"énk éjük ém éd é", "e"},
}
var hungarianPluralOwner = [...]hungarianRule{
	{"jaim jeim aim eim im jaid jeid aid eid id jai jei ai ei i jaink jeink eink aink ink jaitok jeitek aitok eitek itek jeik jaik aik eik ik", ""},
	{"áim áid ái áink áitok áik", "a"}, {"éim éid éi éink éitek éik", "e"},
}
var hungarianPlural = [...]hungarianRule{
	{"ök ak ok ek k", ""}, {"ák", "a"}, {"ék", "e"},
}

func (w *hungarianWord) apply(p1 int, rules []hungarianRule) {
	best, replacement := "", ""
	for _, rule := range rules {
		suffix := w.suffix(rule.endings)
		if len(suffix) > len(best) {
			best, replacement = suffix, rule.replacement
		}
	}
	if best == "" || w.length()-len(best) < p1 {
		return
	}
	w.truncate(w.length() - len(best))
	w.tail = replacement
}

func hungarianStem(s string) string {
	p1 := hungarianRegion(s)
	w := hungarianWord{prefix: s}
	w.assimilation(p1, "al el")
	if suffix := w.suffix(hungarianCases); suffix != "" && w.length()-len(suffix) >= p1 {
		w.truncate(w.length() - len(suffix))
		if w.hasSuffix("á") {
			w.replace(p1, "á", "a")
		} else {
			w.replace(p1, "é", "e")
		}
	}
	// The frequent case stage can shadow these suffixes; retain the released
	// independent stages, including their original-region checks.
	if w.hasSuffix("én") {
		w.replace(p1, "én", "e")
	} else {
		w.replace(p1, "án ánként", "a")
	}
	w.apply(p1, hungarianOther[:])
	w.assimilation(p1, "á é")
	w.apply(p1, hungarianOwned[:])
	w.apply(p1, hungarianSingular[:])
	w.apply(p1, hungarianPluralOwner[:])
	w.apply(p1, hungarianPlural[:])
	return w.text()
}
