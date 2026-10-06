//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceDeleteCoreInsertQuerySource(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			name := map[bool]string{false: "direct", true: "prepared"}[prepared] + "/" + map[bool]string{false: "rollback", true: "commit"}[commit]
			t.Run(name, func(t *testing.T) {
				p := newReferencePair(t)
				for i, db := range []*sql.DB{p.memory, p.postgres} {
					t.Run([]string{"native", "postgres"}[i], func(t *testing.T) {
						r := []*referenceRecorder{p.memoryRecord, p.postgresRecord}[i]
						a, b := referenceLockConnections(t, p.ctx, db)
						run := func(x referenceExecutor, step referenceStep) {
							referenceRunStep(t, p.ctx, x, r, step)
						}
						for _, step := range []referenceStep{
							referenceExec("CREATE TABLE t(id INT)", 0),
							referenceExec("INSERT INTO t VALUES(1),(2),(2),(3)", 4),
							referenceExec("CREATE TABLE destination(id INT)", 0),
							referenceExec("DELETE FROM t WHERE id=1", 1),
						} {
							run(a, step)
						}
						copySQL := "INSERT INTO destination SELECT id FROM t ORDER BY id"
						step := referenceExec(copySQL, 3)
						if prepared {
							step = referencePrepared(step)
						}
						run(a, step)
						run(a, referenceSelect("SELECT id FROM destination ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(2)}, {int64(3)}}))
						run(a, referenceExec("DELETE FROM destination", 3))
						tx, err := a.BeginTx(p.ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						run(tx, referenceExec("INSERT INTO t VALUES(7)", 1))
						run(tx, referenceExec("DELETE FROM t WHERE id=2 OR id=7", 3))
						for _, step := range []referenceStep{
							referenceExec("INSERT INTO t SELECT id FROM t", 1),
							referenceExec(copySQL, 2),
							referenceExec("DELETE FROM t", 2),
							referenceExec(copySQL, 0),
						} {
							if prepared {
								step = referencePrepared(step)
							}
							run(tx, step)
						}
						deleteReferenceRows(t, p, tx, r)
						run(tx, referenceSelect("SELECT id FROM destination", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(3)}}))
						deleteReferenceRows(t, p, b, r, 2, 2, 3)
						run(b, referenceSelect("SELECT id FROM destination", []string{"id"}, []string{"INT4"}, [][]any{}))
						if commit {
							err = tx.Commit()
						} else {
							err = tx.Rollback()
						}
						if err != nil {
							t.Fatal(err)
						}
						if commit {
							deleteReferenceRows(t, p, b, r)
							run(b, referenceSelect("SELECT id FROM destination", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(3)}}))
						} else {
							deleteReferenceRows(t, p, b, r, 2, 2, 3)
							run(b, referenceSelect("SELECT id FROM destination", []string{"id"}, []string{"INT4"}, [][]any{}))
						}
					})
				}
			})
		}
	}
}
