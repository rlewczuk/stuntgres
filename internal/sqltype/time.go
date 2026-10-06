package sqltype

import (
	"fmt"
	"math"
	"strings"
)

// TIME is an inline civil clock, including the distinct end-of-day endpoint.
func TimeValue(d *Descriptor, n int64) (Value, error) {
	if d == nil || d.id != Time {
		return Value{}, fmt.Errorf("invalid time descriptor")
	}
	if n < 0 || n > microsPerDay {
		return Value{}, failure("22008", "time out of range")
	}
	return Value{typ: d, integer: n}, nil
}

func (v Value) Time() (int64, error) {
	if v.null || v.typ == nil || v.typ.id != Time {
		return 0, fmt.Errorf("not a time value")
	}
	return v.integer, nil
}

func timeOutput(n int64) string {
	s := fmt.Sprintf("%02d:%02d:%02d", n/3600000000, n/60000000%60, n/1000000%60)
	if fraction := n % 1000000; fraction != 0 {
		s += "." + strings.TrimRight(fmt.Sprintf("%06d", fraction), "0")
	}
	return s
}

func timeInputError(text string, status timestampDecodeStatus, hint bool) error {
	if status == timestampMalformed {
		return failure("22007", fmt.Sprintf("invalid input syntax for type time: \"%s\"", text))
	}
	kind := "field"
	if status == timestampZoneOverflow {
		kind = "zone"
	}
	return timestampError(text, kind, hint)
}

// DecodeTimeOnly has its own field-order/completion rules; it is not timestamp
// input with an invented date. Shared civil helpers preserve release numeric,
// token-buffer, fraction and DecodeTime error precedence.
func (d *Descriptor) timeInput(text string, typmod int32, env InputEnvironment) (Value, error) {
	fields, n, ok := tokenizeTimestamp(text)
	bad := func(status timestampDecodeStatus) (Value, error) {
		return Value{}, timeInputError(text, status, false)
	}
	if !ok || n == 0 {
		return bad(timestampMalformed)
	}
	p := timestampDecoder{}
	const (
		noPrefix = iota
		timePrefix
		julianPrefix
		unsupportedPrefix
	)
	prefix := noPrefix
	namedZone, dynamicZone := "", false
	compact := func(s string) timestampDecodeStatus {
		mask := p.mask
		p.mask = dateComplete // DecodeTimeOnly supplies DATE_M to DecodeNumber.
		status := p.compact(s)
		p.mask = mask
		return status
	}
	for i := range n {
		f := fields[i]
		s := f.text
		status := timestampDecoded
		switch f.kind {
		case 'd':
			if i == 0 && n >= 2 && (fields[n-1].kind == 'd' || fields[1].kind == 't') {
				status = p.date(s)
			} else if asciiDigit(s[0]) {
				if p.timeSet {
					return bad(timestampMalformed)
				}
				index := strings.IndexByte(s, '-')
				if index < 0 {
					return bad(timestampMalformed)
				}
				status = numericZone(s[index:])
				if status == timestampDecoded {
					status = compact(s[:index])
				}
				if p.zoneSet {
					return bad(timestampMalformed)
				}
				p.zoneSet = true
			} else {
				if timestampZoneKind(s) != timestampNamedZone {
					return Value{}, failure("22023", fmt.Sprintf("time zone \"%s\" not recognized", strings.ToLower(s)))
				}
				if p.zoneSet {
					return bad(timestampMalformed)
				}
				p.zoneSet, namedZone = true, s
			}
		case 't':
			if prefix != noPrefix && prefix != timePrefix {
				return bad(timestampMalformed)
			}
			prefix = noPrefix
			status = p.clockComponents(s)
			if status == timestampDecoded && p.timeSet {
				status = timestampMalformed
			}
			if status == timestampDecoded {
				p.timeSet = true
			}
		case 'z':
			status = numericZone(s)
			if status == timestampDecoded && p.zoneSet {
				return bad(timestampMalformed)
			}
			p.zoneSet = true
		case 'n':
			if prefix != noPrefix {
				v, end, decoded := timestampIntegerPrefix(s, math.MaxInt32)
				if decoded != timestampDecoded {
					return bad(decoded)
				}
				if end < len(s) && s[end] != '.' {
					return bad(timestampMalformed)
				}
				switch prefix {
				case timePrefix:
					status = compact(s)
				case julianPrefix:
					if v < 0 {
						return bad(timestampFieldOverflow)
					}
					if p.mask != 0 {
						return bad(timestampMalformed)
					}
					p.y, p.m, p.d = dayCivil(v - 2451545)
					p.mask, p.julian = dateComplete, true
					if end < len(s) {
						fraction, decoded := fraction(s[end:])
						if decoded != timestampDecoded {
							return bad(decoded)
						}
						if p.timeSet {
							return bad(timestampMalformed)
						}
						us := int64(fraction * float64(microsPerDay))
						p.h, p.min, p.s, p.u, p.timeSet = int(us/3600000000), int(us/60000000%60), int(us/1000000%60), int(us%1000000), true
					}
				default:
					return bad(timestampMalformed)
				}
				prefix = noPrefix
			} else if strings.Contains(s, ".") && i == 0 && n >= 2 && fields[n-1].kind == 'd' {
				status = p.date(s)
			} else {
				// DecodeNumber with DATE_M admits only a four-digit clock;
				// DecodeNumberField also admits six digits and fractions.
				status = compact(s)
			}
		case 's':
			kind := timestampAbbreviationKind(s)
			if kind != timestampUnknownZone {
				if p.zoneSet || kind == timestampDaylightZone && p.daylightSet {
					return bad(timestampMalformed)
				}
				p.zoneSet = true
				p.daylightSet = p.daylightSet || kind == timestampDaylightZone
				dynamicZone = kind == timestampDynamicZone
				break
			}
			switch {
			case timestampASCIIEqual(s, "at"), timestampASCIIEqual(s, "on"):
				continue
			case weekday(s):
				// DecodeTimeOnly rejects reserved DOW tokens, unlike DecodeDateTime.
				return bad(timestampMalformed)
			case timestampASCIIEqual(s, "am"), timestampASCIIEqual(s, "pm"):
				if p.merSet {
					return bad(timestampMalformed)
				}
				p.merSet = true
				if timestampASCIIEqual(s, "pm") {
					p.mer = 12
				}
			case timestampASCIIEqual(s, "ad"), timestampASCIIEqual(s, "bc"):
				if p.eraSet {
					return bad(timestampMalformed)
				}
				p.eraSet, p.bc = true, timestampASCIIEqual(s, "bc")
			case timestampASCIIEqual(s, "dst"):
				if p.dstSet || p.daylightSet {
					return bad(timestampMalformed)
				}
				p.dstSet, p.daylightSet = true, true
			case timestampASCIIEqual(s, "now"):
				if p.timeSet {
					return bad(timestampMalformed)
				}
				if env.TransactionStart.IsZero() {
					return Value{}, fmt.Errorf("missing time input environment")
				}
				t := env.TransactionStart.UTC()
				p.h, p.min, p.s = t.Clock()
				p.u, p.timeSet = int(math.RoundToEven(float64(t.Nanosecond())/1000)), true
			case timestampASCIIEqual(s, "allballs"):
				if p.timeSet || p.zoneSet {
					return bad(timestampMalformed)
				}
				p.timeSet, p.zoneSet = true, true
			case timestampASCIIEqual(s, "t"), timestampASCIIEqual(s, "j"), timestampASCIIEqual(s, "jd"), timestampASCIIEqual(s, "julian"):
				if prefix != noPrefix {
					return bad(timestampMalformed)
				}
				prefix = julianPrefix
				if timestampASCIIEqual(s, "t") {
					prefix = timePrefix
				}
			case timestampASCIIEqual(s, "y"), timestampASCIIEqual(s, "m"), timestampASCIIEqual(s, "d"), timestampASCIIEqual(s, "h"), timestampASCIIEqual(s, "mm"), timestampASCIIEqual(s, "s"), timestampASCIIEqual(s, "dow"), timestampASCIIEqual(s, "doy"), timestampASCIIEqual(s, "isodow"), timestampASCIIEqual(s, "isoyear"):
				if prefix != noPrefix {
					return bad(timestampMalformed)
				}
				prefix = unsupportedPrefix
			default:
				if coreDateWord(s) || timestampZoneKind(s) != timestampNamedZone || p.zoneSet {
					return bad(timestampMalformed)
				}
				p.zoneSet, namedZone = true, s
			}
		default:
			return bad(timestampMalformed)
		}
		if status != timestampDecoded {
			return bad(status)
		}
	}
	if prefix != noPrefix {
		return bad(timestampMalformed)
	}
	// ValidateDate runs before clock completion/range, even for discarded dates.
	if p.mask&dateYear != 0 && !p.julian {
		if p.bc {
			if p.y <= 0 {
				return bad(timestampFieldOverflow)
			}
			p.y = 1 - p.y
		} else if p.short {
			if p.y < 70 {
				p.y += 2000
			} else if p.y < 100 {
				p.y += 1900
			}
		} else if p.y <= 0 {
			return bad(timestampFieldOverflow)
		}
	}
	if p.doy != 0 {
		p.y, p.m, p.d = dayCivil(civilDay(p.y, 1, 1) + int64(p.doy) - 1)
	}
	if p.mask&dateMonth != 0 && (p.m < 1 || p.m > 12) || p.mask&dateDay != 0 && (p.d < 1 || p.d > 31) {
		return Value{}, timeInputError(text, timestampFieldOverflow, true)
	}
	if p.mask == dateComplete && p.d > monthDays(p.y, p.m) {
		return bad(timestampFieldOverflow)
	}
	if p.merSet {
		if p.h > 12 {
			return bad(timestampFieldOverflow)
		}
		if p.h == 12 && p.mer == 0 {
			p.h = 0
		} else if p.h != 12 && p.mer == 12 {
			p.h += 12
		}
	}
	nval := int64(p.h)*3600000000 + int64(p.min)*60000000 + int64(p.s)*1000000 + int64(p.u)
	if p.h < 0 || p.h > 24 || p.min < 0 || p.min > 59 || p.s < 0 || p.s > 60 || p.u < 0 || p.u > 1000000 || nval > microsPerDay {
		return bad(timestampFieldOverflow)
	}
	if !p.timeSet || p.dstSet && (!p.zoneSet || namedZone != "" || dynamicZone) || namedZone != "" && !fixedTimeZone(namedZone) && p.mask != dateComplete {
		return bad(timestampMalformed)
	}
	return TimeValue(d, roundTimestamp(nval, typmod))
}
