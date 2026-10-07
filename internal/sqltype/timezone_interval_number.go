package sqltype

import (
	"math"
	"strconv"
	"strings"
)

// Keep the integral product exact even near int64's limits. Only the
// subsecond remainder uses floating point, as PostgreSQL's decoder does.
func zoneIntervalNumberMicros(text string, scale int64) (int64, bool) {
	wholeText, fractionText := text, ""
	if dot := strings.IndexByte(text, '.'); dot >= 0 {
		wholeText, fractionText = text[:dot], text[dot+1:]
	}
	if wholeText == "" || wholeText == "+" || wholeText == "-" {
		wholeText = "0"
	}
	whole, err := strconv.ParseInt(wholeText, 10, 64)
	if err != nil || whole > math.MaxInt64/scale || whole < math.MinInt64/scale {
		return 0, false
	}
	value := whole * scale
	if fractionText != "" {
		fraction, err := strconv.ParseFloat("0."+fractionText, 64)
		if err != nil {
			return 0, false
		}
		remainder := int64(math.RoundToEven(fraction * float64(scale)))
		if strings.HasPrefix(text, "-") {
			remainder = -remainder
		}
		if remainder > 0 && value > math.MaxInt64-remainder || remainder < 0 && value < math.MinInt64-remainder {
			return 0, false
		}
		value += remainder
	}
	return value, true
}

func zoneIntervalNumberOverflow(err error) bool {
	numeric, ok := err.(*strconv.NumError)
	return ok && numeric.Err == strconv.ErrRange
}
