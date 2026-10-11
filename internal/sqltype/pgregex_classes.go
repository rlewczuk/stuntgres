package sqltype

import "stuntgres/internal/sqltype/unicode16"

type pgRegexClassKind uint8

const (
	pgRegexRangeClass pgRegexClassKind = iota
	pgRegexAlnumClass
	pgRegexAlphaClass
	pgRegexASCIIClass
	pgRegexBlankClass
	pgRegexControlClass
	pgRegexDigitClass
	pgRegexGraphClass
	pgRegexLowerClass
	pgRegexPrintClass
	pgRegexPunctClass
	pgRegexSpaceClass
	pgRegexUpperClass
	pgRegexXDigitClass
	pgRegexWordClass
)

type pgRegexClassEntry struct {
	lo, hi          rune
	class           pgRegexClassKind
	invert, literal bool
	// A nonempty interval indexes the immutable explicit-range case closure.
	rangeLo, rangeHi int
}

type pgRegexRange struct{ lo, hi rune }

var pgRegexClassNames = [...]string{"", "alnum", "alpha", "ascii", "blank", "cntrl", "digit", "graph", "lower", "print", "punct", "space", "upper", "xdigit", "word"}
var pgRegexElementNames = [...]struct {
	name  string
	value rune
}{
	{"NUL", 0}, {"SOH", 1}, {"STX", 2}, {"ETX", 3}, {"EOT", 4}, {"ENQ", 5}, {"ACK", 6}, {"BEL", 7}, {"alert", 7}, {"BS", 8}, {"backspace", 8}, {"HT", 9}, {"tab", 9}, {"LF", 10}, {"newline", 10}, {"VT", 11}, {"vertical-tab", 11}, {"FF", 12}, {"form-feed", 12}, {"CR", 13}, {"carriage-return", 13}, {"SO", 14}, {"SI", 15}, {"DLE", 16}, {"DC1", 17}, {"DC2", 18}, {"DC3", 19}, {"DC4", 20}, {"NAK", 21}, {"SYN", 22}, {"ETB", 23}, {"CAN", 24}, {"EM", 25}, {"SUB", 26}, {"ESC", 27}, {"IS4", 28}, {"FS", 28}, {"IS3", 29}, {"GS", 29}, {"IS2", 30}, {"RS", 30}, {"IS1", 31}, {"US", 31},
	{"space", 32}, {"exclamation-mark", 33}, {"quotation-mark", 34}, {"number-sign", 35}, {"dollar-sign", 36}, {"percent-sign", 37}, {"ampersand", 38}, {"apostrophe", 39}, {"left-parenthesis", 40}, {"right-parenthesis", 41}, {"asterisk", 42}, {"plus-sign", 43}, {"comma", 44}, {"hyphen", 45}, {"hyphen-minus", 45}, {"period", 46}, {"full-stop", 46}, {"slash", 47}, {"solidus", 47}, {"zero", 48}, {"one", 49}, {"two", 50}, {"three", 51}, {"four", 52}, {"five", 53}, {"six", 54}, {"seven", 55}, {"eight", 56}, {"nine", 57}, {"colon", 58}, {"semicolon", 59}, {"less-than-sign", 60}, {"equals-sign", 61}, {"greater-than-sign", 62}, {"question-mark", 63}, {"commercial-at", 64}, {"left-square-bracket", 91}, {"backslash", 92}, {"reverse-solidus", 92}, {"right-square-bracket", 93}, {"circumflex", 94}, {"circumflex-accent", 94}, {"underscore", 95}, {"low-line", 95}, {"grave-accent", 96}, {"left-brace", 123}, {"left-curly-bracket", 123}, {"vertical-line", 124}, {"right-brace", 125}, {"right-curly-bracket", 125}, {"tilde", 126}, {"DEL", 127},
}

// compileClassRanges closes only explicit ranges/collating elements. Plain
// bracket literals and equivalence elements keep the separate allcases rule.
func (p *PGRegexProgram) compileClassRanges(b *pgRegexBudget) error {
	if !p.options.CaseInsensitive {
		return nil
	}
	for at := range p.classes {
		if err := b.work.step(); err != nil {
			return err
		}
		e := &p.classes[at]
		if e.class != pgRegexRangeClass || e.literal {
			continue
		}
		start := len(p.classRanges)
		if err := pgRegexPush(b, &p.classRanges, pgRegexRange{e.lo, e.hi}); err != nil {
			return err
		}
		err := unicode16.WalkSimpleCaseRange(e.lo, e.hi, p.collation.CaseMode(), func(lower, upper rune) error {
			if err := b.work.step(); err != nil {
				return err
			}
			for _, r := range [2]rune{lower, upper} {
				if r < e.lo || r > e.hi {
					if err := pgRegexPush(b, &p.classRanges, pgRegexRange{r, r}); err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		ranges := p.classRanges[start:]
		if err := sortPGRegexRanges(b.work, ranges); err != nil {
			return err
		}
		end := 0
		for _, r := range ranges {
			if err := b.work.step(); err != nil {
				return err
			}
			// Use widened arithmetic: regex entries can exceed Unicode scalars.
			if end > 0 && int64(r.lo) <= int64(ranges[end-1].hi)+1 {
				ranges[end-1].hi = max(ranges[end-1].hi, r.hi)
			} else {
				ranges[end] = r
				end++
			}
		}
		p.classRanges = p.classRanges[:start+end]
		e.rangeLo, e.rangeHi = start, start+end
	}
	return nil
}

// In-place heapsort needs neither construction scratch nor an uncancellable
// library sort. Each heap primitive shares the compiler's work poll.
func sortPGRegexRanges(w *pgRegexWork, ranges []pgRegexRange) error {
	sift := func(root, end int) error {
		for root < end/2 {
			if err := w.step(); err != nil {
				return err
			}
			child := 2*root + 1
			if child+1 < end && ranges[child].lo < ranges[child+1].lo {
				child++
			}
			if ranges[root].lo >= ranges[child].lo {
				break
			}
			ranges[root], ranges[child] = ranges[child], ranges[root]
			root = child
		}
		return nil
	}
	for root := len(ranges)/2 - 1; root >= 0; root-- {
		if err := sift(root, len(ranges)); err != nil {
			return err
		}
	}
	for end := len(ranges) - 1; end > 0; end-- {
		if err := w.step(); err != nil {
			return err
		}
		ranges[0], ranges[end] = ranges[end], ranges[0]
		if err := sift(0, end); err != nil {
			return err
		}
	}
	return nil
}

func (p *PGRegexProgram) classMember(r rune, k pgRegexClassKind) bool {
	alpha := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
	digit := r >= '0' && r <= '9'
	mode := p.collation.CaseMode()
	unicodeMode := mode != unicode16.ASCII
	if unicodeMode {
		switch k {
		case pgRegexAlphaClass, pgRegexAlnumClass, pgRegexWordClass:
			alpha = unicode16.RegexProperty(r, unicode16.RegexAlphabetic)
		}
		if mode == unicode16.Full && (k == pgRegexDigitClass || k == pgRegexAlnumClass || k == pgRegexWordClass) {
			digit = unicode16.RegexProperty(r, unicode16.RegexDecimal)
		}
	}
	switch k {
	case pgRegexAlphaClass:
		return alpha
	case pgRegexDigitClass:
		return digit
	case pgRegexAlnumClass:
		return alpha || digit
	case pgRegexWordClass:
		return alpha || digit || r == '_'
	case pgRegexASCIIClass:
		return r >= 0 && r <= 127
	case pgRegexBlankClass:
		return r == ' ' || r == '\t'
	case pgRegexControlClass:
		return r >= 0 && r <= 31 || r >= 127 && r <= 159
	case pgRegexXDigitClass:
		return digit && r < 128 || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F'
	case pgRegexSpaceClass:
		if unicodeMode {
			return unicode16.RegexProperty(r, unicode16.RegexWhiteSpace)
		}
		return r == ' ' || r >= '\t' && r <= '\r'
	case pgRegexLowerClass:
		if unicodeMode {
			return unicode16.RegexProperty(r, unicode16.RegexLowercase)
		}
		return r >= 'a' && r <= 'z'
	case pgRegexUpperClass:
		if unicodeMode {
			return unicode16.RegexProperty(r, unicode16.RegexUppercase)
		}
		return r >= 'A' && r <= 'Z'
	case pgRegexGraphClass:
		if unicodeMode {
			return unicode16.RegexProperty(r, unicode16.RegexGraph)
		}
		return r >= 33 && r <= 126
	case pgRegexPrintClass:
		if unicodeMode {
			return unicode16.RegexProperty(r, unicode16.RegexPrint)
		}
		return r >= 32 && r <= 126
	case pgRegexPunctClass:
		if unicodeMode {
			if mode == unicode16.Simple {
				return unicode16.RegexProperty(r, unicode16.RegexPunctOrSymbol) && !unicode16.RegexProperty(r, unicode16.RegexAlphabetic)
			}
			return unicode16.RegexProperty(r, unicode16.RegexPunctuation)
		}
		return r >= 33 && r <= 126 && !alpha && !digit
	}
	return false
}
func (p *PGRegexProgram) matchesClass(w *pgRegexWork, i pgRegexInst, r rune) (bool, error) {
	found := false
	for at := i.lo; at < i.hi; at++ {
		if err := w.step(); err != nil {
			return false, err
		}
		e := p.classes[at]
		yes := r >= e.lo && r <= e.hi
		if e.class != pgRegexRangeClass {
			yes = p.classMember(r, e.class)
			// REL_18_0 cclasscvec remaps lower/upper to alpha, including uncased letters.
			if p.options.CaseInsensitive && (e.class == pgRegexLowerClass || e.class == pgRegexUpperClass) {
				yes = p.classMember(r, pgRegexAlphaClass)
			}
		} else if p.options.CaseInsensitive {
			if e.literal {
				yes = p.literalEqual(e.lo, r)
			} else if e.rangeHi > e.rangeLo {
				left, right := e.rangeLo, e.rangeHi
				for left < right {
					mid := left + (right-left)/2
					if p.classRanges[mid].hi < r {
						left = mid + 1
					} else {
						right = mid
					}
				}
				yes = left < e.rangeHi && p.classRanges[left].lo <= r
			}
		}
		if e.invert {
			yes = !yes
		}
		if yes {
			found = true
			break
		}
	}
	if i.value != 0 {
		found = !found
		if p.newlineStop && r == '\n' {
			found = false
		}
	}
	return found, nil
}
