package core_test

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func routineTreeProcedure(t *testing.T, read catalog.Read, name string) *catalog.Procedure {
	t.Helper()
	addresses := read.State().ProceduresNamed(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, name)
	if len(addresses) != 1 {
		t.Fatalf("routine %s: %v", name, addresses)
	}
	d, ok := read.Procedure(addresses[0])
	if !ok {
		t.Fatal("missing routine")
	}
	return d
}
