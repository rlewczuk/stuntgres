package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLWhileAtomicity(t *testing.T) {
	whileScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE TABLE while_atomic(v integer)",
			"CREATE FUNCTION while_atomic_value(x integer) RETURNS integer LANGUAGE plpgsql AS $$DECLARE n integer:=0; BEGIN WHILE n/x<3 LOOP n:=n+1; END LOOP; RETURN n; END$$")
		want := plpgsqlRuntimeDiagnostic("22012", "division by zero", "while_atomic_value(integer)", "WHILE")
		plpgsqlError(t, p, c, "INSERT INTO while_atomic VALUES(while_atomic_value(1)),(while_atomic_value(0))", false, want)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM while_atomic", []string{"v"}, []string{"INT4"}, nil))
		plpgsqlValue(t, p, b, c, "SELECT while_atomic_value(1) AS v", "INT4", int64(3))
		plpgsqlError(t, p, c, "SELECT while_atomic_value(0) AS v", true, want)
		q := "SELECT while_atomic_value($1) AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, input := range []int{1, 0, 1} {
			if input != 0 {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}}, input))
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
	})
	for _, actualError := range []bool{false, true} {
		t.Run(map[bool]string{false: "protected_transfers", true: "protected_condition_error"}[actualError], func(t *testing.T) {
			whileScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				body := "DECLARE n integer:=0; dummy integer; BEGIN <<o>> WHILE n<3 LOOP n:=n+1; BEGIN dummy:=while_put(n); CONTINUE o WHEN n<3; EXIT o; EXCEPTION WHEN OTHERS THEN dummy:=while_put(99); END; dummy:=while_put(98); END LOOP; RETURN n; END"
				want, effects := int64(3), [][]any{{int64(1)}, {int64(2)}, {int64(3)}}
				if actualError {
					body = "DECLARE n integer:=0; x integer:=1; dummy integer; BEGIN BEGIN dummy:=while_put(8); WHILE n/x<3 LOOP n:=n+1; dummy:=while_put(n); x:=0; END LOOP; EXCEPTION WHEN division_by_zero THEN dummy:=while_put(9); BEGIN WHILE 'nope'::text LOOP EXIT; END LOOP; EXCEPTION WHEN invalid_text_representation THEN dummy:=0; END; IF SQLSTATE <> '22012' THEN RETURN 99; END IF; END; RETURN n; END"
					want, effects = 1, [][]any{{int64(9)}}
				}
				userOperatorExec(t, p, b, c,
					"CREATE TABLE while_effect(v integer)",
					"CREATE FUNCTION while_put(x integer) RETURNS integer LANGUAGE SQL AS 'INSERT INTO while_effect VALUES(x); SELECT x'",
					"CREATE FUNCTION while_protected() RETURNS integer LANGUAGE plpgsql AS $$"+body+"$$")
				for _, prepared := range []bool{false, true} {
					for range 2 {
						step := fixture.Select("SELECT while_protected() AS v", []string{"v"}, []string{"INT4"}, [][]any{{want}})
						if prepared {
							step = fixture.Prepared(step)
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM while_effect ORDER BY v", []string{"v"}, []string{"INT4"}, effects))
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM while_effect", int64(len(effects))))
					}
				}
			})
		})
	}
}

func TestReferencePLpgSQLWhilePersistence(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "while_saved")
	whileScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE SCHEMA while_saved",
			"CREATE FUNCTION while_saved.sum_to(x integer) RETURNS integer LANGUAGE plpgsql AS $$DECLARE n integer:=0; s integer:=0; BEGIN <<o>> WHILE n<x LOOP n:=n+1; s:=s+n; CONTINUE o; END LOOP o; RETURN n*100+s; END$$",
			"CREATE VIEW while_saved.captured AS SELECT while_saved.sum_to(5) AS v",
			"CREATE TABLE while_saved.data(id integer,v integer DEFAULT while_saved.sum_to(5))")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO while_saved.data(id) VALUES(1)", 1))
		q := "SELECT while_saved.sum_to($1) AS v"
		original, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer original.Close()
		check := func(conn *sql.Conn, backend fixture.Backend) {
			plpgsqlValue(t, p, backend, conn, "SELECT v FROM while_saved.captured", "INT4", int64(515))
			plpgsqlValue(t, p, backend, conn, "SELECT v FROM while_saved.data WHERE id=1", "INT4", int64(515))
			parametersValue(t, p, backend, conn, q, "v", "INT4", int64(306), 3)
			parametersHeld(t, p, backend, conn, q, "INT4", [][]any{{5}, {1}, {3}, {5}}, []any{int64(515), int64(101), int64(306), int64(515)})
			if conn == c && backend.PG == b.PG {
				for _, tc := range []struct {
					input int
					want  int64
				}{{5, 515}, {1, 101}, {3, 306}, {5, 515}} {
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: original, SQL: q}, backend.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{tc.want}}, tc.input))
				}
			}
		}
		parametersReplay(t, p, b, c, "while_saved", check)
	})
}

func TestReferencePLpgSQLWhileCancellation(t *testing.T) {
	for _, body := range []string{"WHILE true LOOP END LOOP;", "WHILE true LOOP CONTINUE; END LOOP;"} {
		for _, prepared := range []bool{false, true} {
			t.Run(body+map[bool]string{false: "direct", true: "held"}[prepared], func(t *testing.T) {
				whileScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					userOperatorExec(t, p, b, c, "CREATE FUNCTION while_infinite() RETURNS void LANGUAGE plpgsql AS $$BEGIN "+body+" END$$")
					q := "SELECT while_infinite() AS v"
					var stmt *sql.Stmt
					if prepared {
						var err error
						stmt, err = c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
					}
					// Both admitted cancellation contexts were observed on the
					// pinned PostgreSQL 18.6 oracle in tests-1.md.
					action := "WHILE"
					if body == "WHILE true LOOP CONTINUE; END LOOP;" {
						action = "CONTINUE"
					}
					fixture.PLpgSQLLoopActiveCancel(t, p, b, c, q, "PL/pgSQL function while_infinite() line 1 at "+action, func(ctx context.Context) fixture.RelationOutcome {
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
