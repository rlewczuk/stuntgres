package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceBtreeIndexSnapshotCurrency(t *testing.T) {
	p := newReferenceScenario(t)
	referenceBtreeStreams(t, p, func(t *testing.T, db *sql.DB, recorder *referenceRecorder) {
		for _, step := range []referenceStep{referenceExec("CREATE TABLE t(n INT)", 0), referenceExec("INSERT INTO t VALUES(1)", 1), referenceExec("CREATE INDEX idx ON t(n)", 0)} {
			referenceRunStep(t, p.ctx, db, recorder, step)
		}
		tx, err := db.BeginTx(p.ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		referenceRunStep(t, p.ctx, tx, recorder, referenceSelect("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
		referenceRunStep(t, p.ctx, db, recorder, referenceExec("INSERT INTO t VALUES(2)", 1))
		referenceRunStep(t, p.ctx, tx, recorder, referencePrepared(referenceSelect("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}})))
		referenceRunStep(t, p.ctx, tx, recorder, referenceExec("INSERT INTO t VALUES(3)", 1))
		referenceRunStep(t, p.ctx, tx, recorder, referenceSelect("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}}))
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		referenceRunStep(t, p.ctx, db, recorder, referenceSelect("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
	})
}
