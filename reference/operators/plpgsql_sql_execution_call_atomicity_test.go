package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLSQLExecutionCallAtomicity(t *testing.T) {
	t.Run("invocation_failure_no_copyback", func(t *testing.T) {
		sqlExecutionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE TABLE sx_atomic_marks(i integer)",
				"CREATE PROCEDURE sx_fail(INOUT a integer,INOUT b integer) LANGUAGE plpgsql AS $$BEGIN INSERT INTO sx_atomic_marks VALUES(9); a:=7; b:=8; b:=1/(b-8); END$$",
				"CREATE FUNCTION sx_caught_invocation() RETURNS text LANGUAGE plpgsql AS $$DECLARE a integer:=10; b integer:=20; state text; message text; BEGIN INSERT INTO sx_atomic_marks VALUES(8); BEGIN CALL sx_fail(a,b); EXCEPTION WHEN division_by_zero THEN state:=SQLSTATE; message:=SQLERRM; END; RETURN a||':'||b||':'||state||':'||message; END$$")
			for _, prepared := range []bool{false, true} {
				s := sqlExecutionStep(b, "SELECT sx_caught_invocation() AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"10:20:22012:division by zero"}})
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				sqlExecutionValue(t, p, b, c, "SELECT i+0 AS v FROM sx_atomic_marks", "INT4", int64(8))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM sx_atomic_marks", 1))
			}
		})
	})
	for _, failure := range []string{"notnull", "varchar", "field"} {
		t.Run("sequential_"+failure, func(t *testing.T) {
			sqlExecutionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE TABLE sx_atomic_marks(i integer)", "CREATE TYPE sx_narrow AS (a integer,b varchar(3),c integer)")
				formals, output := "OUT a integer,OUT b integer,OUT c integer", "a:=7; b:=NULL; c:=9;"
				decl, call, result := "a integer:=10; b integer NOT NULL:=20; c integer:=30;", "CALL sx_outputs(a,b,c)", "a||':'||b||':'||c"
				code, message, state := "22004", `null value cannot be assigned to variable "b" declared NOT NULL`, "7:20:30"
				if failure != "notnull" {
					formals, output = "OUT a integer,OUT b varchar,OUT c integer", "a:=7; b:='long'; c:=9;"
					decl = "a integer:=10; b varchar(3):='old'; c integer:=30;"
					code, message, state = "22001", "value too long for type character varying(3)", "7:old:30"
					if failure == "field" {
						decl, call, result = "r sx_narrow:=ROW(10,'old',30);", "CALL sx_outputs(r.a,r.b,r.c)", "r.a||':'||r.b||':'||r.c"
					}
				}
				userOperatorExec(t, p, b, c,
					"CREATE PROCEDURE sx_outputs("+formals+") LANGUAGE plpgsql AS $$BEGIN INSERT INTO sx_atomic_marks VALUES(9); "+output+" END$$",
					"CREATE FUNCTION sx_partial() RETURNS text LANGUAGE plpgsql AS $$DECLARE "+decl+" state text; message text; BEGIN BEGIN "+call+"; EXCEPTION WHEN OTHERS THEN state:=SQLSTATE; message:=SQLERRM; END; RETURN "+result+"||':'||state||':'||message; END$$",
					"CREATE FUNCTION sx_uncaught() RETURNS integer LANGUAGE plpgsql AS $$DECLARE "+decl+" BEGIN INSERT INTO sx_atomic_marks VALUES(8); "+call+"; RETURN 1; END$$")
				sqlExecutionValue(t, p, b, c, "SELECT sx_partial() AS v", "TEXT", state+":"+code+":"+message)
				sqlExecutionCount(t, p, b, c, "sx_atomic_marks", 0)
				sqlExecutionCallError(t, p, c, "SELECT sx_uncaught() AS v", "sx_uncaught()", code, message)
				sqlExecutionCount(t, p, b, c, "sx_atomic_marks", 0)
			})
		})
	}
	t.Run("constraint_no_outputs", func(t *testing.T) {
		sqlExecutionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE TABLE sx_unique(i integer PRIMARY KEY)",
				"CREATE PROCEDURE sx_constraint(INOUT a integer,INOUT b integer) LANGUAGE plpgsql AS $$BEGIN a:=7; b:=8; INSERT INTO sx_unique VALUES(1),(1); END$$",
				"CREATE FUNCTION sx_constraint_caught() RETURNS text LANGUAGE plpgsql AS $$DECLARE a integer:=10; b integer:=20; state text; message text; BEGIN BEGIN CALL sx_constraint(a,b); EXCEPTION WHEN unique_violation THEN state:=SQLSTATE; message:=SQLERRM; END; RETURN a||':'||b||':'||state||':'||message; END$$",
				"CREATE FUNCTION sx_constraint_uncaught() RETURNS integer LANGUAGE plpgsql AS $$DECLARE a integer:=10; b integer:=20; BEGIN CALL sx_constraint(a,b); RETURN a; END$$")
			sqlExecutionValue(t, p, b, c, "SELECT sx_constraint_caught() AS v", "TEXT", `10:20:23505:duplicate key value violates unique constraint "sx_unique_pkey"`)
			sqlExecutionCount(t, p, b, c, "sx_unique", 0)
			want := plpgsqlRuntimeDiagnostic("23505", `duplicate key value violates unique constraint "sx_unique_pkey"`, "sx_constraint(integer,integer)", "SQL statement")
			want.Detail = "Key (i)=(1) already exists."
			want.Schema, want.Table, want.Constraint = "public", "sx_unique", "sx_unique_pkey"
			want.Context = "SQL statement \"INSERT INTO sx_unique VALUES(1),(1)\"\n" + want.Context + "\nSQL statement \"CALL sx_constraint(a,b)\"\nPL/pgSQL function sx_constraint_uncaught() line 1 at CALL"
			plpgsqlError(t, p, c, "SELECT sx_constraint_uncaught() AS v", true, want)
			sqlExecutionCount(t, p, b, c, "sx_unique", 0)
		})
	})
	t.Run("uncaught_invocation_and_transaction_owner", func(t *testing.T) {
		sqlExecutionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE TABLE sx_tx_marks(i integer)",
				"CREATE PROCEDURE sx_tx_child(INOUT x integer,IN fail boolean) LANGUAGE plpgsql AS $$BEGIN INSERT INTO sx_tx_marks VALUES(9); x:=x+1; IF fail THEN x:=1/(x-x); END IF; END$$",
				"CREATE FUNCTION sx_tx(fail boolean) RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer:=4; BEGIN CALL sx_tx_child(x,fail); RETURN x; END$$")
			q := "SELECT sx_tx($1::boolean) AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			want := plpgsqlRuntimeDiagnostic("22012", "division by zero", "sx_tx_child(integer,boolean)", "assignment")
			want.Context += "\nSQL statement \"CALL sx_tx_child(x,fail)\"\nPL/pgSQL function sx_tx(boolean) line 1 at CALL"
			for route := range 3 {
				s := fixture.ErrorDiagnostic(q, want, true)
				s.SelectRows, s.ErrorStage = true, "execute"
				var executor fixture.Executor = c
				if route == 1 {
					s.Prepared = true
				}
				if route == 2 {
					executor = held
				}
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
				sqlExecutionCount(t, p, b, c, "sx_tx_marks", 0)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO sx_tx_marks VALUES(8)", 1))
			s := fixture.ErrorDiagnostic(q, want, true)
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			aborted := fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("INSERT INTO sx_tx_marks VALUES(7)", aborted))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ROLLBACK", 0))
			sqlExecutionCount(t, p, b, c, "sx_tx_marks", 0)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, sqlExecutionStep(b, q, []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}}, false))
			sqlExecutionValue(t, p, b, c, "SELECT i+0 AS v FROM sx_tx_marks", "INT4", int64(9))
		})
	})
}
