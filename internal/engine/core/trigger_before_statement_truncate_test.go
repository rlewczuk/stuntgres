package core_test

import (
	"testing"

	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/stored"
)

func beforeStatementTruncateAuthority(t *testing.T) {
	e, scope, read, relation, routine := triggerUnitFixture(t, "BEGIN CREATE TABLE bst_truncate_private(id integer); INSERT INTO bst_truncate_private VALUES(1); RETURN NULL; END")
	parts := triggerUnitDefinition(t, read, relation, routine, "s", stored.TriggerInsert).Parts()
	parts.Level, parts.Events = stored.TriggerStatement, stored.TriggerTruncate
	definition, err := stored.NewTrigger(parts)
	if err != nil {
		t.Fatal(err)
	}
	relation = triggerUnitInstall(t, e, relation, definition)
	original := e.Storage.Relations[relation.Address()]
	out, err := scope.Execute(enginetest.Statement(t, "TRUNCATE public.trigger_unit_table"), 0, nil)
	if err != nil || out.Count != 0 || out.Rows != nil {
		t.Fatal("TRUNCATE did not execute its admitted statement chain", out, err)
	}
	ref := read.Owner().Domain.Ref(read.Epoch(), relation.Address())
	resource := lock.Resource{ObjectRef: ref}
	if !e.Locks.Holds(scope.LockTicket, resource, lock.AccessExclusive) || e.Locks.Holds(scope.LockTicket, resource, lock.RowExclusive) {
		t.Fatal("witness does not distinguish TRUNCATE's actual AX grant from a DML RX grant")
	}
	change, ok := scope.PendingCatalog().Storage[relation.Address()]
	if !ok || change.Replacement == original || change.Replacement.Generation != original.Generation+1 || scope.Catalog == nil || e.Storage.Relations[relation.Address()] != original {
		t.Fatal("TRUNCATE failed to stage replacement after the callback's private catalog work")
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	if e.Storage.Relations[relation.Address()] != change.Replacement {
		t.Fatal("outer completion lost the admitted TRUNCATE replacement")
	}
	helper := enginetest.RoleTableRef(t, e, "bst_truncate_private")
	if len(e.Storage.Relations[helper.Address].Versions) != 1 {
		t.Fatal("post-callback staging discarded or replayed callback catalog/data effects")
	}
}
