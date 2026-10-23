package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLAssignmentLifecycle(t *testing.T) {
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE TYPE pa_saved_box AS (a integer[], sibling integer)",
			"CREATE FUNCTION pa_saved(a integer[], i integer, x text) RETURNS integer[] LANGUAGE plpgsql AS $$BEGIN a[i] := x; a[2:3] = '{7,8}'::integer[]; RETURN a; END$$",
			"CREATE FUNCTION pa_saved_path() RETURNS integer[] LANGUAGE plpgsql AS $$DECLARE r pa_saved_box := ROW('{1,2}'::integer[],9); BEGIN r.a[2] := '23'::text; RETURN r.a; END$$",
			"CREATE TABLE pa_saved_input(a integer[])")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO pa_saved_input VALUES ('{1,2,3}')", 1))
		q := "SELECT pa_saved($1::integer[],$2::integer,$3::text)::text AS v"
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		executor := fixture.HeldStatement{Executor: c, Statement: held, SQL: q}
		check := func() {
			plpgsqlValue(t, p, b, c, "SELECT pa_saved(a,1,'23')::text AS v FROM pa_saved_input", "TEXT", "{23,7,8}")
			plpgsqlValue(t, p, b, c, "SELECT pa_saved_path()::text AS v", "TEXT", "{1,23}")
			for _, tc := range []struct {
				input     string
				index     int
				rhs, want string
			}{
				{"{1,2,3}", 1, "23", "{23,7,8}"},
				{"{4,5,6}", 4, "9", "{4,7,8,9}"},
				{"[0:2]={1,2,3}", 0, "23", "[0:3]={23,2,7,8}"},
			} {
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.input, tc.index, tc.rhs))
			}
			for _, tc := range []struct {
				index              any
				rhs, code, message string
			}{
				{nil, "bad", "22004", "array subscript in assignment must not be null"},
				{1, "bad", "22P02", `invalid input syntax for type integer: "bad"`},
			} {
				want := plpgsqlRuntimeDiagnostic(tc.code, tc.message, "pa_saved(integer[],integer,text)", "assignment")
				s := fixture.ErrorDiagnostic(q, want, "{1,2,3}", tc.index, tc.rhs)
				s.SelectRows, s.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{"{23,7,8}"}}, "{1,2,3}", 1, "23"))
			}
			plpgsqlValue(t, p, b, c, "SELECT a::text AS v FROM pa_saved_input", "TEXT", "{1,2,3}")
		}
		check()
		for range 2 {
			if !b.PG {
				// Only the native controls differ; the logical SQL witnesses and
				// original held statement are identical through both rounds.
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			check()
		}
	})
}
