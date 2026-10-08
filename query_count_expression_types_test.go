package stuntgres_test

import (
	"fmt"
	"stuntgres"
	"testing"
)

func TestCountExpressionTypes(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for i, tc := range []struct{ typ, value string }{
		{"BOOL", "false"}, {"SMALLINT", "-32768"}, {"SMALLINT", "32767"}, {"INT", "-2147483648"}, {"INT", "2147483647"}, {"BIGINT", "-9223372036854775808"}, {"BIGINT", "9223372036854775807"},
		{"TEXT", ""}, {"VARCHAR(4)", ""}, {"CHAR(4)", " "}, {"BYTEA", `\x00ff`},
		{"NUMERIC", "0"}, {"NUMERIC", "NaN"}, {"NUMERIC", "Infinity"}, {"NUMERIC", "-Infinity"},
		{"REAL", "-0"}, {"REAL", "NaN"}, {"REAL", "Infinity"}, {"DOUBLE PRECISION", "-0"}, {"DOUBLE PRECISION", "NaN"}, {"DOUBLE PRECISION", "-Infinity"},
		{"UUID", "00000000-0000-0000-0000-000000000000"}, {"DATE", "2000-02-29"}, {"DATE", "infinity"}, {"TIME", "00:00:00"}, {"TIME WITH TIME ZONE", "00:00:00+00"},
		{"TIMESTAMP", "2000-02-29 00:00:00"}, {"TIMESTAMP", "-infinity"}, {"TIMESTAMP WITH TIME ZONE", "2000-02-29 00:00:00+00"}, {"INTERVAL", "0 seconds"}, {"OID", "4294967295"}, {"REGCLASS", "0"},
		{"NAME", ""}, {"OID", "0"}, {"REAL", "0"}, {"REAL", "-Infinity"}, {"DOUBLE PRECISION", "0"}, {"DOUBLE PRECISION", "Infinity"}, {"TIMESTAMP WITH TIME ZONE", "infinity"}, {"DATE", "-infinity"},
	} {
		table := fmt.Sprintf("typed_count_%d", i)
		columnType, argument := tc.typ, "v"
		if tc.typ == "REGCLASS" {
			columnType, argument = "TEXT", "CASE WHEN v IS NULL THEN CAST(NULL AS REGCLASS) ELSE CAST('0' AS REGCLASS) END"
		}
		t.Run(tc.typ+"/"+tc.value, func(t *testing.T) {
			// Assignment supplies typed NULLs without requiring an explicit cast
			// for types whose general cast surface is deliberately unsupported.
			execSQL(t, db, fmt.Sprintf("CREATE TABLE %s(v %s)", table, columnType))
			execSQL(t, db, fmt.Sprintf("INSERT INTO %s VALUES('%s'),(NULL),('%s')", table, tc.value, tc.value))
			for _, witness := range []struct {
				where string
				want  int64
			}{
				{"", 2}, {" WHERE v IS NULL", 0}, {" WHERE FALSE", 0},
			} {
				q := fmt.Sprintf("SELECT count(%s) FROM %s%s", argument, table, witness.where)
				rows, err := db.Query(q)
				queryCoreRows(t, rows, err, []string{"count"}, []string{"INT8"}, [][]any{{witness.want}}, true)
				stmt, err := db.Prepare(q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				rows, err = stmt.Query()
				queryCoreRows(t, rows, err, []string{"count"}, []string{"INT8"}, [][]any{{witness.want}}, true)
			}
		})
	}
}
