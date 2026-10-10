package engine

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func normalizeClientEncoding(input string) (string, *sqlstate.Error, error) {
	if !utf8.ValidString(input) || strings.IndexByte(input, 0) >= 0 {
		return "", nil, errors.New("stuntgres: invalid UTF-8 or NUL setting")
	}
	candidate, encoding := sqltype.ClientEncodingName(input)
	if encoding == "UTF8" {
		if candidate == "UNICODE" {
			return candidate, nil, nil
		}
		return encoding, nil, nil
	}
	if encoding == "" {
		return "", nil, (SettingDefinition{Name: "client_encoding"}).invalid(candidate, "")
	}
	if encoding == "MULE_INTERNAL" {
		return "", nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: fmt.Sprintf(`invalid value for parameter "client_encoding": "%s"`, candidate), Detail: "Conversion between MULE_INTERNAL and UTF8 is not supported."}
	}
	return "", nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: fmt.Sprintf(`client encoding "%s" is not supported by stuntgres`, encoding), Detail: "Only UTF8 client encoding is supported."}
}

func localeSetting(name string) bool {
	return name == "lc_messages" || name == "lc_monetary" || name == "lc_numeric" || name == "lc_time"
}

func normalizeLocale(name, input string) (string, *sqlstate.Error, error) {
	if !utf8.ValidString(input) || strings.IndexByte(input, 0) >= 0 {
		return "", nil, errors.New("stuntgres: invalid UTF-8 or NUL setting")
	}
	if _, ok := sqltype.ParseCLocale(input); ok && (input != "" || name != "lc_messages") {
		return input, nil, nil
	}
	var warning *sqlstate.Error
	for i := range len(input) {
		if input[i] >= 128 {
			warning = &sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: fmt.Sprintf(`locale name "%s" contains non-ASCII characters`, input)}
			break
		}
	}
	detail := "Only C and POSIX locales are supported by stuntgres."
	if name == "lc_messages" && input == "" {
		detail = ""
	}
	return "", warning, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf(`invalid value for parameter "%s": "%s"`, name, input), Detail: detail}
}

func normalizeLCMessages(input string) (string, *sqlstate.Error, error) {
	return normalizeLocale("lc_messages", input)
}
func normalizeLCMonetary(input string) (string, *sqlstate.Error, error) {
	return normalizeLocale("lc_monetary", input)
}
func normalizeLCNumeric(input string) (string, *sqlstate.Error, error) {
	return normalizeLocale("lc_numeric", input)
}
func normalizeLCTime(input string) (string, *sqlstate.Error, error) {
	return normalizeLocale("lc_time", input)
}

var icuValidationLevel = SettingDefinition{
	Name: "icu_validation_level", Type: SettingEnum, BootDefault: "warning",
	Description: "Log level for reporting invalid ICU locale strings.",
	EnumValues:  []string{"disabled", "debug5", "debug4", "debug3", "debug2", "debug1", "log", "notice", "warning", "error"},
	Normalize:   normalizeICUValidationLevel,
}

func normalizeICUValidationLevel(input string) (string, *sqlstate.Error, error) {
	value := settingFold(input)
	if value == "debug" {
		value = "debug2"
	}
	if value == "disabled" {
		return value, nil, nil
	}
	if _, ok := messageLevel(value); ok {
		return value, nil, nil
	}
	return "", nil, (SettingDefinition{Name: "icu_validation_level"}).invalid(input, "Available values: disabled, debug5, debug4, debug3, debug2, debug1, log, notice, warning, error.")
}
