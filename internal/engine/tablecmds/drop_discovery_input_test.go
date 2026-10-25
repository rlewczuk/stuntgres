package tablecmds_test

import (
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/tablecmds"
)

// A prepared drop's dependency discovery must not widen the inheritance node
// fence: only the node's own reads are revalidated at admission.
func TestDropDiscoveryInputLeavesInheritanceFenceUnobserved(t *testing.T) {
	e := engine.New()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	before := e.CatalogRead()
	roleExecute(t, e, op, "CREATE TABLE discovered(a INT)")
	after := e.CatalogRead()
	discovered := retiredUnitTable(t, e, "discovered")
	reader := table.InheritanceDefinitionReader{Base: after, Observations: &table.InheritanceDefinitionObservations{}}
	if _, present := tablecmds.DropDiscoveryInput(reader).Descriptor(discovered.Address()); !present {
		t.Fatal("drop discovery did not read through the inheritance node definition")
	}
	if err := reader.Validate(before); err != nil {
		t.Fatalf("drop discovery read fenced the inheritance node: %v", err)
	}
	reader.Descriptor(discovered.Address())
	if err := reader.Validate(before); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatalf("node read was not fenced: %v", err)
	}
}
