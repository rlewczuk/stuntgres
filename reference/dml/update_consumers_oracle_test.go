//go:build reference

package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateCoreInsertQueryConsumer(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			p := fixture.NewPair(t)
			for i, db := range []*sql.DB{p.Memory, p.Postgres} {
				t.Run([]string{"memory", "postgres"}[i], func(t *testing.T) {
					owner, peer := fixture.LockConnections(t, p.Ctx, db)
					record := p.MemoryRecord
					if i == 1 {
						record = p.PostgresRecord
					}
					run := func(x fixture.Executor, s fixture.Step) {
						if prepared {
							s = fixture.Prepared(s)
						}
						fixture.RunStep(t, p.Ctx, x, record, s)
					}
					rows := func(q string, values [][]any) fixture.Step {
						s := fixture.Select(q, []string{"id", "v"}, []string{"INT4", "INT4"}, values)
						s.Bag = true
						return s
					}
					run(owner, fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
					run(owner, fixture.Exec("CREATE TABLE copied(id INT,v INT)", 0))
					run(owner, fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
					tx, err := owner.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					run(tx, fixture.Exec("UPDATE t SET v=20", 1))
					// Self-source sees one prior own head, not its predecessor or
					// the current INSERT stage; exactly one row is appended.
					run(tx, fixture.Exec("INSERT INTO t SELECT id,v FROM t", 1))
					run(tx, rows("SELECT id,v FROM t", [][]any{{int64(1), int64(20)}, {int64(1), int64(20)}}))
					run(peer, rows("SELECT id,v FROM t", [][]any{{int64(1), int64(10)}}))
					run(tx, fixture.Exec("UPDATE t SET v=30", 2))
					run(tx, fixture.Exec("INSERT INTO copied SELECT id,v FROM t WHERE v=30", 2))
					run(peer, rows("SELECT id,v FROM copied", [][]any{}))
					if err := tx.Commit(); err != nil {
						t.Fatal(err)
					}
					want := [][]any{{int64(1), int64(30)}, {int64(1), int64(30)}}
					run(peer, rows("SELECT id,v FROM t", want))
					run(peer, rows("SELECT id,v FROM copied", want))
					tx, err = owner.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					run(tx, fixture.Exec("UPDATE t SET v=40", 2))
					run(tx, fixture.Exec("INSERT INTO copied SELECT id,v FROM t WHERE v=40", 2))
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
