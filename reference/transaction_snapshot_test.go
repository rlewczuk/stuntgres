package reference_test

import (
	"database/sql/driver"
	"testing"

	"stuntgres"
)

func TestReferenceTransactionRetainedStatementAndPeerCommit(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			pair := newReferenceScenario(t)
			for _, backend := range pair.backends() {
				t.Run(backend.name, func(t *testing.T) {
					ctx := pair.ctx
					db, recorder := backend.db, backend.recorder
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
					step(b, referenceStep{query: "CREATE TABLE t(v INT NOT NULL)"})
					step(b, referenceStep{query: "CREATE TABLE u(v INT NOT NULL)"})
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
					step(tx, referenceStep{query: "SELECT v FROM t ORDER BY v", prepared: prepared, affected: 3})
					rows, err := tx.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
					if err != nil {
						t.Fatal(err)
					}
					txTypes, err := rows.ColumnTypes()
					if err != nil || len(txTypes) != 1 {
						rows.Close()
						t.Fatal(txTypes, err)
					}
					txNullable, txKnown := txTypes[0].Nullable()
					if txNullable || txKnown != !backend.pg {
						rows.Close()
						t.Fatal("transaction lost NOT NULL descriptor", txNullable, txKnown)
					}
					referenceLimitLockValues(t, relationOutcome{rows: rows}, backend.pg, 1, 2, 7)
					old, closeOld, err := transactionQuery(ctx, b, "SELECT v FROM t ORDER BY v", prepared)
					if err != nil {
						t.Fatal(err)
					}
					defer closeOld()
					types, err := old.ColumnTypes()
					if err != nil || len(types) != 1 {
						t.Fatal(types, err)
					}
					nullable, known := types[0].Nullable()
					if nullable || known != !backend.pg {
						t.Fatal("NOT NULL public descriptor", nullable, known)
					}
					if err = tx.Commit(); err != nil {
						t.Fatal(err)
					}
					// Independently assert the already-started statement excludes
					// the peer's later commit, then assert fresh complete publication.
					referenceLimitLockValues(t, relationOutcome{rows: old}, backend.pg, 7)
					if err = closeOld(); err != nil {
						t.Fatal(err)
					}
					step(b, referenceTransactionRows(prepared, "SELECT v FROM t ORDER BY v", 1, 2, 7))
					step(b, referenceTransactionRows(prepared, "SELECT v FROM u ORDER BY v", 9))
					if !backend.pg {
						raw, _ := referenceTransactionRaw(t, pair, backend)
						handle, err := raw.(driver.ConnBeginTx).BeginTx(ctx, driver.TxOptions{})
						if err != nil {
							t.Fatal(err)
						}
						referenceTransactionRawExec(t, ctx, raw, "INSERT INTO t VALUES(4)", 1)
						retained, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT v FROM t ORDER BY v", nil)
						if err != nil {
							t.Fatal(err)
						}
						defer retained.Close()
						if err := handle.Rollback(); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Reset(ctx, pair.native); err != nil {
							t.Fatal(err)
						}
						referenceTransactionRawRows(t, retained, 1, 2, 4, 7)
					}
				})
			}
		})
	}
}
