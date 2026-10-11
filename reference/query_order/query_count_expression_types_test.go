package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCountExpressionTypes(t *testing.T) {
	p := fixture.NewScenario(t)
	for i, tc := range []struct{ typ, value string }{
		{"BOOL", "false"}, {"SMALLINT", "-32768"}, {"SMALLINT", "32767"}, {"INT", "-2147483648"}, {"INT", "2147483647"}, {"BIGINT", "-9223372036854775808"}, {"BIGINT", "9223372036854775807"},
		{"TEXT", ""}, {"VARCHAR(4)", ""}, {"CHAR(4)", " "}, {"BYTEA", `\x00ff`},
		{"NUMERIC", "0"}, {"NUMERIC", "NaN"}, {"NUMERIC", "Infinity"}, {"NUMERIC", "-Infinity"},
		{"REAL", "-0"}, {"REAL", "NaN"}, {"REAL", "Infinity"}, {"DOUBLE PRECISION", "-0"}, {"DOUBLE PRECISION", "NaN"}, {"DOUBLE PRECISION", "-Infinity"},
		{"UUID", "00000000-0000-0000-0000-000000000000"}, {"DATE", "2000-02-29"}, {"DATE", "infinity"}, {"TIME", "00:00:00"}, {"TIME WITH TIME ZONE", "00:00:00+00"},
		{"TIMESTAMP", "2000-02-29 00:00:00"}, {"TIMESTAMP", "-infinity"}, {"TIMESTAMP WITH TIME ZONE", "2000-02-29 00:00:00+00"}, {"INTERVAL", "0 seconds"}, {"OID", "4294967295"}, {"REGCLASS", "0"},
		{"NAME", ""}, {"OID", "0"}, {"REAL", "0"}, {"REAL", "-Infinity"}, {"DOUBLE PRECISION", "0"}, {"DOUBLE PRECISION", "Infinity"}, {"TIMESTAMP WITH TIME ZONE", "infinity"}, {"DATE", "-infinity"},
	} {
		// Use the existing assignment provider for both boundary values and
		// typed NULLs; count(any) must not widen the explicit cast surface.
		table := fmt.Sprintf("typed_count_%d", i)
		columnType, argument := tc.typ, "v"
		if tc.typ == "REGCLASS" {
			columnType, argument = "TEXT", "CASE WHEN v IS NULL THEN CAST(NULL AS REGCLASS) ELSE CAST('0' AS REGCLASS) END"
		}
		p.Step(t, fixture.Exec(fmt.Sprintf("CREATE TABLE %s(v %s)", table, columnType), 0))
		p.Step(t, fixture.Exec(fmt.Sprintf("INSERT INTO %s VALUES('%s'),(NULL),('%s')", table, tc.value, tc.value), 3))
		for _, witness := range []struct {
			where string
			want  int64
		}{
			{"", 2}, {" WHERE v IS NULL", 0}, {" WHERE FALSE", 0},
		} {
			q := fmt.Sprintf("SELECT count(%s) FROM %s%s", argument, table, witness.where)
			for _, prepared := range []bool{false, true} {
				step := fixture.Select(q, []string{"count"}, []string{"INT8"}, [][]any{{witness.want}})
				step.Prepared = prepared
				p.Step(t, step)
			}
		}
	}
}
