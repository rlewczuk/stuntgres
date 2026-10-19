package engine

import (
	"context"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"testing"
)

// Provider-only fixture: this does not enable CREATE TYPE in a TEMP schema.
func typedControlsTemporary(e *Engine, tables ...*catalog.Relation) {
	e.catalog.state = catalog.CloneState(e.catalog.state)
	namespace := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 21000}, ObjectName: "pg_temp_typed_unit", ObjectRevision: 1, Owner: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Temporary: e.catalog.Domain.Session(99)}
	e.catalog.state.SetDescriptor(namespace.Address(), namespace)
	e.catalog.state.BindName(catalog.DescriptorNameKey(namespace), namespace.Address())
	for _, old := range tables {
		table := *old
		e.catalog.state.UnbindName(catalog.DescriptorNameKey(old))
		table.ObjectNamespace = namespace.Address()
		e.catalog.state.SetDescriptor(table.Address(), &table)
		e.catalog.state.BindName(catalog.DescriptorNameKey(&table), table.Address())
		rowAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(table.RowType)}
		row := e.catalog.state.MustType(rowAddress)
		for _, address := range []ids.ObjectAddress{rowAddress, {ClassID: ids.TypeClassID, ObjectID: ids.OID(row.Array)}} {
			oldType := e.catalog.state.MustType(address)
			typ := *oldType
			e.catalog.state.UnbindName(catalog.DescriptorNameKey(oldType))
			typ.ObjectNamespace = namespace.Address()
			e.catalog.state.SetDescriptor(address, &typ)
			e.catalog.state.BindName(catalog.DescriptorNameKey(&typ), address)
		}
	}
}

func TestTypedTemporaryReplacementAdmission(t *testing.T) {
	e, _, table := typedUnitCatalog(t, false)
	typedControlsTemporary(e, table)
	e.storage.relations[table.Address()] = &relationStorage{}
	state, oid, revision, storage := e.catalog.state, e.catalog.nextOID, e.catalog.nextRevision, e.storage.relations[table.Address()]
	next := catalog.CloneState(New().catalog.state)
	e.mu.Lock()
	err := e.preserveTemporaryLocked(context.Background(), next, map[ids.ObjectAddress]*relationStorage{}, map[ids.ObjectAddress]*sequenceGenerator{}, nil, nil)
	e.mu.Unlock()
	if err == nil || err.Error() != temporaryTypedReplacementError {
		t.Fatal(err)
	}
	if e.catalog.state != state || e.catalog.nextOID != oid || e.catalog.nextRevision != revision || e.storage.relations[table.Address()] != storage {
		t.Fatal("final admission rejection mutated target")
	}
	if err := e.Reset(context.Background()); err == nil || err.Error() != temporaryTypedReplacementError {
		t.Fatal(err)
	}
	if e.catalog.state != state || e.catalog.nextOID != oid || e.catalog.nextRevision != revision || e.storage.relations[table.Address()] != storage {
		t.Fatal("preallocation rejection mutated target")
	}
}

func TestTypedTemporaryProviderSourceIdentityRetention(t *testing.T) {
	e, source, table := typedUnitCatalog(t, false)
	typedControlsTemporary(e, source, table)
	e.storage.relations[table.Address()] = &relationStorage{}
	next := catalog.CloneState(New().catalog.state)
	storage := map[ids.ObjectAddress]*relationStorage{}
	e.mu.Lock()
	err := e.preserveTemporaryLocked(context.Background(), next, storage, map[ids.ObjectAddress]*sequenceGenerator{}, nil, nil)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	retained := next.MustRelation(table.Address())
	backing := next.MustRelation(source.Address())
	if retained.OfType != table.OfType || retained.RowType != table.RowType || backing.RowType != source.RowType || storage[table.Address()] != e.storage.relations[table.Address()] {
		t.Fatal("retained TEMP providers were retargeted")
	}
	if err := validateTypedAttachment(e.catalog.read(next), retained); err != nil {
		t.Fatal(err)
	}
}
