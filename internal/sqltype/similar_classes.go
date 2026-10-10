package sqltype

import (
	"stuntgres/internal/sqltype/unicode16"
	"unicode/utf8"
)

type similarClassKind uint8

const (
	similarRangeClass similarClassKind = iota
	similarAlnumClass
	similarAlphaClass
	similarASCIIClass
	similarBlankClass
	similarControlClass
	similarDigitClass
	similarGraphClass
	similarLowerClass
	similarPrintClass
	similarPunctClass
	similarSpaceClass
	similarUpperClass
	similarXDigitClass
	similarWordClass
)

type similarClassEntry struct {
	lo, hi              rune
	class               similarClassKind
	invert, equivalence bool
}

var similarClassNames = [...]string{"", "alnum", "alpha", "ascii", "blank", "cntrl", "digit", "graph", "lower", "print", "punct", "space", "upper", "xdigit", "word"}
var similarElementNames = [...]struct {
	name  string
	value rune
}{
	{"NUL", 0}, {"SOH", 1}, {"STX", 2}, {"ETX", 3}, {"EOT", 4}, {"ENQ", 5}, {"ACK", 6}, {"BEL", 7}, {"alert", 7}, {"BS", 8}, {"backspace", 8}, {"HT", 9}, {"tab", 9}, {"LF", 10}, {"newline", 10}, {"VT", 11}, {"vertical-tab", 11}, {"FF", 12}, {"form-feed", 12}, {"CR", 13}, {"carriage-return", 13}, {"SO", 14}, {"SI", 15}, {"DLE", 16}, {"DC1", 17}, {"DC2", 18}, {"DC3", 19}, {"DC4", 20}, {"NAK", 21}, {"SYN", 22}, {"ETB", 23}, {"CAN", 24}, {"EM", 25}, {"SUB", 26}, {"ESC", 27}, {"IS4", 28}, {"FS", 28}, {"IS3", 29}, {"GS", 29}, {"IS2", 30}, {"RS", 30}, {"IS1", 31}, {"US", 31},
	{"space", 32}, {"exclamation-mark", 33}, {"quotation-mark", 34}, {"number-sign", 35}, {"dollar-sign", 36}, {"percent-sign", 37}, {"ampersand", 38}, {"apostrophe", 39}, {"left-parenthesis", 40}, {"right-parenthesis", 41}, {"asterisk", 42}, {"plus-sign", 43}, {"comma", 44}, {"hyphen", 45}, {"hyphen-minus", 45}, {"period", 46}, {"full-stop", 46}, {"slash", 47}, {"solidus", 47}, {"zero", 48}, {"one", 49}, {"two", 50}, {"three", 51}, {"four", 52}, {"five", 53}, {"six", 54}, {"seven", 55}, {"eight", 56}, {"nine", 57}, {"colon", 58}, {"semicolon", 59}, {"less-than-sign", 60}, {"equals-sign", 61}, {"greater-than-sign", 62}, {"question-mark", 63}, {"commercial-at", 64}, {"left-square-bracket", 91}, {"backslash", 92}, {"reverse-solidus", 92}, {"right-square-bracket", 93}, {"circumflex", 94}, {"circumflex-accent", 94}, {"underscore", 95}, {"low-line", 95}, {"grave-accent", 96}, {"left-brace", 123}, {"left-curly-bracket", 123}, {"vertical-line", 124}, {"right-brace", 125}, {"right-curly-bracket", 125}, {"tilde", 126}, {"DEL", 127},
}

func (s *similarParser) bracketItem() (similarClassEntry, error) {
	if s.peek() == -1 {
		return similarClassEntry{}, similarCompileError("brackets [] not balanced")
	}
	c := s.read()
	if c == '\\' {
		n, err := s.escape(true)
		if err != nil {
			return similarClassEntry{}, err
		}
		if n.op == similarClass {
			entry := s.program.classes[n.lo]
			s.program.classes = s.program.classes[:n.lo]
			return entry, nil
		}
		return similarClassEntry{lo: n.value, hi: n.value}, nil
	}
	if c == '[' && (s.peek() == ':' || s.peek() == '.' || s.peek() == '=') {
		delimiter := s.read()
		start := s.at
		for s.peek() != -1 {
			if err := s.work.step(); err != nil {
				return similarClassEntry{}, err
			}
			if s.peek() == delimiter && s.at+1 < len(s.text) && s.text[s.at+1] == ']' {
				break
			}
			s.read()
		}
		if s.peek() == -1 {
			return similarClassEntry{}, similarCompileError("brackets [] not balanced")
		}
		name := s.text[start:s.at]
		s.at += 2
		if delimiter == ':' {
			for i := 1; i < len(similarClassNames); i++ {
				if name == similarClassNames[i] {
					return similarClassEntry{class: similarClassKind(i)}, nil
				}
			}
			return similarClassEntry{}, similarCompileError("invalid character class")
		}
		r, n := utf8.DecodeRuneInString(name)
		if name != "" && n == len(name) {
			return similarClassEntry{lo: r, hi: r, equivalence: delimiter == '='}, nil
		}
		for _, entry := range similarElementNames {
			if name == entry.name {
				return similarClassEntry{lo: entry.value, hi: entry.value, equivalence: delimiter == '='}, nil
			}
		}
		return similarClassEntry{}, similarCompileError("invalid collating element")
	}
	return similarClassEntry{lo: c, hi: c}, nil
}
func (s *similarParser) bracket() (similarNode, error) {
	// PostgreSQL's two legacy word constraints are not character sets.
	if len(s.text)-s.at >= 6 && (s.text[s.at:s.at+6] == "[:<:]]" || s.text[s.at:s.at+6] == "[:>:]]") {
		op := similarWordBegin
		if s.text[s.at+2] == '>' {
			op = similarWordEnd
		}
		s.at += 6
		return similarNode{op: op}, nil
	}
	n := similarNode{op: similarClass, lo: len(s.program.classes)}
	if s.peek() == '^' {
		n.value = 1
		s.at++
	}
	first, rangeBefore := true, false
	for {
		if err := s.work.step(); err != nil {
			return similarNode{}, err
		}
		if s.peek() == -1 {
			return similarNode{}, similarCompileError("brackets [] not balanced")
		}
		if s.peek() == ']' && !first {
			s.at++
			n.hi = len(s.program.classes)
			return n, nil
		}
		if s.peek() == '-' && rangeBefore && s.at+1 < len(s.text) && s.text[s.at+1] != ']' {
			return similarNode{}, similarCompileError("invalid character range")
		}
		entry, err := s.bracketItem()
		if err != nil {
			return similarNode{}, err
		}
		rangeBefore = false
		if s.peek() == '-' && s.at+1 < len(s.text) && s.text[s.at+1] != ']' {
			s.at++
			end, err := s.bracketItem()
			if err != nil {
				return similarNode{}, err
			}
			if entry.class != similarRangeClass || end.class != similarRangeClass || entry.equivalence || end.equivalence || entry.lo > end.lo {
				return similarNode{}, similarCompileError("invalid character range")
			}
			entry.hi = end.lo
			rangeBefore = true
		}
		if err := similarPush(s.budget, &s.program.classes, entry); err != nil {
			return similarNode{}, err
		}
		first = false
	}
}
func (p *SimilarProgram) classMember(r rune, k similarClassKind) bool {
	alpha := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
	digit := r >= '0' && r <= '9'
	mode := p.collation.CaseMode()
	unicodeMode := mode != unicode16.ASCII
	if unicodeMode {
		switch k {
		case similarAlphaClass, similarAlnumClass, similarWordClass:
			alpha = unicode16.RegexProperty(r, unicode16.RegexAlphabetic)
		}
		if mode == unicode16.Full && (k == similarDigitClass || k == similarAlnumClass || k == similarWordClass) {
			digit = unicode16.RegexProperty(r, unicode16.RegexDecimal)
		}
	}
	switch k {
	case similarAlphaClass:
		return alpha
	case similarDigitClass:
		return digit
	case similarAlnumClass:
		return alpha || digit
	case similarWordClass:
		return alpha || digit || r == '_'
	case similarASCIIClass:
		return r >= 0 && r <= 127
	case similarBlankClass:
		return r == ' ' || r == '\t'
	case similarControlClass:
		return r >= 0 && r <= 31 || r >= 127 && r <= 159
	case similarXDigitClass:
		return digit && r < 128 || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F'
	case similarSpaceClass:
		if unicodeMode {
			return unicode16.RegexProperty(r, unicode16.RegexWhiteSpace)
		}
		return r == ' ' || r >= '\t' && r <= '\r'
	case similarLowerClass:
		if unicodeMode {
			return unicode16.RegexProperty(r, unicode16.RegexLowercase)
		}
		return r >= 'a' && r <= 'z'
	case similarUpperClass:
		if unicodeMode {
			return unicode16.RegexProperty(r, unicode16.RegexUppercase)
		}
		return r >= 'A' && r <= 'Z'
	case similarGraphClass:
		if unicodeMode {
			return unicode16.RegexProperty(r, unicode16.RegexGraph)
		}
		return r >= 33 && r <= 126
	case similarPrintClass:
		if unicodeMode {
			return unicode16.RegexProperty(r, unicode16.RegexPrint)
		}
		return r >= 32 && r <= 126
	case similarPunctClass:
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
func (p *SimilarProgram) matchesClass(w *similarWork, i similarInst, r rune) (bool, error) {
	found := false
	for at := i.lo; at < i.hi; at++ {
		if err := w.step(); err != nil {
			return false, err
		}
		e := p.classes[at]
		yes := r >= e.lo && r <= e.hi
		if e.class != similarRangeClass {
			yes = p.classMember(r, e.class)
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
	}
	return found, nil
}
