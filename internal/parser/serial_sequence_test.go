package parser

import "testing"

func TestSerialSequenceCallAdmission(t *testing.T) {
	for _, name := range []string{"pg_get_serial_sequence", "pg_catalog.pg_get_serial_sequence", "public.pg_get_serial_sequence", `"PG_GET_SERIAL_SEQUENCE"`} {
		for _, args := range []string{"'owned','ID'", "$1,$2", "", "NULL", "1,'ID',true"} {
			query := "SELECT " + name + "(" + args + ")"
			statements, err := Parse(query)
			if err != nil || len(statements) != 1 {
				t.Fatal(query, statements, err)
			}
			call, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
			if !ok {
				t.Fatal(query, statements[0])
			}
			want := "pg_get_serial_sequence"
			if name == `"PG_GET_SERIAL_SEQUENCE"` {
				want = "PG_GET_SERIAL_SEQUENCE"
			}
			if call.Name.Parts[len(call.Name.Parts)-1].Text != want {
				t.Fatal("callee spelling changed", call.Name)
			}
		}
	}
	for _, query := range []string{
		`SELECT COALESCE(pg_get_serial_sequence('owned','ID'),'none')`,
		`SELECT nextval(pg_catalog.pg_get_serial_sequence('owned','ID'))`,
		`SELECT CASE WHEN pg_get_serial_sequence(NULL,'ID') IS NULL THEN 'none' ELSE 'bad' END`,
		`SELECT col FROM inputs WHERE pg_get_serial_sequence('owned',col) IS NOT NULL ORDER BY pg_get_serial_sequence('owned',col)`,
		`CREATE VIEW inquiry AS SELECT pg_get_serial_sequence('owned','ID') AS result`,
		`CREATE TABLE inquiry_default(result TEXT DEFAULT pg_get_serial_sequence('owned','ID'))`,
		`CREATE TABLE inquiry_check(col TEXT CHECK(pg_get_serial_sequence('owned',col) IS NOT NULL))`,
		`SELECT pg_get_serial_sequenc('owned','ID')`,
		`SELECT pg_get_owned_sequence('owned','ID')`,
	} {
		if statements, err := Parse(query); err != nil || len(statements) != 1 {
			t.Fatal(query, statements, err)
		}
	}
}
