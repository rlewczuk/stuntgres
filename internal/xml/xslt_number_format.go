package xml

import (
	"context"
	"math"
	"strings"
	"unicode/utf8"
)

// These symbols are invocation-owned declarations, not process locale state.
type xsltDecimalFormat struct {
	DecimalSeparator, GroupingSeparator, MinusSign, Percent, PerMille, ZeroDigit, Digit, PatternSeparator rune
	Infinity, NaN                                                                                         string
}

func xsltDefaultDecimalFormat() xsltDecimalFormat {
	return xsltDecimalFormat{'.', ',', '-', '%', '‰', '0', '#', ';', "Infinity", "NaN"}
}

// Numbering follows XSLT 1.0 §7.7 and the released libxslt 1.1.35
// numbers.c tokenizer/helpers. In particular its alphabet is ASCII, its decimal
// zeros are the XML 1.0 ranges, and Roman notation has the range 1..5000.
// Values are supplied in display order; counting and ancestry belong to callers.
func formatXSLTNumber(ctx context.Context, values []float64, format, groupingSeparator, groupingSize, letterValue, lang string) (string, error) {
	p := parser{ctx: ctx}
	p.err = ctx.Err()
	if p.err != nil {
		return "", p.err
	}
	if len(values) == 0 {
		return "", nil
	}
	var out strings.Builder
	var tokens [1024]xsltNumberToken
	n, at := 0, 0
	for at < len(format) && format[at] != 0 {
		if !xsltNumberPoll(&p) {
			return "", p.err
		}
		r, size := utf8.DecodeRuneInString(format[at:])
		if xsltNumberAlphanumeric(r) {
			break
		}
		at += size
	}
	start := format[:at]
	end := ""
	for at < len(format) && format[at] != 0 && n < len(tokens) {
		if !xsltNumberPoll(&p) {
			return "", p.err
		}
		t := xsltNumberToken{zero: '0', width: 1}
		if n > 0 {
			t.separator = end
			end = ""
		}
		r, size := utf8.DecodeRuneInString(format[at:])
		if xsltNumberZero(r) || xsltNumberZero(r-1) {
			for xsltNumberZero(r) {
				t.width++
				at += size
				if !xsltNumberPoll(&p) {
					return "", p.err
				}
				r, size = xsltNumberRune(format, at)
			}
			if xsltNumberZero(r - 1) {
				t.zero = r - 1
				at += size
			} else {
				t.width = 1
			}
		} else if r == 'A' || r == 'a' || r == 'I' || r == 'i' {
			t.kind = r
			at += size
		}
		for at < len(format) && format[at] != 0 {
			if !xsltNumberPoll(&p) {
				return "", p.err
			}
			r, size = utf8.DecodeRuneInString(format[at:])
			if !xsltNumberAlphanumeric(r) {
				break
			}
			at += size
		}
		sep := at
		for at < len(format) && format[at] != 0 {
			if !xsltNumberPoll(&p) {
				return "", p.err
			}
			r, size = utf8.DecodeRuneInString(format[at:])
			if xsltNumberAlphanumeric(r) {
				break
			}
			at += size
		}
		end = format[sep:at]
		tokens[n] = t
		n++
	}
	groupRune, _ := utf8.DecodeRuneInString(groupingSeparator)
	if groupingSeparator == "" {
		groupRune = 0
	}
	group := xsltNumberGroupingSize(&p, groupingSize)
	if p.err != nil {
		return "", p.err
	}
	p.appendValue(&out, start)
	for i, value := range values {
		if !xsltNumberPoll(&p) {
			return "", p.err
		}
		t := xsltNumberToken{zero: '0', width: 1}
		if n > 0 {
			t = tokens[min(i, n-1)]
		}
		if i > 0 {
			sep := t.separator
			if sep == "" {
				sep = "."
			}
			p.appendValue(&out, sep)
		}
		value = math.Floor(value + 0.5)
		if value < 0 {
			value = 0
		}
		switch {
		case math.IsNaN(value):
			p.appendValue(&out, "NaN")
		case math.IsInf(value, 1):
			p.appendValue(&out, "Infinity")
		case (t.kind == 'A' || t.kind == 'a') && value >= 1:
			var digits [64]byte
			pos := len(digits)
			base := byte('A')
			if t.kind == 'a' {
				base = 'a'
			}
			for pos > 0 {
				value--
				pos--
				digits[pos] = base + byte(math.Mod(value, 26))
				value /= 26
				if value < 1 {
					break
				}
			}
			p.appendValue(&out, string(digits[pos:]))
		case (t.kind == 'I' || t.kind == 'i') && value >= 1 && value <= 5000:
			xsltNumberRoman(&p, &out, int(value), t.kind == 'i')
		default:
			xsltNumberDecimal(&p, &out, value, t.zero, t.width, group, groupRune)
		}
		if p.err != nil {
			return "", p.err
		}
	}
	p.appendValue(&out, end)
	if p.err == nil {
		p.err = ctx.Err()
	}
	if p.err != nil {
		return "", p.err
	}
	return out.String(), nil
}

type xsltNumberToken struct {
	separator  string
	zero, kind rune
	width      int
}

func xsltNumberRune(s string, at int) (rune, int) {
	if at >= len(s) || s[at] == 0 {
		return 0, 0
	}
	return utf8.DecodeRuneInString(s[at:])
}

func xsltNumberZero(r rune) bool {
	switch r {
	case 0x30, 0x660, 0x6f0, 0x966, 0x9e6, 0xa66, 0xae6, 0xb66, 0xc66, 0xce6, 0xd66, 0xe50, 0xed0, 0xf20:
		return true
	}
	return false
}

// grouping-size is scanf's signed decimal integer, including its accepted
// leading space/sign and trailing text. The pinned 64-bit libc saturates at
// signed long limits before narrowing to the instruction's signed int.
func xsltNumberGroupingSize(p *parser, s string) int {
	at := 0
	for at < len(s) && (s[at] == ' ' || s[at] >= '\t' && s[at] <= '\r') {
		if !xsltNumberPoll(p) {
			return 0
		}
		at++
	}
	negative := at < len(s) && s[at] == '-'
	if at < len(s) && (s[at] == '+' || s[at] == '-') {
		at++
	}
	limit := uint64(math.MaxInt64)
	if negative {
		limit++
	}
	var n uint64
	for at < len(s) && s[at] >= '0' && s[at] <= '9' {
		if !xsltNumberPoll(p) {
			return 0
		}
		d := uint64(s[at] - '0')
		if n > (limit-d)/10 {
			n = limit
		} else {
			n = n*10 + d
		}
		at++
	}
	if negative {
		return int(int32(-n))
	}
	return int(int32(n))
}

// The released helper builds backwards in a 500-byte local buffer including
// its NUL. Keep that observable truncation, including separator boundaries;
// never allocate in proportion to a supplied minimum width.
func xsltNumberDecimal(p *parser, out *strings.Builder, number float64, zero rune, width, group int, separator rune) {
	var storage [499]byte
	at := len(storage)
	var encoded [utf8.UTFMax]byte
	for i := 0; at > 0; i++ {
		if !xsltNumberPoll(p) {
			return
		}
		if i >= width && math.Abs(number) < 1 {
			break
		}
		if i > 0 && separator != 0 && group > 0 && i%group == 0 {
			size := utf8.EncodeRune(encoded[:], separator)
			if at < size {
				break
			}
			at -= size
			copy(storage[at:], encoded[:size])
		}
		v := math.Mod(number, 10)
		// On the pinned amd64 release the C double-to-int conversion of
		// nonfinite values yields INT_MIN. Its byte branch retains low bits.
		var digit rune
		if math.IsNaN(v) || math.IsInf(v, 0) {
			digit = -2147483648 + zero
		} else {
			digit = zero + rune(v)
		}
		if digit < 0x80 {
			if at == 0 {
				break
			}
			at--
			storage[at] = byte(digit)
		} else {
			size := utf8.EncodeRune(encoded[:], digit)
			if at < size {
				break
			}
			at -= size
			copy(storage[at:], encoded[:size])
		}
		number /= 10
	}
	p.appendValue(out, string(storage[at:]))
}

func xsltNumberPoll(p *parser) bool {
	if p.err == nil {
		p.err = p.ctx.Err()
	}
	return p.err == nil
}

func xsltNumberRoman(p *parser, out *strings.Builder, number int, lower bool) {
	values := [...]int{1000, 900, 500, 400, 100, 90, 50, 40, 10, 9, 5, 4, 1}
	upper := [...]string{"M", "CM", "D", "CD", "C", "XC", "L", "XL", "X", "IX", "V", "IV", "I"}
	low := [...]string{"m", "cm", "d", "cd", "c", "xc", "l", "xl", "x", "ix", "v", "iv", "i"}
	for i, value := range values {
		for number >= value {
			s := upper[i]
			if lower {
				s = low[i]
			}
			if !p.appendValue(out, s) {
				return
			}
			number -= value
		}
	}
}

// Fixed XML 1.0 BaseChar/Ideographic classes used by libxml2's IS_LETTER,
// not Go's evolving Unicode letters or the XML fifth-edition name grammar.
func xsltNumberAlphanumeric(r rune) bool {
	if r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= 0xc0 && r <= 0xd6 || r >= 0xd8 && r <= 0xf6 || r >= 0xf8 && r <= 0xff {
		return true
	}
	for _, zero := range [...]rune{0x660, 0x6f0, 0x966, 0x9e6, 0xa66, 0xae6, 0xb66, 0xbe6, 0xc66, 0xce6, 0xd66, 0xe50, 0xed0, 0xf20} {
		if r >= zero && r <= zero+9 && (zero != 0xbe6 || r != zero) {
			return true
		}
	}
	lo, hi := 0, len(xsltNumberLetterRanges)
	for lo < hi {
		mid := lo + (hi-lo)/2
		x := xsltNumberLetterRanges[mid]
		if r < x[0] {
			hi = mid
		} else if r > x[1] {
			lo = mid + 1
		} else {
			return true
		}
	}
	return false
}

var xsltNumberLetterRanges = [...][2]rune{
	{0x100, 0x131}, {0x134, 0x13e}, {0x141, 0x148}, {0x14a, 0x17e}, {0x180, 0x1c3}, {0x1cd, 0x1f0}, {0x1f4, 0x1f5}, {0x1fa, 0x217}, {0x250, 0x2a8}, {0x2bb, 0x2c1},
	{0x386, 0x386}, {0x388, 0x38a}, {0x38c, 0x38c}, {0x38e, 0x3a1}, {0x3a3, 0x3ce}, {0x3d0, 0x3d6}, {0x3da, 0x3da}, {0x3dc, 0x3dc}, {0x3de, 0x3de}, {0x3e0, 0x3e0}, {0x3e2, 0x3f3},
	{0x401, 0x40c}, {0x40e, 0x44f}, {0x451, 0x45c}, {0x45e, 0x481}, {0x490, 0x4c4}, {0x4c7, 0x4c8}, {0x4cb, 0x4cc}, {0x4d0, 0x4eb}, {0x4ee, 0x4f5}, {0x4f8, 0x4f9},
	{0x531, 0x556}, {0x559, 0x559}, {0x561, 0x586}, {0x5d0, 0x5ea}, {0x5f0, 0x5f2}, {0x621, 0x63a}, {0x641, 0x64a}, {0x671, 0x6b7}, {0x6ba, 0x6be}, {0x6c0, 0x6ce}, {0x6d0, 0x6d3}, {0x6d5, 0x6d5}, {0x6e5, 0x6e6},
	{0x905, 0x939}, {0x93d, 0x93d}, {0x958, 0x961}, {0x985, 0x98c}, {0x98f, 0x990}, {0x993, 0x9a8}, {0x9aa, 0x9b0}, {0x9b2, 0x9b2}, {0x9b6, 0x9b9}, {0x9dc, 0x9dd}, {0x9df, 0x9e1}, {0x9f0, 0x9f1},
	{0xa05, 0xa0a}, {0xa0f, 0xa10}, {0xa13, 0xa28}, {0xa2a, 0xa30}, {0xa32, 0xa33}, {0xa35, 0xa36}, {0xa38, 0xa39}, {0xa59, 0xa5c}, {0xa5e, 0xa5e}, {0xa72, 0xa74}, {0xa85, 0xa8b}, {0xa8d, 0xa8d}, {0xa8f, 0xa91}, {0xa93, 0xaa8}, {0xaaa, 0xab0}, {0xab2, 0xab3}, {0xab5, 0xab9}, {0xabd, 0xabd}, {0xae0, 0xae0},
	{0xb05, 0xb0c}, {0xb0f, 0xb10}, {0xb13, 0xb28}, {0xb2a, 0xb30}, {0xb32, 0xb33}, {0xb36, 0xb39}, {0xb3d, 0xb3d}, {0xb5c, 0xb5d}, {0xb5f, 0xb61}, {0xb85, 0xb8a}, {0xb8e, 0xb90}, {0xb92, 0xb95}, {0xb99, 0xb9a}, {0xb9c, 0xb9c}, {0xb9e, 0xb9f}, {0xba3, 0xba4}, {0xba8, 0xbaa}, {0xbae, 0xbb5}, {0xbb7, 0xbb9},
	{0xc05, 0xc0c}, {0xc0e, 0xc10}, {0xc12, 0xc28}, {0xc2a, 0xc33}, {0xc35, 0xc39}, {0xc60, 0xc61}, {0xc85, 0xc8c}, {0xc8e, 0xc90}, {0xc92, 0xca8}, {0xcaa, 0xcb3}, {0xcb5, 0xcb9}, {0xcde, 0xcde}, {0xce0, 0xce1}, {0xd05, 0xd0c}, {0xd0e, 0xd10}, {0xd12, 0xd28}, {0xd2a, 0xd39}, {0xd60, 0xd61},
	{0xe01, 0xe2e}, {0xe30, 0xe30}, {0xe32, 0xe33}, {0xe40, 0xe45}, {0xe81, 0xe82}, {0xe84, 0xe84}, {0xe87, 0xe88}, {0xe8a, 0xe8a}, {0xe8d, 0xe8d}, {0xe94, 0xe97}, {0xe99, 0xe9f}, {0xea1, 0xea3}, {0xea5, 0xea5}, {0xea7, 0xea7}, {0xeaa, 0xeab}, {0xead, 0xeae}, {0xeb0, 0xeb0}, {0xeb2, 0xeb3}, {0xebd, 0xebd}, {0xec0, 0xec4}, {0xf40, 0xf47}, {0xf49, 0xf69},
	{0x10a0, 0x10c5}, {0x10d0, 0x10f6}, {0x1100, 0x1100}, {0x1102, 0x1103}, {0x1105, 0x1107}, {0x1109, 0x1109}, {0x110b, 0x110c}, {0x110e, 0x1112}, {0x113c, 0x113c}, {0x113e, 0x113e}, {0x1140, 0x1140}, {0x114c, 0x114c}, {0x114e, 0x114e}, {0x1150, 0x1150}, {0x1154, 0x1155}, {0x1159, 0x1159}, {0x115f, 0x1161}, {0x1163, 0x1163}, {0x1165, 0x1165}, {0x1167, 0x1167}, {0x1169, 0x1169}, {0x116d, 0x116e}, {0x1172, 0x1173}, {0x1175, 0x1175}, {0x119e, 0x119e}, {0x11a8, 0x11a8}, {0x11ab, 0x11ab}, {0x11ae, 0x11af}, {0x11b7, 0x11b8}, {0x11ba, 0x11ba}, {0x11bc, 0x11c2}, {0x11eb, 0x11eb}, {0x11f0, 0x11f0}, {0x11f9, 0x11f9},
	{0x1e00, 0x1e9b}, {0x1ea0, 0x1ef9}, {0x1f00, 0x1f15}, {0x1f18, 0x1f1d}, {0x1f20, 0x1f45}, {0x1f48, 0x1f4d}, {0x1f50, 0x1f57}, {0x1f59, 0x1f59}, {0x1f5b, 0x1f5b}, {0x1f5d, 0x1f5d}, {0x1f5f, 0x1f7d}, {0x1f80, 0x1fb4}, {0x1fb6, 0x1fbc}, {0x1fbe, 0x1fbe}, {0x1fc2, 0x1fc4}, {0x1fc6, 0x1fcc}, {0x1fd0, 0x1fd3}, {0x1fd6, 0x1fdb}, {0x1fe0, 0x1fec}, {0x1ff2, 0x1ff4}, {0x1ff6, 0x1ffc}, {0x2126, 0x2126}, {0x212a, 0x212b}, {0x212e, 0x212e}, {0x2180, 0x2182},
	{0x3007, 0x3007}, {0x3021, 0x3029}, {0x3041, 0x3094}, {0x30a1, 0x30fa}, {0x3105, 0x312c}, {0x4e00, 0x9fa5}, {0xac00, 0xd7a3},
}

// A picture retains raw affix spans: libxslt compares their raw bytes (with
// quotes) before emitting escaped scalars. Positive digit counts always govern
// the negative subpattern as required by §12.3.
type xsltDecimalPicture struct {
	integerHash, integerDigits, fracDigits, fracHash, group, multiplier int
	addDecimal, multiplierSet                                           bool
	prefix, suffix                                                      xsltNumberAffix
	defaultSign                                                         bool
}
type xsltNumberAffix struct{ start, end, count int }

type xsltPictureScanner struct {
	p       *parser
	s       string
	at      int
	symbols xsltDecimalFormat
}

func (s *xsltPictureScanner) current() rune { r, _ := xsltNumberRune(s.s, s.at); return r }
func (s *xsltPictureScanner) next() bool {
	if !xsltNumberPoll(s.p) {
		return false
	}
	_, n := xsltNumberRune(s.s, s.at)
	if n == 0 {
		return false
	}
	s.at += n
	return true
}
func (s *xsltPictureScanner) special(r rune) bool {
	x := s.symbols
	return r == x.ZeroDigit || r == x.Digit || r == x.DecimalSeparator || r == x.GroupingSeparator || r == x.PatternSeparator
}
func (s *xsltPictureScanner) affix(info *xsltDecimalPicture) (xsltNumberAffix, bool) {
	a := xsltNumberAffix{start: s.at}
	for r := s.current(); r != 0; r = s.current() {
		if r == '\'' {
			if !s.next() || s.current() == 0 {
				return a, false
			}
		} else if s.special(r) {
			break
		} else if r == s.symbols.Percent || r == s.symbols.PerMille {
			if info.multiplierSet {
				return a, false
			}
			info.multiplier = 100
			if r == s.symbols.PerMille {
				info.multiplier = 1000
			}
			info.multiplierSet = true
		}
		_, size := xsltNumberRune(s.s, s.at)
		a.count += size
		if !s.next() {
			return a, false
		}
	}
	a.end = s.at
	return a, true
}

func (s *xsltPictureScanner) picture(negative bool) (xsltDecimalPicture, bool) {
	info := xsltDecimalPicture{group: -1, multiplier: 1}
	var ok bool
	info.prefix, ok = s.affix(&info)
	if !ok {
		return info, false
	}
	delayed, last := 0, s.at
	for r := s.current(); r != 0 && r != s.symbols.DecimalSeparator && r != s.symbols.PatternSeparator; r = s.current() {
		if delayed != 0 {
			info.multiplier = delayed
			info.multiplierSet = true
			delayed = 0
		}
		switch r {
		case s.symbols.Digit:
			if info.integerDigits > 0 {
				return info, false
			}
			info.integerHash++
			if info.group >= 0 {
				info.group++
			}
		case s.symbols.ZeroDigit:
			info.integerDigits++
			if info.group >= 0 {
				info.group++
			}
		case s.symbols.GroupingSeparator:
			info.group = 0
			if !s.next() {
				return info, false
			}
			continue
		case s.symbols.Percent, s.symbols.PerMille:
			if info.multiplierSet {
				return info, false
			}
			delayed = 100
			if r == s.symbols.PerMille {
				delayed = 1000
			}
		default:
			goto fraction
		}
		last = s.at
		if !s.next() {
			return info, false
		}
	}
fraction:
	if s.current() == s.symbols.DecimalSeparator {
		info.addDecimal = true
		if !s.next() {
			return info, false
		}
	}
	for r := s.current(); r != 0; r = s.current() {
		switch r {
		case s.symbols.ZeroDigit:
			if info.fracHash != 0 {
				return info, false
			}
			info.fracDigits++
		case s.symbols.Digit:
			info.fracHash++
		case s.symbols.Percent, s.symbols.PerMille:
			if info.multiplierSet {
				return info, false
			}
			delayed = 100
			if r == s.symbols.PerMille {
				delayed = 1000
			}
			last = s.at
			if !s.next() {
				return info, false
			}
			continue
		case s.symbols.GroupingSeparator:
		default:
			goto suffix
		}
		last = s.at
		if !s.next() {
			return info, false
		}
		if delayed != 0 {
			info.multiplier = delayed
			delayed = 0
			info.multiplierSet = true
		}
	}
suffix:
	if delayed != 0 {
		s.at = last
	}
	info.suffix, ok = s.affix(&info)
	if !ok || s.current() != 0 && s.current() != s.symbols.PatternSeparator {
		return info, false
	}
	if !negative {
		return info, true
	}
	// The source searches the first separator even inside an escaped affix.
	at := 0
	for at < len(s.s) {
		if !xsltNumberPoll(s.p) {
			return info, false
		}
		r, size := xsltNumberRune(s.s, at)
		if r == 0 {
			break
		}
		at += size
		if r == s.symbols.PatternSeparator {
			goto negativePattern
		}
	}
	info.defaultSign = true
	return info, true
negativePattern:
	s.at = at
	info.multiplierSet = false
	prefix, ok := s.affix(&info)
	if !ok {
		return info, false
	}
	delayed = 0
	for r := s.current(); r != 0; r = s.current() {
		if r == s.symbols.Percent || r == s.symbols.PerMille {
			if info.multiplierSet {
				return info, false
			}
			info.multiplierSet = true
			delayed = 1
		} else if s.special(r) {
			delayed = 0
		} else {
			break
		}
		last = s.at
		if !s.next() {
			return info, false
		}
	}
	if delayed != 0 {
		info.multiplierSet = false
		s.at = last
	}
	suffix, ok := s.affix(&info)
	if !ok || s.current() != 0 {
		return info, false
	}
	if !s.sameAffix(prefix, info.prefix) || !s.sameAffix(suffix, info.suffix) {
		info.prefix = prefix
		info.suffix = suffix
	}
	return info, true
}

func (s *xsltPictureScanner) sameAffix(a, b xsltNumberAffix) bool {
	if a.count != b.count {
		return false
	}
	for i := range a.count {
		if i%4096 == 0 && !xsltNumberPoll(s.p) {
			return false
		}
		if s.s[a.start+i] != s.s[b.start+i] {
			return false
		}
	}
	return true
}
func (s *xsltPictureScanner) emitAffix(out *strings.Builder, a xsltNumberAffix) {
	for at := a.start; at < a.end; {
		if !xsltNumberPoll(s.p) {
			return
		}
		if s.s[at] == '\'' {
			at++
		}
		r, n := xsltNumberRune(s.s, at)
		if n == 0 {
			return
		}
		if !s.p.appendScalar(out, r) {
			return
		}
		at += n
	}
}

// §12.3 uses a real decimal-picture parser, including the release's allowed
// malformed-picture recovery (0.0####), one-scalar quote escaping, and delayed
// suffix multipliers. NaN/Infinity bypass pictures; negative zero is unsigned.
func formatXSLTDecimal(ctx context.Context, number float64, mask string, symbols xsltDecimalFormat) (string, error) {
	p := parser{ctx: ctx}
	if p.err = ctx.Err(); p.err != nil {
		return "", p.err
	}
	var out strings.Builder
	if math.IsNaN(number) {
		p.appendValue(&out, symbols.NaN)
	} else if math.IsInf(number, 0) {
		if number < 0 {
			p.appendScalar(&out, symbols.MinusSign)
		}
		p.appendValue(&out, symbols.Infinity)
	} else {
		scanner := xsltPictureScanner{p: &p, s: mask, symbols: symbols}
		info, valid := scanner.picture(number < 0)
		if p.err != nil {
			return "", p.err
		}
		if !valid {
			info = xsltDecimalPicture{integerDigits: 1, fracDigits: 1, fracHash: 4, group: -1, multiplier: 1, addDecimal: true, defaultSign: number < 0}
		}
		if info.defaultSign {
			p.appendScalar(&out, symbols.MinusSign)
		}
		scanner.emitAffix(&out, info.prefix)
		number = math.Abs(number) * float64(info.multiplier)
		scale := math.Pow(10, float64(info.fracDigits+info.fracHash))
		number = math.Floor(scale*number+0.5) / scale
		// libxslt's numeric helper takes zeroDigit[0], the leading UTF8
		// byte, although its mask recognizer accepts the full scalar.
		var zeroBytes [utf8.UTFMax]byte
		utf8.EncodeRune(zeroBytes[:], symbols.ZeroDigit)
		zero := rune(zeroBytes[0])
		xsltNumberDecimal(&p, &out, math.Floor(number), zero, info.integerDigits, info.group, symbols.GroupingSeparator)
		if info.integerDigits+info.integerHash+info.fracDigits == 0 && info.fracHash > 0 {
			info.fracDigits++
			info.fracHash--
		}
		if math.Floor(number) == 0 && info.integerDigits+info.fracDigits == 0 {
			p.appendScalar(&out, symbols.ZeroDigit)
		}
		if info.fracDigits+info.fracHash == 0 {
			if info.addDecimal {
				p.appendScalar(&out, symbols.DecimalSeparator)
			}
		} else {
			number -= math.Floor(number)
			if number != 0 || info.fracDigits != 0 {
				p.appendScalar(&out, symbols.DecimalSeparator)
				number = math.Floor(scale*number + 0.5)
				j := info.fracHash
				for j > 0 {
					if !xsltNumberPoll(&p) {
						return "", p.err
					}
					if math.Mod(number, 10) >= 1 {
						break
					}
					number /= 10
					j--
				}
				xsltNumberDecimal(&p, &out, math.Floor(number), zero, info.fracDigits+j, 0, 0)
			}
		}
		scanner.emitAffix(&out, info.suffix)
	}
	if p.err == nil {
		p.err = ctx.Err()
	}
	if p.err != nil {
		return "", p.err
	}
	return out.String(), nil
}
