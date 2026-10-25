package core_test

import (
	"context"
	"database/sql/driver"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
)

func TestUpdateFromQueuedContenderRecheck(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "described"}[prepared], func(t *testing.T) {
			e := engine.New()
			holder := enginetest.StartSession(t, e, nil)
			writer := enginetest.StartSession(t, e, nil)
			peer := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "CREATE TABLE s(lo INT,hi INT,n INT)", "INSERT INTO t VALUES(1,10)", "INSERT INTO s VALUES(0,10,101),(0,30,202),(11,30,303)"} {
				enginetest.SessionSQL(t, peer, q)
			}
			ast := enginetest.Statement(t, "UPDATE t x SET v=x.v+s.n FROM s WHERE x.id=1 AND x.v>=s.lo AND x.v<=s.hi RETURNING old.v,s.n,new.v,s.lo,s.hi")
			var expected *core.StatementDescription
			if prepared {
				op := enginetest.TransactionOperation(t, writer)
				description, err := e.Describe(op, ast)
				op.Done()
				if err != nil {
					t.Fatal(err)
				}
				expected = &description
			}
			ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
			original := e.Storage.Relations[ref.Address].Versions[0]
			tx := enginetest.TransactionBegin(t, holder)
			enginetest.SessionSQL(t, holder, "UPDATE t SET v=20 WHERE id=1")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			type outcome struct {
				count int64
				rows  [][]driver.Value
				scope *core.StatementScope
				err   error
			}
			done := make(chan outcome, 1)
			go func() {
				var o outcome
				op, err := writer.Acquire(ctx)
				if err == nil {
					var result core.Result
					o.scope, err = e.StartStatement(op)
					if err == nil {
						o.scope.Expected = expected
						result, err = o.scope.Execute(ast, 0, nil)
						err = o.scope.Finish(err)
						o.count = result.Count
						if result.Rows != nil {
							if err == nil {
								o.rows = enginetest.Tuples(result)
							}
							result.Rows.Close()
						}
					}
					op.Done()
				}
				o.err = err
				done <- o
			}()
			enginetest.LockPending(t, e, 1)
			lockState := e.Locks.Inspect()
			entry := lockState.Resources[lock.Resource{ObjectRef: ref, Row: original.Row}]
			queued := entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Mode == lock.ForNoKeyUpdate && entry.Holders[holder.Transactions.Ticket].Mode == lock.ForNoKeyUpdate
			if !queued {
				t.Fatal("not a genuine same-RowID UPDATE queue")
			}
			// Native execution freezes all qualifying pairs before this first
			// row wait. Captured v10 admits101 and202, never303. Grant refreshes
			// only the target to20:101 then rejects and202 must still succeed.
			enginetest.TransactionEnd(t, holder, tx, true)
			var got outcome
			select {
			case got = <-done:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			want := [][]driver.Value{{int64(20), int64(202), int64(222), int64(0), int64(30)}}
			if got.err != nil || got.count != 1 || !reflect.DeepEqual(got.rows, want) {
				t.Fatal("success-only suppression/frozen eligibility", got, want)
			}
			if got.scope == nil || len(got.scope.QuerySources) != 0 {
				t.Fatal("statement source admission leaked")
			}
			store := e.Storage.Relations[ref.Address]
			if len(store.Versions) != 1 || store.Versions[0].Row != original.Row || store.Versions[0].Version == original.Version || enginetest.Export(original.Values[1]) != int64(10) {
				t.Fatal("physical target identity or captured predecessor changed", store.Versions, original)
			}
			enginetest.TransactionRows(t, peer, "SELECT v FROM t", 222)
			enginetest.LockEmpty(t, e)
		})
	}
}
