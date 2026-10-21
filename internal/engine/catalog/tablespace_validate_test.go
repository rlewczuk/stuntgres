package catalog

import (
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func tablespaceUnitDiagnostic(t *testing.T, err error, code, message, detail string) {
	t.Helper()
	if code == "" {
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	var d *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail}
	if !errors.As(err, &d) || *d != want {
		t.Fatalf("diagnostic got %#v (%v), want %#v", d, err, want)
	}
}

func TestTablespaceNameValidation(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		existing              bool
		code, message, detail string
	}{
		{"ts_existing", true, "42710", "tablespace \"ts_existing\" already exists", ""},
		{"ts_existing", false, "", "", ""},
		{"pg_default", true, "42939", "unacceptable tablespace name \"pg_default\"", "The prefix \"pg_\" is reserved for system tablespaces."},
		{"pg_missing", false, "42939", "unacceptable tablespace name \"pg_missing\"", "The prefix \"pg_\" is reserved for system tablespaces."},
		{"PG_custom", false, "", "", ""},
		{"PG_custom", true, "42710", "tablespace \"PG_custom\" already exists", ""},
	} {
		tablespaceUnitDiagnostic(t, ValidateTablespaceName(tc.name, tc.existing), tc.code, tc.message, tc.detail)
	}
}

func TestTablespaceCanonicalPaths(t *testing.T) {
	for _, tc := range []struct{ input, canonical, code, message string }{
		{"", "", "42P17", "tablespace location must be an absolute path"},
		{" ", " ", "42P17", "tablespace location must be an absolute path"},
		{"a/../b", "b", "42P17", "tablespace location must be an absolute path"},
		{"/a//./b/../c/", "/a/c", "", ""},
		{"/../../", "/", "", ""},
		{"/bad'quote/../valid", "/valid", "", ""},
		{"/bad'quote", "/bad'quote", "42602", "tablespace location cannot contain single quotes"},
		{"bad'quote", "bad'quote", "42602", "tablespace location cannot contain single quotes"},
		{"/a\\b", "/a\\b", "", ""},
	} {
		got, err := ValidateTablespaceLocation(tc.input)
		if got != tc.canonical {
			t.Fatal(tc.input, got, tc.canonical)
		}
		tablespaceUnitDiagnostic(t, err, tc.code, tc.message, "")
	}
	for _, size := range []int{970, 971} {
		for _, multibyte := range []bool{false, true} {
			location := "/" + strings.Repeat("a", size-1)
			if multibyte {
				location = "/" + strings.Repeat("é", (size-1)/2) + strings.Repeat("z", (size-1)%2)
			}
			if len(location) != size {
				t.Fatal("incorrect byte fixture")
			}
			canonical, err := ValidateTablespaceLocation(location)
			if canonical != location {
				t.Fatal("changed clean path")
			}
			if size == 970 {
				tablespaceUnitDiagnostic(t, err, "", "", "")
			} else {
				tablespaceUnitDiagnostic(t, err, "42P17", "tablespace location \""+location+"\" is too long", "")
			}
		}
	}
	location := "/" + strings.Repeat("x/../", 300) + "valid"
	if got, err := ValidateTablespaceLocation(location); got != "/valid" || err != nil {
		t.Fatal("length must follow canonicalization", got, err)
	}
}
