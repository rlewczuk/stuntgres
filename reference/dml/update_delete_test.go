package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateCoreDeleteComposition(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			for _, deleteFirst := range []bool{false, true} {
				name := map[bool]string{false: "direct", true: "prepared"}[prepared] + map[bool]string{false: "/rollback", true: "/commit"}[commit] + map[bool]string{false: "/update_first", true: "/delete_first"}[deleteFirst]
				t.Run(name, func(t *testing.T) {
					p := fixture.NewScenario(t)
					for _, backend := range p.Backends() {
						db := backend.DB
						t.Run(backend.Name, func(t *testing.T) {
							owner, peer := fixture.LockConnections(t, p.Ctx, db)
							record := backend.Recorder
							run := func(x fixture.Executor, step fixture.Step) {
								if prepared {
									step = fixture.Prepared(step)
								}
								fixture.RunStep(t, p.Ctx, x, record, step)
							}
							exec := func(x fixture.Executor, q string, n int64) { fixture.UpdateCoreExec(t, p.Ctx, x, q, prepared, n) }
							rows := func(x fixture.Executor, q string, want [][]any) {
								step := fixture.Select(q, []string{"id", "v"}, []string{"INT4", "INT4"}, want)
								step.Bag = true
								run(x, step)
							}
							exec(owner, "CREATE TABLE t(id INT,v INT)", 0)
							exec(owner, "CREATE TABLE copied(id INT,v INT)", 0)
							exec(owner, "INSERT INTO t VALUES(1,10),(2,20),(2,20)", 3)
							original := [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(2), int64(20)}}
							tx, err := owner.BeginTx(p.Ctx, nil)
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
