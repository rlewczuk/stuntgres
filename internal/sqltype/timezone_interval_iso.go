package sqltype

import (
	"strconv"
	"strings"
)

func decodeAlternativeZoneInterval(body, qualifier string, precision int32) (ZoneInterval, bool, error) {
	if strings.ContainsAny(body, "YMWDHS") {
		return ZoneInterval{}, false, nil
	}
	if strings.Trim(body, "T") == "" {
		return ZoneInterval{}, false, nil
	}
	parts := strings.Split(body, "T")
	bad := func() (ZoneInterval, bool, error) {
		return ZoneInterval{}, true, failure("22007", "invalid input syntax for type interval: \"P"+body+"\"")
	}
	if len(parts) > 2 {
		return bad()
	}
	numbers := func(input string, separator byte) ([]string, bool) {
		var values []string
		for input != "" {
			at := 0
			if input[0] == '-' || input[0] == '+' {
				at++
			}
			for at < len(input) && (asciiDigit(input[at]) || input[at] == '.') {
				at++
			}
			if at == 0 {
				return nil, false
			}
			value := input[:at]
			if _, err := strconv.ParseFloat(value, 64); err != nil {
				return nil, false
			}
			values = append(values, value)
			input = input[at:]
			if input == "" {
				return values, true
			}
			if input[0] != separator {
				return nil, false
			}
			input = input[1:]
			if input == "" {
				return nil, false
			}
		}
		return values, true
	}
	var fields []string
	if parts[0] != "" {
		values, ok := numbers(parts[0], '-')
		if !ok || len(values) > 3 {
			return bad()
		}
		whole, fraction, _ := strings.Cut(values[0], ".")
		digits := strings.TrimLeft(whole, "+-")
		if len(values) == 1 && len(digits) == 8 {
			n, err := strconv.ParseInt(whole, 10, 64)
			if err != nil {
				return bad()
			}
			day := strconv.FormatInt(n%100, 10)
			if fraction != "" {
				day += "." + fraction
			}
			values = []string{strconv.FormatInt(n/10000, 10), strconv.FormatInt(n/100%100, 10), day}
		}
		for i, value := range values {
			fields = append(fields, value, []string{"Y", "M", "D"}[i])
		}
	}
	if len(parts) == 2 && parts[1] != "" {
		fields = append(fields, "T")
		values, ok := numbers(parts[1], ':')
		if !ok || len(values) > 3 {
			return bad()
		}
		whole, fraction, _ := strings.Cut(values[0], ".")
		digits := strings.TrimLeft(whole, "+-")
		if len(values) == 1 && len(digits) == 6 {
			n, err := strconv.ParseInt(whole, 10, 64)
			if err != nil {
				return bad()
			}
			second := strconv.FormatInt(n%100, 10)
			if fraction != "" {
				second += "." + fraction
			}
			values = []string{strconv.FormatInt(n/10000, 10), strconv.FormatInt(n/100%100, 10), second}
		}
		for i, value := range values {
			fields = append(fields, value, []string{"H", "M", "S"}[i])
		}
	}
	if len(fields) == 0 {
		return bad()
	}
	value, err := DecodeZoneInterval("P"+strings.Join(fields, ""), qualifier, precision)
	return value, true, err
}
