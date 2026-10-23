package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLExceptionTransactions(t *testing.T) {
	for _, inline := range []bool{false, true} {
		name := "select"
		if inline {
			name = "inline"
		}
		t.Run(name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				plpgsqlExceptionSetup(t, p, b, c)
				plpgsqlExceptionDefine(t, p, b, c, "ex_run", "integer", plpgsqlExceptionRollbackBody)
				peer, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer peer.Close()
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ex_log VALUES(9)", 1))
				userOperatorExec(t, p, b, c, "BEGIN")
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ex_log VALUES(7)", 1))
				if inline {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DO $$DECLARE x integer; BEGIN x := ex_put(1); BEGIN x := ex_put(2); x := 1/0; EXCEPTION WHEN division_by_zero THEN x := ex_put(3); END; x := ex_put(4); END$$", 0))
				} else {
					// The handler counts the two pre-existing rows as well as put1.
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT ex_run() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(13)}}))
				}
				plpgsqlExceptionRows(t, p, b, c, 1, 3, 4, 7, 9)
				plpgsqlExceptionRows(t, p, b, peer, 9)
				userOperatorExec(t, p, b, c, "COMMIT")
				plpgsqlExceptionRows(t, p, b, peer, 1, 3, 4, 7, 9)
			})
		})
	}
	for _, tc := range []struct {
		name, body, state string
		savepoint         bool
	}{
		{"unmatched", "DECLARE x integer; BEGIN x := ex_put(2); x := 1/0; EXCEPTION WHEN unique_violation THEN RETURN 99; END", "22012", false},
		{"handler_failure", "DECLARE x integer; BEGIN x := ex_put(2); x := 1/0; EXCEPTION WHEN division_by_zero THEN x := 'bad'::integer; RETURN 99; END", "22P02", false},
		{"savepoint_recovery", "DECLARE x integer; BEGIN x := ex_put(2); x := 1/0; EXCEPTION WHEN unique_violation THEN RETURN 99; END", "22012", true},
		{"inline_abort", "", "22012", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				plpgsqlExceptionSetup(t, p, b, c)
				if tc.body != "" {
					plpgsqlExceptionDefine(t, p, b, c, "ex_abort", "integer", tc.body)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ex_log VALUES(9)", 1))
				userOperatorExec(t, p, b, c, "BEGIN")
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ex_log VALUES(7)", 1))
				if tc.savepoint {
					userOperatorExec(t, p, b, c, "SAVEPOINT ex_user", "SAVEPOINT ex_user")
				}
				q := "SELECT ex_abort() AS v"
				if tc.body == "" {
					q = "DO $$DECLARE x integer; BEGIN x := ex_put(2); x := 1/0; END$$"
				}
				_, err := c.ExecContext(p.Ctx, q)
				want := plpgsqlExceptionDivision("ex_abort()")
				if tc.body == "" {
					want = plpgsqlExceptionDivision("inline_code_block")
				} else if tc.state == "22P02" {
					want = plpgsqlRuntimeDiagnostic("22P02", `invalid input syntax for type integer: "bad"`, "ex_abort()", "assignment")
					want.InternalPosition = "6"
					want.InternalQuery = "x := 'bad'::integer"
				}
				fixture.AssertDiagnostic(t, err, &want)
				_, err = c.ExecContext(p.Ctx, "SELECT 1")
				aborted := fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")
				fixture.AssertDiagnostic(t, err, &aborted)
				if tc.savepoint {
					userOperatorExec(t, p, b, c, "ROLLBACK TO SAVEPOINT ex_user", "RELEASE SAVEPOINT ex_user", "ROLLBACK TO SAVEPOINT ex_user", "RELEASE SAVEPOINT ex_user")
					plpgsqlExceptionRows(t, p, b, c, 7, 9)
					userOperatorExec(t, p, b, c, "COMMIT")
					plpgsqlExceptionRows(t, p, b, c, 7, 9)
				} else {
					userOperatorExec(t, p, b, c, "ROLLBACK")
					plpgsqlExceptionRows(t, p, b, c, 9)
				}
				plpgsqlValue(t, p, b, c, "SELECT 7 AS v", "INT4", int64(7))
			})
		})
	}
}

func TestReferencePLpgSQLExceptionPersistence(t *testing.T) {
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c, "CREATE FUNCTION ex_repeat(x integer) RETURNS text LANGUAGE plpgsql AS $$DECLARE n integer; BEGIN n := 12/x; RETURN 'ok:' || n; EXCEPTION WHEN division_by_zero THEN RETURN SQLSTATE || ':' || SQLERRM; END$$")
		plpgsqlExceptionDefine(t, p, b, c, "ex_original", "integer", "DECLARE x integer; BEGIN x := 1/0; EXCEPTION WHEN division_by_zero THEN RAISE; END")
		plpgsqlExceptionDefine(t, p, b, c, "ex_bare", "integer", "BEGIN RAISE; RETURN 1; END")
		userOperatorExec(t, p, b, c, "CREATE FUNCTION ex_held(x integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 12/x; EXCEPTION WHEN division_by_zero THEN RAISE; END$$")
		errorSQL := "SELECT ex_held($1) AS v"
		errorHeld, err := c.PrepareContext(p.Ctx, errorSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer errorHeld.Close()
		checkHeld := func() {
			for _, arg := range []int{3, 0, 4, 0, 3} {
				if arg != 0 {
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: errorHeld, SQL: errorSQL}, b.Recorder, fixture.Select(errorSQL, []string{"v"}, []string{"INT4"}, [][]any{{int64(12 / arg)}}, arg))
					continue
				}
				failedRows, err := errorHeld.QueryContext(p.Ctx, arg)
				if failedRows != nil {
					failedRows.Close()
					t.Fatal("held error exposed rows")
				}
				want := plpgsqlRuntimeDiagnostic("22012", "division by zero", "ex_held(integer)", "RETURN")
				fixture.AssertDiagnostic(t, err, &want)
			}
		}
		checkHeld()
		q := "SELECT ex_repeat($1) AS v"
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		check := func() {
			for _, tc := range []struct {
				arg  int
				want string
			}{{3, "ok:4"}, {0, "22012:division by zero"}, {4, "ok:3"}, {0, "22012:division by zero"}} {
				s := fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.arg)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, s)
			}
		}
		check()
		plpgsqlError(t, p, c, "SELECT ex_original() AS v", true, plpgsqlExceptionDivision("ex_original()"))
		check()
		plpgsqlError(t, p, c, "SELECT ex_bare() AS v", true, plpgsqlRuntimeDiagnostic("0Z002", "RAISE without parameters cannot be used outside an exception handler", "ex_bare()", "RAISE"))
		other, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer other.Close()
		plpgsqlValue(t, p, b, other, "SELECT ex_repeat(0) AS v", "TEXT", "22012:division by zero")
		plpgsqlError(t, p, other, "SELECT ex_bare() AS v", true, plpgsqlRuntimeDiagnostic("0Z002", "RAISE without parameters cannot be used outside an exception handler", "ex_bare()", "RAISE"))
		rows, err := c.QueryContext(p.Ctx, "SELECT ex_repeat(0) AS v")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		// pq streams, so retain an already scanned string, not a live protocol
		// cursor that would prevent another command on this physical session.
		if !rows.Next() {
			t.Fatal("missing retained value", rows.Err())
		}
		var retained string
		if err := rows.Scan(&retained); err != nil {
			t.Fatal(err)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("retained cardinality", rows.Err())
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		check()
		if !b.PG {
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
			check()
			checkHeld()
			plpgsqlError(t, p, c, "SELECT ex_original() AS v", true, plpgsqlExceptionDivision("ex_original()"))
		}
		if retained != "22012:division by zero" {
			t.Fatalf("retained text changed: %q", retained)
		}
	})
}

func TestReferencePLpgSQLExceptionSmoke(t *testing.T) {
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		plpgsqlExceptionSetup(t, p, b, c)
		plpgsqlExceptionDefine(t, p, b, c, "ex_run", "integer", plpgsqlExceptionRollbackBody)
		plpgsqlExceptionDefine(t, p, b, c, "ex_rethrow", "integer", "DECLARE x integer; BEGIN x := 1/0; EXCEPTION WHEN division_by_zero THEN BEGIN x := 'bad'::integer; EXCEPTION WHEN invalid_text_representation THEN x := 1; END; RAISE; END")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT ex_run() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(11)}}))
		plpgsqlExceptionRows(t, p, b, c, 1, 3, 4)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM ex_log", 3))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DO $$DECLARE x integer; BEGIN x := ex_put(1); BEGIN x := ex_put(2); x := 1/0; EXCEPTION WHEN division_by_zero THEN x := ex_put(3); END; x := ex_put(4); END$$", 0))
		plpgsqlExceptionRows(t, p, b, c, 1, 3, 4)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM ex_log", 3))
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		userOperatorExec(t, p, b, c, "BEGIN")
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT ex_run() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(11)}}))
		plpgsqlExceptionRows(t, p, b, c, 1, 3, 4)
		plpgsqlExceptionRows(t, p, b, peer)
		userOperatorExec(t, p, b, c, "COMMIT")
		plpgsqlExceptionRows(t, p, b, peer, 1, 3, 4)
		userOperatorExec(t, p, b, c, "BEGIN")
		_, err = c.ExecContext(p.Ctx, "SELECT ex_rethrow() AS v")
		want := plpgsqlExceptionDivision("ex_rethrow()")
		fixture.AssertDiagnostic(t, err, &want)
		_, err = c.ExecContext(p.Ctx, "SELECT 1")
		aborted := fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")
		fixture.AssertDiagnostic(t, err, &aborted)
		userOperatorExec(t, p, b, c, "ROLLBACK")
		plpgsqlExceptionRows(t, p, b, c, 1, 3, 4)
		plpgsqlValue(t, p, b, c, "SELECT 7 AS v", "INT4", int64(7))
	})
}
