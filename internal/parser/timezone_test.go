package parser

import (
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestTimezoneParserDeclarations(t *testing.T) {
	for _, query := range []string{
		"CREATE TABLE t(z TIMESTAMP(3) WITH TIME ZONE,q TIME(3) WITH TIME ZONE)",
		"CREATE TABLE t(z TIMESTAMPTZ(3),q TIMETZ(3))",
		"CREATE TABLE t(z pg_catalog.timestamptz(3),q pg_catalog.timetz(3))",
	} {
		statements, err := Parse(query)
		if err != nil {
			t.Fatal(query, err)
		}
		table := statements[0].(*CreateTable)
		for i, want := range []string{"timestamptz", "timetz"} {
			typ := table.Elements[i].Column.TypeName
			if typ.Name != want || len(typ.Modifiers) != 1 || typ.Modifiers[0].Integer == nil || *typ.Modifiers[0].Integer != 3 {
				t.Fatal(query, typ)
			}
		}
	}
}
func TestTimezoneParserSettings(t *testing.T) {
	for _, query := range []string{
		"SET TIME ZONE -7.5e0", "SET SESSION TIME ZONE -7.5e0", "SET LOCAL TIME ZONE -7.5e0",
	} {
		statements, err := Parse(query)
		if err != nil {
			t.Fatal(query, err)
		}
		setting := statements[0].(*Set)
		if setting.Name.Text != "timezone" || len(setting.Values) != 1 || setting.Values[0] != "-7.5e0" {
			t.Fatal(query, setting)
		}
	}
	for _, query := range []string{"SET TIME ZONE INTERVAL(7) '-08:00' HOUR TO MINUTE", "SET LOCAL TIME ZONE INTERVAL(7) '-08:00' HOUR TO MINUTE"} {
		statements, err := Parse(query)
		if err != nil {
			t.Fatal(query, err)
		}
		setting := statements[0].(*Set)
		atom := setting.Interval
		if atom == nil || atom.Text != "-08:00" || atom.Precision != 7 || atom.Qualifier != "hour to minute" {
			t.Fatal(query, setting)
		}
	}
	for _, tc := range []struct{ sql, code, message, position string }{
		{"SET TIME ZONE = 'UTC'", "42601", "syntax error at or near \"=\"", "15"},
		{"SET TIME ZONE INTERVAL '1' DAY", "42601", "time zone interval must be HOUR or HOUR TO MINUTE", "28"},
	} {
		_, err := Parse(tc.sql)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != tc.message || diagnostic.Position != tc.position {
			t.Fatal(tc, err)
		}
	}
}
func TestTimezoneParserATAndOrdinaryNames(t *testing.T) {
	for _, query := range []string{
		"SELECT TIMESTAMP '2004-07-01 12:00' AT TIME ZONE 'America/New_York' FROM t",
		"SELECT TIMESTAMPTZ '2004-07-01 12:00+00' AT LOCAL FROM t",
		"SELECT TIMESTAMP '2004-07-01 12:00' AT TIME ZONE 'UTC' AT TIME ZONE 'America/New_York' FROM t",
		"SELECT TIMESTAMPTZ(3) '2004-01-01 00:00:00.9995+00',TIMETZ(3) '05:34:17.9995-05' FROM t",
		"SELECT pg_catalog.timestamptz(3) '2004-01-01 00:00:00.9995+00',pg_catalog.timetz(3) '05:34:17.9995-05' FROM t",
		"SELECT pg_catalog.timestamptz '2004-01-01 00:00+00',pg_catalog.timetz '05:34-05' FROM t",
		"CREATE TABLE at(at INTEGER,hour INTEGER,minute INTEGER,second INTEGER,day INTEGER,month INTEGER,year INTEGER)",
		"SELECT at FROM at",
	} {
		if _, err := Parse(query); err != nil {
			t.Fatal(query, err)
		}
	}
}
