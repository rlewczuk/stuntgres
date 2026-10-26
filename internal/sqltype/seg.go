package sqltype

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"stuntgres/internal/sqlstate"
)

// Seg creates the allocated scalar identity owned by the registered provider.
func Seg(id ID, name, declaration string) (*Descriptor, error) {
	if id == 0 || id == Unknown || name == "" || declaration == "" {
		return nil, fmt.Errorf("invalid seg descriptor")
	}
	return &Descriptor{id: id, name: name, sql: declaration, database: "SEG", seg: true}, nil
}
func (d *Descriptor) IsSeg() bool { return d != nil && d.seg }

// SegDatum is the portable fixed representation. Words retain binary32 signed
// zero and precision independently of the deliberately lossy SQL rendering.
// Construction always goes through SegValue, including native replay.
type SegDatum struct {
	LowerBits   uint32 `json:"lower_bits"`
	UpperBits   uint32 `json:"upper_bits"`
	LowerDigits uint8  `json:"l_sigd"`
	UpperDigits uint8  `json:"u_sigd"`
	LowerMarker uint8  `json:"l_ext"`
	UpperMarker uint8  `json:"u_ext"`
}

func (s SegDatum) valid() bool {
	side := func(word uint32, digits, marker uint8, lower bool) bool {
		f := float64(math.Float32frombits(word))
		if marker == '-' {
			return digits == 0 && (lower && math.IsInf(f, -1) || !lower && math.IsInf(f, 1))
		}
		return digits >= 1 && digits <= 6 && (marker == 0 || marker == '<' || marker == '>' || marker == '~') && !math.IsNaN(f)
	}
	if !side(s.LowerBits, s.LowerDigits, s.LowerMarker, true) || !side(s.UpperBits, s.UpperDigits, s.UpperMarker, false) || s.LowerMarker == '-' && s.UpperMarker == '-' {
		return false
	}
	lower, upper := float64(math.Float32frombits(s.LowerBits)), float64(math.Float32frombits(s.UpperBits))
	lowerInf := math.IsInf(lower, 0) && s.LowerMarker != '-'
	upperInf := math.IsInf(upper, 0) && s.UpperMarker != '-'
	if lowerInf || upperInf {
		// Finite center/deviation arithmetic can overflow exactly one side;
		// it always discards both annotations. NaNs/two infinities cannot arise.
		return lowerInf != upperInf && s.LowerMarker == 0 && s.UpperMarker == 0
	}
	return lower <= upper || s.LowerMarker == 0 && s.UpperMarker == 0
}

// Reinputtable distinguishes stock seg_out spellings that stock seg_in accepts.
// Unchecked (+-) arithmetic can create infinity or inverted endpoints; these
// retain canonical output but require the bounded native typed replay path.
func (s SegDatum) Reinputtable() bool {
	if !s.valid() {
		return false
	}
	lower, upper := float64(math.Float32frombits(s.LowerBits)), float64(math.Float32frombits(s.UpperBits))
	return lower <= upper && (!math.IsInf(lower, 0) || s.LowerMarker == '-') && (!math.IsInf(upper, 0) || s.UpperMarker == '-')
}
func SegValue(d *Descriptor, s SegDatum) (Value, error) {
	if !d.IsSeg() || !s.valid() {
		return Value{}, fmt.Errorf("invalid seg payload")
	}
	return Value{typ: d, seg: s}, nil
}
func (v Value) SegDatum() (SegDatum, error) {
	if v.typ == nil || !v.typ.BaseType().IsSeg() || v.null || !v.seg.valid() {
		return SegDatum{}, fmt.Errorf("invalid seg payload")
	}
	return v.seg, nil
}

func segDigits(s string) uint8 {
	i := 0
	for i < len(s) && (s[i] == '0' || s[i] == '+' || s[i] == '-') {
		i++
	}
	zeroes := 1
	for i < len(s) && (s[i] == '0' || s[i] == '.') {
		if s[i] != '.' {
			zeroes++
		}
		i++
	}
	n := 0
	for i < len(s) && ((s[i] >= '0' && s[i] <= '9') || s[i] == '.') {
		if s[i] != '.' {
			n++
		}
		i++
	}
	if n == 0 {
		n = zeroes
	}
	return uint8(min(n, 6))
}

func segDigitRun(ctx context.Context, text string, pos int) (int, error) {
	for pos < len(text) && text[pos] >= '0' && text[pos] <= '9' {
		if pos&255 == 0 {
			if err := ctx.Err(); err != nil {
				return pos, err
			}
		}
		pos++
	}
	return pos, nil
}

// segNumberEnd implements the scanner token, not FLOAT4's broader input grammar.
// A point requires digits on both sides; an incomplete exponent is another token.
func segNumberEnd(ctx context.Context, text string, start int) (int, error) {
	pos := start
	if pos < len(text) && (text[pos] == '+' || text[pos] == '-') {
		pos++
	}
	digits := pos
	pos, err := segDigitRun(ctx, text, pos)
	if err != nil {
		return pos, err
	}
	if pos == digits {
		return start, nil
	}
	if pos+1 < len(text) && text[pos] == '.' && text[pos+1] >= '0' && text[pos+1] <= '9' {
		pos, err = segDigitRun(ctx, text, pos+1)
		if err != nil {
			return pos, err
		}
	}
	if pos < len(text) && (text[pos] == 'e' || text[pos] == 'E') {
		exponent := pos
		pos++
		if pos < len(text) && (text[pos] == '+' || text[pos] == '-') {
			pos++
		}
		digits = pos
		pos, err = segDigitRun(ctx, text, pos)
		if err != nil {
			return pos, err
		}
		if pos == digits {
			pos = exponent
		}
	}
	return pos, nil
}

type segScanner struct {
	text string
	pos  int
	ctx  context.Context
}

func (s *segScanner) skip() {
	for s.pos < len(s.text) && space(s.text[s.pos]) {
		s.pos++
	}
}
func (s *segScanner) syntax() error {
	s.skip()
	detail := "syntax error at end of input"
	if s.pos < len(s.text) {
		end, err := segNumberEnd(s.ctx, s.text, s.pos)
		if err != nil {
			return err
		}
		n := max(1, end-s.pos)
		if strings.HasPrefix(s.text[s.pos:], "...") {
			n = 3
		} else if strings.HasPrefix(s.text[s.pos:], "..") {
			n = 2
		} else if strings.HasPrefix(s.text[s.pos:], "(+-)") || strings.HasPrefix(s.text[s.pos:], "'+-'") {
			n = 4
		}
		detail = `syntax error at or near "` + s.text[s.pos:s.pos+n] + `"`
	}
	return sqlstate.SoftInputError(&sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "bad seg representation", Detail: detail})
}
func (s *segScanner) number(annotation bool) (float32, uint8, uint8, error) {
	s.skip()
	var marker uint8
	if annotation && s.pos < len(s.text) && strings.ContainsRune("<>~", rune(s.text[s.pos])) {
		marker = s.text[s.pos]
		s.pos++
		s.skip()
	}
	start := s.pos
	end, err := segNumberEnd(s.ctx, s.text, start)
	if err != nil {
		return 0, 0, 0, err
	}
	if end == start {
		return 0, 0, 0, s.syntax()
	}
	s.pos = end
	token := s.text[start:s.pos]
	v, e := (&Descriptor{id: Float4}).floatInput(token)
	if e != nil {
		return 0, 0, 0, e
	}
	return float32(v.floating), segDigits(token), marker, nil
}
func (s *segScanner) rangeToken() bool {
	s.skip()
	if strings.HasPrefix(s.text[s.pos:], "...") {
		s.pos += 3
		return true
	}
	if strings.HasPrefix(s.text[s.pos:], "..") {
		s.pos += 2
		return true
	}
	return false
}
func (d *Descriptor) segInput(ctx context.Context, text string) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	s := segScanner{text: text, ctx: ctx}
	var lower, upper float32
	var lowerDigits, upperDigits, lowerMarker, upperMarker uint8
	if s.rangeToken() {
		lower, lowerMarker = float32(math.Inf(-1)), '-'
		var err error
		upper, upperDigits, upperMarker, err = s.number(true)
		if err != nil {
			return Value{}, err
		}
	} else {
		var err error
		lower, lowerDigits, lowerMarker, err = s.number(true)
		if err != nil {
			return Value{}, err
		}
		if s.rangeToken() {
			s.skip()
			if s.pos == len(text) {
				upper, upperMarker = float32(math.Inf(1)), '-'
			} else {
				upper, upperDigits, upperMarker, err = s.number(true)
				if err != nil {
					return Value{}, err
				}
				if lower > upper {
					return Value{}, sqlstate.SoftInputError(failure("22023", fmt.Sprintf("swapped boundaries: %.6g is greater than %.6g", lower, upper)))
				}
			}
		} else {
			s.skip()
			if strings.HasPrefix(text[s.pos:], "(+-)") || strings.HasPrefix(text[s.pos:], "'+-'") {
				s.pos += 4
				deviation, digits, _, err := s.number(false)
				if err != nil {
					return Value{}, err
				}
				precision := max(lowerDigits, digits)
				// Each operation rounds separately to binary32. The released
				// uncertainty action discards annotations and does not reject
				// negative deviations or derived arithmetic overflow.
				upper = float32(lower + deviation)
				lower = float32(lower - deviation)
				var scratch [32]byte
				lowerDigits = max(precision, segDigits(string(strconv.AppendFloat(scratch[:0], float64(lower), 'g', 6, 32))))
				upperDigits = max(precision, segDigits(string(strconv.AppendFloat(scratch[:0], float64(upper), 'g', 6, 32))))
				lowerMarker, upperMarker = 0, 0
			} else {
				upper, upperDigits, upperMarker = lower, lowerDigits, lowerMarker
			}
		}
	}
	s.skip()
	if s.pos != len(text) {
		return Value{}, s.syntax()
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	return SegValue(d, SegDatum{
		LowerBits: math.Float32bits(lower), UpperBits: math.Float32bits(upper),
		LowerDigits: lowerDigits, UpperDigits: upperDigits,
		LowerMarker: lowerMarker, UpperMarker: upperMarker,
	})
}

// segRestore follows the released seg restore algorithm: precision is stored,
// positive decimal expansion must not invent significant trailing zeroes.
func segRestore(dst []byte, word uint32, precision uint8) []byte {
	var scratch [32]byte
	value := math.Float32frombits(word)
	if math.IsInf(float64(value), 0) {
		if value < 0 {
			return append(dst, "-Infinity"...)
		}
		return append(dst, "Infinity"...)
	}
	raw := strconv.AppendFloat(scratch[:0], float64(value), 'e', int(precision)-1, 32)
	exponentAt := 0
	for raw[exponentAt] != 'e' {
		exponentAt++
	}
	exponent, _ := strconv.Atoi(string(raw[exponentAt+1:]))
	if exponent == 0 {
		return append(dst, raw[:exponentAt]...)
	}
	if exponent > 4 || exponent < -4 {
		return append(dst, raw...)
	}
	start := 0
	if raw[0] == '-' {
		dst = append(dst, '-')
		start = 1
	}
	var digits [6]byte
	count := 0
	for _, digit := range raw[start:exponentAt] {
		if digit != '.' {
			digits[count] = digit
			count++
		}
	}
	if exponent > 0 {
		if exponent >= int(precision)-1 {
			// Decimal expansion here would imply significant trailing zeroes
			// absent from the stored precision. Keep the unpadded exponent.
			dst = append(dst, digits[0])
			if precision > 1 {
				dst = append(dst, '.')
				dst = append(dst, digits[1:count]...)
			}
			dst = append(dst, 'e')
			return strconv.AppendInt(dst, int64(exponent), 10)
		}
		dst = append(dst, digits[:exponent+1]...)
		dst = append(dst, '.')
		return append(dst, digits[exponent+1:count]...)
	}
	dst = append(dst, '0', '.')
	for zero := -1; zero > exponent; zero-- {
		dst = append(dst, '0')
	}
	return append(dst, digits[:count]...)
}
func (d *Descriptor) segOutput(ctx context.Context, v Value) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if v.typ != d || v.null || !v.seg.valid() {
		return "", fmt.Errorf("invalid seg payload")
	}
	s := v.seg
	var scratch [64]byte
	out := scratch[:0]
	marker := func(m byte) {
		if m == '<' || m == '>' || m == '~' {
			out = append(out, m)
		}
	}
	marker(s.LowerMarker)
	// seg_out intentionally ignores upper precision when choosing point output.
	if math.Float32frombits(s.LowerBits) == math.Float32frombits(s.UpperBits) && s.LowerMarker == s.UpperMarker {
		out = segRestore(out, s.LowerBits, s.LowerDigits)
	} else {
		if s.LowerMarker != '-' {
			out = segRestore(out, s.LowerBits, s.LowerDigits)
			out = append(out, ' ')
		}
		out = append(out, '.', '.')
		if s.UpperMarker != '-' {
			out = append(out, ' ')
			marker(s.UpperMarker)
			out = segRestore(out, s.UpperBits, s.UpperDigits)
		}
	}
	return string(out), nil
}

// RemapSeg is used only after the catalog importer admits the source/destination
// provider identities; unlike Retag, that operation may allocate a new OID.
func (v Value) RemapSeg(target *Descriptor) (Value, error) {
	if v.typ == nil || !v.typ.IsSeg() || !target.IsSeg() || !v.null && !v.seg.valid() {
		return Value{}, fmt.Errorf("incompatible imported seg")
	}
	v.typ = target
	return v, nil
}
