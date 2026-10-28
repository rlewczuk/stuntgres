package settings

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestClientSettingsBootAndStartup(t *testing.T) {
	r := NewRegistry(nil, false)
	for _, tc := range []struct {
		name, boot, initial string
		null                bool
	}{
		{"client_encoding", "SQL_ASCII", "UTF8", false},
		{"lc_messages", "", "C", false},
		{"timezone", "GMT", "UTC", false},
		{"timezone_abbreviations", "", "Default", true},
		{"default_text_search_config", "pg_catalog.simple", "pg_catalog.simple", false},
	} {
		d, err := r.Lookup(tc.name)
		if err != nil || d.Raw(d.BootValue()) != tc.boot || d.Format(d.DefaultValue()) != tc.initial || d.BootDefaultNull != tc.null {
			t.Fatalf("%s: %+v %v", tc.name, d, err)
		}
	}
	for _, tc := range []struct{ name, input string }{{"lc_messages", ""}} {
		d, _ := r.Lookup(tc.name)
		if _, err := d.Parse(tc.input); err == nil {
			t.Fatalf("boot-only metadata admitted by live parser: %s", tc.name)
		}
	}
	d, _ := r.Lookup("client_encoding")
	if value, err := d.Parse("SQL_ASCII"); err != nil || value.Text != "SQL_ASCII" || value.ClientEncoding != 0 {
		t.Fatalf("live SQL_ASCII parse %+v %v", value, err)
	}
	for _, name := range []string{"default_transaction_isolation", "transaction_isolation"} {
		d, _ := r.Lookup(name)
		if !reflect.DeepEqual(d.EnumValues, []string{"serializable", "repeatable read", "read committed", "read uncommitted"}) {
			t.Fatal(name, d.EnumValues)
		}
	}
}

func TestClientSettingsKeywordLists(t *testing.T) {
	r := NewRegistry(nil, false)
	for _, tc := range []struct{ name, first, second string }{{"createrole_self_grant", "SET", "INHERIT"}, {"restrict_nonsystem_relation_kind", "VIEW", "FOREIGN-TABLE"}} {
		d, _ := r.Lookup(tc.name)
		for _, input := range []string{"", tc.first, "  " + tc.first + " ," + tc.second + " ", tc.first + "," + tc.first, `"` + tc.first + `"`} {
			v, err := d.Parse(input)
			if err != nil || v.Text != input {
				t.Fatal(tc.name, input, v, err)
			}
		}
		for _, bad := range []struct{ input, detail string }{{"unknown", `Unrecognized key word: "unknown".`}, {"a,,b", "List syntax is invalid."}, {`"unclosed`, "List syntax is invalid."}} {
			_, err := d.Parse(bad.input)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "22023" || diagnostic.Detail != bad.detail {
				t.Fatal(tc.name, bad.input, err)
			}
		}
	}
	for _, input := range []string{`""`, `pg_default,""`, `"a""b", pg_default`} {
		if _, err := ParseIdentifierList("temp_tablespaces", input); err != nil {
			t.Fatal(input, err)
		}
	}
}

func TestClientCatalogNameNormalization(t *testing.T) {
	for _, tc := range []struct {
		input, want string
		notice      bool
	}{
		{"Heap", "Heap", false},
		{"", "", false},
		{strings.Repeat("x", 63), strings.Repeat("x", 63), false},
		{strings.Repeat("x", 64), strings.Repeat("x", 63), true},
		{strings.Repeat("x", 62) + "é", strings.Repeat("x", 62), true},
		{strings.Repeat("x", 61) + "é", strings.Repeat("x", 61) + "é", false},
		{strings.Repeat("X", 61) + "éZ", strings.Repeat("X", 61) + "é", true},
		{strings.Repeat("x", 60) + "界Z", strings.Repeat("x", 60) + "界", true},
	} {
		got, notice, err := NormalizeCatalogName(tc.input)
		if err != nil || got != tc.want || (notice != nil) != tc.notice {
			t.Fatal(tc, got, notice, err)
		}
		if notice != nil && !reflect.DeepEqual(*notice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: `identifier "` + tc.input + `" will be truncated to "` + tc.want + `"`}) {
			t.Fatal(notice)
		}
	}
	for _, input := range []string{"a\x00b", "\xff"} {
		got, notice, err := NormalizeCatalogName(input)
		if err == nil || err.Error() != "stuntgres: invalid UTF-8 or NUL setting" || got != "" || notice != nil {
			t.Fatal("invalid name accepted", input, got, notice, err)
		}
	}
}
