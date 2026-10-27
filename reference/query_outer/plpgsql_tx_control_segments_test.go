package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func txControlSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	resultsExec(t, p, b, c, "CREATE TABLE tx_log(v integer)", "CREATE PROCEDURE tx_segments(IN base integer) LANGUAGE plpgsql AS $$BEGIN INSERT INTO tx_log VALUES(base+1); COMMIT; INSERT INTO tx_log VALUES(base+2); ROLLBACK; INSERT INTO tx_log VALUES(base+3); END$$")
}

func txControlRows(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, values ...int64) {
	t.Helper()
	rows := make([][]any, len(values))
	for i, v := range values {
		rows[i] = []any{v}
	}
	resultsStoredBoth(t, p, b, c, "SELECT v FROM tx_log ORDER BY v", []string{"v"}, []string{"INT4"}, rows)
}

func TestReferencePLpgSQLTxControlSegments(t *testing.T) {
	proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		txControlSetup(t, p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CALL tx_segments(0)", 0))
		txControlRows(t, p, b, c, 1, 3)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM tx_log", 2))
		q := "CALL tx_segments($1::integer)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		txControlRows(t, p, b, c)
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, base := range []int{10, 20} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 0, base))
		}
		txControlRows(t, p, b, c, 11, 13, 21, 23)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM tx_log", 4))
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec("DO $$BEGIN INSERT INTO tx_log VALUES(1); COMMIT AND NO CHAIN; INSERT INTO tx_log VALUES(2); ROLLBACK AND NO CHAIN; COMMIT; ROLLBACK; INSERT INTO tx_log VALUES(3); END$$", 0)
			if prepared {
				s = fixture.Prepared(s)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			txControlRows(t, p, b, c, 1, 3)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM tx_log", 2))
		}
		resultsExec(t, p, b, c, "CREATE FUNCTION tx_dead() RETURNS integer LANGUAGE plpgsql AS $$BEGIN IF false THEN COMMIT; END IF; RETURN 7; END$$", "CREATE PROCEDURE tx_dead_proc() LANGUAGE plpgsql SECURITY DEFINER AS $$BEGIN IF false THEN ROLLBACK; END IF; END$$")
		resultsBoth(t, p, b, c, "SELECT tx_dead() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CALL tx_dead_proc()", 0))
	})
}

func TestReferencePLpgSQLTxControlSmoke(t *testing.T) {
	proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		txControlSetup(t, p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CALL tx_segments(0)", 0))
		q := "CALL tx_segments($1::integer)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.Exec(q, 0, 10))
		txControlRows(t, p, b, c, 1, 3, 11, 13)
		for _, prepared := range []bool{false, true} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM tx_log", 4))
			s := fixture.Exec("DO $$BEGIN INSERT INTO tx_log VALUES(1); COMMIT; INSERT INTO tx_log VALUES(2); ROLLBACK; INSERT INTO tx_log VALUES(3); END$$", 0)
			if prepared {
				s = fixture.Prepared(s)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			txControlRows(t, p, b, c, 1, 3)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO tx_log VALUES(11),(13)", 2))
		}
		tx, err := c.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		want := proceduresDiagnostic("2D000", "invalid transaction termination", 0)
		want.Context = "PL/pgSQL function tx_segments(integer) line 1 at COMMIT"
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.ErrorDiagnostic("CALL tx_segments(0)", want))
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.ErrorDiagnostic("SELECT 1", proceduresDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block", 0)))
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		txControlRows(t, p, b, c, 1, 3, 11, 13)
		resultsExec(t, p, b, c, "CREATE PROCEDURE tx_frame(INOUT x integer) LANGUAGE plpgsql AS $$BEGIN x:=x+1;COMMIT;END$$")
		proceduresBoth(t, p, b, c, "CALL tx_frame(10)", []string{"x"}, []string{"INT4"}, [][]any{{int64(11)}})
		for _, bound := range []bool{false, true} {
			resultsExec(t, p, b, c, "CREATE OR REPLACE PROCEDURE tx_smoke_hold(OUT visits text) LANGUAGE plpgsql AS $$"+txLoopSource(bound, "SELECT x AS v FROM generate_series(1,3) x", "visits:=coalesce(visits,'')||r.v::text;IF r.v=1 THEN COMMIT;ELSE ROLLBACK;END IF;")+"$$")
			proceduresBoth(t, p, b, c, "CALL tx_smoke_hold(NULL)", []string{"visits"}, []string{"TEXT"}, [][]any{{"123"}})
			resultsCountBoth(t, p, b, c, "SELECT count(*) AS v FROM pg_cursors WHERE name='tx_bound'", 0)
		}
		resultsExec(t, p, b, c, "CREATE PROCEDURE tx_smoke_modes(OUT chained text,OUT plain text) LANGUAGE plpgsql AS $$BEGIN ROLLBACK;SET TRANSACTION ISOLATION LEVEL REPEATABLE READ,READ ONLY,DEFERRABLE;COMMIT AND CHAIN;chained:=current_setting('transaction_isolation')||'/'||current_setting('transaction_read_only')||'/'||current_setting('transaction_deferrable');COMMIT;plain:=current_setting('transaction_isolation')||'/'||current_setting('transaction_read_only')||'/'||current_setting('transaction_deferrable');END$$")
		proceduresBoth(t, p, b, c, "CALL tx_smoke_modes(NULL,NULL)", []string{"chained", "plain"}, []string{"TEXT", "TEXT"}, [][]any{{"repeatable read/on/on", "read committed/off/off"}})
	})
}
