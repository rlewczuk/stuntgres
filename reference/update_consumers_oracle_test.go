//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceUpdateCoreInsertQueryConsumer(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			p := newReferencePair(t)
			for i, db := range []*sql.DB{p.memory, p.postgres} {
				t.Run([]string{"memory", "postgres"}[i], func(t *testing.T) {
					owner, peer := referenceLockConnections(t, p.ctx, db)
					record := p.memoryRecord
					if i == 1 {
						record = p.postgresRecord
					}
					run := func(x referenceExecutor, s referenceStep) {
						if prepared {
							s = referencePrepared(s)
						}
						referenceRunStep(t, p.ctx, x, record, s)
					}
					rows := func(q string, values [][]any) referenceStep {
						s := referenceSelect(q, []string{"id", "v"}, []string{"INT4", "INT4"}, values)
						s.bag = true
						return s
					}
					run(owner, referenceExec("CREATE TABLE t(id INT,v INT)", 0))
					run(owner, referenceExec("CREATE TABLE copied(id INT,v INT)", 0))
					run(owner, referenceExec("INSERT INTO t VALUES(1,10)", 1))
					tx, err := owner.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					run(tx, referenceExec("UPDATE t SET v=20", 1))
					// Self-source sees one prior own head, not its predecessor or
					// the current INSERT stage; exactly one row is appended.
					run(tx, referenceExec("INSERT INTO t SELECT id,v FROM t", 1))
					run(tx, rows("SELECT id,v FROM t", [][]any{{int64(1), int64(20)}, {int64(1), int64(20)}}))
					run(peer, rows("SELECT id,v FROM t", [][]any{{int64(1), int64(10)}}))
					run(tx, referenceExec("UPDATE t SET v=30", 2))
					run(tx, referenceExec("INSERT INTO copied SELECT id,v FROM t WHERE v=30", 2))
					run(peer, rows("SELECT id,v FROM copied", [][]any{}))
					if err := tx.Commit(); err != nil {
						t.Fatal(err)
					}
					want := [][]any{{int64(1), int64(30)}, {int64(1), int64(30)}}
					run(peer, rows("SELECT id,v FROM t", want))
					run(peer, rows("SELECT id,v FROM copied", want))
					tx, err = owner.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					run(tx, referenceExec("UPDATE t SET v=40", 2))
					run(tx, referenceExec("INSERT INTO copied SELECT id,v FROM t WHERE v=40", 2))
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					run(peer, rows("SELECT id,v FROM t", want))
					run(peer, rows("SELECT id,v FROM copied", want))
				})
			}
		})
	}
}
