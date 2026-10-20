package settings

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestEncodingLocaleAndLevelNormalizers(t *testing.T) {
	for _, tc := range []struct{ input, want string }{{"utf-8", "UTF8"}, {"u.t_f-8", "UTF8"}, {"UNICODE", "UNICODE"}, {"unicode", "UTF8"}} {
		got, notice, err := normalizeClientEncoding(tc.input)
		if err != nil || notice != nil || got != tc.want {
			t.Fatal(tc, got, notice, err)
		}
	}
	for _, tc := range []struct{ input, code, message, detail string }{
		{"not_an_encoding", "22023", `invalid value for parameter "client_encoding": "not_an_encoding"`, ""},
		{strings.Repeat("-", 80) + "UTF8", "22023", `invalid value for parameter "client_encoding": "` + strings.Repeat("-", 63) + `"`, ""},
		{"MULE_INTERNAL", "0A000", `invalid value for parameter "client_encoding": "MULE_INTERNAL"`, "Conversion between MULE_INTERNAL and UTF8 is not supported."},
		{"latin1", "0A000", `client encoding "LATIN1" is not supported by stuntgres`, "Only UTF8 client encoding is supported."},
		{"SQL_ASCII", "0A000", `client encoding "SQL_ASCII" is not supported by stuntgres`, "Only UTF8 client encoding is supported."},
	} {
		_, notice, err := normalizeClientEncoding(tc.input)
		var d *sqlstate.Error
		if notice != nil || !errors.As(err, &d) || d.Code != tc.code || d.Message != tc.message || d.Detail != tc.detail || d.Position != "" {
			t.Fatal(tc, notice, err)
		}
	}
	for _, name := range []string{"lc_messages", "lc_monetary", "lc_numeric", "lc_time"} {
		for _, value := range []string{"C", "POSIX", ""} {
			got, notice, err := normalizeLocale(name, value)
			if name == "lc_messages" && value == "" {
				var d *sqlstate.Error
				if !errors.As(err, &d) || d.Code != "22023" || d.Detail != "" || notice != nil {
					t.Fatal(name, value, notice, err)
				}
			} else if err != nil || notice != nil || got != value {
				t.Fatal(name, value, got, notice, err)
			}
		}
		for _, value := range []string{"c", "C.UTF-8", "missing", "é🙂"} {
			_, notice, err := normalizeLocale(name, value)
			var d *sqlstate.Error
			if !errors.As(err, &d) || d.Code != "22023" || d.Message != fmt.Sprintf(`invalid value for parameter "%s": "%s"`, name, value) || d.Detail != "Only C and POSIX locales are supported by stuntgres." {
				t.Fatal(name, value, notice, err)
			}
			if value == "é🙂" {
				if notice == nil || notice.Severity != "WARNING" || notice.Code != "22023" || notice.Message != `locale name "é🙂" contains non-ASCII characters` {
					t.Fatal(notice)
				}
			} else if notice != nil {
				t.Fatal(notice)
			}
		}
	}
	for _, value := range []string{"info", "disabled", "debug5", "debug4", "debug3", "debug2", "debug1", "log", "notice", "warning", "error"} {
		got, notice, err := normalizeICUValidationLevel(strings.ToUpper(value))
		if err != nil || notice != nil || got != value {
			t.Fatal(value, got, notice, err)
		}
	}
	got, notice, err := normalizeICUValidationLevel("debug")
	if err != nil || notice != nil || got != "debug2" {
		t.Fatal(got, notice, err)
	}
	_, _, err = normalizeICUValidationLevel("bogus")
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != "22023" || d.Hint != "Available values: disabled, debug5, debug4, debug3, debug2, debug1, log, notice, warning, error." {
		t.Fatal(err)
	}
}
