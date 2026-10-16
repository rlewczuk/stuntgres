package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func holdRun(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, q string) {
	t.Helper()
	sqlCursorStep(t, p, b, c, fixture.Exec(q, 0))
}
func holdDeclare(name string) string {
	return "DECLARE " + name + " NO SCROLL CURSOR WITH HOLD FOR SELECT i,t FROM cursor_rows ORDER BY i"
}
func holdBatch(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, q string, rows ...[]any) {
	t.Helper()
	sqlCursorStep(t, p, b, c, sqlCursorBatch(q, rows, b.PG))
}
func holdRemaining(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, route string) {
	sqlCursorSetup(t, p, b, c)
	var x fixture.Executor = c
	var tx *sql.Tx
	if route == "go" {
		var err error
		tx, err = c.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		x = tx
	} else {
		holdRun(t, p, b, c, "BEGIN")
	}
	sqlCursorStep(t, p, b, x, fixture.Select(holdDeclare("h"), []string{}, []string{}, [][]any{}))
	holdRun(t, p, b, x, "DECLARE n NO SCROLL CURSOR FOR SELECT i,t FROM cursor_rows ORDER BY i")
	holdBatch(t, p, b, x, "FETCH 1 FROM h", []any{int64(1), "one"})
	if tx != nil {
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	} else if route == "chain" {
		holdRun(t, p, b, c, "COMMIT AND CHAIN")
	} else {
		holdRun(t, p, b, c, "COMMIT")
	}
	if route == "chain" {
		sqlCursorError(t, p, b, c, "FETCH ALL FROM n", sqlCursorMissing("n"))
	} else {
		sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic("FETCH ALL FROM n", sqlCursorMissing("n")))
	}
	holdBatch(t, p, b, c, "FETCH 1 FROM h", []any{int64(2), nil})
	holdBatch(t, p, b, c, "FETCH ALL FROM h", []any{int64(3), "three"})
	for range 2 {
		holdBatch(t, p, b, c, "FETCH ALL FROM h")
	}
	if route == "chain" {
		holdRun(t, p, b, c, "COMMIT")
	}
	holdRun(t, p, b, c, "BEGIN")
	holdRun(t, p, b, c, "COMMIT")
	holdBatch(t, p, b, c, "FETCH ALL FROM h")
	holdRun(t, p, b, c, "CLOSE h")
}
func TestReferenceSQLCursorHoldCommitRemaining(t *testing.T) {
	for _, route := range []string{"sql", "go", "chain"} {
		t.Run(route, func(t *testing.T) {
			sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) { holdRemaining(t, p, b, c, route) })
		})
	}
	for _, count := range []int{0, 3, 4} {
		t.Run(string(rune('0'+count)), func(t *testing.T) {
			sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				sqlCursorSetup(t, p, b, c)
				holdRun(t, p, b, c, "BEGIN")
				holdRun(t, p, b, c, holdDeclare("h"))
				if count > 0 {
					holdBatch(t, p, b, c, "FETCH 3 FROM h", []any{int64(1), "one"}, []any{int64(2), nil}, []any{int64(3), "three"})
				}
				if count == 4 {
					holdBatch(t, p, b, c, "FETCH ALL FROM h")
				}
				holdRun(t, p, b, c, "DECLARE e NO SCROLL CURSOR WITH HOLD FOR SELECT i,t FROM cursor_rows WHERE FALSE")
				holdRun(t, p, b, c, "DECLARE z NO SCROLL CURSOR WITH HOLD FOR SELECT FROM cursor_rows")
				holdRun(t, p, b, c, "COMMIT")
				if count == 0 {
					holdBatch(t, p, b, c, "FETCH ALL FROM h", []any{int64(1), "one"}, []any{int64(2), nil}, []any{int64(3), "three"})
				} else {
					holdBatch(t, p, b, c, "FETCH ALL FROM h")
				}
				holdBatch(t, p, b, c, "FETCH ALL FROM e")
				sqlCursorStep(t, p, b, c, fixture.Select("FETCH 1 FROM z", []string{}, []string{}, [][]any{{}}))
				sqlCursorStep(t, p, b, c, fixture.Exec("FETCH ALL FROM z", 2))
				sqlCursorStep(t, p, b, c, fixture.Select("FETCH ALL FROM z", []string{}, []string{}, [][]any{}))
			})
		})
	}
}
func TestReferenceSQLCursorHoldAutocommitAndPrepared(t *testing.T) {
	sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sqlCursorSetup(t, p, b, c)
		for _, clause := range []string{"", " WITHOUT HOLD"} {
			sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic("DECLARE n CURSOR"+clause+" FOR SELECT 1", sqlCursorDiagnostic("25P01", "DECLARE CURSOR can only be used in transaction blocks")))
		}
		q := "DECLARE h NO SCROLL CURSOR WITH HOLD FOR SELECT i,$1::TEXT AS t FROM cursor_rows WHERE i >= $2 ORDER BY i"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic("FETCH ALL FROM h", sqlCursorMissing("h")))
		marker := []byte("owned")
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		sqlCursorStep(t, p, b, held, fixture.Exec(q, 0, marker, 2))
		copy(marker, []byte("other"))
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		sqlCursorStep(t, p, b, c, fixture.Select("FETCH ALL FROM h", []string{"i", "t"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "owned"}, {int64(3), "owned"}}))
		sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic(holdDeclare("h"), sqlCursorDiagnostic("42P03", "cursor \"h\" already exists")))
		holdRun(t, p, b, c, "CLOSE h")
		holdRun(t, p, b, c, holdDeclare("h"))
		holdBatch(t, p, b, c, "FETCH 1 FROM h", []any{int64(1), "one"})
		for _, q := range []string{"FETCH ALL FROM absent", "CLOSE absent"} {
			s, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			sqlCursorStep(t, p, b, fixture.HeldStatement{Executor: c, Statement: s, SQL: q}, fixture.ErrorDiagnostic(q, sqlCursorMissing("absent")))
			s.Close()
		}
	})
}
func TestReferenceSQLCursorHoldRollbackAndSavepoints(t *testing.T) {
	sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sqlCursorSetup(t, p, b, c)
		holdRun(t, p, b, c, holdDeclare("old"))
		holdRun(t, p, b, c, "BEGIN")
		holdRun(t, p, b, c, holdDeclare("fresh"))
		holdBatch(t, p, b, c, "FETCH 1 FROM old", []any{int64(1), "one"})
		holdRun(t, p, b, c, "ROLLBACK")
		sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic("FETCH ALL FROM fresh", sqlCursorMissing("fresh")))
		holdBatch(t, p, b, c, "FETCH 1 FROM old", []any{int64(2), nil})
		holdRun(t, p, b, c, "BEGIN")
		holdRun(t, p, b, c, holdDeclare("parent"))
		holdRun(t, p, b, c, "SAVEPOINT s")
		holdRun(t, p, b, c, holdDeclare("child"))
		holdBatch(t, p, b, c, "FETCH 1 FROM parent", []any{int64(1), "one"})
		holdRun(t, p, b, c, "ROLLBACK TO s")
		sqlCursorError(t, p, b, c, "FETCH ALL FROM child", sqlCursorMissing("child"))
		holdBatch(t, p, b, c, "FETCH 1 FROM parent", []any{int64(2), nil})
		holdRun(t, p, b, c, "COMMIT")
		holdRun(t, p, b, c, "BEGIN")
		holdRun(t, p, b, c, "CLOSE old")
		holdRun(t, p, b, c, "ROLLBACK")
		sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic("FETCH ALL FROM old", sqlCursorMissing("old")))
		holdRun(t, p, b, c, "BEGIN")
		holdRun(t, p, b, c, holdDeclare("aborted"))
		sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic("SELECT 1/0", sqlCursorDiagnostic("22012", "division by zero")))
		holdRun(t, p, b, c, "ROLLBACK")
		sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic("FETCH ALL FROM aborted", sqlCursorMissing("aborted")))
		holdBatch(t, p, b, c, "FETCH ALL FROM parent", []any{int64(3), "three"})
		holdRun(t, p, b, c, "CLOSE ALL")
		holdRun(t, p, b, c, "CLOSE ALL")
		tx, err := c.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		holdRun(t, p, b, tx, holdDeclare("go_rollback"))
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic("FETCH ALL FROM go_rollback", sqlCursorMissing("go_rollback")))
		holdRun(t, p, b, c, "BEGIN")
		holdRun(t, p, b, c, holdDeclare("chain_rollback"))
		holdRun(t, p, b, c, "ROLLBACK AND CHAIN")
		sqlCursorError(t, p, b, c, "FETCH ALL FROM chain_rollback", sqlCursorMissing("chain_rollback"))
		holdRun(t, p, b, c, "ROLLBACK")
	})
}
func holdMaterialization(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	sqlCursorSetup(t, p, b, c)
	holdRun(t, p, b, c, "CREATE SEQUENCE hold_draw START 10 CACHE 1")
	holdRun(t, p, b, c, "BEGIN")
	holdRun(t, p, b, c, "DECLARE h NO SCROLL CURSOR WITH HOLD FOR SELECT i,nextval('hold_draw') AS n FROM cursor_rows ORDER BY i")
	sqlCursorStep(t, p, b, c, fixture.Select("SELECT last_value,is_called FROM hold_draw", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(10), false}}))
	sqlCursorStep(t, p, b, c, fixture.Select("FETCH 1 FROM h", []string{"i", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(10)}}))
	holdRun(t, p, b, c, "COMMIT")
	sqlCursorStep(t, p, b, c, fixture.Select("SELECT last_value FROM hold_draw", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(12)}}))
	holdRun(t, p, b, c, "DROP TABLE cursor_rows")
	sqlCursorStep(t, p, b, c, fixture.Select("FETCH ALL FROM h", []string{"i", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(2), int64(11)}, {int64(3), int64(12)}}))
	sqlCursorStep(t, p, b, c, fixture.Select("FETCH ALL FROM h", []string{"i", "n"}, []string{"INT4", "INT8"}, [][]any{}))
	holdRun(t, p, b, c, "CLOSE h")
	sqlCursorStep(t, p, b, c, fixture.Select("SELECT last_value FROM hold_draw", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(12)}}))
}
func TestReferenceSQLCursorHoldMaterialization(t *testing.T) {
	t.Run("remaining_draws", func(t *testing.T) { sqlCursorScenario(t, holdMaterialization) })
	t.Run("snapshot_and_implicit", func(t *testing.T) { sqlCursorScenario(t, holdSnapshotAndImplicit) })
}
func TestReferenceSQLCursorHoldFeatureSmoke(t *testing.T) {
	t.Run("remaining", func(t *testing.T) {
		sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) { holdRemaining(t, p, b, c, "sql") })
	})
	t.Run("draws", func(t *testing.T) { sqlCursorScenario(t, holdMaterialization) })
}
