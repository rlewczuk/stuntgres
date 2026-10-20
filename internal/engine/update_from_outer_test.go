package engine

import (
	"context"
	"database/sql/driver"
	"reflect"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func TestUpdateFromOuterReturningDescriptors(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE t(id INT NOT NULL,v INT NOT NULL)", "CREATE TABLE s(k INT NOT NULL,n INT NOT NULL)", "CREATE TABLE r(k INT NOT NULL,z INT NOT NULL)"} {
		sessionSQL(t, s, q)
	}
	op := transactionOperation(t, s)
	defer op.Done()
	physical := statement(t, "SELECT s.*,r.* FROM s CROSS JOIN r")
	before, err := e.Describe(op, physical)
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range before.Results {
		if column.Nullable || !column.NullabilityKnown {
			t.Fatal("physical source NOT NULL", column)
		}
	}
	for _, kind := range []string{"LEFT", "RIGHT", "FULL"} {
		description, err := e.Describe(op, statement(t, "UPDATE t x SET v=r.z FROM s "+kind+" JOIN r USING(k) WHERE x.id=k RETURNING x.id,old.v,new.v,s.k,s.n,r.k,r.z,k"))
		if err != nil {
			t.Fatal(kind, err)
		}
		if len(description.Results) != 8 {
			t.Fatal(kind, description)
		}
		for i, column := range description.Results {
			if column.Usage.Type.ID() != sqltype.Int4 {
				t.Fatal(kind, i, column)
			}
			if i < 3 && (column.Nullable || !column.NullabilityKnown || column.OriginTable == 0) {
				t.Fatal("destination declaration changed", kind, i, column)
			}
			if i >= 3 && i < 7 {
				wantNullable := kind == "FULL" || kind == "RIGHT" && i < 5 || kind == "LEFT" && i >= 5
				if !column.NullabilityKnown || column.Nullable != wantNullable || column.OriginTable == 0 {
					t.Fatal("outward source nullability", kind, i, column)
				}
			}
		}
		merged := description.Results[7]
		if kind == "FULL" {
			if merged.OriginTable != 0 || merged.OriginAttribute != 0 {
				t.Fatal("computed merged key fabricated physical origin", merged)
			}
		} else {
			chosen := description.Results[3]
			if kind == "RIGHT" {
				chosen = description.Results[5]
			}
			if merged.OriginTable != chosen.OriginTable || merged.OriginAttribute != chosen.OriginAttribute || merged.Nullable || !merged.NullabilityKnown {
				t.Fatal("chosen merged key descriptor", kind, merged, chosen)
			}
		}
	}
	after, err := e.Describe(op, physical)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("UPDATE outward nullability mutated physical catalog descriptors", before, after, err)
	}
	lockEmpty(t, e)
}

func TestUpdateFromQueuedFrozenOuterAbsence(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "described"}[prepared], func(t *testing.T) {
			e := New()
			holder := sessionStart(t, e, nil)
			writer := sessionStart(t, e, nil)
			peer := sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "CREATE TABLE s(k INT NOT NULL,n INT NOT NULL)", "CREATE TABLE r(k INT NOT NULL,z INT NOT NULL)", "INSERT INTO t VALUES(2,20)", "INSERT INTO s VALUES(2,202)"} {
				sessionSQL(t, peer, q)
			}
			ast := statement(t, "UPDATE t x SET v=coalesce(r.z,x.v) FROM s LEFT JOIN r USING(k) WHERE x.id=s.k RETURNING old.v,new.v,s.k,r.k,r.z")
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
			original := e.storage.Relations[ref.Address].Versions[0]
			tx := transactionBegin(t, holder)
			sessionSQL(t, holder, "UPDATE t SET v=21 WHERE id=2")
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
			leftRef := catalogRef(t, e, e.CatalogRead(), "s")
			rightRef := catalogRef(t, e, e.CatalogRead(), "r")
			lockPending(t, e, 1)
			lockState := e.locks.Inspect()
			entry := lockState.Resources[lock.Resource{ObjectRef: ref, Row: original.Row}]
			queued := entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Mode == lock.ForNoKeyUpdate && entry.Holders[holder.Transactions.Ticket].Mode == lock.ForNoKeyUpdate
			sourcesHeld := false
			if queued {
				ticket := entry.Pending[0].Ticket
				left := lockState.Resources[lock.Resource{ObjectRef: leftRef}]
				right := lockState.Resources[lock.Resource{ObjectRef: rightRef}]
				sourcesHeld = left != nil && right != nil && left.Holders[ticket].Mode&lock.AccessShare != 0 && right.Holders[ticket].Mode&lock.AccessShare != 0
			}
			if !queued || !sourcesHeld {
				t.Fatal("not a genuine same-target queue with both physical outer sides admitted", queued, sourcesHeld)
			}
			// The initially empty right relation was admitted and captured before
			// this queue. Its compatible insert must not reform the frozen join.
			sessionSQL(t, peer, "INSERT INTO r VALUES(2,200)")
			transactionEnd(t, holder, tx, true)
			var got outcome
			select {
			case got = <-done:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			want := [][]driver.Value{{int64(21), int64(21), int64(2), nil, nil}}
			if got.err != nil || got.count != 1 || !reflect.DeepEqual(got.rows, want) {
				t.Fatal("NULL-extended source was reformed after target grant", got, want)
			}
			if got.scope == nil || len(got.scope.querySources) != 0 {
				t.Fatal("outer source admission leaked")
			}
			store := e.storage.Relations[ref.Address]
			if len(store.Versions) != 1 || store.Versions[0].Row != original.Row || testExport(original.Values[1]) != int64(20) {
				t.Fatal("absent source replaced physical target identity or mutated capture", store.Versions, original)
			}
			transactionRows(t, peer, "SELECT v FROM t", 21)
			transactionRows(t, peer, "SELECT z FROM r", 200)
			lockEmpty(t, e)
		})
	}
}
