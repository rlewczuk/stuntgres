package xml

import (
	"context"
	"math"
	"strconv"
	"strings"
)

// libxml's XPath numeric scanner intentionally admits exponent notation, even
// an exponent without digits. Fraction accumulation is limited to twenty digits
// after leading fractional zeroes. Keep these released semantics separate from
// Go's more permissive numeric grammar and correctly-rounded decimal decoder.
func xpathParseNumber(ctx context.Context, s string) (float64, error) {
	i := 0
	poll := func() error {
		if i&4095 == 0 {
			return ctx.Err()
		}
		return nil
	}
	for i < len(s) && space(s[i]) {
		if err := poll(); err != nil {
			return 0, err
		}
		i++
	}
	if i == len(s) || s[i] != '.' && s[i] != '-' && (s[i] < '0' || s[i] > '9') {
		return math.NaN(), ctx.Err()
	}
	negative := s[i] == '-'
	if negative {
		i++
	}
	number := 0.0
	digits := false
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		if err := poll(); err != nil {
			return 0, err
		}
		number = number * 10
		number += float64(s[i] - '0')
		digits = true
		i++
	}
	if i < len(s) && s[i] == '.' {
		i++
		if (i == len(s) || s[i] < '0' || s[i] > '9') && !digits {
			return math.NaN(), ctx.Err()
		}
		places := 0
		for i < len(s) && s[i] == '0' {
			if err := poll(); err != nil {
				return 0, err
			}
			places++
			i++
		}
		maximum := places + 20
		fraction := 0.0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' && places < maximum {
			if err := poll(); err != nil {
				return 0, err
			}
			fraction = fraction * 10
			fraction += float64(s[i] - '0')
			places++
			i++
		}
		number += fraction / math.Pow(10, float64(places))
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			if err := poll(); err != nil {
				return 0, err
			}
			i++
		}
	}
	exponent := 0
	exponentNegative := false
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			exponentNegative = s[i] == '-'
			i++
		}
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			if err := poll(); err != nil {
				return 0, err
			}
			if exponent < 1000000 {
				exponent = exponent*10 + int(s[i]-'0')
			}
			i++
		}
	}
	for i < len(s) && space(s[i]) {
		if err := poll(); err != nil {
			return 0, err
		}
		i++
	}
	if i != len(s) {
		return math.NaN(), ctx.Err()
	}
	if negative {
		number = -number
	}
	if exponentNegative {
		exponent = -exponent
	}
	number *= math.Pow(10, float64(exponent))
	return number, ctx.Err()
}
func xpathFormatNumber(number float64) string {
	switch {
	case math.IsNaN(number):
		return "NaN"
	case math.IsInf(number, 1):
		return "Infinity"
	case math.IsInf(number, -1):
		return "-Infinity"
	case number == 0:
		return "0"
	}
	if number > math.MinInt32 && number < math.MaxInt32 && number == math.Trunc(number) {
		return strconv.FormatInt(int64(number), 10)
	}
	absolute := math.Abs(number)
	var text, exponent string
	if absolute > 1e9 || absolute < 1e-5 {
		text = strconv.FormatFloat(number, 'e', 14, 64)
		text, exponent, _ = strings.Cut(text, "e")
		exponent = "e" + exponent
	} else {
		integerPlace := int(math.Log10(absolute))
		fractionPlace := 15 - integerPlace
		if integerPlace > 0 {
			fractionPlace--
		}
		text = strconv.FormatFloat(number, 'f', fractionPlace, 64)
	}
	text = strings.TrimRight(text, "0")
	text = strings.TrimSuffix(text, ".")
	return text + exponent
}
