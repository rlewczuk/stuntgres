package reference_test

import (
	"database/sql/driver"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionRetainedStatementAndPeerCommit(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			pair := fixture.NewScenario(t)
			for _, backend := range pair.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					ctx := pair.Ctx
					db, recorder := backend.DB, backend.Recorder
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
					step := func(x fixture.Executor, s fixture.Step) {
						fixture.RunStep(t, ctx, x, recorder, s)
					}
					step(b, fixture.Step{Query: "CREATE TABLE t(v INT NOT NULL)"})
					step(b, fixture.Step{Query: "CREATE TABLE u(v INT NOT NULL)"})
					tx, err := a.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					step(tx, fixture.Step{Query: "INSERT INTO t VALUES(1),(2)", Prepared: prepared, Affected: 2})
					step(tx, fixture.Step{Query: "INSERT INTO u VALUES(9)", Prepared: prepared, Affected: 1})
					step(b, fixture.Step{Query: "INSERT INTO t VALUES(7)", Prepared: prepared, Affected: 1})
					step(tx, fixture.TransactionRows(prepared, "SELECT v FROM t ORDER BY v", 1, 2, 7))
					step(b, fixture.TransactionRows(prepared, "SELECT v FROM u ORDER BY v"))
					step(tx, fixture.Step{Query: "SELECT v FROM t ORDER BY v", Prepared: prepared, Affected: 3})
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
					if txNullable || txKnown != !backend.PG {
						rows.Close()
						t.Fatal("transaction lost NOT NULL descriptor", txNullable, txKnown)
					}
					fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows}, backend.PG, 1, 2, 7)
					old, closeOld, err := fixture.TransactionQuery(ctx, b, "SELECT v FROM t ORDER BY v", prepared)
					if err != nil {
						t.Fatal(err)
					}
					defer closeOld()
					types, err := old.ColumnTypes()
					if err != nil || len(types) != 1 {
						t.Fatal(types, err)
					}
					nullable, known := types[0].Nullable()
					if nullable || known != !backend.PG {
						t.Fatal("NOT NULL public descriptor", nullable, known)
					}
					if err = tx.Commit(); err != nil {
						t.Fatal(err)
					}
					// Independently assert the already-started statement excludes
					// the peer's later commit, then assert fresh complete publication.
					fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: old}, backend.PG, 7)
					if err = closeOld(); err != nil {
						t.Fatal(err)
					}
					step(b, fixture.TransactionRows(prepared, "SELECT v FROM t ORDER BY v", 1, 2, 7))
					step(b, fixture.TransactionRows(prepared, "SELECT v FROM u ORDER BY v", 9))
					if !backend.PG {
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
						if err := stuntgres.Reset(ctx, pair.Native); err != nil {
							t.Fatal(err)
						}
						referenceTransactionRawRows(t, retained, 1, 2, 4, 7)
					}
				})
			}
		})
	}
}
