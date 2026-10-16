package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantRoutineBodyConstantFolding(t *testing.T) {
	// clauses.c evaluate_function tries an all-constant immutable call before
	// inline_function. Source therefore predicts an SQL-function statement
	// context, unlike the stable quote(anyelement) inlining witness. The root
	// pins the exact context on PostgreSQL before any native remedy.
	for _, tc := range []struct {
		name, target, deniedName, wrapper, expression, typ string
		value                                              any
	}{
		{"text_octets", "pg_catalog.octet_length(text)", "octet_length", "bit_length", "bit_length('é'::TEXT)", "INT4", int64(16)},
		{"bytea_octets", "pg_catalog.octet_length(bytea)", "octet_length", "bit_length", "bit_length('\\x00ff'::bytea)", "INT4", int64(16)},
		{"multiply", "pg_catalog.int4mul(integer,integer)", "int4mul", "bit_length", "bit_length('é'::TEXT)", "INT4", int64(16)},
		{"left_padding", "pg_catalog.lpad(text,integer,text)", "lpad", "lpad", "lpad('xy',4)", "TEXT", "  xy"},
		{"right_padding", "pg_catalog.rpad(text,integer,text)", "rpad", "rpad", "rpad('xy',4)", "TEXT", "xy  "},
		{"round_numeric", "pg_catalog.round(numeric,integer)", "round", "round", "round(-12.56::NUMERIC)", "NUMERIC", "-13"},
		{"trunc_numeric", "pg_catalog.trunc(numeric,integer)", "trunc", "trunc", "trunc(-12.56::NUMERIC)", "NUMERIC", "-12"},
		{"date_part_date", "pg_catalog.date_part(text,timestamp without time zone)", "date_part", "date_part", "date_part('year',DATE '2001-01-01')", "FLOAT8", float64(2001)},
		{"date_part_cast", "pg_catalog.timestamp(date)", "timestamp", "date_part", "date_part('year',DATE '2001-01-01')", "FLOAT8", float64(2001)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				defer objectGrantRoutineCleanup(t, p, c, []string{tc.target})
				query := "SELECT " + tc.expression + " AS v"
				want := fixture.Select(query, []string{"v"}, []string{tc.typ}, [][]any{{tc.value}})
				fixture.RoleGrantActor(run, "og_object_reader", func() { run(want) })
				denied := func() {
					fixture.RoleGrantActor(run, "og_object_reader", func() {
						s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + tc.deniedName, Context: `SQL function "` + tc.wrapper + `" statement 1`})
						s.SelectRows = true
						s.ErrorStage = "execute"
						run(s)
					})
				}
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+tc.target+" FROM PUBLIC", 0))
				denied()
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+tc.target+" TO og_object_reader", 0))
				fixture.RoleGrantActor(run, "og_object_reader", func() { run(want) })
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+tc.target+" FROM og_object_reader", 0))
				denied()
			})
		})
	}
}
