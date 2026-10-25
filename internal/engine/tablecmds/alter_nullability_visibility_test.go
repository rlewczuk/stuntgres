package tablecmds_test

import (
	"database/sql/driver"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
)

func TestAlterNullabilityPeerCommitVisibility(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			e := engine.New()
			a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, a, "CREATE TABLE t(a INT,b INT)")
			enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(7,9)")
			ref := enginetest.RoleTableRef(t, e, "t")
			original := e.Catalog.State.Get(ref.Address)
			store := e.Storage.Relations[ref.Address]
			tx := enginetest.TransactionBegin(t, a)
			enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(8,10)")
			enginetest.SessionSQL(t, a, "ALTER TABLE t DROP b, ALTER a SET NOT NULL")
			private := e.PrivateCatalogLocked(a.Transactions.Catalog).MustRelation(ref.Address)
			if len(private.Columns) != 1 || private.Columns[0].Nullable || e.Catalog.State.Get(ref.Address) != original || e.Storage.Relations[ref.Address] != store {
				t.Fatal("private descriptor published early")
			}
			done := make(chan struct {
				result core.Result
				err    error
			}, 1)
			go func() {
				op := enginetest.TransactionOperation(t, b)
				r, err := e.Execute(op, enginetest.Statement(t, "SELECT * FROM t ORDER BY a"), 0, nil)
				op.Done()
				done <- struct {
					result core.Result
					err    error
				}{r, err}
			}()
			enginetest.LockPending(t, e, 1)
			enginetest.TransactionEnd(t, a, tx, commit)
			var outcome struct {
				result core.Result
				err    error
			}
			select {
			case outcome = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("peer remained blocked after transaction end")
			}
			if outcome.err != nil {
				t.Fatal(outcome.err)
			}
			defer outcome.result.Rows.Close()
			if commit {
				if len(outcome.result.Rows.Schema()) != 1 || outcome.result.Rows.Schema()[0].Nullable || !reflect.DeepEqual(enginetest.Tuples(outcome.result), [][]driver.Value{{int64(7)}, {int64(8)}}) {
					t.Fatal("peer did not see combined install")
				}
			} else {
				if len(outcome.result.Rows.Schema()) != 2 || !outcome.result.Rows.Schema()[0].Nullable || !reflect.DeepEqual(enginetest.Tuples(outcome.result), [][]driver.Value{{int64(7), int64(9)}}) {
					t.Fatal("peer saw rollback prefix")
				}
			}
			enginetest.LockEmpty(t, e)
		})
	}
}
