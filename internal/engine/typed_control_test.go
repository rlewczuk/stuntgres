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
	e.catalog.PublishLocked(catalog.CloneState(e.catalog.State))
	namespace := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 21000}, ObjectName: "pg_temp_typed_unit", ObjectRevision: 1, Owner: e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Temporary: e.catalog.Domain.Session(99)}
	e.catalog.State.SetDescriptor(namespace.Address(), namespace)
	e.catalog.State.BindName(catalog.DescriptorNameKey(namespace), namespace.Address())
	for _, old := range tables {
		table := *old
		e.catalog.State.UnbindName(catalog.DescriptorNameKey(old))
		table.ObjectNamespace = namespace.Address()
		e.catalog.State.SetDescriptor(table.Address(), &table)
		e.catalog.State.BindName(catalog.DescriptorNameKey(&table), table.Address())
		rowAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(table.RowType)}
		row := e.catalog.State.MustType(rowAddress)
		for _, address := range []ids.ObjectAddress{rowAddress, {ClassID: ids.TypeClassID, ObjectID: ids.OID(row.Array)}} {
			oldType := e.catalog.State.MustType(address)
			typ := *oldType
			e.catalog.State.UnbindName(catalog.DescriptorNameKey(oldType))
			typ.ObjectNamespace = namespace.Address()
			e.catalog.State.SetDescriptor(address, &typ)
			e.catalog.State.BindName(catalog.DescriptorNameKey(&typ), address)
		}
	}
}

func TestTypedTemporaryReplacementAdmission(t *testing.T) {
	e, _, table := typedUnitCatalog(t, false)
	typedControlsTemporary(e, table)
	e.mu.Lock()
	e.storage.InstallRelationLocked(table.Address(), &storage.Relation{})
	e.mu.Unlock()
	state, oid, revision, stored := e.catalog.State, e.catalog.NextOID(), e.catalog.NextRevision(), e.storage.Relations[table.Address()]
	next := catalog.CloneState(New().catalog.State)
	e.mu.Lock()
	err := e.preserveTemporaryLocked(context.Background(), next, map[ids.ObjectAddress]*storage.Relation{}, map[ids.ObjectAddress]*storage.SequenceGenerator{}, nil, nil, nil, &catalog.OIDReservation{Base: e.catalog.NextOID(), Next: e.catalog.NextOID()})
	e.mu.Unlock()
	if err == nil || err.Error() != typedef.TemporaryTypedReplacementError {
		t.Fatal(err)
	}
	if e.catalog.State != state || e.catalog.NextOID() != oid || e.catalog.NextRevision() != revision || e.storage.Relations[table.Address()] != stored {
		t.Fatal("final admission rejection mutated target")
	}
	if err := e.Reset(context.Background()); err == nil || err.Error() != typedef.TemporaryTypedReplacementError {
		t.Fatal(err)
	}
	if e.catalog.State != state || e.catalog.NextOID() != oid || e.catalog.NextRevision() != revision || e.storage.Relations[table.Address()] != stored {
		t.Fatal("preallocation rejection mutated target")
	}
}

func TestTypedTemporaryProviderSourceIdentityRetention(t *testing.T) {
	e, source, table := typedUnitCatalog(t, false)
	typedControlsTemporary(e, source, table)
	e.mu.Lock()
	e.storage.InstallRelationLocked(table.Address(), &storage.Relation{})
	e.mu.Unlock()
	imported := New()
	next := catalog.CloneState(imported.catalog.State)
	relations := map[ids.ObjectAddress]*storage.Relation{}
	e.mu.Lock()
	err := e.preserveTemporaryLocked(context.Background(), next, relations, map[ids.ObjectAddress]*storage.SequenceGenerator{}, nil, nil, replacementProcedurePolicyFixture(imported, e), &catalog.OIDReservation{Base: e.catalog.NextOID(), Next: e.catalog.NextOID()})
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	retained := next.MustRelation(table.Address())
	backing := next.MustRelation(source.Address())
	if retained.OfType != table.OfType || retained.RowType != table.RowType || backing.RowType != source.RowType || relations[table.Address()] != e.storage.Relations[table.Address()] {
		t.Fatal("retained TEMP providers were retargeted")
	}
	if err := typedef.ValidateTypedAttachment(e.catalog.Read(next), retained); err != nil {
		t.Fatal(err)
	}
}
