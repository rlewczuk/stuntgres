package engine

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)
import "stuntgres/internal/sqltype"

type SettingType uint8

const (
	SettingBoolean SettingType = iota
	SettingString
	SettingInteger
	SettingFloating
	SettingEnum
)

type SettingAccess uint8

const (
	SettingSession SettingAccess = iota
	SettingReadOnly
	SettingPrivileged
)

type SettingUnit uint8

const (
	SettingUnitNone SettingUnit = iota
	SettingBytes
	SettingKilobytes
	SettingMegabytes
	SettingMilliseconds
	SettingSeconds
	SettingMinutes
)

// SettingDefinition is immutable shared metadata, never session value storage.
type SettingDefinition struct {
	Name, Description, BootDefault string
	Type                           SettingType
	Access                         SettingAccess
	Unit                           SettingUnit
	IntegerMin, IntegerMax         int32
	FloatMin, FloatMax             float64
	EnumValues                     []string
	// Normalize is reserved for builtin behavior; registration has no callbacks.
	Normalize func(string) (string, *sqlstate.Error, error)
	bootValue settingValue
}

func settingDisplayName(name string) string {
	switch name {
	case "timezone":
		return "TimeZone"
	case "datestyle":
		return "DateStyle"
	}
	return name
}

// SessionDefaultProvider is evaluated once per definition at physical creation.
// DEFAULT and RESET restore that captured default, not a live provider value.
type SessionDefaultProvider interface {
	SessionDefault(context.Context, SettingDefinition) (string, bool, error)
}

var applicationName = SettingDefinition{
	Name: "application_name", Type: SettingString,
	Description: "Sets the application name to be reported in statistics and logs.",
	Normalize:   normalizeApplicationName,
}
var builtinSettings = []SettingDefinition{
	applicationName,
	{Name: "default_transaction_isolation", Type: SettingEnum, BootDefault: "read committed", EnumValues: []string{"read committed", "read uncommitted", "repeatable read", "serializable"}, Description: "Sets the transaction isolation level of each new transaction."},
	{Name: "transaction_isolation", Type: SettingEnum, BootDefault: "read committed", EnumValues: []string{"read committed", "read uncommitted", "repeatable read", "serializable"}, Description: "Sets the current transaction's isolation level."},
	{Name: "default_transaction_read_only", Type: SettingBoolean, BootDefault: "off", Description: "Sets the default read-only status of new transactions."},
	{Name: "transaction_read_only", Type: SettingBoolean, BootDefault: "off", Description: "Sets the current transaction's read-only status."},
	{Name: "bytea_output", Type: SettingEnum, BootDefault: "hex", EnumValues: []string{"escape", "hex"}, Description: "Sets the output format for bytea."},
	{Name: "timezone", Type: SettingString, BootDefault: "UTC", Description: "Sets the time zone for displaying and interpreting time stamps."},
	{Name: "datestyle", Type: SettingString, BootDefault: "ISO, MDY", Description: "Sets the display format for date and time values."},
	clientMinMessages,
	{Name: "search_path", Type: SettingString, BootDefault: `"$user", public`, Description: "Sets the schema search order for names that are not schema-qualified."},
	{Name: "log_statement_stats", Type: SettingBoolean, Access: SettingPrivileged, BootDefault: "off", Description: "Writes cumulative performance statistics to the server log."},
	{Name: "server_encoding", Type: SettingString, Access: SettingReadOnly, BootDefault: "UTF8", Description: "Shows the server (database) character set encoding."},
	{Name: "standard_conforming_strings", Type: SettingBoolean, BootDefault: "on", Description: "Causes '...' strings to treat backslashes literally."},
	{Name: "statement_timeout", Type: SettingInteger, Unit: SettingMilliseconds, IntegerMax: math.MaxInt32, BootDefault: "0", Description: "Sets the maximum allowed duration of any statement."},
	{Name: "lock_timeout", Type: SettingInteger, Unit: SettingMilliseconds, IntegerMax: math.MaxInt32, BootDefault: "0", Description: "Sets the maximum allowed duration of any wait for a lock."},
}

func RegistrationError(message string) error { return errors.New("stuntgres: " + message) }

func settingFold(s string) string {
	for i := range len(s) {
		if s[i] >= 'A' && s[i] <= 'Z' {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if b[j] >= 'A' && b[j] <= 'Z' {
					b[j] += 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}

func customName(name string) bool {
	if !utf8.ValidString(name) || !strings.Contains(name, ".") {
		return false
	}
	for _, part := range strings.Split(name, ".") {
		if part == "" {
			return false
		}
		for i := range len(part) {
			c := part[i]
			if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 128 || i > 0 && (c >= '0' && c <= '9' || c == '$')) {
				return false
			}
		}
	}
	return true
}

// ValidateSetting returns an owned definition without publishing partial state.
func ValidateSetting(d SettingDefinition) (SettingDefinition, error) {
	d.Name = settingFold(d.Name)
	if !customName(d.Name) || d.Type > SettingEnum || d.Access > SettingPrivileged || d.Unit > SettingMinutes || !utf8.ValidString(d.Description) || strings.IndexByte(d.Description, 0) >= 0 {
		return d, RegistrationError("invalid setting definition")
	}
	if d.Unit != SettingUnitNone && d.Type != SettingInteger && d.Type != SettingFloating {
		return d, RegistrationError("units require a numeric setting")
	}
	if d.Type == SettingInteger && d.IntegerMin > d.IntegerMax || d.Type == SettingFloating && (math.IsNaN(d.FloatMin) || math.IsNaN(d.FloatMax) || math.IsInf(d.FloatMin, 0) || math.IsInf(d.FloatMax, 0) || d.FloatMin > d.FloatMax) {
		return d, RegistrationError("invalid setting bounds")
	}
	if d.Type != SettingInteger && (d.IntegerMin != 0 || d.IntegerMax != 0) || d.Type != SettingFloating && (d.FloatMin != 0 || d.FloatMax != 0) {
		return d, RegistrationError("bounds require the corresponding numeric setting type")
	}
	d.EnumValues = append([]string(nil), d.EnumValues...)
	if d.Type == SettingEnum {
		if len(d.EnumValues) == 0 {
			return d, RegistrationError("empty setting enum")
		}
		seen := make(map[string]bool, len(d.EnumValues))
		for _, value := range d.EnumValues {
			key := settingFold(value)
			if value == "" || !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 || seen[key] {
				return d, RegistrationError("invalid setting enum")
			}
			seen[key] = true
		}
	} else if len(d.EnumValues) != 0 {
		return d, RegistrationError("enum values require an enum setting")
	}
	v, err := d.parse(d.BootDefault)
	if err != nil {
		return d, RegistrationError("invalid setting default: " + err.Error())
	}
	d.bootValue = v
	d.BootDefault = d.format(v)
	return d, nil
}

func (e *Engine) initializeSettings(defs []SettingDefinition) {
	e.settingsDefinitions = make(map[string]SettingDefinition, len(builtinSettings)+len(defs))
	for i, list := range [][]SettingDefinition{builtinSettings, defs} {
		for _, d := range list {
			if i == 0 {
				v, err := d.parse(d.BootDefault)
				if err != nil {
					panic(err)
				} // Builtin definitions are source-owned constants.
				d.bootValue = v
				d.BootDefault = d.format(v)
			}
			e.settingsDefinitions[d.Name] = d
			e.settingsOrder = append(e.settingsOrder, d.Name)
		}
	}
	sort.Strings(e.settingsOrder)
}

func (e *Engine) settingDefinitions() map[string]SettingDefinition { return e.settingsDefinitions }
func (e *Engine) setting(name string) (SettingDefinition, error) {
	if d, ok := e.settingsDefinitions[settingFold(name)]; ok {
		return d, nil
	}
	return SettingDefinition{}, sqlError("42704", fmt.Sprintf(`unrecognized configuration parameter "%s"`, name))
}
func normalizeApplicationName(value string) (string, *sqlstate.Error, error) {
	if !utf8.ValidString(value) {
		return "", nil, errors.New("stuntgres: invalid UTF-8 setting")
	}
	var notice *sqlstate.Error
	if len(value) > 63 {
		n := 63
		for !utf8.RuneStart(value[n]) {
			n--
		}
		notice = &sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: fmt.Sprintf(`identifier "%s" will be truncated to "%s"`, value, value[:n])}
		value = value[:n]
	}
	printable := true
	for i := range len(value) {
		if value[i] < 32 || value[i] > 126 {
			printable = false
			break
		}
	}
	if printable {
		return value, notice, nil
	}
	const hex = "0123456789abcdef"
	var b strings.Builder
	for i := range len(value) {
		c := value[i]
		if c >= 32 && c <= 126 {
			b.WriteByte(c)
		} else {
			b.WriteString(`\x`)
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String(), notice, nil
}

type settingValue struct {
	text      string
	number    float64
	boolean   bool
	path      []string
	zone      *sqltype.Zone
	dateStyle sqltype.DateStyle
}

func settingDiagnostic(message, hint string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: message, Hint: hint}
}
func (d SettingDefinition) invalid(input, hint string) error {
	return settingDiagnostic(fmt.Sprintf(`invalid value for parameter "%s": "%s"`, d.Name, input), hint)
}

func (d SettingDefinition) parse(input string) (settingValue, error) {
	v := settingValue{}
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
		v.zone, v.text = zone, zone.Name()
		return v, nil
	}
	if d.Name == "datestyle" {
		style, detail := sqltype.ParseDateStyle(input, sqltype.DateStyle{}, sqltype.DateStyle{})
		if detail != "" {
			return v, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf(`invalid value for parameter "DateStyle": "%s"`, input), Detail: detail}
		}
		v.dateStyle, v.text = style, style.String()
		return v, nil
	}
	switch d.Type {
	case SettingString:
		if !utf8.ValidString(input) || strings.IndexByte(input, 0) >= 0 {
			return v, errors.New("stuntgres: invalid UTF-8 or NUL setting")
		}
		v.text = input
		if d.Name == "search_path" {
			var err error
			v.path, err = parseSearchPath(input)
			if err != nil {
				return settingValue{}, err
			}
		}
		if d.Normalize != nil {
			var err error
			v.text, _, err = d.Normalize(input)
			return v, err
		}
		return v, nil
	case SettingBoolean:
		s := settingFold(input)
		if s == "1" {
			v.boolean = true
			return v, nil
		}
		if s == "0" {
			return v, nil
		}
		if s != "" && s != "o" {
			for _, word := range []string{"true", "yes", "on"} {
				if strings.HasPrefix(word, s) {
					v.boolean = true
					return v, nil
				}
			}
			for _, word := range []string{"false", "no", "off"} {
				if strings.HasPrefix(word, s) {
					return v, nil
				}
			}
		}
		return v, settingDiagnostic(fmt.Sprintf(`parameter "%s" requires a Boolean value`, d.Name), "")
	case SettingEnum:
		key := settingFold(input)
		if d.Normalize != nil {
			var err error
			v.text, _, err = d.Normalize(input)
			return v, err
		}
		for _, word := range d.EnumValues {
			if settingFold(word) == key {
				v.text = word
				return v, nil
			}
		}
		return v, d.invalid(input, "Available values: "+strings.Join(d.EnumValues, ", ")+".")
	}
	number, suffix, ok := settingNumber(input, d.Type == SettingInteger)
	if !ok {
		return v, d.invalid(input, "")
	}
	if d.Unit != SettingUnitNone {
		number, ok = convertSettingUnit(number, suffix, d.Unit)
		if !ok {
			return v, d.invalid(input, unitHint(d.Unit))
		}
	} else if suffix != "" {
		return v, d.invalid(input, "")
	}
	if d.Type == SettingInteger {
		number = math.RoundToEven(number)
		if number == 0 {
			number = 0
		} // Integer negative zero displays as zero.
		if math.IsInf(number, 0) || number < math.MinInt32 || number > math.MaxInt32 {
			return v, d.invalid(input, "Value exceeds integer range.")
		}
		if number < float64(d.IntegerMin) || number > float64(d.IntegerMax) {
			return v, settingDiagnostic(fmt.Sprintf(`%s%s is outside the valid range for parameter "%s" (%d%s .. %d%s)`, strconv.FormatFloat(number, 'f', 0, 64), baseUnitLabel(d.Unit), d.Name, d.IntegerMin, baseUnitLabel(d.Unit), d.IntegerMax, baseUnitLabel(d.Unit)), "")
		}
	} else if number < d.FloatMin || number > d.FloatMax {
		return v, settingDiagnostic(fmt.Sprintf(`%s%s is outside the valid range for parameter "%s" (%s%s .. %s%s)`, settingRealText(number), baseUnitLabel(d.Unit), d.Name, settingRealText(d.FloatMin), baseUnitLabel(d.Unit), settingRealText(d.FloatMax), baseUnitLabel(d.Unit)), "")
	}
	v.number = number
	return v, nil
}

func settingRealText(n float64) string {
	if math.IsInf(n, 1) {
		return "Infinity"
	}
	if math.IsInf(n, -1) {
		return "-Infinity"
	}
	return strconv.FormatFloat(n, 'g', 6, 64)
}

// Scan the numeric prefix separately from its unit. Integer input first uses
// strtol(base 0), falling back to strtod for fractions/exponents or overflow.
func settingNumber(input string, integer bool) (float64, string, bool) {
	s := strings.TrimLeft(input, " \t\n\r\v\f")
	if s == "" {
		return 0, "", false
	}
	i := 0
	if s[i] == '+' || s[i] == '-' {
		i++
		if i == len(s) {
			return 0, "", false
		}
	}
	if integer {
		// strtol's actual stopping point matters: 08.5 and 0x1p0 do not
		// trigger its decimal-point/e fallback, whereas 010.5 does.
		base, start := 10, i
		if s[i] == '0' {
			base = 8
			if i+2 < len(s) && (s[i+1] == 'x' || s[i+1] == 'X') && settingDigit(s[i+2]) >= 0 {
				base = 16
				start = i + 2
			}
		}
		end := start
		for end < len(s) {
			digit := settingDigit(s[end])
			if digit < 0 || digit >= base {
				break
			}
			end++
		}
		if end == start {
			if input[0] != '.' && input[0] != 'e' && input[0] != 'E' {
				return 0, "", false
			}
		} else {
			n, err := strconv.ParseInt(s[:end], 0, 64)
			overflow := false
			if err != nil {
				ne, yes := err.(*strconv.NumError)
				if !yes || ne.Err != strconv.ErrRange {
					return 0, "", false
				}
				overflow = true
			}
			if !overflow && (end == len(s) || s[end] != '.' && s[end] != 'e' && s[end] != 'E') {
				return float64(n), strings.Trim(s[end:], " \t\n\r\v\f"), true
			}
		}
	}
	if !integer && (s[i] == 'i' || s[i] == 'I') {
		for _, word := range []string{"infinity", "inf"} {
			if len(s)-i >= len(word) && strings.EqualFold(s[i:i+len(word)], word) {
				sign := 1
				if s[0] == '-' {
					sign = -1
				}
				return math.Inf(sign), strings.Trim(s[i+len(word):], " \t\n\r\v\f"), true
			}
		}
	}
	start := i
	numberStart := i
	hex := i+2 <= len(s) && s[i] == '0' && (s[i+1] == 'x' || s[i+1] == 'X')
	if hex {
		i += 2
		start = i
	}
	digit := func(c byte) bool {
		return c >= '0' && c <= '9' || hex && (c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F')
	}
	for i < len(s) && digit(s[i]) {
		i++
	}
	digits := i - start
	if i < len(s) && s[i] == '.' {
		i++
		start = i
		for i < len(s) && digit(s[i]) {
			i++
		}
		digits += i - start
	}
	if digits == 0 {
		if hex {
			return 0, strings.Trim(s[numberStart+1:], " \t\n\r\v\f"), true
		}
		return 0, "", false
	}
	mantissaEnd := i
	if i < len(s) && (!hex && (s[i] == 'e' || s[i] == 'E') || hex && (s[i] == 'p' || s[i] == 'P')) {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		start = i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == start {
			i = mantissaEnd
		}
	}
	token := s[:i]
	suffix := strings.Trim(s[i:], " \t\n\r\v\f")
	if hex && !strings.ContainsAny(token, "pP") {
		token += "p0"
	}
	n, err := strconv.ParseFloat(token, 64)
	if err != nil {
		return 0, "", false
	}
	if n != 0 && math.Abs(n) < 0x1p-1022 {
		exact := false
		if hex {
			exact = exactHexSubnormal(s[:mantissaEnd], token[mantissaEnd+1:])
		} else {
			// Only the exceptional decimal-subnormal path needs arbitrary
			// precision: strtod reports ERANGE if conversion discarded bits.
			var decimal, binary big.Rat
			_, valid := decimal.SetString(token)
			exact = valid && decimal.Cmp(binary.SetFloat64(n)) == 0
		}
		if !exact {
			return 0, "", false
		}
	}
	// Go does not report all strtod underflows. Reject a nonzero mantissa
	// rounded to zero, rather than silently accepting a different setting.
	if n == 0 {
		for _, c := range s[:mantissaEnd] {
			if c >= '1' && c <= '9' || hex && (c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
				return 0, "", false
			}
		}
	}
	return n, suffix, err == nil && !math.IsNaN(n)
}

func settingDigit(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// strtod accepts exact hexadecimal subnormals, but reports ERANGE when a
// nonzero bit below the 2^-1074 grid was lost to rounding.
func exactHexSubnormal(mantissa, exponent string) bool {
	exp, err := strconv.ParseInt(exponent, 10, 64)
	if err != nil {
		return false
	}
	if mantissa[0] == '+' || mantissa[0] == '-' {
		mantissa = mantissa[1:]
	}
	mantissa = mantissa[2:]
	integerDigits := strings.IndexByte(mantissa, '.')
	if integerDigits < 0 {
		integerDigits = len(mantissa)
	}
	position, last, nibble := 0, -1, 0
	for i := range len(mantissa) {
		if mantissa[i] == '.' {
			continue
		}
		digit := settingDigit(mantissa[i])
		if digit > 0 {
			last = position
			nibble = digit
		}
		position++
	}
	if last < 0 {
		return true
	}
	exp += 4 * int64(integerDigits-last-1)
	for nibble&1 == 0 {
		exp++
		nibble >>= 1
	}
	return exp >= -1074
}

type unitEntry struct {
	name   string
	factor float64
}

var memoryUnits = []unitEntry{{"TB", 1 << 40}, {"GB", 1 << 30}, {"MB", 1 << 20}, {"kB", 1 << 10}, {"B", 1}}
var timeUnits = []unitEntry{{"d", 86400000000}, {"h", 3600000000}, {"min", 60000000}, {"s", 1000000}, {"ms", 1000}, {"us", 1}}

func unitTable(u SettingUnit) ([]unitEntry, float64, string) {
	switch u {
	case SettingBytes:
		return memoryUnits, 1, "B"
	case SettingKilobytes:
		return memoryUnits, 1024, "kB"
	case SettingMegabytes:
		return memoryUnits, 1 << 20, "MB"
	case SettingMilliseconds:
		return timeUnits, 1000, "ms"
	case SettingSeconds:
		return timeUnits, 1000000, "s"
	case SettingMinutes:
		return timeUnits, 60000000, "min"
	}
	return nil, 1, ""
}
func baseUnitLabel(u SettingUnit) string {
	_, _, name := unitTable(u)
	if name != "" {
		return " " + name
	}
	return ""
}
func unitHint(u SettingUnit) string {
	if u <= SettingMegabytes {
		return `Valid units for this parameter are "B", "kB", "MB", "GB", and "TB".`
	}
	return `Valid units for this parameter are "us", "ms", "s", "min", "h", and "d".`
}
func convertSettingUnit(n float64, suffix string, u SettingUnit) (float64, bool) {
	table, base, _ := unitTable(u)
	if suffix == "" {
		return n, true
	}
	for i, unit := range table {
		if unit.name == suffix {
			n *= unit.factor / base
			if i+1 < len(table) {
				smaller := table[i+1].factor / base
				n = math.RoundToEven(n/smaller) * smaller
			}
			return n, true
		}
	}
	return 0, false
}
func (d SettingDefinition) format(v settingValue) string {
	switch d.Type {
	case SettingBoolean:
		if v.boolean {
			return "on"
		}
		return "off"
	case SettingString, SettingEnum:
		return v.text
	}
	n := v.number
	suffix := ""
	if d.Unit != SettingUnitNone && n > 0 {
		table, base, _ := unitTable(d.Unit)
		for _, unit := range table {
			q := v.number / (unit.factor / base)
			if d.Type == SettingFloating {
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
	if d.Type == SettingInteger {
		return strconv.FormatFloat(n, 'f', 0, 64) + suffix
	}
	return settingRealText(n) + suffix
}
