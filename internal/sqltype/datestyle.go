package sqltype

import (
	"fmt"
	"strings"
)

type DateStyle struct {
	Style DateStyleFormat
	Order DateOrder
}
type DateStyleFormat uint8
type DateOrder uint8

const (
	DateISO DateStyleFormat = iota
	DateSQL
	DatePostgres
	DateGerman
)
const (
	DateMDY DateOrder = iota
	DateDMY
	DateYMD
)

func (s DateStyle) String() string {
	return [...]string{"ISO", "SQL", "Postgres", "German"}[s.Style] + ", " + [...]string{"MDY", "DMY", "YMD"}[s.Order]
}
func ParseDateStyle(text string, current, defaults DateStyle) (DateStyle, string) {
	result := current
	styleSet, orderSet := false, false
	conflict := false
	words, ok := dateStyleWords(text)
	if !ok {
		return current, "List syntax is invalid."
	}
	for _, word := range words {
		key := strings.ToLower(word)
		style, order := result.Style, result.Order
		isStyle, isOrder := false, false
		switch {
		case key == "iso":
			style, isStyle = DateISO, true
		case key == "sql":
			style, isStyle = DateSQL, true
		case strings.HasPrefix(key, "postgres"):
			style, isStyle = DatePostgres, true
		case key == "german":
			style, isStyle = DateGerman, true
			if !orderSet {
				result.Order = DateDMY
			}
		case key == "mdy" || key == "us" || strings.HasPrefix(key, "noneuro"):
			order, isOrder = DateMDY, true
		case key == "dmy" || strings.HasPrefix(key, "euro"):
			order, isOrder = DateDMY, true
		case key == "ymd":
			order, isOrder = DateYMD, true
		case key == "default":
			if !styleSet {
				result.Style = defaults.Style
			}
			if !orderSet {
				result.Order = defaults.Order
			}
			continue
		default:
			return current, fmt.Sprintf("Unrecognized key word: \"%s\".", word)
		}
		if isStyle {
			if styleSet && style != result.Style {
				conflict = true
			}
			result.Style = style
			styleSet = true
		}
		if isOrder {
			if orderSet && order != result.Order {
				conflict = true
			}
			result.Order = order
			orderSet = true
		}
	}
	if conflict {
		return current, "Conflicting \"DateStyle\" specifications."
	}
	return result, ""
}
func zoneOffsetOutput(east int32) string {
	// PostgreSQL's TIMETZ formatter prints the wrapped INT32_MIN components
	// unsigned. Only conversion can produce this payload; literal/SET bounds
	// remain unchanged.
	if east == -2147483648 {
		return fmt.Sprintf("+%02d:%02d:%02d", uint32(east/3600), uint32(east/60%60), uint32(east%60))
	}
	sign := "+"
	if east < 0 {
		sign = "-"
		east = -east
	}
	result := fmt.Sprintf("%s%02d", sign, east/3600)
	if east%3600 != 0 {
		result += fmt.Sprintf(":%02d", east/60%60)
	}
	if east%60 != 0 {
		result += fmt.Sprintf(":%02d", east%60)
	}
	return result
}
func timestamptzOutput(n int64, env OutputEnvironment) string {
	if n == TimestampNegativeInfinity || n == TimestampPositiveInfinity {
		return timestampOutput(n)
	}
	second := floorDiv(n, 1000000) + 946684800
	east, zone := env.Zone.OffsetAt(second)
	return timestampStyledOutput(n+int64(east)*1000000, env.DateStyle, zone, east, true)
}
