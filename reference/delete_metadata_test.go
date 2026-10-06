//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceDeleteCoreMetadataComment(t *testing.T) {
	for _, tc := range []struct{ name, comment string }{
		{"table", "COMMENT ON TABLE t IS 'metadata only'"},
		{"column", "COMMENT ON COLUMN t.id IS 'metadata only'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newReferencePair(t)
			for i, db := range []*sql.DB{p.memory, p.postgres} {
				t.Run([]string{"native", "postgres"}[i], func(t *testing.T) {
					r := []*referenceRecorder{p.memoryRecord, p.postgresRecord}[i]
					a, b := referenceLockConnections(t, p.ctx, db)
					run := func(x referenceExecutor, step referenceStep) {
						referenceRunStep(t, p.ctx, x, r, step)
					}
					run(b, referenceExec("CREATE TABLE t(id INT NOT NULL)", 0))
					run(b, referenceExec("INSERT INTO t VALUES(1),(2),(2)", 3))
					tx, err := a.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					run(tx, referenceExec("INSERT INTO t VALUES(7)", 1))
					run(tx, referenceExec("DELETE FROM t WHERE id=1", 1))
					// RX and COMMENT's SUE are compatible. Require completion
					// before commit, not a goroutine-start timing assumption.
					run(b, referenceExec(tc.comment, 0))
					deleteReferenceRows(t, p, b, r, 1, 2, 2)
					deleteReferenceRows(t, p, tx, r, 2, 2, 7)
					if err := tx.Commit(); err != nil {
						t.Fatal("metadata-only COMMENT invalidated staged DELETE and prior INSERT", err)
					}
					// Each backend must satisfy this prescribed bag, including
					// the prior private INSERT; parity alone is not sufficient.
					deleteReferenceRows(t, p, b, r, 2, 2, 7)
				})
			}
		})
	}
}
