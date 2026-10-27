package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// USING, not the enclosing formal frame or public parameter inference, owns
// every computed-command parameter. Expectations precede the pinned oracle.
func TestReferencePLpgSQLDynamicSQLParameters(t *testing.T) {
	for _, set := range []bool{false, true} {
		name, result, action := "execute", "integer", "EXECUTE"
		probeBody := "DECLARE x integer; BEGIN\n IF supplied THEN EXECUTE command INTO x USING n; ELSE EXECUTE command INTO x; END IF; RETURN x; END"
		caughtBody := "DECLARE x integer:=99; BEGIN PERFORM 1; BEGIN IF supplied THEN EXECUTE command INTO x USING n; ELSE EXECUTE command INTO x; END IF; EXCEPTION WHEN undefined_parameter THEN IF x<>99 OR NOT FOUND THEN RAISE EXCEPTION 'failed bind changed destination or FOUND'; END IF; INSERT INTO ds_parameter_marks VALUES(1); x:=-1; END; RETURN x; END"
		if set {
			name, result, action = "return_query", "SETOF integer", "RETURN QUERY"
			probeBody = "BEGIN\n IF supplied THEN RETURN QUERY EXECUTE command USING n; ELSE RETURN QUERY EXECUTE command; END IF; END"
			caughtBody = "BEGIN BEGIN IF supplied THEN RETURN QUERY EXECUTE command USING n; ELSE RETURN QUERY EXECUTE command; END IF; EXCEPTION WHEN undefined_parameter THEN INSERT INTO ds_parameter_marks VALUES(1); RETURN QUERY EXECUTE 'SELECT -1::integer'; END; END"
		}
		t.Run(name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c,
					"CREATE TABLE ds_parameter_marks(i integer)",
					"CREATE FUNCTION ds_parameter_probe(command text,n integer,supplied boolean) RETURNS "+result+" LANGUAGE plpgsql AS $$"+probeBody+"$$",
					"CREATE FUNCTION ds_parameter_caught(command text,n integer,supplied boolean) RETURNS "+result+" LANGUAGE plpgsql AS $$"+caughtBody+"$$")
				q := "SELECT ds_parameter_probe($1::text,$2::integer,$3::boolean) AS v"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				for _, tc := range []struct {
					command, parameter string
					supplied           bool
				}{
					{"SELECT $1::integer", "$1", false},
					{"SELECT $2::integer", "$2", true},
					{"SELECT $1", "$1", false},
					{"SELECT $2", "$2", true},
				} {
					want := fixture.ControlsReferenceDiagnostic("42P02", "there is no parameter "+tc.parameter)
					want.InternalQuery, want.InternalPosition = tc.command, fixture.Position(8)
					want.Context = "PL/pgSQL function ds_parameter_probe(text,integer,boolean) line 2 at " + action
					s := fixture.ErrorDiagnostic(q, want, tc.command, 71, tc.supplied)
					s.SelectRows, s.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					// A failed computed bind must not poison the original held call.
					fixture.RunStep(t, p.Ctx, held, b.Recorder, sqlExecutionStep(b, q, []string{"v"}, []string{"INT4"}, [][]any{{int64(71)}}, "SELECT $1::integer", 71, true))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, sqlExecutionStep(b, q, []string{"v"}, []string{"INT4"}, [][]any{{int64(23)}}, "SELECT 23::integer", 71, false))
				}
				caughtQuery := "SELECT ds_parameter_caught($1::text,$2::integer,$3::boolean) AS v"
				caught, err := c.PrepareContext(p.Ctx, caughtQuery)
				if err != nil {
					t.Fatal(err)
				}
				defer caught.Close()
				caughtHeld := fixture.HeldStatement{Executor: c, Statement: caught, SQL: caughtQuery}
				for _, tc := range []struct {
					command  string
					supplied bool
					value    int64
				}{
					{"SELECT $1::integer", false, -1},
					{"SELECT $1::integer", true, 71},
					{"SELECT $2::integer", true, -1},
					{"SELECT 23::integer", false, 23},
					{"SELECT $1::integer", true, 71},
				} {
					s := sqlExecutionStep(b, caughtQuery, []string{"v"}, []string{"INT4"}, [][]any{{tc.value}}, tc.command, 71, tc.supplied)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, caughtHeld, b.Recorder, s)
					marks := int64(0)
					if tc.value == -1 {
						marks = 3
					}
					plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM ds_parameter_marks", "INT8", marks)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM ds_parameter_marks", marks))
				}
			})
		})
	}
}
