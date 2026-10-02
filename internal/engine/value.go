package engine

import (
	"database/sql/driver"
	"strconv"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/parser"
)

func input(text string, t parser.Type) (driver.Value, error) {
	if !utf8.ValidString(text) || strings.IndexByte(text, 0) >= 0 {
		return nil, sqlError("22P02", "invalid textual input", "", "")
	}
	switch t {
	case parser.Text:
		return text, nil
	case parser.Int4, parser.Int8:
		s := strings.TrimSpace(text)
		digits := s
		if len(digits) > 0 && (digits[0] == '+' || digits[0] == '-') {
			digits = digits[1:]
		}
		if digits == "" {
			return nil, sqlError("22P02", "invalid integer input", "", "")
		}
		for _, c := range digits {
			if c < '0' || c > '9' {
				return nil, sqlError("22P02", "invalid integer input", "", "")
			}
		}
		bits := 64
		if t == parser.Int4 {
			bits = 32
		}
		v, err := strconv.ParseInt(s, 10, bits)
		if err != nil {
			return nil, sqlError("22003", "integer out of range", "", "")
		}
		return v, nil
	case parser.Bool:
		s := strings.ToLower(strings.TrimSpace(text))
		if s == "1" {
			return true, nil
		}
		if s == "0" {
			return false, nil
		}
		if s != "" && s != "o" {
			for _, candidate := range []string{"true", "yes", "on"} {
				if strings.HasPrefix(candidate, s) {
					return true, nil
				}
			}
			for _, candidate := range []string{"false", "no", "off"} {
				if strings.HasPrefix(candidate, s) {
					return false, nil
				}
			}
		}
		return nil, sqlError("22P02", "invalid boolean input", "", "")
	}
	return nil, sqlError("0A000", "unsupported type", "", "")
}
func parameter(v driver.Value, t parser.Type) (driver.Value, error) {
	switch v := v.(type) {
	case nil:
		return nil, nil
	case string:
		return input(v, t)
	case []byte:
		return input(string(v), t)
	case int64:
		return input(strconv.FormatInt(v, 10), t)
	case bool:
		return input(strconv.FormatBool(v), t)
	}
	return nil, sqlError("0A000", "unsupported parameter value", "", "")
}
func literal(v parser.Value, t parser.Type, where bool) (driver.Value, error) {
	mismatch := func() (driver.Value, error) {
		code := "42804"
		if where {
			code = "42883"
		}
		return nil, sqlError(code, "incompatible literal and column types", "", "")
	}
	switch v.Kind {
	case parser.NullValue:
		return nil, nil
	case parser.StringValue:
		return input(v.Text, t)
	case parser.IntegerValue:
		if t == parser.Int4 {
			if v.Integer < -2147483648 || v.Integer > 2147483647 {
				if where {
					return v.Integer, nil
				}
				return nil, sqlError("22003", "integer out of range", "", "")
			}
			return v.Integer, nil
		}
		if t == parser.Int8 {
			return v.Integer, nil
		}
		if t == parser.Text && !where {
			return strconv.FormatInt(v.Integer, 10), nil
		}
		return mismatch()
	case parser.BooleanValue:
		if t == parser.Bool {
			return v.Boolean, nil
		}
		if t == parser.Text && !where {
			return strconv.FormatBool(v.Boolean), nil
		}
		return mismatch()
	}
	return nil, sqlError("0A000", "unsupported value", "", "")
}
