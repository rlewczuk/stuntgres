package sqltype

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"stuntgres/internal/sqlstate"
)

// Timestamp follows REL_18_0 timestamp.h: microseconds from 2000, with
// infinities outside the finite domain. Civil arithmetic never uses UnixNano.
const (
	TimestampNegativeInfinity int64 = math.MinInt64
	TimestampPositiveInfinity int64 = math.MaxInt64
	timestampMinimum          int64 = -211813488000000000
	timestampEnd              int64 = 9223371331200000000
	microsPerDay              int64 = 86400000000
)

type InputEnvironment struct {
	TransactionStart time.Time
	Zone             *Zone
	DateStyle        DateStyle
	IntervalStyle    IntervalStyle
	Regclass         RegclassResolver
}

type RegclassResolver interface {
	ResolveRegclass(context.Context, string) (uint32, error)
}

func TimestampValue(d *Descriptor, n int64) (Value, error) {
	if d == nil || (d.id != Timestamp && d.id != Timestamptz) {
		return Value{}, fmt.Errorf("invalid timestamp descriptor")
	}
	if n != TimestampNegativeInfinity && n != TimestampPositiveInfinity && (n < timestampMinimum || n > timestampEnd) {
		return Value{}, failure("22008", "timestamp out of range")
	}
	return Value{typ: d, integer: n}, nil
}
func (v Value) Timestamp() (int64, error) {
	if v.null || v.typ == nil || (v.typ.id != Timestamp && v.typ.id != Timestamptz) {
		return 0, fmt.Errorf("not a timestamp value")
	}
	return v.integer, nil
}
func (d *Descriptor) validTypmod(p int32) bool {
	if d.IsArray() {
		return d.element.validTypmod(p)
	}
	return p == -1 || isBitString(d.id) && p >= 1 && p <= maximumBitLength || d.id == Interval && intervalTypmodValid(p) || (d.id == Timestamp || d.id == Time || d.id == Timestamptz || d.id == Timetz) && p >= 0 && p <= 6 || d.id == Numeric && numericTypmodValid(p) || (d.id == BPChar || d.id == VarChar) && p >= 5 && p <= maximumCharacterLength+4
}
func (u Usage) Valid() bool {
	if !u.Type.Concrete() || !u.Type.validTypmod(u.Typmod) {
		return false
	}
	return u.validCollation()
}

func (u Usage) validCollation() bool {
	if u.Type.IsDomain() {
		base := u.BaseUsage()
		base.Collation = u.Collation
		return base.validCollation()
	}
	if u.Type.IsArray() {
		u.Type = u.Type.element
		return u.validCollation()
	}
	if IsCharacter(u.Type.ID()) || u.Type.ID() == Name {
		return u.Collation.Derivation == Default && (u.Collation.ID == 100 || u.Collation.ID == 950 || u.Collation.ID == 951 || u.Collation.ID == 811 || u.Collation.ID == 6411)
	}
	return u.Collation == u.Type.Usage().Collation
}

// CheckModifierSupport rejects absent type-modifier providers before the
// statement owner interprets raw modifier expressions.
func (d *Descriptor) CheckModifierSupport() error {
	if d.IsArray() {
		return d.element.CheckModifierSupport()
	}
	if d.IsUserDefined() {
		return failure("42601", fmt.Sprintf(`type modifier is not allowed for type "%s"`, d.DiagnosticName()))
	}
	switch d.id {
	case Bit, VarBit, Numeric, Timestamp, Time, Timestamptz, Timetz, Interval, BPChar, VarChar:
		return nil
	case Int2, Int4, Int8, JSON, JSONB:
		return failure("42601", fmt.Sprintf(`type modifier is not allowed for type "%s"`, d.DiagnosticName()))
	case Text:
		return failure("42601", `type modifier is not allowed for type "text"`)
	case Date:
		return failure("42601", `type modifier is not allowed for type "date"`)
	case Bytea:
		return failure("42601", `type modifier is not allowed for type "bytea"`)
	case Name:
		return failure("42601", `type modifier is not allowed for type "name"`)
	default:
		return fmt.Errorf("type modifiers are unsupported")
	}
}

// ResolveModifiers returns an effective usage and optional provider warning.
// Source positions and warning delivery belong to the statement owner.
func (d *Descriptor) ResolveModifiers(modifiers ...int32) (Usage, *sqlstate.Error, error) {
	if d.IsArray() {
		u, warning, err := d.element.ResolveModifiers(modifiers...)
		u.Type = d
		return u, warning, err
	}
	u := d.Usage()
	if err := d.CheckModifierSupport(); err != nil {
		return u, nil, err
	}
	if isBitString(d.id) {
		u, err := d.bitModifiers(modifiers)
		return u, nil, err
	}
	if d.id == Interval {
		return d.intervalModifiers(modifiers)
	}
	if d.id == BPChar || d.id == VarChar {
		if len(modifiers) != 1 {
			return u, nil, failure("22023", "invalid type modifier")
		}
		name := "char"
		if d.id == VarChar {
			name = "varchar"
		}
		n := modifiers[0]
		if n < 1 {
			return u, nil, failure("22023", "length for type "+name+" must be at least 1")
		}
		if n > maximumCharacterLength {
			return u, nil, failure("22023", "length for type "+name+" cannot exceed 10485760")
		}
		u.Typmod = n + 4
		return u, nil, nil
	}
	if d.id == Numeric {
		if len(modifiers) < 1 || len(modifiers) > 2 {
			return u, nil, failure("22023", "invalid NUMERIC type modifier")
		}
		p, s := modifiers[0], int32(0)
		if len(modifiers) == 2 {
			s = modifiers[1]
		}
		if p < 1 || p > 1000 {
			return u, nil, failure("22023", fmt.Sprintf("NUMERIC precision %d must be between 1 and 1000", p))
		}
		if s < -1000 || s > 1000 {
			return u, nil, failure("22023", fmt.Sprintf("NUMERIC scale %d must be between -1000 and 1000", s))
		}
		u.Typmod = ((p << 16) | (s & 2047)) + 4
		return u, nil, nil
	}
	if len(modifiers) != 1 {
		return u, nil, fmt.Errorf("type modifiers are unsupported")
	}
	p := modifiers[0]
	name := ""
	if p < 0 || p > 6 {
		base, suffix := d.SQLName(), ""
		if d.id == Timestamptz {
			base, suffix = "TIMESTAMP", " WITH TIME ZONE"
		}
		if d.id == Timetz {
			base, suffix = "TIME", " WITH TIME ZONE"
		}
		name = fmt.Sprintf("%s(%d)%s", base, p, suffix)
	}
	if p < 0 {
		return u, nil, failure("22023", name+" precision must not be negative")
	}
	u.Typmod = p
	if p > 6 {
		u.Typmod = 6
		return u, &sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: name + " precision reduced to maximum allowed, 6"}, nil
	}
	return u, nil, nil
}
func (u Usage) Declaration() (string, error) {
	if !u.Valid() {
		return "", fmt.Errorf("invalid declaration usage")
	}
	if u.Type.IsArray() {
		u.Type = u.Type.element
		declaration, err := u.Declaration()
		return declaration + "[]", err
	}
	if isBitString(u.Type.id) {
		if u.Typmod >= 0 {
			return fmt.Sprintf("%s(%d)", u.Type.SQLName(), u.Typmod), nil
		}
		if u.Type.id == Bit {
			return `"bit"`, nil
		}
		return "BIT VARYING", nil
	}
	if u.Type.id == Interval {
		return intervalDeclaration(u.Typmod), nil
	}
	if u.Type.id == Numeric && u.Typmod >= 0 {
		p, s := numericParts(u.Typmod)
		return fmt.Sprintf("NUMERIC(%d,%d)", p, s), nil
	}
	if (u.Type.id == BPChar || u.Type.id == VarChar) && u.Typmod >= 0 {
		name := "CHARACTER"
		if u.Type.id == VarChar {
			name = "VARCHAR"
		}
		return fmt.Sprintf("%s(%d)", name, u.Typmod-4), nil
	}
	if u.Type.id == Timestamptz || u.Type.id == Timetz {
		name := "TIMESTAMP"
		if u.Type.id == Timetz {
			name = "TIME"
		}
		if u.Typmod >= 0 {
			name = fmt.Sprintf("%s(%d)", name, u.Typmod)
		}
		return name + " WITH TIME ZONE", nil
	}
	if u.Typmod >= 0 {
		return fmt.Sprintf("%s(%d)", u.Type.SQLName(), u.Typmod), nil
	}
	return u.Type.SQLName(), nil
}
func roundTimestamp(n int64, p int32) int64 {
	if p < 0 || p == 6 || n == TimestampNegativeInfinity || n == TimestampPositiveInfinity {
		return n
	}
	unit := int64(1)
	for i := p; i < 6; i++ {
		unit *= 10
	}
	q, r := n/unit, n%unit
	if r >= unit/2 {
		q++
	} else if r <= -unit/2 {
		q--
	}
	return q * unit
}
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b < 0 {
		q--
	}
	return q
}

// Gregorian civil algorithms use an astronomical year and a March-based era.
func civilDay(y, m, d int) int64 {
	yy := int64(y)
	if m <= 2 {
		yy--
	}
	era := floorDiv(yy, 400)
	yoe := yy - era*400
	mm := int64(m)
	if m > 2 {
		mm -= 3
	} else {
		mm += 9
	}
	return era*146097 + yoe*365 + yoe/4 - yoe/100 + (153*mm+2)/5 + int64(d) - 1 - 730425
}
func dayCivil(day int64) (int, int, int) {
	z := day + 730425
	era := floorDiv(z, 146097)
	doe := z - era*146097
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	y := yoe + era*400
	doy := doe - (365*yoe + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153
	d := doy - (153*mp+2)/5 + 1
	m := mp + 3
	if mp >= 10 {
		m = mp - 9
	}
	if m <= 2 {
		y++
	}
	return int(y), int(m), int(d)
}
func leap(y int) bool { return y%4 == 0 && (y%100 != 0 || y%400 == 0) }
func monthDays(y, m int) int {
	days := [12]int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
	if m == 2 && leap(y) {
		return 29
	}
	return days[m-1]
}
func civilTimestamp(y, m, d, h, min, sec, usec int) (int64, error) {
	if m < 1 || m > 12 || d < 1 || d > monthDays(y, m) || h < 0 || h > 24 || min < 0 || min > 59 || sec < 0 || sec > 60 || usec < 0 || usec > 1000000 || int64(h)*3600000000+int64(min)*60000000+int64(sec)*1000000+int64(usec) > microsPerDay {
		return 0, fmt.Errorf("invalid civil fields")
	}
	if y < -4713 || y > 294276 {
		return 0, fmt.Errorf("timestamp out of range")
	}
	days := civilDay(y, m, d)
	tod := int64(h)*3600000000 + int64(min)*60000000 + int64(sec)*1000000 + int64(usec)
	if days < timestampMinimum/microsPerDay || days >= timestampEnd/microsPerDay {
		return 0, fmt.Errorf("timestamp out of range")
	}
	n := days*microsPerDay + tod
	if n < timestampMinimum || n >= timestampEnd {
		return 0, fmt.Errorf("timestamp out of range")
	}
	return n, nil
}
func timestampOutput(n int64) string {
	if n == TimestampNegativeInfinity {
		return "-infinity"
	}
	if n == TimestampPositiveInfinity {
		return "infinity"
	}
	days := floorDiv(n, microsPerDay)
	tod := n - days*microsPerDay
	y, m, d := dayCivil(days)
	bc := y <= 0
	if bc {
		y = 1 - y
	}
	s := fmt.Sprintf("%04d-%02d-%02d %02d:%02d:%02d", y, m, d, tod/3600000000, tod/60000000%60, tod/1000000%60)
	if f := tod % 1000000; f != 0 {
		s += "." + strings.TrimRight(fmt.Sprintf("%06d", f), "0")
	}
	if bc {
		s += " BC"
	}
	return s
}
func (v Value) TimestampTime() (time.Time, error) {
	n, err := v.Timestamp()
	if err != nil {
		return time.Time{}, err
	}
	if n == TimestampNegativeInfinity || n == TimestampPositiveInfinity {
		return time.Time{}, fmt.Errorf("infinite timestamp")
	}
	day := floorDiv(n, microsPerDay)
	tod := n - day*microsPerDay
	y, m, d := dayCivil(day)
	return time.Date(y, time.Month(m), d, int(tod/3600000000), int(tod/60000000%60), int(tod/1000000%60), int(tod%1000000)*1000, time.UTC), nil
}
func TimestampFromTime(d *Descriptor, t time.Time) (Value, error) {
	y, m, day := t.Date()
	// The serialized date's integer decode precedes its zone decode; finite
	// timestamp range validation follows both. Avoid forming 1-y for BC here.
	if y > math.MaxInt32 || y < 1-math.MaxInt32 {
		return Value{}, timestampError(timestampTimeInput(t), "field", false)
	}
	_, offset := t.Zone()
	const maxOffset = 15*60*60 + 59*60 + 59
	if offset < -maxOffset || offset > maxOffset {
		return Value{}, timestampError(timestampTimeInput(t), "zone", false)
	}
	h, min, s := t.Clock()
	// Reproduce decimal fractional-second input's binary64 conversion before
	// scaling; dividing nanoseconds directly by 1000 changes midpoint results.
	fractionalSeconds := float64(t.Nanosecond()) / 1_000_000_000
	u := int(math.RoundToEven(fractionalSeconds * 1_000_000))
	n, err := civilTimestamp(y, int(m), day, h, min, s, u)
	if err != nil {
		return Value{}, timestampError(timestampTimeInput(t), "range", false)
	}
	return TimestampValue(d, n)
}

// Format only failed civil/zone imports; successful coercion stays allocation-free.
func timestampTimeInput(t time.Time) string {
	bc := t.Year() <= 0
	if bc {
		t = t.AddDate((-t.Year())*2+1, 0, 0)
	}
	text := t.Format("2006-01-02 15:04:05.999999999Z07:00")
	_, offset := t.Zone()
	if seconds := offset % 60; seconds != 0 {
		if seconds < 0 {
			seconds = -seconds
		}
		text += fmt.Sprintf(":%02d", seconds)
	}
	if bc {
		text += " BC"
	}
	return text
}

type dateToken struct {
	text string
	kind byte
}

func asciiDigit(c byte) bool { return c >= '0' && c <= '9' }
func asciiAlpha(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

func timestampASCIILower(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

// The comparison key is canonical lowercase ASCII. Input spans stay borrowed.
func timestampASCIIEqual(s, lower string) bool {
	if len(s) != len(lower) {
		return false
	}
	for i := 0; i < len(s); i++ {
		if timestampASCIILower(s[i]) != lower[i] {
			return false
		}
	}
	return true
}

func coreDateWord(s string) bool {
	if monthNumber(s) != 0 || weekday(s) {
		return true
	}
	switch {
	case timestampASCIIEqual(s, "+infinity"), timestampASCIIEqual(s, "-infinity"),
		timestampASCIIEqual(s, "ad"), timestampASCIIEqual(s, "allballs"),
		timestampASCIIEqual(s, "am"), timestampASCIIEqual(s, "at"), timestampASCIIEqual(s, "bc"),
		timestampASCIIEqual(s, "d"), timestampASCIIEqual(s, "dow"), timestampASCIIEqual(s, "doy"),
		timestampASCIIEqual(s, "dst"), timestampASCIIEqual(s, "epoch"), timestampASCIIEqual(s, "h"),
		timestampASCIIEqual(s, "infinity"), timestampASCIIEqual(s, "isodow"), timestampASCIIEqual(s, "isoyear"),
		timestampASCIIEqual(s, "j"), timestampASCIIEqual(s, "jd"), timestampASCIIEqual(s, "julian"),
		timestampASCIIEqual(s, "m"), timestampASCIIEqual(s, "mm"), timestampASCIIEqual(s, "now"),
		timestampASCIIEqual(s, "on"), timestampASCIIEqual(s, "pm"), timestampASCIIEqual(s, "s"),
		timestampASCIIEqual(s, "t"), timestampASCIIEqual(s, "today"), timestampASCIIEqual(s, "tomorrow"),
		timestampASCIIEqual(s, "y"), timestampASCIIEqual(s, "yesterday"):
		return true
	}
	return false
}

// Token categories and punctuation boundaries mirror release ParseDateTime.
// Fixed fields and the release token-buffer budget bound decoder storage.
func tokenizeTimestamp(s string) ([25]dateToken, int, bool) {
	return tokenizeDateTime(s, 153)
}
func tokenizeDateTime(s string, budget int) ([25]dateToken, int, bool) {
	var fields [25]dateToken
	nf, used := 0, 0
	for i := 0; i < len(s); {
		if space(s[i]) {
			i++
			continue
		}
		if nf == len(fields) {
			return fields, 0, false
		}
		start := i
		kind := byte('s')
		switch {
		case asciiDigit(s[i]):
			i++
			for i < len(s) && asciiDigit(s[i]) {
				i++
			}
			kind = 'n'
			if i < len(s) && s[i] == ':' {
				kind = 't'
				i++
				for i < len(s) && (asciiDigit(s[i]) || s[i] == ':' || s[i] == '.') {
					i++
				}
			}
			if kind == 'n' && i < len(s) && (s[i] == '-' || s[i] == '/' || s[i] == '.') {
				delim := s[i]
				i++
				kind = 'd'
				if i < len(s) && asciiDigit(s[i]) {
					if delim == '.' {
						kind = 'n'
					}
					for i < len(s) && asciiDigit(s[i]) {
						i++
					}
					if i < len(s) && s[i] == delim {
						kind = 'd'
						i++
						for i < len(s) && (asciiDigit(s[i]) || s[i] == delim) {
							i++
						}
					}
				} else {
					for i < len(s) && (asciiDigit(s[i]) || asciiAlpha(s[i]) || s[i] == delim) {
						i++
					}
				}
			}
		case asciiAlpha(s[i]):
			i++
			for i < len(s) && asciiAlpha(s[i]) {
				i++
			}
			core := i-start <= 9 && coreDateWord(s[start:i])
			if i < len(s) && (s[i] == '-' || s[i] == '/' || s[i] == '.' || (s[i] == '+' || asciiDigit(s[i])) && !core) {
				kind = 'd'
				i++
				for i < len(s) && (asciiDigit(s[i]) || asciiAlpha(s[i]) || strings.ContainsRune("+-/_.:", rune(s[i]))) {
					i++
				}
			}
		case s[i] == '+' || s[i] == '-':
			i++
			for i < len(s) && space(s[i]) {
				i++
			}
			if i == len(s) {
				return fields, 0, false
			}
			if asciiDigit(s[i]) {
				kind = 'z'
				i++
				for i < len(s) && (asciiDigit(s[i]) || s[i] == ':' || s[i] == '.' || s[i] == '-') {
					i++
				}
			} else if asciiAlpha(s[i]) {
				kind = 's'
				i++
				for i < len(s) && asciiAlpha(s[i]) {
					i++
				}
			} else {
				return fields, 0, false
			}
		case s[i] == '.':
			kind = 'n'
			i++
			for i < len(s) && asciiDigit(s[i]) {
				i++
			}
		default:
			if s[i] >= 127 || s[i] < 33 {
				return fields, 0, false
			}
			i++
			continue
		}
		token := s[start:i]
		tokenLen := len(token)
		if token[0] == '+' || token[0] == '-' {
			tokenLen = 1 + len(strings.TrimLeft(token[1:], " \t\n\r\f\v"))
		}
		used += tokenLen + 1
		if nf == len(fields) || used > budget {
			return fields, 0, false
		}
		fields[nf] = dateToken{token, kind}
		nf++
	}
	return fields, nf, true
}
func monthNumber(s string) int {
	switch {
	case timestampASCIIEqual(s, "jan"), timestampASCIIEqual(s, "january"):
		return 1
	case timestampASCIIEqual(s, "feb"), timestampASCIIEqual(s, "february"):
		return 2
	case timestampASCIIEqual(s, "mar"), timestampASCIIEqual(s, "march"):
		return 3
	case timestampASCIIEqual(s, "apr"), timestampASCIIEqual(s, "april"):
		return 4
	case timestampASCIIEqual(s, "may"):
		return 5
	case timestampASCIIEqual(s, "jun"), timestampASCIIEqual(s, "june"):
		return 6
	case timestampASCIIEqual(s, "jul"), timestampASCIIEqual(s, "july"):
		return 7
	case timestampASCIIEqual(s, "aug"), timestampASCIIEqual(s, "august"):
		return 8
	case timestampASCIIEqual(s, "sep"), timestampASCIIEqual(s, "sept"), timestampASCIIEqual(s, "september"):
		return 9
	case timestampASCIIEqual(s, "oct"), timestampASCIIEqual(s, "october"):
		return 10
	case timestampASCIIEqual(s, "nov"), timestampASCIIEqual(s, "november"):
		return 11
	case timestampASCIIEqual(s, "dec"), timestampASCIIEqual(s, "december"):
		return 12
	}
	return 0
}
func weekday(s string) bool {
	switch {
	case timestampASCIIEqual(s, "sun"), timestampASCIIEqual(s, "sunday"),
		timestampASCIIEqual(s, "mon"), timestampASCIIEqual(s, "monday"),
		timestampASCIIEqual(s, "tue"), timestampASCIIEqual(s, "tues"), timestampASCIIEqual(s, "tuesday"),
		timestampASCIIEqual(s, "wed"), timestampASCIIEqual(s, "weds"), timestampASCIIEqual(s, "wednesday"),
		timestampASCIIEqual(s, "thu"), timestampASCIIEqual(s, "thur"), timestampASCIIEqual(s, "thurs"), timestampASCIIEqual(s, "thursday"),
		timestampASCIIEqual(s, "fri"), timestampASCIIEqual(s, "friday"),
		timestampASCIIEqual(s, "sat"), timestampASCIIEqual(s, "saturday"):
		return true
	}
	return false
}

type timestampDecodeStatus uint8

const (
	timestampDecoded timestampDecodeStatus = iota
	timestampMalformed
	timestampFieldOverflow
	timestampZoneOverflow
)

func (status timestampDecodeStatus) inputErrorFor(text, typeName string) error {
	kind := ""
	if status == timestampFieldOverflow {
		kind = "field"
	} else if status == timestampZoneOverflow {
		kind = "zone"
	}
	return civilInputError(typeName, text, kind, false)
}

// Match strtoint/strtoi64 prefix consumption and their field-specific bounds.
// Overflow precedes a malformed tail; callers own delimiter/full-field checks.
func timestampIntegerPrefix(s string, maximum int64) (int64, int, timestampDecodeStatus) {
	i, negative := 0, false
	limit := uint64(maximum)
	if len(s) > 0 && (s[0] == '+' || s[0] == '-') {
		negative = s[0] == '-'
		i++
		if negative {
			limit++
		}
	}
	start := i
	var n uint64
	cutoff, lastDigit := limit/10, limit%10
	for i < len(s) && asciiDigit(s[i]) {
		digit := uint64(s[i] - '0')
		if n > cutoff || n == cutoff && digit > lastDigit {
			return 0, i, timestampFieldOverflow
		}
		n = n*10 + digit
		i++
	}
	if i == start {
		return 0, 0, timestampDecoded
	}
	if negative {
		return -int64(n), i, timestampDecoded
	}
	return int64(n), i, timestampDecoded
}

func decimal(s string) (int, timestampDecodeStatus) {
	n, end, status := timestampIntegerPrefix(s, math.MaxInt32)
	if status != timestampDecoded {
		return 0, status
	}
	if end == 0 || end != len(s) {
		return 0, timestampMalformed
	}
	return int(n), timestampDecoded
}

// ParseFraction accepts the dot alone as zero. Seconds and Julian days apply
// their distinct release conversions to this shared decimal value.
func fraction(s string) (float64, timestampDecodeStatus) {
	if s == "" || s[0] != '.' {
		return 0, timestampMalformed
	}
	if len(s) == 1 {
		return 0, timestampDecoded
	}
	for i := 1; i < len(s); i++ {
		if !asciiDigit(s[i]) {
			return 0, timestampMalformed
		}
	}
	f, e := strconv.ParseFloat(s, 64)
	if e != nil {
		return 0, timestampMalformed
	}
	return f, timestampDecoded
}

const (
	dateYear     = 1
	dateMonth    = 2
	dateDay      = 4
	dateComplete = 7
)

type timestampDecoder struct {
	y, m, d, h, min, s, u, mask                                             int
	short, textMonth, timeSet, zoneSet, eraSet, bc, merSet, weekSet, julian bool
	dstSet, daylightSet                                                     bool
	mer                                                                     int
	doy                                                                     int
	zone                                                                    zoneSpecification
	order                                                                   DateOrder
}

func (p *timestampDecoder) number(s string, textMonth bool) timestampDecodeStatus {
	value, end, status := timestampIntegerPrefix(s, math.MaxInt32)
	if status != timestampDecoded {
		return status
	}
	if end == 0 {
		return timestampMalformed
	}
	if end < len(s) {
		if s[end] != '.' {
			return timestampMalformed
		}
		if end > 2 {
			return p.compact(s)
		}
		f, status := fraction(s[end:])
		if status != timestampDecoded {
			return status
		}
		p.u = int(math.RoundToEven(f * 1000000))
	}
	n := int(value)
	if p.mask == dateComplete {
		return p.compact(s)
	}
	if p.mask == dateYear && len(s) == 3 && n >= 1 && n <= 366 {
		p.doy = n
		p.mask = dateComplete
		return timestampDecoded
	}
	switch p.mask {
	case 0:
		if len(s) >= 3 || p.order == DateYMD {
			p.y = n
			p.short = len(s) <= 2
			p.mask |= dateYear
		} else if p.order == DateDMY {
			p.d = n
			p.mask |= dateDay
		} else {
			p.m = n
			p.mask |= dateMonth
		}
	case dateYear:
		p.m = n
		p.mask |= dateMonth
	case dateDay:
		p.m = n
		p.mask |= dateMonth
	case dateMonth:
		if textMonth && len(s) >= 3 {
			p.y = n
			p.short = false
			p.mask |= dateYear
		} else {
			p.d = n
			p.mask |= dateDay
		}
	case dateYear | dateMonth:
		p.d = n
		p.mask |= dateDay
	case dateMonth | dateDay:
		p.y = n
		p.short = len(s) <= 2
		p.mask |= dateYear
	default:
		return timestampMalformed
	}
	return timestampDecoded
}

func (p *timestampDecoder) compact(s string) timestampDecodeStatus {
	base, _, hasFraction := strings.Cut(s, ".")
	u := 0
	if hasFraction {
		f, status := fraction(s[len(base):])
		if status != timestampDecoded {
			return status
		}
		u = int(math.RoundToEven(f * 1000000))
	}
	if p.mask != dateComplete && !hasFraction && len(base) >= 6 {
		if p.mask != 0 {
			return timestampMalformed
		}
		y, status := decimal(base[:len(base)-4])
		if status != timestampDecoded {
			return status
		}
		m, status := decimal(base[len(base)-4 : len(base)-2])
		if status != timestampDecoded {
			return status
		}
		d, status := decimal(base[len(base)-2:])
		if status != timestampDecoded {
			return status
		}
		p.y, p.m, p.d, p.mask, p.short = y, m, d, dateComplete, len(base)-4 == 2
		return timestampDecoded
	}
	if p.timeSet || len(base) != 4 && len(base) != 6 {
		return timestampMalformed
	}
	h, status := decimal(base[:2])
	if status != timestampDecoded {
		return status
	}
	min, status := decimal(base[2:4])
	if status != timestampDecoded {
		return status
	}
	sec := 0
	if len(base) == 6 {
		sec, status = decimal(base[4:])
		if status != timestampDecoded {
			return status
		}
	}
	p.h, p.min, p.s, p.u, p.timeSet = h, min, sec, u, true
	return timestampDecoded
}

// clockComponents mirrors DecodeTimeCommon plus DecodeTime's integer-hour
// bound. Whole-clock domain and duplicate masks belong to the caller.
func (p *timestampDecoder) clockComponents(s string) timestampDecodeStatus {
	h, end, status := timestampIntegerPrefix(s, math.MaxInt64)
	if status != timestampDecoded {
		return status
	}
	if end == len(s) || s[end] != ':' {
		return timestampMalformed
	}
	tail := s[end+1:]
	mi, end, status := timestampIntegerPrefix(tail, math.MaxInt32)
	if status != timestampDecoded {
		return status
	}
	se, u := int64(0), 0
	if end < len(tail) {
		if tail[end] == '.' {
			f, status := fraction(tail[end:])
			if status != timestampDecoded {
				return status
			}
			u = int(math.RoundToEven(f * 1000000))
			if h > math.MaxInt32 || h < math.MinInt32 {
				return timestampFieldOverflow
			}
			h, mi, se = 0, h, mi
		} else if tail[end] == ':' {
			tail = tail[end+1:]
			se, end, status = timestampIntegerPrefix(tail, math.MaxInt32)
			if status != timestampDecoded {
				return status
			}
			if end < len(tail) {
				f, status := fraction(tail[end:])
				if status != timestampDecoded {
					return status
				}
				u = int(math.RoundToEven(f * 1000000))
			}
		} else {
			return timestampMalformed
		}
	}
	if h < 0 || h > math.MaxInt32 || mi < 0 || mi > 59 || se < 0 || se > 60 || u < 0 || u > 1000000 {
		return timestampFieldOverflow
	}
	p.h, p.min, p.s, p.u = int(h), int(mi), int(se), u
	return timestampDecoded
}

func (p *timestampDecoder) clock(s string) timestampDecodeStatus {
	if status := p.clockComponents(s); status != timestampDecoded {
		return status
	}
	// DecodeDateTime checks whole-clock range before duplicate-field masks.
	if p.h > 24 || int64(p.h)*3600000000+int64(p.min)*60000000+int64(p.s)*1000000+int64(p.u) > microsPerDay {
		return timestampFieldOverflow
	}
	if p.timeSet {
		return timestampMalformed
	}
	p.timeSet = true
	return timestampDecoded
}

func (p *timestampDecoder) date(s string) timestampDecodeStatus {
	var parts [25]string
	n := 0
	// DecodeDate has its own text-month interpretation flag, unlike its shared
	// accumulated date mask. An earlier outer month must not change this field.
	textMonth := false
	for i := 0; i < len(s); {
		for i < len(s) && !asciiDigit(s[i]) && !asciiAlpha(s[i]) {
			i++
		}
		if i == len(s) {
			return timestampMalformed
		}
		start := i
		if asciiDigit(s[i]) {
			for i < len(s) && asciiDigit(s[i]) {
				i++
			}
		} else {
			for i < len(s) && asciiAlpha(s[i]) {
				i++
			}
		}
		if n == len(parts) {
			return timestampMalformed
		}
		parts[n] = s[start:i]
		n++
		// DecodeDate consumes one following character with each component.
		// Further separators must lead to another component, not an empty tail.
		if i < len(s) {
			i++
		}
	}
	if n == 0 {
		return timestampMalformed
	}
	// Reject unknown text before numeric decoding. Release IGNORE_DTF words
	// at/on skip only this pass and remain for the numeric pass's error order.
	for i, s := range parts[:n] {
		if m := monthNumber(s); m != 0 {
			if p.mask&dateMonth != 0 {
				return timestampMalformed
			}
			p.m, textMonth = m, true
			p.mask |= dateMonth
			parts[i] = "" // Mark the month consumed, as DecodeDate does.
		} else if asciiAlpha(s[0]) && !timestampASCIIEqual(s, "at") && !timestampASCIIEqual(s, "on") {
			return timestampMalformed
		}
	}
	for _, s := range parts[:n] {
		if s == "" {
			continue
		}
		if status := p.number(s, textMonth); status != timestampDecoded {
			return status
		}
	}
	if !p.decodeDateComplete() {
		return timestampMalformed
	}
	return timestampDecoded
}

// DecodeDate's global completion mask excludes only DOY and TZ. Other consumed
// fields remain relevant here even though their ordinary decoder paths can mix.
func (p *timestampDecoder) decodeDateComplete() bool {
	return p.mask == dateComplete && !p.timeSet && !p.weekSet && !p.eraSet && !p.merSet && !p.daylightSet
}

func timestampError(text, kind string, hint bool) error {
	return civilInputError("timestamp", text, kind, hint)
}
func civilInputError(typeName, text, kind string, hint bool) error {
	if typeName == "timestamptz" {
		typeName = "timestamp with time zone"
	}
	if typeName == "timetz" {
		typeName = "time with time zone"
	}
	code, msg := "22007", fmt.Sprintf("invalid input syntax for type %s: \"%s\"", typeName, text)
	switch kind {
	case "field":
		code, msg = "22008", fmt.Sprintf("date/time field value out of range: \"%s\"", text)
	case "range":
		// PostgreSQL uses the shared timestamp range label for both timestamp
		// input functions, but retains the zoned type name for syntax errors.
		if typeName == "timestamp with time zone" {
			typeName = "timestamp"
		}
		code, msg = "22008", fmt.Sprintf("%s out of range: \"%s\"", typeName, text)
	case "zone":
		code, msg = "22009", fmt.Sprintf("time zone displacement out of range: \"%s\"", text)
	}
	e := failure(code, msg).(*sqlstate.Error)
	if hint {
		e.Hint = "Perhaps you need a different \"DateStyle\" setting."
	}
	return e
}
func numericZone(s string) timestampDecodeStatus {
	if len(s) < 2 || s[0] != '+' && s[0] != '-' {
		return timestampMalformed
	}
	s = trim(s[1:])
	h, end, status := timestampIntegerPrefix(s, math.MaxInt32)
	if status == timestampFieldOverflow {
		return timestampZoneOverflow
	}
	m, sec := int64(0), int64(0)
	if end < len(s) && s[end] == ':' {
		part := s[end+1:]
		var consumed int
		m, consumed, status = timestampIntegerPrefix(part, math.MaxInt32)
		if status == timestampFieldOverflow {
			return timestampZoneOverflow
		}
		end += 1 + consumed
		if end < len(s) && s[end] == ':' {
			part = s[end+1:]
			sec, consumed, status = timestampIntegerPrefix(part, math.MaxInt32)
			if status == timestampFieldOverflow {
				return timestampZoneOverflow
			}
			end += 1 + consumed
		}
	} else if end == len(s) && len(s) > 2 {
		m = h % 100
		h /= 100
	}
	// Release checks displacement bounds before rejecting an unconsumed tail.
	if h < 0 || h > 15 || m < 0 || m > 59 || sec < 0 || sec > 59 {
		return timestampZoneOverflow
	}
	if end != len(s) {
		return timestampMalformed
	}
	return timestampDecoded
}
