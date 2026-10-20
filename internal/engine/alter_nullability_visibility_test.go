package engine

import (
	"database/sql/driver"
	"reflect"
	"testing"
	"time"
)

func TestAlterNullabilityPeerCommitVisibility(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			e := New()
			a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
			sessionSQL(t, a, "CREATE TABLE t(a INT,b INT)")
			sessionSQL(t, a, "INSERT INTO t VALUES(7,9)")
			ref := roleTableRef(t, e, "t")
			original := e.catalog.state.Get(ref.Address)
			store := e.storage.Relations[ref.Address]
			tx := transactionBegin(t, a)
			sessionSQL(t, a, "INSERT INTO t VALUES(8,10)")
			sessionSQL(t, a, "ALTER TABLE t DROP b, ALTER a SET NOT NULL")
			private := e.privateCatalogLocked(a.Transactions.catalog).MustRelation(ref.Address)
			if len(private.Columns) != 1 || private.Columns[0].Nullable || e.catalog.state.Get(ref.Address) != original || e.storage.Relations[ref.Address] != store {
				t.Fatal("private descriptor published early")
			}
			done := make(chan struct {
				result Result
				err    error
			}, 1)
			go func() {
				op := transactionOperation(t, b)
				r, err := e.Execute(op, statement(t, "SELECT * FROM t ORDER BY a"), 0, nil)
				op.Done()
				done <- struct {
					result Result
					err    error
				}{r, err}
			}()
			lockPending(t, e, 1)
			transactionEnd(t, a, tx, commit)
			var outcome struct {
				result Result
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
				if len(outcome.result.Rows.Schema()) != 1 || outcome.result.Rows.Schema()[0].Nullable || !reflect.DeepEqual(tuples(outcome.result), [][]driver.Value{{int64(7)}, {int64(8)}}) {
					t.Fatal("peer did not see combined install")
				}
			} else {
				if len(outcome.result.Rows.Schema()) != 2 || !outcome.result.Rows.Schema()[0].Nullable || !reflect.DeepEqual(tuples(outcome.result), [][]driver.Value{{int64(7), int64(9)}}) {
					t.Fatal("peer saw rollback prefix")
				}
			}
			lockEmpty(t, e)
		})
	}
}
