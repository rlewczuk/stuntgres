package engine

import (
	"context"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"testing"
)

// Provider-only fixture: this does not enable CREATE TYPE in a TEMP schema.
func typedControlsTemporary(e *Engine, tables ...*relationDescriptor) {
	e.catalog.state = cloneCatalog(e.catalog.state)
	namespace := &namespaceDescriptor{address: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 21000}, name: "pg_temp_typed_unit", revision: 1, Owner: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Temporary: e.catalog.domain.Session(99)}
	e.catalog.state.setDescriptor(namespace.address, namespace)
	e.catalog.state.bindName(descriptorNameKey(namespace), namespace.address)
	for _, old := range tables {
		table := *old
		e.catalog.state.unbindName(descriptorNameKey(old))
		table.namespace = namespace.address
		e.catalog.state.setDescriptor(table.address, &table)
		e.catalog.state.bindName(descriptorNameKey(&table), table.address)
		rowAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(table.RowType)}
		row := e.catalog.state.MustType(rowAddress)
		for _, address := range []ids.ObjectAddress{rowAddress, {ClassID: ids.TypeClassID, ObjectID: ids.OID(row.Array)}} {
			oldType := e.catalog.state.MustType(address)
			typ := *oldType
			e.catalog.state.unbindName(descriptorNameKey(oldType))
			typ.namespace = namespace.address
			e.catalog.state.setDescriptor(address, &typ)
			e.catalog.state.bindName(descriptorNameKey(&typ), address)
		}
	}
}

func TestTypedTemporaryReplacementAdmission(t *testing.T) {
	e, _, table := typedUnitCatalog(t, false)
	typedControlsTemporary(e, table)
	e.storage[table.address] = &relationStorage{}
	state, oid, revision, storage := e.catalog.state, e.catalog.nextOID, e.catalog.nextRevision, e.storage[table.address]
	next := cloneCatalog(New().catalog.state)
	e.mu.Lock()
	err := e.preserveTemporaryLocked(context.Background(), next, map[ids.ObjectAddress]*relationStorage{}, map[ids.ObjectAddress]*sequenceGenerator{}, nil, nil)
	e.mu.Unlock()
	if err == nil || err.Error() != temporaryTypedReplacementError {
		t.Fatal(err)
	}
	if e.catalog.state != state || e.catalog.nextOID != oid || e.catalog.nextRevision != revision || e.storage[table.address] != storage {
		t.Fatal("final admission rejection mutated target")
	}
	if err := e.Reset(context.Background()); err == nil || err.Error() != temporaryTypedReplacementError {
		t.Fatal(err)
	}
	if e.catalog.state != state || e.catalog.nextOID != oid || e.catalog.nextRevision != revision || e.storage[table.address] != storage {
		t.Fatal("preallocation rejection mutated target")
	}
}

func TestTypedTemporaryProviderSourceIdentityRetention(t *testing.T) {
	e, source, table := typedUnitCatalog(t, false)
	typedControlsTemporary(e, source, table)
	e.storage[table.address] = &relationStorage{}
	next := cloneCatalog(New().catalog.state)
	storage := map[ids.ObjectAddress]*relationStorage{}
	e.mu.Lock()
	err := e.preserveTemporaryLocked(context.Background(), next, storage, map[ids.ObjectAddress]*sequenceGenerator{}, nil, nil)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	retained := next.MustRelation(table.address)
	backing := next.MustRelation(source.address)
	if retained.OfType != table.OfType || retained.RowType != table.RowType || backing.RowType != source.RowType || storage[table.address] != e.storage[table.address] {
		t.Fatal("retained TEMP providers were retargeted")
	}
	if err := validateTypedAttachment(e.catalog.read(next), retained); err != nil {
		t.Fatal(err)
	}
}
