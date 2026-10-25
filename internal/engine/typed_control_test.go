package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/typedef"
)

// Provider-only fixture: this does not enable CREATE TYPE in a TEMP schema.
func typedControlsTemporary(e *Engine, tables ...*catalog.Relation) {
	e.Catalog.PublishLocked(catalog.CloneState(e.Catalog.State))
	namespace := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 21000}, ObjectName: "pg_temp_typed_unit", ObjectRevision: 1, Owner: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Temporary: e.Catalog.Domain.Session(99)}
	e.Catalog.State.SetDescriptor(namespace.Address(), namespace)
	e.Catalog.State.BindName(catalog.DescriptorNameKey(namespace), namespace.Address())
	for _, old := range tables {
		table := *old
		e.Catalog.State.UnbindName(catalog.DescriptorNameKey(old))
		table.ObjectNamespace = namespace.Address()
		e.Catalog.State.SetDescriptor(table.Address(), &table)
		e.Catalog.State.BindName(catalog.DescriptorNameKey(&table), table.Address())
		rowAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(table.RowType)}
		row := e.Catalog.State.MustType(rowAddress)
		for _, address := range []ids.ObjectAddress{rowAddress, {ClassID: ids.TypeClassID, ObjectID: ids.OID(row.Array)}} {
			oldType := e.Catalog.State.MustType(address)
			typ := *oldType
			e.Catalog.State.UnbindName(catalog.DescriptorNameKey(oldType))
			typ.ObjectNamespace = namespace.Address()
			e.Catalog.State.SetDescriptor(address, &typ)
			e.Catalog.State.BindName(catalog.DescriptorNameKey(&typ), address)
		}
	}
}

func TestTypedTemporaryReplacementAdmission(t *testing.T) {
	e, _, table := typedUnitCatalog(t, false)
	typedControlsTemporary(e, table)
	e.Mu.Lock()
	e.Storage.InstallRelationLocked(table.Address(), &storage.Relation{})
	e.Mu.Unlock()
	state, oid, revision, stored := e.Catalog.State, e.Catalog.NextOID(), e.Catalog.NextRevision(), e.Storage.Relations[table.Address()]
	next := catalog.CloneState(New().Catalog.State)
	e.Mu.Lock()
	err := e.preserveTemporaryLocked(context.Background(), next, map[ids.ObjectAddress]*storage.Relation{}, map[ids.ObjectAddress]*storage.SequenceGenerator{}, nil, nil, nil, &catalog.OIDReservation{Base: e.Catalog.NextOID(), Next: e.Catalog.NextOID()})
	e.Mu.Unlock()
	if err == nil || err.Error() != typedef.TemporaryTypedReplacementError {
		t.Fatal(err)
	}
	if e.Catalog.State != state || e.Catalog.NextOID() != oid || e.Catalog.NextRevision() != revision || e.Storage.Relations[table.Address()] != stored {
		t.Fatal("final admission rejection mutated target")
	}
	if err := Reset(context.Background(), e); err == nil || err.Error() != typedef.TemporaryTypedReplacementError {
		t.Fatal(err)
	}
	if e.Catalog.State != state || e.Catalog.NextOID() != oid || e.Catalog.NextRevision() != revision || e.Storage.Relations[table.Address()] != stored {
		t.Fatal("preallocation rejection mutated target")
	}
}

func TestTypedTemporaryProviderSourceIdentityRetention(t *testing.T) {
	e, source, table := typedUnitCatalog(t, false)
	typedControlsTemporary(e, source, table)
	e.Mu.Lock()
	e.Storage.InstallRelationLocked(table.Address(), &storage.Relation{})
	e.Mu.Unlock()
	imported := New()
	next := catalog.CloneState(imported.Catalog.State)
	relations := map[ids.ObjectAddress]*storage.Relation{}
	e.Mu.Lock()
	err := e.preserveTemporaryLocked(context.Background(), next, relations, map[ids.ObjectAddress]*storage.SequenceGenerator{}, nil, nil, replacementProcedurePolicyFixture(imported, e), &catalog.OIDReservation{Base: e.Catalog.NextOID(), Next: e.Catalog.NextOID()})
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	retained := next.MustRelation(table.Address())
	backing := next.MustRelation(source.Address())
	if retained.OfType != table.OfType || retained.RowType != table.RowType || backing.RowType != source.RowType || relations[table.Address()] != e.Storage.Relations[table.Address()] {
		t.Fatal("retained TEMP providers were retargeted")
	}
	if err := typedef.ValidateTypedAttachment(e.Catalog.Read(next), retained); err != nil {
		t.Fatal(err)
	}
}
