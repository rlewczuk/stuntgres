package sqltype

import (
	"math"
	"math/big"
	"strconv"
)

func (p DatetimeExtractPlan) epoch(v Value) (Value, error) {
	const origin int64 = -946684800000000
	switch v.typ.ID() {
	case Date:
		seconds := (v.integer + 10957) * 86400
		if p.name == "date_part" {
			return Float(p.result, float64(seconds))
		}
		return datetimeExtractNumeric(p.result, seconds, 0), nil
	case Time, Timetz:
		n := v.integer
		if v.typ.ID() == Timetz {
			n += int64(v.west) * 1000000
		}
		if p.name == "date_part" {
			return Float(p.result, float64(v.integer)/1000000+float64(v.west))
		}
		return datetimeExtractNumeric(p.result, n, 6), nil
	case Timestamp, Timestamptz:
		if v.integer < math.MaxInt64+origin {
			n := v.integer - origin
			if p.name == "date_part" {
				return Float(p.result, float64(n)/1000000)
			}
			return datetimeExtractNumeric(p.result, n, 6), nil
		}
		if p.name == "date_part" {
			return Float(p.result, (float64(v.integer)-float64(origin))/1000000)
		}
		var numerator, epoch big.Int
		numerator.SetInt64(v.integer)
		epoch.SetInt64(origin)
		numerator.Sub(&numerator, &epoch)
		result := datetimeExtractBigDivide(p.result, &numerator, 1000000)
		return roundNumeric(result, 6), nil
	case Interval:
		// Broken-down years/remainder are derived from the stored month tuple,
		// not from the original literal's separate year/month words.
		years, months := int64(v.months/12), int64(v.months%12)
		seconds := years*31557600 + months*2592000 + int64(v.days)*86400
		if p.name == "date_part" {
			seconds := float64(v.integer) / 1000000
			seconds += 31557600 * float64(years)
			seconds += 2592000 * float64(months)
			seconds += 86400 * float64(v.days)
			return Float(p.result, seconds)
		}
		if seconds >= math.MinInt64/1000000 && seconds <= math.MaxInt64/1000000 {
			n := seconds * 1000000
			if !(v.integer > 0 && n > math.MaxInt64-v.integer || v.integer < 0 && n < math.MinInt64-v.integer) {
				return datetimeExtractNumeric(p.result, n+v.integer, 6), nil
			}
		}
		var coefficient, micros, factor big.Int
		coefficient.SetInt64(seconds)
		factor.SetInt64(1000000)
		coefficient.Mul(&coefficient, &factor)
		micros.SetInt64(v.integer)
		coefficient.Add(&coefficient, &micros)
		return datetimeExtractBigNumeric(p.result, &coefficient, 6), nil
	}
	panic("invalid validated extraction epoch source")
}

func datetimeExtractBigNumeric(d *Descriptor, n *big.Int, scale int64) Value {
	text := n.String()
	negative := n.Sign() < 0
	if negative {
		text = text[1:]
	}
	return normalizeNumeric(Value{typ: d, text: text, negative: negative, exponent: -scale, scale: scale})
}

// Only the selected datetime ratios use this bounded scalar division seam. PG
// selects display scale from normalized base10000 leading groups, then rounds
// ties away from zero. The division is exact integer arithmetic, never float.
func datetimeExtractBigDivide(d *Descriptor, n *big.Int, denominator int64) Value {
	weight := func(text string) (int, int) {
		if text[0] == '-' {
			text = text[1:]
		}
		if text == "0" {
			return 0, 0
		}
		count := (len(text)-1)%4 + 1
		leading, _ := strconv.Atoi(text[:count])
		return (len(text) - 1) / 4, leading
	}
	w1, first1 := weight(n.String())
	w2, first2 := weight(strconv.FormatInt(denominator, 10))
	qweight := w1 - w2
	if first1 <= first2 {
		qweight--
	}
	scale := int64(max(16-4*qweight, 0))
	var power, scaled, divisor, quotient, remainder big.Int
	power.Exp(big.NewInt(10), big.NewInt(scale), nil)
	scaled.Abs(n)
	scaled.Mul(&scaled, &power)
	divisor.SetInt64(denominator)
	quotient.QuoRem(&scaled, &divisor, &remainder)
	remainder.Lsh(&remainder, 1)
	if remainder.Cmp(&divisor) >= 0 {
		quotient.Add(&quotient, big.NewInt(1))
	}
	if n.Sign() < 0 {
		quotient.Neg(&quotient)
	}
	return datetimeExtractBigNumeric(d, &quotient, scale)
}
func datetimeExtractDivide(d *Descriptor, n, denominator int64) Value {
	var numerator big.Int
	numerator.SetInt64(n)
	return datetimeExtractBigDivide(d, &numerator, denominator)
}
func datetimeExtractJulian(d *Descriptor, day int64, fraction Value) Value {
	var coefficient, whole, power big.Int
	coefficient.SetString(fraction.text, 10)
	if fraction.text == "" {
		coefficient.SetInt64(0)
	}
	if fraction.exponent+fraction.scale > 0 {
		power.Exp(big.NewInt(10), big.NewInt(fraction.exponent+fraction.scale), nil)
		coefficient.Mul(&coefficient, &power)
	}
	power.Exp(big.NewInt(10), big.NewInt(fraction.scale), nil)
	whole.SetInt64(day)
	whole.Mul(&whole, &power)
	coefficient.Add(&coefficient, &whole)
	return datetimeExtractBigNumeric(d, &coefficient, fraction.scale)
}
