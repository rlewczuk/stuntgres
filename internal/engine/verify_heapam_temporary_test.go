package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
)

func TestVerifyHeapamTemporaryAdmission(t *testing.T) {
	for _, kind := range []string{"table", "sequence"} {
		t.Run(kind, func(t *testing.T) {
			e, _, persistent := typedUnitCatalog(t, false)
			heapUnitInstalled(t, e)
			n := heapUnitExpression(t, e)
			owner := sessionStart(t, e, nil)
			if kind == "table" {
				sessionSQL(t, owner, "CREATE TEMP TABLE heap_checked_tmp(v INT)")
				sessionSQL(t, owner, "INSERT INTO heap_checked_tmp VALUES(99)")
			} else {
				sessionSQL(t, owner, "CREATE TEMP SEQUENCE heap_checked_tmp")
				sequenceScalar(t, owner, "SELECT nextval('heap_checked_tmp')", 1)
			}
			sessionSQL(t, owner, "CREATE TEMP VIEW heap_wrong_tmp AS SELECT 1 AS v")
			view := e.CatalogRead()
			lookup := func(name string) *catalog.Relation {
				return view.MustRelation(view.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: owner.TemporaryNamespace, Name: name}))
			}
			target, wrong := lookup("heap_checked_tmp"), lookup("heap_wrong_tmp")
			store, generator := e.Storage.Relations[target.Address()], e.Storage.Sequences[target.Address()]
			var versions []storage.RowVersion
			var ends *storage.DeletionRoot
			var sequence storage.SequenceGenerator
			if store != nil { versions, ends = append([]storage.RowVersion(nil), store.Versions...), store.Ends }
			if generator != nil { sequence = *generator }
			unchanged := func(t *testing.T) {
				t.Helper()
				if e.CatalogRead().Get(target.Address()) != target || e.Storage.Relations[target.Address()] != store || e.Storage.Sequences[target.Address()] != generator { t.Fatal("TEMP verification replaced source authority") }
				if store != nil && (!reflect.DeepEqual(store.Versions, versions) || store.Ends != ends) { t.Fatal("TEMP verification changed retained row roots") }
				if generator != nil && *generator != sequence { t.Fatal("TEMP verification changed actual sequence generator") }
			}
			for _, prior := range []bool{false, true} {
				t.Run(fmt.Sprintf("foreign-prior-lock-%t", prior), func(t *testing.T) {
					peer := sessionStart(t, e, nil)
					sessionSQL(t, peer, "CREATE TEMP TABLE heap_unused_tmp(v INT)")
					namespace := peer.TemporaryNamespace
					transactionBegin(t, peer)
					scope := pgrowlocksUnitScope(t, e, peer)
					resource := lock.Resource{ObjectRef: e.Catalog.Domain.Ref(view.Epoch(), target.Address())}
					if prior {
						request, err := e.Locks.StartAcquire(context.Background(), scope.LockTicket, resource, lock.AccessShare, lock.Policy{})
						if request != nil || err != nil { t.Fatal(request, err) }
					}
					rows, err := scope.WriteHost().VerifyHeapam(heapUnitArgs(t, e, target, false), n.Usage.Type)
					var diagnostic *sqlstate.Error
					want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cannot access temporary tables of other sessions"}
					if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) || len(rows) != 0 { t.Fatal("foreign TEMP must be denied before materialization", rows, err) }
					if e.Locks.HasAccessShare(scope.LockTicket, resource) != prior { t.Fatal("foreign rejection changed prior AccessShare contribution", prior) }
					if !peer.Transactions.AccessedTemporary || peer.TemporaryNamespace != namespace { t.Fatal("foreign rejection omitted TEMP-attempt footprint or changed caller namespace") }
					unchanged(t)
					rows, err = scope.WriteHost().VerifyHeapam(heapUnitArgs(t, e, persistent, false), n.Usage.Type)
					if err != nil || len(rows) != 0 || !peer.Transactions.AccessedTemporary { t.Fatal("persistent admission failed or erased sticky TEMP-attempt footprint", rows, err) }
				})
			}
			// Missing/wrong-kind targets must not mark the owner's TEMP namespace.
			t.Run("rejected-targets", func(t *testing.T) {
				transactionBegin(t, owner)
				scope := pgrowlocksUnitScope(t, e, owner)
				missing := *target
				missing.ObjectAddress.ObjectID = e.Catalog.NextOID()+999
				for _, tc := range []struct { target *catalog.Relation; code string }{{&missing, "XX000"}, {wrong, "42809"}} {
					_, err := scope.WriteHost().VerifyHeapam(heapUnitArgs(t, e, tc.target, false), n.Usage.Type)
					var diagnostic *sqlstate.Error
					if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || owner.Transactions.AccessedTemporary { t.Fatal("rejected target changed own TEMP footprint", err) }
				}
				unchanged(t)
			})
			sessionSQL(t, owner, "ROLLBACK")
			sessionSQL(t, owner, "BEGIN")
			sessionSQL(t, owner, "SAVEPOINT heap_before")
			t.Run("own-admitted", func(t *testing.T) {
				scope := pgrowlocksUnitScope(t, e, owner)
				rows, err := scope.WriteHost().VerifyHeapam(heapUnitArgs(t, e, target, false), n.Usage.Type)
				if err != nil || len(rows) != 0 || !owner.Transactions.AccessedTemporary { t.Fatal("own TEMP admission omitted sticky transaction accounting", rows, err) }
				resource := lock.Resource{ObjectRef: e.Catalog.Domain.Ref(view.Epoch(), target.Address())}
				if e.Locks.HasAccessShare(scope.LockTicket, resource) { t.Fatal("own TEMP verification retained added AccessShare") }
				unchanged(t)
			})
			sessionSQL(t, owner, "ROLLBACK TO heap_before")
			if !owner.Transactions.AccessedTemporary { t.Fatal("ROLLBACK TO erased TEMP access footprint") }
			sessionSQL(t, owner, "ROLLBACK")
			unchanged(t)
		})
	}
}
