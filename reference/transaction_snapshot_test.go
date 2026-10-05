//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceTransactionRetainedStatementAndPeerCommit(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			pair := newReferencePair(t)
			for index, db := range []*sql.DB{pair.memory, pair.postgres} {
				t.Run([]string{"memory", "postgres"}[index], func(t *testing.T) {
					ctx := pair.ctx
					recorder := pair.memoryRecord
					if index == 1 {
						recorder = pair.postgresRecord
					}
					a, err := db.Conn(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer a.Close()
					b, err := db.Conn(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer b.Close()
					step := func(x referenceExecutor, s referenceStep) {
						referenceRunStep(t, ctx, x, recorder, s)
					}
					step(b, referenceStep{query: "CREATE TABLE t(v INT)"})
					step(b, referenceStep{query: "CREATE TABLE u(v INT)"})
					tx, err := a.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					step(tx, referenceStep{query: "INSERT INTO t VALUES(1),(2)", prepared: prepared, affected: 2})
					step(tx, referenceStep{query: "INSERT INTO u VALUES(9)", prepared: prepared, affected: 1})
					step(b, referenceStep{query: "INSERT INTO t VALUES(7)", prepared: prepared, affected: 1})
					step(tx, referenceTransactionRows(prepared, "SELECT v FROM t ORDER BY v", 1, 2, 7))
					step(b, referenceTransactionRows(prepared, "SELECT v FROM u ORDER BY v"))
					old, closeOld, err := transactionQuery(ctx, b, "SELECT v FROM t ORDER BY v", prepared)
					if err != nil {
						t.Fatal(err)
					}
					defer closeOld()
					if err = tx.Commit(); err != nil {
						t.Fatal(err)
					}
					// Independently assert the already-started statement excludes
					// the peer's later commit, then assert fresh complete publication.
					referenceTransactionValues(t, old, 7)
					if err = closeOld(); err != nil {
						t.Fatal(err)
					}
					step(b, referenceTransactionRows(prepared, "SELECT v FROM t ORDER BY v", 1, 2, 7))
					step(b, referenceTransactionRows(prepared, "SELECT v FROM u ORDER BY v", 9))
				})
			}
		})
	}
}
