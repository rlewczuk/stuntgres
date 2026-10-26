package settings

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Value is a typed setting value. Path is borrowed immutable storage when the
// value comes from a definition or registry.
type Value struct {
	Text                  string
	Number                float64
	Boolean               bool
	Path                  []string
	Zone                  *sqltype.Zone
	DateStyle             sqltype.DateStyle
	TimezoneAbbreviations sqltype.TimezoneAbbreviations
	Locale                sqltype.Locale
}

func parameterError(message, hint string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: message, Hint: hint}
}
func (d Definition) invalid(input, hint string) error {
	return parameterError(fmt.Sprintf(`invalid value for parameter "%s": "%s"`, DisplayName(d.Name), input), hint)
}

// Parse validates input against the definition and returns its typed value.
func (d Definition) Parse(input string) (Value, error) {
	v := Value{}
	if d.Name == "timezone_abbreviations" {
		set, ok := sqltype.ParseTimezoneAbbreviations(input)
		if !ok {
			return v, d.invalid(input, "")
		}
		v.TimezoneAbbreviations, v.Text = set, set.String()
		return v, nil
	}
	if d.Name == "timezone" {
		zone, detail, err := sqltype.ParseSessionZone(input)
		if err != nil {
			// check_timezone invokes interval_in directly. Its input errors
			// escape unchanged, unlike ordinary GUC-hook rejections.
			var diagnostic *sqlstate.Error
			if errors.As(err, &diagnostic) {
				switch diagnostic.Code {
				case "22007", "22008", "22015":
					return v, err
				}
			}
			return v, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf(`invalid value for parameter "TimeZone": "%s"`, input), Detail: detail}
		}
		v.Zone, v.Text = zone, zone.Name()
		return v, nil
	}
	if d.Name == "datestyle" {
		style, detail := sqltype.ParseDateStyle(input, sqltype.DateStyle{}, sqltype.DateStyle{})
		if detail != "" {
			return v, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf(`invalid value for parameter "DateStyle": "%s"`, input), Detail: detail}
		}
		v.DateStyle, v.Text = style, style.String()
		return v, nil
	}
	switch d.Type {
	case String:
		if !utf8.ValidString(input) || strings.IndexByte(input, 0) >= 0 {
			return v, errors.New("stuntgres: invalid UTF-8 or NUL setting")
		}
		v.Text = input
		if d.Name == "search_path" {
			var err error
			v.Path, err = ParseIdentifierList(d.Name, input)
			if err != nil {
				return Value{}, err
			}
		}
		if d.Normalize != nil {
			var err error
			v.Text, _, err = d.Normalize(input)
			if err == nil && IsLocaleSetting(d.Name) {
				v.Locale, _ = sqltype.ParseCLocale(v.Text)
			}
			return v, err
		}
		return v, nil
	case Boolean:
		s := FoldName(input)
		if s == "1" {
			v.Boolean = true
			return v, nil
		}
		if s == "0" {
			return v, nil
		}
		if s != "" && s != "o" {
			for _, word := range []string{"true", "yes", "on"} {
				if strings.HasPrefix(word, s) {
					v.Boolean = true
					return v, nil
				}
			}
			for _, word := range []string{"false", "no", "off"} {
				if strings.HasPrefix(word, s) {
					return v, nil
				}
			}
		}
		return v, parameterError(fmt.Sprintf(`parameter "%s" requires a Boolean value`, d.Name), "")
	case Enum:
		key := FoldName(input)
		if d.Normalize != nil {
			var err error
			v.Text, _, err = d.Normalize(input)
			return v, err
		}
		for _, word := range d.EnumValues {
			if FoldName(word) == key {
				v.Text = word
				return v, nil
			}
		}
		return v, d.invalid(input, "Available values: "+strings.Join(d.EnumValues, ", ")+".")
	}
	number, suffix, ok := ParseNumber(input, d.Type == Integer)
	if !ok {
		return v, d.invalid(input, "")
	}
	if d.Unit != UnitNone {
		number, ok = convertUnit(number, suffix, d.Unit)
		if !ok {
			return v, d.invalid(input, unitHint(d.Unit))
		}
	} else if suffix != "" {
		return v, d.invalid(input, "")
	}
	if d.Type == Integer {
		number = math.RoundToEven(number)
		if number == 0 {
			number = 0
		} // Integer negative zero displays as zero.
		if math.IsInf(number, 0) || number < math.MinInt32 || number > math.MaxInt32 {
			return v, d.invalid(input, "Value exceeds integer range.")
		}
		if number < float64(d.IntegerMin) || number > float64(d.IntegerMax) {
			return v, parameterError(fmt.Sprintf(`%s%s is outside the valid range for parameter "%s" (%d%s .. %d%s)`, strconv.FormatFloat(number, 'f', 0, 64), baseUnitLabel(d.Unit), d.Name, d.IntegerMin, baseUnitLabel(d.Unit), d.IntegerMax, baseUnitLabel(d.Unit)), "")
		}
	} else if number < d.FloatMin || number > d.FloatMax {
		return v, parameterError(fmt.Sprintf(`%s%s is outside the valid range for parameter "%s" (%s%s .. %s%s)`, realText(number), baseUnitLabel(d.Unit), d.Name, realText(d.FloatMin), baseUnitLabel(d.Unit), realText(d.FloatMax), baseUnitLabel(d.Unit)), "")
	}
	v.Number = number
	return v, nil
}

// Format renders a typed value as SHOW displays it.
func (d Definition) Format(v Value) string {
	switch d.Name {
	case "unix_socket_permissions":
		return fmt.Sprintf("%04o", int64(v.Number))
	case "tcp_keepalives_count", "tcp_keepalives_idle", "tcp_keepalives_interval", "tcp_user_timeout":
		return d.Raw(v)
	}
	switch d.Type {
	case Boolean:
		if v.Boolean {
			return "on"
		}
		return "off"
	case String, Enum:
		return v.Text
	}
	n := v.Number
	suffix := ""
	if d.Unit != UnitNone && n > 0 {
		table, base, _ := unitTable(d.Unit)
		for _, unit := range table {
			q := v.Number / (unit.factor / base)
			if d.Type == Floating {
				n = q
				suffix = unit.name
				if q > 0 && math.Abs(math.RoundToEven(q)/q-1) <= 1e-8 {
					break
				}
			} else if q == math.Trunc(q) {
				n = q
				suffix = unit.name
				break
			}
		}
	}
	if d.Type == Integer {
		return strconv.FormatFloat(n, 'f', 0, 64) + suffix
	}
	return realText(n) + suffix
}

// Raw formats the stored base-unit value for pg_settings, without SHOW scaling.
func (d Definition) Raw(v Value) string {
	if d.Type == Integer {
		return strconv.FormatFloat(v.Number, 'f', 0, 64)
	}
	if d.Type == Floating {
		return realText(v.Number)
	}
	return d.Format(v)
}

// CurrentRaw renders pg_settings.setting. Hook-style current presentation is
// separate from the numeric base-unit boot/reset values and bounds.
func (d Definition) CurrentRaw(v Value) string {
	if d.Name == "unix_socket_permissions" {
		return d.Format(v)
	}
	return d.Raw(v)
}
