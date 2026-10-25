package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

func TestPGAggregateIntaggRetainedCollision(t *testing.T) {
	source, ext := intaggUnitInstalled(t)
	proc := source.Catalog.State.MustProcedure(ext.Members[0])
	original := *proc.Aggregate
	target := New()
	target.Catalog.PublishLocked(catalog.CloneState(target.Catalog.State))
	namespace := &catalog.Namespace{
		ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: original.Helpers[0].ID},
		ObjectName:    "pg_temp_pga_collision", ObjectRevision: 1, Owner: target.RoleStore.Domain.Principal(10),
		Temporary: target.Catalog.Domain.Session(99),
	}
	target.Catalog.State.SetDescriptor(namespace.Address(), namespace)
	target.Catalog.State.BindName(catalog.DescriptorNameKey(namespace), namespace.Address())
	target.Catalog.InstallOIDFrontierLocked(30000)
	next := catalog.CloneState(source.Catalog.State)
	target.Mu.Lock()
	err := EnginePreserveTemporaryLocked(target, context.Background(), next, map[ids.ObjectAddress]*storage.Relation{}, map[ids.ObjectAddress]*storage.SequenceGenerator{}, nil, nil, replacementProcedurePolicyFixture(source, target), &catalog.OIDReservation{Base: target.Catalog.NextOID(), Next: target.Catalog.NextOID()})
	target.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	imported := next.MustProcedure(proc.Address())
	if next.Get(namespace.Address()) != namespace || imported == proc || imported.Aggregate == proc.Aggregate || imported.Aggregate.Helpers[0].ID == original.Helpers[0].ID || imported.Aggregate.Definition.Functions[0] != imported.Aggregate.Helpers[0].ID {
		t.Fatal("helper collision did not preserve TEMP/remap owner metadata")
	}
	if imported.Aggregate.Helpers[1].ID != original.Helpers[1].ID || imported.Aggregate.Helpers[0].Namespace != proc.Namespace().ObjectID || *proc.Aggregate != original {
		t.Fatal("unrelated helper/source changed")
	}
	for _, helper := range imported.Aggregate.Helpers {
		if next.Get(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: helper.ID}) != nil {
			t.Fatal("mapping-only helper became a descriptor")
		}
	}
	if err := catalog.ValidateDefinitionOIDs(target.Catalog.Read(next)); err != nil {
		t.Fatal(err)
	}
}
