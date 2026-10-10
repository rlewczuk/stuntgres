package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceDeleteCoreExcludedForms(t *testing.T) {
	updateFromReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		run(referenceExec("CREATE TABLE t(id INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(1),(2)", 2))
		if !b.pg {
			for _, prepared := range []bool{false, true} {
				step := referenceImplicitError("DELETE FROM t WHERE CURRENT OF c", referenceDiagnostic{Code: "0A000", Message: "DELETE CURRENT OF is unsupported", Position: "21"})
				step.prepared, step.selectRows, step.errorStage = prepared, true, "execute"
				if prepared {
					step.errorStage = "prepare"
				}
				run(step)
				deleteReferenceRows(t, p, c, b.recorder, 1, 2)
			}
			return
		}
		// Successful self-USING/RETURNING moved to the shared
		// TestReferenceDeleteUsingMigratedExcludedForm, not deleted coverage.
		tx, err := c.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		txRun := updateFromRun(t, p, b, tx)
		txRun(referenceExec("DECLARE c CURSOR FOR SELECT id FROM t ORDER BY id FOR UPDATE", 0))
		txRun(referenceSelect("FETCH NEXT FROM c", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		txRun(referenceExec("DELETE FROM t WHERE CURRENT OF c", 1))
		deleteReferenceRows(t, p, tx, b.recorder, 2)
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		deleteReferenceRows(t, p, c, b.recorder, 2)
	})
}
