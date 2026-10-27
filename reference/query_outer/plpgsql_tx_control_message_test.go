package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePLpgSQLTxControlMessageContext(t *testing.T) {
	proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		txControlSetup(t, p, b, c)
		want := proceduresDiagnostic("2D000", "invalid transaction termination", 0)
		want.Context = "PL/pgSQL function tx_segments(integer) line 1 at COMMIT"
		for _, q := range []string{"CALL tx_segments(0);SELECT 42", "SELECT 42;CALL tx_segments(0)", "COMMIT;CALL tx_segments(0)", "INSERT INTO tx_log VALUES(9);COMMIT;CALL tx_segments(0)"} {
			rows, err := c.QueryContext(p.Ctx, q)
			if rows != nil {
				if q == "SELECT 42;CALL tx_segments(0)" {
					fixture.BatchReadSet(t, rows, fixture.BatchSet{Columns: []string{"?column?"}, Types: []string{"INT4"}, Rows: [][]any{{int64(42)}}})
				}
				for rows.NextResultSet() {
					for rows.Next() {
						t.Fatal("unexpected result after failed CALL")
					}
				}
				if err == nil {
					err = rows.Err()
				}
				rows.Close()
			}
			fixture.AssertDiagnostic(t, err, &want)
			if q == "INSERT INTO tx_log VALUES(9);COMMIT;CALL tx_segments(0)" {
				txControlRows(t, p, b, c, 9)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM tx_log", 1))
			} else {
				txControlRows(t, p, b, c)
			}
			stmt, err := c.PrepareContext(p.Ctx, q)
			if stmt != nil {
				stmt.Close()
				t.Fatal("prepared multiple commands")
			}
			fixture.AssertDiagnostic(t, err, new(proceduresDiagnostic("42601", "cannot insert multiple commands into a prepared statement", 0)))
			txControlRows(t, p, b, c)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(";/* one command */CALL tx_segments(0);; -- tail\n", 0))
		txControlRows(t, p, b, c, 1, 3)
		resultsExec(t, p, b, c, "CREATE PROCEDURE tx_noend() LANGUAGE plpgsql AS $$BEGIN INSERT INTO tx_log VALUES(5);IF false THEN COMMIT;END IF;END$$")
		rows, err := c.QueryContext(p.Ctx, "CALL tx_noend();SELECT 42 AS v")
		if err != nil {
			t.Fatal(err)
		}
		fixture.BatchSets(t, rows, fixture.BatchSet{Columns: []string{"v"}, Types: []string{"INT4"}, Rows: [][]any{{int64(42)}}})
		txControlRows(t, p, b, c, 1, 3, 5)
	})
}
