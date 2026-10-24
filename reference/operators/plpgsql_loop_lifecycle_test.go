package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLLoopAtomicity(t *testing.T) {
	loopScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE TABLE loop_atomic(v integer)",
			"CREATE FUNCTION loop_atomic_value(x integer) RETURNS integer LANGUAGE plpgsql AS $$DECLARE n integer:=0; BEGIN LOOP n:=n+1; EXIT WHEN n/x=3; END LOOP; RETURN n; END$$",
			"CREATE FUNCTION loop_continue_error(x integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN LOOP CONTINUE WHEN 1/x=0; EXIT; END LOOP; RETURN 7; END$$")
		want := plpgsqlRuntimeDiagnostic("22012", "division by zero", "loop_atomic_value(integer)", "EXIT")
		plpgsqlError(t, p, c, "INSERT INTO loop_atomic VALUES(loop_atomic_value(1)),(loop_atomic_value(0))", false, want)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM loop_atomic", []string{"v"}, []string{"INT4"}, nil))
		plpgsqlError(t, p, c, "SELECT loop_continue_error(0) AS v", true, plpgsqlRuntimeDiagnostic("22012", "division by zero", "loop_continue_error(integer)", "CONTINUE"))
		q := "SELECT loop_atomic_value($1) AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, input := range []int{1, 0, 1} {
			if input != 0 {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}}, input))
				continue
			}
			rows, err := stmt.QueryContext(p.Ctx, input)
			if rows != nil {
				rows.Close()
				t.Fatal("condition error exposed rows")
			}
			if err == nil {
				t.Fatal("division by zero succeeded")
			}
			fixture.AssertDiagnostic(t, err, &want)
		}
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := stmt.QueryContext(ctx, 1)
			if rows != nil {
				rows.Close()
				t.Fatal("pre-cancel exposed rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("pre-cancel: %v", err)
			}
			var state interface{ SQLState() string }
			if errors.As(err, &state) {
				t.Fatalf("ordinary cancellation has SQLSTATE: %v", err)
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}}, 1))
		}
	})
	loopScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE TABLE loop_effect(v integer)",
			"CREATE FUNCTION loop_put(x integer) RETURNS integer LANGUAGE SQL AS 'INSERT INTO loop_effect VALUES(x); SELECT x'",
			"CREATE FUNCTION loop_protected() RETURNS integer LANGUAGE plpgsql AS $$DECLARE n integer:=0; dummy integer; BEGIN <<o>> LOOP n:=n+1; EXIT WHEN n>3; BEGIN dummy:=loop_put(n); CONTINUE o WHEN n<3; EXIT o; EXCEPTION WHEN OTHERS THEN dummy:=loop_put(99); END; dummy:=loop_put(98); END LOOP; <<b>> BEGIN dummy:=loop_put(4); EXIT b; EXCEPTION WHEN OTHERS THEN dummy:=loop_put(97); END b; RETURN n; END$$")
		// Each route has its own effect bag, proving normal flow commits protected
		// work instead of rolling it back or entering an exception handler.
		for _, prepared := range []bool{false, true} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM loop_effect", 0))
			s := fixture.Select("SELECT loop_protected() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}})
			if prepared {
				s = fixture.Prepared(s)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM loop_effect ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM loop_effect", 4))
		}
	})
}

func TestReferencePLpgSQLLoopPersistence(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "loop_saved")
	loopScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE SCHEMA loop_saved",
			"CREATE FUNCTION loop_saved.sum_to(x integer) RETURNS integer LANGUAGE plpgsql AS $$DECLARE n integer:=0; s integer:=0; BEGIN <<o>> LOOP n:=n+1; s:=s+n; EXIT o WHEN n>=x; END LOOP o; RETURN n*100+s; END$$",
			"CREATE FUNCTION loop_saved.positive(x integer) RETURNS boolean LANGUAGE plpgsql AS $$BEGIN LOOP EXIT WHEN x>0; RETURN false; END LOOP; RETURN true; END$$",
			"CREATE VIEW loop_saved.captured AS SELECT loop_saved.sum_to(5) AS v",
			"CREATE TABLE loop_saved.data(id integer,v integer DEFAULT loop_saved.sum_to(5),CONSTRAINT loop_positive CHECK(loop_saved.positive(v)))")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO loop_saved.data(id) VALUES(1)", 1))
		q := "SELECT loop_saved.sum_to($1) AS v"
		original, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer original.Close()
		check := func(conn *sql.Conn, backend fixture.Backend) {
			plpgsqlValue(t, p, backend, conn, "SELECT v FROM loop_saved.captured", "INT4", int64(515))
			plpgsqlValue(t, p, backend, conn, "SELECT v FROM loop_saved.data WHERE id=1", "INT4", int64(515))
			parametersValue(t, p, backend, conn, q, "v", "INT4", int64(306), 3)
			parametersHeld(t, p, backend, conn, q, "INT4", [][]any{{5}, {1}, {3}, {5}}, []any{int64(515), int64(101), int64(306), int64(515)})
			want := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "data" violates check constraint "loop_positive"`)
			want.Detail = "Failing row contains (2, -2)."
			want.Schema = "loop_saved"
			want.Table = "data"
			want.Constraint = "loop_positive"
			plpgsqlError(t, p, conn, "INSERT INTO loop_saved.data VALUES(2,-2)", false, want)
			plpgsqlValue(t, p, backend, conn, "SELECT count(*) AS v FROM loop_saved.data", "INT8", int64(1))
			if conn == c && backend.PG == b.PG {
				for _, tc := range []struct {
					input int
					want  int64
				}{{5, 515}, {1, 101}, {3, 306}, {5, 515}} {
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: original, SQL: q}, backend.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{tc.want}}, tc.input))
				}
			}
		}
		parametersReplay(t, p, b, c, "loop_saved", check)
	})
}

func TestReferencePLpgSQLLoopCancellation(t *testing.T) {
	for _, body := range []string{"LOOP END LOOP;", "LOOP CONTINUE; END LOOP;"} {
		for _, prepared := range []bool{false, true} {
			t.Run(body+map[bool]string{false: "direct", true: "held"}[prepared], func(t *testing.T) {
				loopScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					userOperatorExec(t, p, b, c, "CREATE FUNCTION loop_infinite() RETURNS void LANGUAGE plpgsql AS $$BEGIN "+body+" END$$")
					q := "SELECT loop_infinite() AS v"
					var stmt *sql.Stmt
					if prepared {
						var err error
						stmt, err = c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
					}
					// PostgreSQL checks the empty body under its enclosing LOOP;
					// a nonempty body checks after selecting its CONTINUE statement.
					action := "LOOP"
					if body == "LOOP CONTINUE; END LOOP;" {
						action = "CONTINUE"
					}
					fixture.PLpgSQLLoopActiveCancel(t, p, b, c, q, "PL/pgSQL function loop_infinite() line 1 at "+action, func(ctx context.Context) fixture.RelationOutcome {
						var rows *sql.Rows
						var err error
						if stmt != nil {
							rows, err = stmt.QueryContext(ctx)
						} else {
							rows, err = c.QueryContext(ctx, q)
						}
						return fixture.RelationOutcome{Rows: rows, Err: err}
					})
					plpgsqlValue(t, p, b, c, "SELECT 1 AS v", "INT4", int64(1))
				})
			})
		}
	}
}
