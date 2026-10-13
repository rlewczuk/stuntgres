package sqltype

import "strconv"

// JSON's XSD spelling is independent of DateStyle. Only TIMESTAMPTZ consults
// the live zone, and its offset always includes minutes (including UTC).
func jsonCreateDateTime(v Value, env OutputEnvironment) string {
	if v.typ.BaseType().id == Date {
		return dateOutput(v.integer)
	}
	n := v.integer
	if n == TimestampNegativeInfinity {
		return "-infinity"
	}
	if n == TimestampPositiveInfinity {
		return "infinity"
	}
	zoned := v.typ.BaseType().id == Timestamptz
	var east int32
	if zoned {
		east, _ = env.Zone.OffsetAt(floorDiv(n, 1000000) + 946684800)
		n += int64(east) * 1000000
	}
	day := floorDiv(n, microsPerDay)
	tod := n - day*microsPerDay
	y, m, d := dayCivil(day)
	bc := y <= 0
	if bc {
		y = 1 - y
	}
	var buffer [96]byte
	out := jsonDateTimeDigits(buffer[:0], int64(y), 4)
	out = append(out, '-')
	out = jsonDateTimeDigits(out, int64(m), 2)
	out = append(out, '-')
	out = jsonDateTimeDigits(out, int64(d), 2)
	out = append(out, 'T')
	out = jsonDateTimeDigits(out, tod/3600000000, 2)
	out = append(out, ':')
	out = jsonDateTimeDigits(out, tod/60000000%60, 2)
	out = append(out, ':')
	out = jsonDateTimeDigits(out, tod/1000000%60, 2)
	if fraction := tod % 1000000; fraction != 0 {
		out = append(out, '.')
		out = jsonDateTimeDigits(out, fraction, 6)
		for out[len(out)-1] == '0' {
			out = out[:len(out)-1]
		}
	}
	if zoned {
		sign := byte('+')
		if east < 0 {
			sign = '-'
			east = -east
		}
		out = append(out, sign)
		out = jsonDateTimeDigits(out, int64(east/3600), 2)
		out = append(out, ':')
		out = jsonDateTimeDigits(out, int64(east/60%60), 2)
		if east%60 != 0 {
			out = append(out, ':')
			out = jsonDateTimeDigits(out, int64(east%60), 2)
		}
	}
	if bc {
		out = append(out, ' ', 'B', 'C')
	}
	return string(out)
}

func jsonDateTimeDigits(out []byte, n int64, width int) []byte {
	var scratch [20]byte
	digits := strconv.AppendInt(scratch[:0], n, 10)
	for range max(0, width-len(digits)) {
		out = append(out, '0')
	}
	return append(out, digits...)
}
