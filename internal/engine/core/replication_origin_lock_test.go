package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

func TestReplicationOriginStateCatalogObjectAdmission(t *testing.T) {
	e := engine.New()
	op, done, err := enginetest.Operation(e, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := scope.Finish(nil); err != nil {
			t.Error(err)
		}
	}()
	if err := scope.Gate(enginetest.Statement(t, "SELECT 1")); err != nil {
		t.Fatal(err)
	}
	for _, class := range []ids.OID{ids.ReplicationOriginClassID, ids.SubscriptionClassID} {
		address := ids.ObjectAddress{ClassID: class, ObjectID: 1}
		local := lock.Resource{ObjectRef: e.Catalog.Domain.Ref(scope.TxRef.Epoch(), address)}
		shared := lock.Resource{ObjectRef: e.Cluster.RoleDomain.Ref(1, address)}
		if err := scope.AcquireCatalogResource(local, lock.AccessShare); err != nil {
			t.Fatal("catalog dependency object admission", class, err)
		}
		if !e.Locks.Holds(scope.LockTicket, shared, lock.AccessShare) || e.Locks.Holds(scope.LockTicket, local, lock.AccessShare) {
			t.Fatal("catalog adapter retained local object alias", class)
		}
		if err := scope.AcquireCatalogResource(local, lock.AccessExclusive); err != nil {
			t.Fatal("catalog retirement object admission", class, err)
		}
		if !e.Locks.Holds(scope.LockTicket, shared, lock.AccessExclusive) {
			t.Fatal("catalog retirement bypassed shared object authority", class)
		}
		rows, err := e.Locks.PGLocks(e.Catalog.Domain, scope.TxRef.Epoch())
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, row := range rows {
			if row.Class == uint32(class) && row.Object == 1 {
				if row.Kind != lock.ObjectRecord || !row.Shared || row.Waiting || row.Mode != lock.AccessShare|lock.AccessExclusive {
					t.Fatal("catalog object lost exact shared lock tag", row)
				}
				found = true
			}
		}
		if !found {
			t.Fatal("catalog object grant missing from actual manager", class)
		}
	}
}
