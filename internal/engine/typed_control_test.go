package engine

import (
	"context"
	"stuntgres/internal/engine/authorization"
	"testing"
)

// Provider-only fixture: this does not enable CREATE TYPE in a TEMP schema.
func typedControlsTemporary(e *Engine, tables ...*relationDescriptor) {
	e.state = cloneCatalog(e.state)
	namespace := &namespaceDescriptor{address: ObjectAddress{ClassID: NamespaceClassID, ObjectID: 21000}, name: "pg_temp_typed_unit", revision: 1, owner: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), temporary: SessionToken{owner: e, id: 99}}
	e.state.descriptors[namespace.address] = namespace
	e.state.names[descriptorNameKey(namespace)] = namespace.address
	for _, old := range tables {
		table := *old
		delete(e.state.names, descriptorNameKey(old))
		table.namespace = namespace.address
		e.state.descriptors[table.address] = &table
		e.state.names[descriptorNameKey(&table)] = table.address
		rowAddress := ObjectAddress{ClassID: TypeClassID, ObjectID: OID(table.rowType)}
		row := e.state.descriptors[rowAddress].(*typeDescriptor)
		for _, address := range []ObjectAddress{rowAddress, {ClassID: TypeClassID, ObjectID: OID(row.array)}} {
			oldType := e.state.descriptors[address].(*typeDescriptor)
			typ := *oldType
			delete(e.state.names, descriptorNameKey(oldType))
			typ.namespace = namespace.address
			e.state.descriptors[address] = &typ
			e.state.names[descriptorNameKey(&typ)] = address
		}
	}
}

func TestTypedTemporaryReplacementAdmission(t *testing.T) {
	e, _, table := typedUnitCatalog(t, false)
	typedControlsTemporary(e, table)
	e.storage[table.address] = &relationStorage{}
	state, oid, revision, storage := e.state, e.nextOID, e.nextRevision, e.storage[table.address]
	next := cloneCatalog(New().state)
	e.mu.Lock()
	err := e.preserveTemporaryLocked(context.Background(), next, map[ObjectAddress]*relationStorage{}, map[ObjectAddress]*sequenceGenerator{}, nil, nil)
	e.mu.Unlock()
	if err == nil || err.Error() != temporaryTypedReplacementError {
		t.Fatal(err)
	}
	if e.state != state || e.nextOID != oid || e.nextRevision != revision || e.storage[table.address] != storage {
		t.Fatal("final admission rejection mutated target")
	}
	if err := e.Reset(context.Background()); err == nil || err.Error() != temporaryTypedReplacementError {
		t.Fatal(err)
	}
	if e.state != state || e.nextOID != oid || e.nextRevision != revision || e.storage[table.address] != storage {
		t.Fatal("preallocation rejection mutated target")
	}
}

func TestTypedTemporaryProviderSourceIdentityRetention(t *testing.T) {
	e, source, table := typedUnitCatalog(t, false)
	typedControlsTemporary(e, source, table)
	e.storage[table.address] = &relationStorage{}
	next := cloneCatalog(New().state)
	storage := map[ObjectAddress]*relationStorage{}
	e.mu.Lock()
	err := e.preserveTemporaryLocked(context.Background(), next, storage, map[ObjectAddress]*sequenceGenerator{}, nil, nil)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	retained := next.descriptors[table.address].(*relationDescriptor)
	backing := next.descriptors[source.address].(*relationDescriptor)
	if retained.ofType != table.ofType || retained.rowType != table.rowType || backing.rowType != source.rowType || storage[table.address] != e.storage[table.address] {
		t.Fatal("retained TEMP providers were retargeted")
	}
	if err := validateTypedAttachment(CatalogRead{owner: e, state: next}, retained); err != nil {
		t.Fatal(err)
	}
}
