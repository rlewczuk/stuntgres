package reference_test

import (
	"testing"
)

func TestReferenceUpdateCoreDeleteComposition(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			for _, deleteFirst := range []bool{false, true} {
				name := map[bool]string{false: "direct", true: "prepared"}[prepared] + map[bool]string{false: "/rollback", true: "/commit"}[commit] + map[bool]string{false: "/update_first", true: "/delete_first"}[deleteFirst]
				t.Run(name, func(t *testing.T) {
					p := newReferenceScenario(t)
					for _, backend := range p.backends() {
						db := backend.db
						t.Run(backend.name, func(t *testing.T) {
							owner, peer := referenceLockConnections(t, p.ctx, db)
							record := backend.recorder
							run := func(x referenceExecutor, step referenceStep) {
								if prepared {
									step = referencePrepared(step)
								}
								referenceRunStep(t, p.ctx, x, record, step)
							}
							exec := func(x referenceExecutor, q string, n int64) { updateCoreExec(t, p.ctx, x, q, prepared, n) }
							rows := func(x referenceExecutor, q string, want [][]any) {
								step := referenceSelect(q, []string{"id", "v"}, []string{"INT4", "INT4"}, want)
								step.bag = true
								run(x, step)
							}
							exec(owner, "CREATE TABLE t(id INT,v INT)", 0)
							exec(owner, "CREATE TABLE copied(id INT,v INT)", 0)
							exec(owner, "INSERT INTO t VALUES(1,10),(2,20),(2,20)", 3)
							original := [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(2), int64(20)}}
							tx, err := owner.BeginTx(p.ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer tx.Rollback()
							survivors := [][]any{{int64(2), int64(20)}, {int64(2), int64(20)}}
							if deleteFirst {
								exec(tx, "DELETE FROM t WHERE id=2", 2)
								exec(tx, "INSERT INTO copied SELECT id,v FROM t", 1)
								rows(tx, "SELECT id,v FROM copied", [][]any{{int64(1), int64(10)}})
								exec(tx, "UPDATE copied SET v=11", 1)
								exec(tx, "DELETE FROM copied WHERE v=11", 1)
								survivors = [][]any{{int64(1), int64(10)}}
							} else {
								exec(tx, "UPDATE t SET v=30 WHERE id=1", 1)
								exec(tx, "INSERT INTO t SELECT id,v FROM t WHERE id=1", 1)
								rows(tx, "SELECT id,v FROM t", [][]any{{int64(1), int64(30)}, {int64(1), int64(30)}, {int64(2), int64(20)}, {int64(2), int64(20)}})
								rows(peer, "SELECT id,v FROM t", original)
								exec(tx, "DELETE FROM t WHERE id=1", 2)
								exec(tx, "UPDATE t SET v=40 WHERE id=1", 0)
								exec(tx, "INSERT INTO t VALUES(3,30)", 1)
								exec(tx, "UPDATE t SET v=40 WHERE id=3", 1)
								exec(tx, "DELETE FROM t WHERE id=3 AND v=40", 1)
								exec(tx, "INSERT INTO copied SELECT id,v FROM t WHERE id=2", 2)
								rows(tx, "SELECT id,v FROM copied", survivors)
								exec(tx, "DELETE FROM copied WHERE v=20", 2)
								exec(tx, "UPDATE copied SET v=7", 0)
							}
							rows(tx, "SELECT id,v FROM t", survivors)
							rows(tx, "SELECT id,v FROM copied", [][]any{})
							rows(peer, "SELECT id,v FROM t", original)
							rows(peer, "SELECT id,v FROM copied", [][]any{})
							if commit {
								err = tx.Commit()
							} else {
								err = tx.Rollback()
								survivors = original
							}
							if err != nil {
								t.Fatal(err)
							}
							rows(peer, "SELECT id,v FROM t", survivors)
							rows(peer, "SELECT id,v FROM copied", [][]any{})
						})
					}
				})
			}
		}
	}
}
