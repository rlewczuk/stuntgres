package engine

import (
	"context"
	"database/sql/driver"
	"reflect"
	"testing"
	"time"
)

func TestUpdateFromQueuedContenderRecheck(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "described"}[prepared], func(t *testing.T) {
			e := New()
			holder := sessionStart(t, e, nil)
			writer := sessionStart(t, e, nil)
			peer := sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "CREATE TABLE s(lo INT,hi INT,n INT)", "INSERT INTO t VALUES(1,10)", "INSERT INTO s VALUES(0,10,101),(0,30,202),(11,30,303)"} {
				sessionSQL(t, peer, q)
			}
			ast := statement(t, "UPDATE t x SET v=x.v+s.n FROM s WHERE x.id=1 AND x.v>=s.lo AND x.v<=s.hi RETURNING old.v,s.n,new.v,s.lo,s.hi")
			var expected *StatementDescription
			if prepared {
				op := transactionOperation(t, writer)
				description, err := e.Describe(op, ast)
				op.Done()
				if err != nil {
					t.Fatal(err)
				}
				expected = &description
			}
			ref := catalogRef(t, e, e.CatalogRead(), "t")
			original := e.storage[ref.Address].versions[0]
			tx := transactionBegin(t, holder)
			sessionSQL(t, holder, "UPDATE t SET v=20 WHERE id=1")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			type outcome struct {
				count int64
				rows  [][]driver.Value
				scope *StatementScope
				err   error
			}
			done := make(chan outcome, 1)
			go func() {
				var o outcome
				op, err := writer.Acquire(ctx)
				if err == nil {
					var result Result
					o.scope, err = e.StartStatement(op)
					if err == nil {
						o.scope.Expected = expected
						result, err = o.scope.Execute(ast, 0, nil)
						err = o.scope.Finish(err)
						o.count = result.Count
						if result.Rows != nil {
							if err == nil {
								o.rows = tuples(result)
							}
							result.Rows.Close()
						}
					}
					op.Done()
				}
				o.err = err
				done <- o
			}()
			lockPending(t, e, 1)
			lockState := e.locks.Inspect()
			entry := lockState.Resources[lockResource{ObjectRef: ref, Row: original.row}]
			queued := entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Mode == noKeyUpdate && entry.Holders[holder.transactions.ticket].Mode == noKeyUpdate
			if !queued {
				t.Fatal("not a genuine same-RowID UPDATE queue")
			}
			// Native execution freezes all qualifying pairs before this first
			// row wait. Captured v10 admits101 and202, never303. Grant refreshes
			// only the target to20:101 then rejects and202 must still succeed.
			transactionEnd(t, holder, tx, true)
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
			if got.scope == nil || len(got.scope.querySources) != 0 {
				t.Fatal("statement source admission leaked")
			}
			store := e.storage[ref.Address]
			if len(store.versions) != 1 || store.versions[0].row != original.row || store.versions[0].version == original.version || testExport(original.values[1]) != int64(10) {
				t.Fatal("physical target identity or captured predecessor changed", store.versions, original)
			}
			transactionRows(t, peer, "SELECT v FROM t", 222)
			lockEmpty(t, e)
		})
	}
}
