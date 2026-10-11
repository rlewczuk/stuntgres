package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeleteCoreExcludedForms(t *testing.T) {
	updateFromReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		run(fixture.Exec("CREATE TABLE t(id INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1),(2)", 2))
		if !b.PG {
			for _, prepared := range []bool{false, true} {
				step := fixture.ImplicitError("DELETE FROM t WHERE CURRENT OF c", fixture.Diagnostic{Code: "0A000", Message: "DELETE CURRENT OF is unsupported", Position: "21"})
				step.Prepared, step.SelectRows, step.ErrorStage = prepared, true, "execute"
				if prepared {
					step.ErrorStage = "prepare"
				}
				run(step)
				fixture.DeleteReferenceRows(t, p, c, b.Recorder, 1, 2)
			}
			return
		}
		// Successful self-USING/RETURNING moved to the shared
		// TestReferenceDeleteUsingMigratedExcludedForm, not deleted coverage.
		tx, err := c.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		txRun := updateFromRun(t, p, b, tx)
		txRun(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t ORDER BY id FOR UPDATE", 0))
		txRun(fixture.Select("FETCH NEXT FROM c", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		txRun(fixture.Exec("DELETE FROM t WHERE CURRENT OF c", 1))
		fixture.DeleteReferenceRows(t, p, tx, b.Recorder, 2)
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		fixture.DeleteReferenceRows(t, p, c, b.Recorder, 2)
	})
}
