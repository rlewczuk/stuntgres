package catalog

import (
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"

	"stuntgres/internal/sqltype"
)

// ColumnStatisticsConfig is immutable stable-attribute ANALYZE configuration.
// TargetSet distinguishes zero (disabled) from the session default.
type ColumnStatisticsConfig struct {
	Target    int16
	TargetSet bool
	Options   []ForeignOption
}

func (r *Relation) WithColumnStatistics(slot int, value ColumnStatisticsConfig) *Relation {
	old := r.ColumnMeta.Statistics[r.Attributes[slot]]
	if old.Target == value.Target && old.TargetSet == value.TargetSet && slices.Equal(old.Options, value.Options) {
		return r
	}
	next := *r
	next.ColumnMeta.Statistics = maps.Clone(r.ColumnMeta.Statistics)
	attribute := r.Attributes[slot]
	if !value.TargetSet && len(value.Options) == 0 {
		delete(next.ColumnMeta.Statistics, attribute)
	} else {
		if next.ColumnMeta.Statistics == nil {
			next.ColumnMeta.Statistics = make(map[int32]ColumnStatisticsConfig)
		}
		value.Options = slices.Clone(value.Options)
		next.ColumnMeta.Statistics[attribute] = value
	}
	return &next
}

// Valid checks imported immutable payloads without evaluating SQL.
func (c ColumnStatisticsConfig) Valid() bool {
	if c.Target < 0 || c.Target > 10000 || !c.TargetSet && c.Target != 0 {
		return false
	}
	var local, inherited bool
	for _, option := range c.Options {
		switch option.Name {
		case "n_distinct":
			if local {
				return false
			}
			local = true
		case "n_distinct_inherited":
			if inherited {
				return false
			}
			inherited = true
		default:
			return false
		}
		n, ok := ParseColumnDistinct(option.Value)
		if !ok || n < -1 || n > math.MaxFloat64 {
			return false
		}
	}
	return true
}

// ParseColumnDistinct follows the real-option strtod contract: NaN and
// conversion ERANGE fail; exact subnormals remain valid, and infinities reach
// range validation.
func ParseColumnDistinct(text string) (float64, bool) {
	text = strings.Trim(text, " \t\n\r\f\v")
	if strings.ContainsRune(text, '_') {
		return 0, false
	}
	magnitude := strings.TrimLeft(text, "+-")
	hexadecimal := strings.HasPrefix(magnitude, "0x") || strings.HasPrefix(magnitude, "0X")
	input := text
	if hexadecimal && !strings.ContainsAny(magnitude, "pP") {
		// strtod permits an omitted binary exponent; strconv requires one.
		// Normalize only conversion input, never the stored option spelling.
		input += "p0"
	}
	n, err := strconv.ParseFloat(input, 64)
	if err != nil || math.IsNaN(n) {
		return 0, false
	}
	nonzero := n != 0
	if !nonzero {
		for _, c := range magnitude {
			if hexadecimal && (c == 'p' || c == 'P') || !hexadecimal && (c == 'e' || c == 'E') {
				break
			}
			if c >= '1' && c <= '9' || hexadecimal && (c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
				nonzero = true
				break
			}
		}
	}
	if sqltype.StrtodUnderflow(text, n, nonzero) {
		return 0, false
	}
	return n, true
}
