package sqltype

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// civilInput shares DecodeDateTime field decoding and calendar completion.
// DATE and TIMESTAMP deliberately finalize the decoded civil fields separately.
func (d *Descriptor) civilInput(text string, typmod int32, env InputEnvironment) (Value, error) {
	typeName := d.Name()
	timestampError := func(text, kind string, hint bool) error {
		return civilInputError(typeName, text, kind, hint)
	}
	fields, n, ok := tokenizeTimestamp(text)
	if !ok || n == 0 {
		return Value{}, timestampError(text, "", false)
	}
	p := timestampDecoder{order: env.DateStyle.Order, zone: zoneSpecification{zone: env.Zone}}
	// Special results still consume fields and check the whole input. The
	// caller ignores civil components for epoch and either infinity.
	const (
		civilResult = iota
		epochResult
		positiveInfinityResult
		negativeInfinityResult
	)
	result := civilResult
	const (
		noPrefix = iota
		julianPrefix
		timePrefix
		unsupportedPrefix
	)
	prefix := noPrefix
	namedZone := false
	for i := range n {
		f := fields[i]
		s := f.text
		if prefix == julianPrefix && (f.kind == 'n' || f.kind == 'd') {
			prefix = noPrefix
			v, end, status := timestampIntegerPrefix(s, math.MaxInt32)
			if status != timestampDecoded {
				return Value{}, status.inputErrorFor(text, typeName)
			}
			// A pending Julian NUMBER may begin with a fraction: strtoint
			// leaves the integer at zero, then the fraction claims TIME.
			if end == 0 && (f.kind != 'n' || s[0] != '.') {
				return Value{}, timestampError(text, "", false)
			}
			if v < 0 {
				return Value{}, timestampError(text, "field", false)
			}
			claimsTime, claimsZone := false, false
			us := int64(0)
			if f.kind == 'd' {
				// Release's Julian DTK_DATE branch expects a numeric zone,
				// not a fraction: a bare trailing dot is consequently invalid.
				if status := p.setNumericZone(s[end:]); status != timestampDecoded {
					return Value{}, status.inputErrorFor(text, typeName)
				}
				claimsTime, claimsZone = true, true
			} else if end < len(s) {
				f, status := fraction(s[end:])
				if status != timestampDecoded {
					return Value{}, status.inputErrorFor(text, typeName)
				}
				us = int64(f * float64(microsPerDay))
				claimsTime = true
			}
			if p.mask != 0 || claimsTime && p.timeSet || claimsZone && p.zoneSet {
				return Value{}, timestampError(text, "", false)
			}
			p.y, p.m, p.d = dayCivil(v - 2451545)
			p.mask, p.julian = dateComplete, true
			if claimsTime {
				p.h, p.min, p.s, p.u, p.timeSet = int(us/3600000000), int(us/60000000%60), int(us/1000000%60), int(us%1000000), true
			}
			p.zoneSet = p.zoneSet || claimsZone
			continue
		}
		if f.kind == 'z' {
			if status := p.setNumericZone(s); status != timestampDecoded {
				return Value{}, status.inputErrorFor(text, typeName)
			}
			if p.zoneSet {
				return Value{}, timestampError(text, "", false)
			}
			p.zoneSet = true
			continue
		}
		if f.kind == 't' {
			if prefix != noPrefix && prefix != timePrefix {
				return Value{}, timestampError(text, "", false)
			}
			if status := p.clock(s); status != timestampDecoded {
				return Value{}, status.inputErrorFor(text, typeName)
			}
			prefix = noPrefix
			continue
		}
		if f.kind == 'd' {
			if prefix != noPrefix || p.mask&(dateMonth|dateDay) == dateMonth|dateDay {
				if asciiDigit(s[0]) || prefix != noPrefix {
					if prefix != noPrefix && prefix != timePrefix {
						return Value{}, timestampError(text, "", false)
					}
					prefix = noPrefix
					if p.timeSet {
						return Value{}, timestampError(text, "", false)
					}
					split := strings.IndexByte(s, '-')
					if split > 0 {
						if status := p.setNumericZone(s[split:]); status != timestampDecoded {
							return Value{}, status.inputErrorFor(text, typeName)
						}
						if status := p.compact(s[:split]); status != timestampDecoded {
							return Value{}, status.inputErrorFor(text, typeName)
						}
						if p.zoneSet {
							return Value{}, timestampError(text, "", false)
						}
						p.zoneSet = true
						prefix = noPrefix
						continue
					}
					return Value{}, timestampError(text, "", false)
				}
				zone, err := resolveParsedInputZone(s, env.Zone)
				if err != nil {
					return Value{}, err
				}
				p.zone = zone
				if p.zoneSet {
					return Value{}, timestampError(text, "", false)
				}
				p.zoneSet = true
				namedZone = true
				continue
			}
			if status := p.date(s); status != timestampDecoded {
				return Value{}, status.inputErrorFor(text, typeName)
			}
			continue
		}
		if f.kind == 'n' {
			if prefix != noPrefix {
				_, end, status := timestampIntegerPrefix(s, math.MaxInt32)
				if status != timestampDecoded {
					return Value{}, status.inputErrorFor(text, typeName)
				}
				if end < len(s) && s[end] != '.' {
					return Value{}, timestampError(text, "", false)
				}
				if prefix != timePrefix {
					return Value{}, timestampError(text, "", false)
				}
				if status := p.compact(s); status != timestampDecoded {
					return Value{}, status.inputErrorFor(text, typeName)
				}
				prefix = noPrefix
				continue
			}
			if p.mask == 0 && strings.Contains(s, ".") {
				if status := p.date(s); status != timestampDecoded {
					return Value{}, status.inputErrorFor(text, typeName)
				}
				continue
			}
			dot := strings.IndexByte(s, '.')
			if dot > 2 || len(s) >= 6 && (p.mask == 0 || !p.timeSet) {
				if status := p.compact(s); status != timestampDecoded {
					return Value{}, status.inputErrorFor(text, typeName)
				}
				prefix = noPrefix
			} else if status := p.number(s, p.textMonth); status != timestampDecoded {
				return Value{}, status.inputErrorFor(text, typeName)
			}
			continue
		}
		if m := monthNumber(s); m != 0 {
			if p.mask&dateMonth != 0 && p.mask&dateDay == 0 && !p.textMonth && p.m >= 1 && p.m <= 31 {
				p.d = p.m
				p.mask = p.mask&^dateMonth | dateDay
			}
			if p.mask&dateMonth != 0 {
				return Value{}, timestampError(text, "", false)
			}
			p.m, p.textMonth = m, true
			p.mask |= dateMonth
			continue
		}
		if weekday(s) {
			if p.weekSet {
				return Value{}, timestampError(text, "", false)
			}
			p.weekSet = true
			continue
		}
		switch {
		case timestampASCIIEqual(s, "at"), timestampASCIIEqual(s, "on"):
			continue
		case timestampASCIIEqual(s, "epoch"), timestampASCIIEqual(s, "infinity"),
			len(s) > 1 && (s[0] == '+' || s[0] == '-') &&
				timestampASCIIEqual(strings.TrimLeft(s[1:], " \t\n\r\f\v"), "infinity"):
			if p.mask != 0 || p.timeSet || p.zoneSet {
				return Value{}, timestampError(text, "", false)
			}
			p.mask, p.timeSet, p.zoneSet = dateComplete, true, true
			switch {
			case timestampASCIIEqual(s, "epoch"):
				result = epochResult
			case s[0] == '-':
				result = negativeInfinityResult
			default:
				result = positiveInfinityResult
			}
			continue
		case timestampASCIIEqual(s, "dst"):
			if p.daylightSet {
				return Value{}, timestampError(text, "", false)
			}
			p.dstSet, p.daylightSet = true, true
			continue
		case timestampASCIIEqual(s, "ad"), timestampASCIIEqual(s, "bc"):
			if p.eraSet {
				return Value{}, timestampError(text, "", false)
			}
			p.eraSet, p.bc = true, timestampASCIIEqual(s, "bc")
			continue
		case timestampASCIIEqual(s, "am"), timestampASCIIEqual(s, "pm"):
			if p.merSet {
				return Value{}, timestampError(text, "", false)
			}
			p.merSet = true
			if timestampASCIIEqual(s, "pm") {
				p.mer = 12
			}
			continue
		case timestampASCIIEqual(s, "t"):
			if p.mask != dateComplete || prefix != noPrefix {
				return Value{}, timestampError(text, "", false)
			}
			prefix = timePrefix
			continue
		case timestampASCIIEqual(s, "j"), timestampASCIIEqual(s, "jd"), timestampASCIIEqual(s, "julian"):
			if prefix != noPrefix {
				return Value{}, timestampError(text, "", false)
			}
			prefix = julianPrefix
			continue
		case timestampASCIIEqual(s, "y"), timestampASCIIEqual(s, "m"), timestampASCIIEqual(s, "d"),
			timestampASCIIEqual(s, "h"), timestampASCIIEqual(s, "mm"), timestampASCIIEqual(s, "s"),
			timestampASCIIEqual(s, "dow"), timestampASCIIEqual(s, "doy"),
			timestampASCIIEqual(s, "isodow"), timestampASCIIEqual(s, "isoyear"):
			if prefix != noPrefix {
				return Value{}, timestampError(text, "", false)
			}
			prefix = unsupportedPrefix
			continue
		case timestampASCIIEqual(s, "allballs"):
			if p.timeSet || p.zoneSet {
				return Value{}, timestampError(text, "", false)
			}
			p.timeSet, p.zoneSet = true, true
			p.zone = zoneSpecification{fixed: true}
			continue
		case timestampASCIIEqual(s, "now"), timestampASCIIEqual(s, "today"), timestampASCIIEqual(s, "tomorrow"), timestampASCIIEqual(s, "yesterday"):
			if p.mask != 0 || timestampASCIIEqual(s, "now") && (p.timeSet || p.zoneSet) {
				return Value{}, timestampError(text, "", false)
			}
			if env.TransactionStart.IsZero() {
				return Value{}, fmt.Errorf("missing timestamp input environment")
			}
			east, _ := env.Zone.OffsetAt(env.TransactionStart.Unix())
			t := env.TransactionStart.UTC().Add(time.Duration(east) * time.Second)
			y, m, day := t.Date()
			days := civilDay(y, int(m), day)
			if timestampASCIIEqual(s, "tomorrow") {
				days++
			} else if timestampASCIIEqual(s, "yesterday") {
				days--
			}
			p.y, p.m, p.d = dayCivil(days)
			p.mask = dateComplete
			if timestampASCIIEqual(s, "now") {
				p.h, p.min, p.s = t.Clock()
				p.u = int(math.RoundToEven(float64(t.Nanosecond()) / 1000))
				p.timeSet, p.zoneSet = true, true
				p.zone = zoneSpecification{east: east, fixed: true}
			}
			continue
		}
		zone, err := resolveParsedInputZone(s, env.Zone)
		kind := inputAbbreviationKind(s, env.Zone)
		if p.zoneSet || err != nil || kind == timestampDaylightZone && p.daylightSet {
			return Value{}, timestampError(text, "", false)
		}
		p.zone, p.zoneSet = zone, true
		p.daylightSet = p.daylightSet || kind == timestampDaylightZone
		namedZone = namedZone || !zone.fixed
	}
	if prefix != noPrefix {
		return Value{}, timestampError(text, "", false)
	}
	switch result {
	case epochResult:
		if d.id == Date {
			return DateValue(d, -10957)
		}
		v, _ := civilTimestamp(1970, 1, 1, 0, 0, 0, 0)
		return TimestampValue(d, roundTimestamp(v, typmod))
	case positiveInfinityResult:
		if d.id == Date {
			return DateValue(d, DatePositiveInfinity)
		}
		return TimestampValue(d, TimestampPositiveInfinity)
	case negativeInfinityResult:
		if d.id == Date {
			return DateValue(d, DateNegativeInfinity)
		}
		return TimestampValue(d, TimestampNegativeInfinity)
	}
	// Validate consumed date fields before rejecting an incomplete outer date.
	// DecodeDate's own completion check still runs earlier for delimited inputs.
	if p.mask&dateYear != 0 && !p.julian {
		if p.bc {
			if p.y <= 0 {
				return Value{}, timestampError(text, "field", false)
			}
			p.y = 1 - p.y
		} else if p.short {
			if p.y < 70 {
				p.y += 2000
			} else if p.y < 100 {
				p.y += 1900
			}
		} else if p.y <= 0 {
			return Value{}, timestampError(text, "field", false)
		}
	}
	if p.doy != 0 {
		p.y, p.m, p.d = dayCivil(civilDay(p.y, 1, 1) + int64(p.doy) - 1)
	}
	if p.mask&dateMonth != 0 && (p.m < 1 || p.m > 12) ||
		p.mask&dateDay != 0 && (p.d < 1 || p.d > 31) {
		return Value{}, timestampError(text, "field", true)
	}
	if p.mask == dateComplete && p.d > monthDays(p.y, p.m) {
		return Value{}, timestampError(text, "field", false)
	}
	if p.merSet {
		if p.h > 12 {
			return Value{}, timestampError(text, "field", false)
		}
		p.h = p.h%12 + p.mer
	}
	if p.mask != dateComplete {
		return Value{}, timestampError(text, "", false)
	}
	if p.dstSet && (!p.zoneSet || namedZone) {
		return Value{}, timestampError(text, "", false)
	}
	if d.id == Date {
		return d.dateFromCivil(text, p.y, p.m, p.d)
	}
	// Colon clocks are range-checked by clock(). Compact fields instead retain
	// the release tm2timestamp summation semantics (for example 250000 carries).
	// Check the finite result after summing, not an invented compact-clock range.
	if p.y < -4713 || p.y == -4713 && p.m < 11 || p.y > 294276 {
		return Value{}, timestampError(text, "range", false)
	}
	days := civilDay(p.y, p.m, p.d)
	tod := int64(p.h)*3600000000 + int64(p.min)*60000000 + int64(p.s)*1000000 + int64(p.u)
	if days < timestampMinimum/microsPerDay-1 || days > timestampEnd/microsPerDay {
		return Value{}, timestampError(text, "range", false)
	}
	nval := days*microsPerDay + tod
	if d.id == Timestamptz {
		east := p.zone.offsetCivil(floorDiv(nval, 1000000) + 946684800)
		if p.dstSet {
			east += 3600
		}
		nval -= int64(east) * 1000000
	}
	if nval < timestampMinimum || nval >= timestampEnd {
		return Value{}, timestampError(text, "range", false)
	}
	// PG checks finite input before destination sizing, not a second time after it.
	return Value{typ: d, integer: roundTimestamp(nval, typmod)}, nil
}
