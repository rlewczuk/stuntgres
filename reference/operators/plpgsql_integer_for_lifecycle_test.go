package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLIntegerForAtomicity(t *testing.T) {
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE TABLE if_atomic(v integer)",
			"CREATE TABLE if_effect(v integer)",
			"CREATE FUNCTION if_put(x integer) RETURNS integer LANGUAGE SQL AS 'INSERT INTO if_effect VALUES(x); SELECT x'",
			"CREATE FUNCTION if_atomic_value(stride integer) RETURNS integer LANGUAGE plpgsql AS $$DECLARE s integer:=0; BEGIN FOR i IN 1..3 BY stride LOOP s:=s+i; END LOOP; RETURN s; END$$")
		want := plpgsqlRuntimeDiagnostic("22023", "BY value of FOR loop must be greater than zero", "if_atomic_value(integer)", "FOR with integer loop variable")
		q := "SELECT if_atomic_value($1) AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, input := range []int{1, 0, 1} {
			if input != 0 {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(6)}}, input))
			} else {
				s := fixture.ErrorDiagnostic(q, want, input)
				s.SelectRows = true
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
		}
		plpgsqlError(t, p, c, "INSERT INTO if_atomic VALUES(if_atomic_value(1)),(if_atomic_value(0))", false, want)
		plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM if_atomic", "INT8", int64(0))
		for _, tc := range []struct{ name, body string }{
			{"bound_rollback", "FOR i IN if_put(1)..3 BY 0 LOOP END LOOP;"},
			{"body_rollback", "FOR i IN 1..3 LOOP d:=if_put(i); d:=1/(2-i); END LOOP;"},
		} {
			userOperatorExec(t, p, b, c, "CREATE FUNCTION "+tc.name+"() RETURNS integer LANGUAGE plpgsql AS $$DECLARE d integer; BEGIN "+tc.body+" RETURN 0; END$$")
			d := plpgsqlRuntimeDiagnostic("22023", "BY value of FOR loop must be greater than zero", tc.name+"()", "FOR with integer loop variable")
			if tc.name == "body_rollback" {
				d = plpgsqlRuntimeDiagnostic("22012", "division by zero", tc.name+"()", "assignment")
			}
			plpgsqlError(t, p, c, "SELECT "+tc.name+"() AS v", true, d)
			plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM if_effect", "INT8", int64(0))
		}
		userOperatorExec(t, p, b, c, "CREATE FUNCTION if_protected() RETURNS integer LANGUAGE plpgsql AS $$DECLARE d integer; BEGIN <<o>> FOR i IN 1..3 LOOP BEGIN d:=if_put(i); CONTINUE o WHEN i<3; EXIT o; EXCEPTION WHEN OTHERS THEN d:=if_put(99); END; d:=if_put(98); END LOOP; <<b>> BEGIN FOR i IN 4..5 LOOP d:=if_put(i); EXIT b; END LOOP; EXCEPTION WHEN OTHERS THEN d:=if_put(97); END b; BEGIN FOR i IN 5..6 LOOP d:=if_put(i); RETURN i; END LOOP; EXCEPTION WHEN OTHERS THEN d:=if_put(96); END; RETURN 0; END$$")
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT if_protected() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}})
			if prepared {
				s = fixture.Prepared(s)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM if_effect ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}}))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM if_effect", 5))
		}
	})
}

func TestReferencePLpgSQLIntegerForPersistence(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "if_saved")
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE SCHEMA if_saved",
			"CREATE FUNCTION if_saved.sum_to(x integer) RETURNS integer LANGUAGE plpgsql AS $$DECLARE s integer:=0; BEGIN FOR i IN 1..x LOOP s:=s+i; END LOOP; RETURN s; END$$",
			"CREATE FUNCTION if_saved.positive(x integer) RETURNS boolean LANGUAGE plpgsql AS $$BEGIN FOR i IN 1..x LOOP RETURN true; END LOOP; RETURN false; END$$",
			"CREATE VIEW if_saved.captured AS SELECT if_saved.sum_to(5) AS v",
			"CREATE TABLE if_saved.data(id integer,v integer DEFAULT if_saved.sum_to(5),CONSTRAINT if_positive CHECK(if_saved.positive(v)))")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO if_saved.data(id) VALUES(1)", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO if_saved.data VALUES(2,3)", 1))
		q := "SELECT if_saved.sum_to($1) AS v"
		original, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer original.Close()
		check := func(conn *sql.Conn, backend fixture.Backend) {
			plpgsqlValue(t, p, backend, conn, "SELECT v FROM if_saved.captured", "INT4", int64(15))
			plpgsqlValue(t, p, backend, conn, "SELECT v FROM if_saved.data WHERE id=1", "INT4", int64(15))
			parametersHeld(t, p, backend, conn, q, "INT4", [][]any{{5}, {1}, {3}}, []any{int64(15), int64(1), int64(6)})
			want := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "data" violates check constraint "if_positive"`)
			want.Detail = "Failing row contains (3, -1)."
			want.Schema = "if_saved"
			want.Table = "data"
			want.Constraint = "if_positive"
			plpgsqlError(t, p, conn, "INSERT INTO if_saved.data VALUES(3,-1)", false, want)
			plpgsqlValue(t, p, backend, conn, "SELECT count(*) AS v FROM if_saved.data", "INT8", int64(2))
			if conn == c && backend.PG == b.PG {
				for _, tc := range []struct {
					input int
					want  int64
				}{{5, 15}, {1, 1}, {3, 6}} {
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: original, SQL: q}, backend.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{tc.want}}, tc.input))
				}
			}
		}
		parametersReplay(t, p, b, c, "if_saved", check)
	})
}
