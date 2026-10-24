package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Independent REL_18_6 provider diagnostics. One typed runtime failure per
// distinct boundary; analysis direct/Prepare/NULL/empty differences are focused.
func TestReferenceCastImplementedDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("SET TIME ZONE 'UTC'", 0))
			for i, tc := range []struct{ source, target, input, code, message, hint string }{
				{"bytea", "int2", `\x000001`, "22003", "smallint out of range", ""}, {"bytea", "int4", `\x0000000001`, "22003", "integer out of range", ""}, {"bytea", "int8", `\x000000000000000001`, "22003", "bigint out of range", ""},
				{"bit varying", "int4", "1", "42846", "cannot cast type bit varying to integer", ""}, {"bit varying", "int8", "1", "42846", "cannot cast type bit varying to bigint", ""}, {"int4", "varbit", "1", "42846", "cannot cast type integer to bit varying", ""},
				{"jsonb", "int4", `"1"`, "22023", "cannot cast jsonb string to type integer", ""}, {"jsonb", "int4", "[]", "22023", "cannot cast jsonb array to type integer", ""}, {"jsonb", "int4", "{}", "22023", "cannot cast jsonb object to type integer", ""}, {"jsonb", "bool", "1", "22023", "cannot cast jsonb numeric to type boolean", ""}, {"jsonb", "int2", "true", "22023", "cannot cast jsonb boolean to type smallint", ""},
				{"jsonb", "int2", "32768", "22003", "smallint out of range", ""}, {"jsonb", "int4", "2147483648", "22003", "integer out of range", ""}, {"jsonb", "int8", "9223372036854775808", "22003", "bigint out of range", ""},
				{"macaddr8", "macaddr", "00:34:56:00:00:ab:cd:ef", "22003", "macaddr8 data out of range to convert to macaddr", "Only addresses that have FF and FE as values in the 4th and 5th bytes from the left, for example xx:xx:xx:ff:fe:xx:xx:xx, are eligible to be converted from macaddr8 to macaddr."},
				{"interval", "time", "infinity", "22008", "cannot convert infinite interval to time", ""}, {"path", "polygon", "[(0,0),(2,0),(0,2)]", "22023", "open path cannot be converted to polygon", ""},
				{"text", "uuid", "bad", "22P02", `invalid input syntax for type uuid: "bad"`, ""}, {"text", "timestamptz", "nonsense", "22007", `invalid input syntax for type timestamp with time zone: "nonsense"`, ""},
				{"uuid", "int4", "00000000-0000-0000-0000-000000000001", "42846", "cannot cast type uuid to integer", ""}, {"int8", "bool", "1", "42846", "cannot cast type bigint to boolean", ""}, {"bool", "int8", "t", "42846", "cannot cast type boolean to bigint", ""}, {"int2", "money", "1", "42846", "cannot cast type smallint to money", ""},
				{"xid", "xid8", "1", "42846", "cannot cast type xid to xid8", ""}, {"json", "int4", "1", "42846", "cannot cast type json to integer", ""}, {"int4", "jsonb", "1", "42846", "cannot cast type integer to jsonb", ""}, {"line", "point", "{1,-1,0}", "42846", "cannot cast type line to point", ""}, {"regclass", "regtype", "0", "42846", "cannot cast type regclass to regtype", ""},
				{"int4range", "int8range", "[1,3)", "42846", "cannot cast type int4range to int8range", ""}, {"int4multirange", "int4range", "{[1,3)}", "42846", "cannot cast type int4multirange to int4range", ""},
			} {
				table := fmt.Sprintf("cast_error_%d", i)
				run(fixture.Exec("CREATE TABLE "+table+"(v "+tc.source+")", 0))
				run(fixture.Exec("INSERT INTO "+table+" VALUES($1)", 1, tc.input))
				q := "SELECT CAST(v AS " + tc.target + ") AS v FROM " + table
				d := fixture.Diagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint}
				if tc.code == "42846" {
					d.Position = "8"
				}
				bad := fixture.ImplicitError(q, d)
				bad.SelectRows = true
				bad.ErrorStage = "execute"
				run(bad)
				if tc.source == "uuid" && tc.target == "int4" {
					bad = fixture.Prepared(bad)
					bad.ErrorStage = "prepare"
					run(bad)
					q = "SELECT v::int4 AS v FROM " + table
					d.Position = "9"
					bad = fixture.Prepared(fixture.ImplicitError(q, d))
					bad.SelectRows = true
					bad.ErrorStage = "prepare"
					run(bad)
					q = "SELECT CAST(v AS int4) AS v FROM " + table + " WHERE false"
					d.Position = "8"
					bad = fixture.ImplicitError(q, d)
					bad.SelectRows = true
					bad.ErrorStage = "execute"
					run(bad)
				}
			}
			for _, tc := range []struct{ source, target, message string }{{"uuid", "int4", "cannot cast type uuid to integer"}, {"int8", "bool", "cannot cast type bigint to boolean"}} {
				q := "SELECT CAST(NULL::" + tc.source + " AS " + tc.target + ") AS v"
				castWitnessDenied(t, p, b, q, fixture.Diagnostic{Code: "42846", Message: tc.message, Position: "8"})
			}
			run(fixture.Exec("CREATE TABLE cast_wide_bit(v bit(33))", 0))
			run(fixture.Exec("INSERT INTO cast_wide_bit VALUES(B'100000000000000000000000000000000')", 1))
			bad := fixture.ImplicitError("SELECT v::int4 FROM cast_wide_bit", fixture.Diagnostic{Code: "22003", Message: "integer out of range"})
			bad.SelectRows = true
			bad.ErrorStage = "execute"
			run(bad)
			q := "SELECT substr(1234,3) AS v"
			castWitnessDenied(t, p, b, q, fixture.Diagnostic{Code: "42883", Message: "function substr(integer, integer) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"})
			run(fixture.Select(strings.Replace(q, "1234", "CAST(1234 AS text)", 1), []string{"v"}, []string{"TEXT"}, [][]any{{"34"}}))
		})
	}
}
