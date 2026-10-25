package engine

import (
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

func TestAggregateLifecycleViewLocks(t *testing.T) {
	for _, form := range []string{"permanent", "temporary", "inferred"} {
		t.Run(form, func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			roleExecute(t, e, op, "CREATE AGGREGATE aggregate_lock_sum(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0')")
			roleExecute(t, e, op, "CREATE AGGREGATE aggregate_lock_unrelated(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0')")
			read := e.CatalogRead()
			key := ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "aggregate_lock_sum", Signature: "23"}
			selected := read.Domain().Ref(read.Epoch(), read.NameAddress(key))
			key.Name = "aggregate_lock_unrelated"
			unrelated := read.Domain().Ref(read.Epoch(), read.NameAddress(key))
			query := "CREATE VIEW aggregate_lock_view AS SELECT aggregate_lock_sum(x) AS v FROM (VALUES(1),(2)) s(x)"
			if form == "temporary" {
				query = "CREATE TEMP VIEW aggregate_lock_view AS SELECT aggregate_lock_sum(x) AS v FROM (VALUES(1),(2)) s(x)"
			} else if form == "inferred" {
				roleExecute(t, e, op, "CREATE TEMP TABLE aggregate_lock_input(x integer)")
				query = "CREATE VIEW aggregate_lock_view AS SELECT aggregate_lock_sum(x) AS v FROM aggregate_lock_input"
			}
			roleExecute(t, e, op, "BEGIN")
			defer roleExecute(t, e, op, "ROLLBACK")
			roleExecute(t, e, op, query)
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for address, descriptor := range auth.Catalog.Descriptors() {
				if address.ClassID == ids.RelationClassID && descriptor.Name() == "aggregate_lock_view" {
					found = true
					temporary := auth.Catalog.MustNamespace(descriptor.Namespace()).Temporary != (ids.SessionToken{})
					if temporary != (form != "permanent") {
						t.Fatal("wrong actual view persistence", form, temporary)
					}
				}
			}
			if !found {
				t.Fatal("view missing from own transaction")
			}
			// Inspect the actual transaction's retained procedure grant, including
			// TEMP overlay publication. Unreferenced routines add no lock.
			locks := e.Locks.Inspect()
			entry := locks.Resources[lock.Resource{ObjectRef: selected}]
			if entry == nil || entry.Holders[op.Session.Transactions.Ticket].Mode&lock.AccessShare == 0 {
				t.Fatal("checked aggregate dependency lacks transaction AccessShare lock", selected)
			}
			if entry := locks.Resources[lock.Resource{ObjectRef: unrelated}]; entry != nil && len(entry.Holders) != 0 {
				t.Fatal("unreferenced aggregate acquired a provider lock", unrelated)
			}
		})
	}
}
