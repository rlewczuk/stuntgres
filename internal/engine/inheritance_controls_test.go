package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqltype"
	"testing"
)

func inheritanceControlsTemporary(e *Engine, id OID, name string, parents ...ObjectAddress) *relationDescriptor {
	ns := ObjectAddress{ClassID: NamespaceClassID, ObjectID: 21000}
	if e.state.descriptors[ns] == nil {
		e.state = cloneCatalog(e.state)
		d := &namespaceDescriptor{address: ns, name: "pg_temp_inheritance_unit", revision: 1, owner: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), temporary: SessionToken{domain: e.domain, id: 99}}
		e.state.descriptors[ns] = d
		e.state.names[descriptorNameKey(d)] = ns
	}
	table := inheritanceLifecycleTable(e, id, name, parents...)
	table.namespace = ns
	inheritanceLifecycleInstall(e, table)
	return table
}

func TestInheritanceControlsTemporaryReplacementRejection(t *testing.T) {
	e := New()
	p := inheritanceLifecycleTable(e, 20000, "p")
	inheritanceLifecycleInstall(e, p)
	temp := inheritanceControlsTemporary(e, 20001, "tc", p.address)
	state, oid, revision, epoch, storage := e.state, e.nextOID, e.nextRevision, e.state.epoch, e.storage[temp.address]
	next := cloneCatalog(New().state)
	e.mu.Lock()
	err := e.preserveTemporaryLocked(context.Background(), next, map[ObjectAddress]*relationStorage{}, map[ObjectAddress]*sequenceGenerator{}, nil, nil)
	e.mu.Unlock()
	if err == nil || err.Error() != temporaryInheritanceReplacementError {
		t.Fatal(err)
	}
	if e.state != state || e.nextOID != oid || e.nextRevision != revision || e.state.epoch != epoch || e.storage[temp.address] != storage {
		t.Fatal("rejection changed target/allocator")
	}
	// Reset uses the same final replacement fence, not just a helper-only guard.
	if err = e.Reset(context.Background()); err == nil || err.Error() != temporaryInheritanceReplacementError {
		t.Fatal(err)
	}
	if e.state != state || e.nextOID != oid || e.nextRevision != revision || e.storage[temp.address] != storage {
		t.Fatal("Reset rejection changed state")
	}
}

func TestInheritanceControlsRetainedTemporaryCollision(t *testing.T) {
	e := New()
	temp := inheritanceControlsTemporary(e, 20000, "tp")
	tempChild := inheritanceControlsTemporary(e, 20004, "tc", temp.address)
	retainedStorage := e.storage[temp.address]
	source := New()
	parent := inheritanceLifecycleTable(source, 20000, "p")
	child := inheritanceLifecycleTable(source, 20001, "c", parent.address)
	inheritanceLifecycleInstall(source, parent, child)
	next := cloneCatalog(source.state)
	storage := map[ObjectAddress]*relationStorage{parent.address: {}, child.address: {}}
	e.nextOID = 30000
	e.mu.Lock()
	err := e.preserveTemporaryLocked(context.Background(), next, storage, map[ObjectAddress]*sequenceGenerator{}, nil, nil)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if next.descriptors[temp.address] != temp || next.descriptors[tempChild.address] != tempChild || storage[temp.address] != retainedStorage {
		t.Fatal("TEMP identities changed")
	}
	imported := next.descriptors[child.address].(*relationDescriptor)
	mapped := imported.inheritance.parents[0]
	if mapped == parent.address || mapped.ObjectID <= child.address.ObjectID || next.descriptors[mapped].Name() != "p" {
		t.Fatal("imported parent not remapped", mapped)
	}
	if child.inheritance.parents[0] != parent.address || imported == child {
		t.Fatal("source graph mutated")
	}
	predecessor, err := dumpPredecessor(CatalogRead{owner: e, state: next}, mapped, child.address)
	if err != nil {
		t.Fatal(err)
	}
	order, err := viewDumpOrder(context.Background(), map[dumpEventKey][]dumpEventKey{{oid: child.address.ObjectID}: {{oid: predecessor}}, {oid: mapped.ObjectID}: nil}, nil)
	if err != nil || !reflect.DeepEqual(dumpDeclarationIDs(order), []OID{mapped.ObjectID, child.address.ObjectID}) {
		t.Fatal(order, err)
	}
}

func TestInheritanceControlsConstraintOriginRemap(t *testing.T) {
	e := New()
	parent := inheritanceLifecycleTable(e, 20000, "p")
	child := inheritanceLifecycleTable(e, 20001, "c", parent.address)
	parent.notNullConstraints = []notNullConstraint{{OID: 22000, Name: "nn", Attribute: 1}}
	child.inheritance.notNull[1] = inheritedConstraint{origins: []inheritanceOrigin{{parent: parent.address, attribute: 1, constraint: 22000}}}
	old := map[ObjectAddress]Descriptor{parent.address: parent, child.address: child}
	changed := *parent
	changed.notNullConstraints = []notNullConstraint{{OID: 30000, Name: "nn", Attribute: 1}}
	next := cloneCatalog(e.state)
	next.descriptors[parent.address] = &changed
	next.descriptors[child.address] = child
	if err := remapInheritanceConstraintOrigins(next, old); err != nil {
		t.Fatal(err)
	}
	current := next.descriptors[child.address].(*relationDescriptor)
	if current.inheritance.notNull[1].origins[0].constraint != 30000 || child.inheritance.notNull[1].origins[0].constraint != 22000 {
		t.Fatal("constraint origin not independently remapped")
	}
}

func TestInheritanceControlsRetainedDefinitionCollision(t *testing.T) {
	e := New()
	temp := inheritanceControlsTemporary(e, 20000, "tp")
	temp.columns = []ColumnDescriptor{{Name: "a", Usage: testUsage(e, sqltype.Int4), Nullable: false, NullabilityKnown: true}}
	temp.attributes = []int32{1}
	temp.attributeHighWater = 1
	temp.index = map[string]int{"a": 0}
	temp.notNullConstraints = []notNullConstraint{{OID: 22000, Name: "tp_a_nn", Attribute: 1}}
	source := New()
	parent := inheritanceLifecycleTable(source, 22000, "p")
	child := inheritanceLifecycleTable(source, 20001, "c", parent.address)
	inheritanceLifecycleInstall(source, parent, child)
	next := cloneCatalog(source.state)
	storage := map[ObjectAddress]*relationStorage{parent.address: {}, child.address: {}}
	e.mu.Lock()
	err := e.preserveTemporaryLocked(context.Background(), next, storage, map[ObjectAddress]*sequenceGenerator{}, nil, nil)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	mapped := next.descriptors[child.address].(*relationDescriptor).inheritance.parents[0]
	if mapped.ObjectID == 22000 || next.descriptors[mapped].Name() != "p" {
		t.Fatal("imported object did not yield to retained definition identity", mapped)
	}
	if next.descriptors[temp.address] != temp || temp.notNullConstraints[0].OID != 22000 {
		t.Fatal("retained definition or descriptor changed")
	}
	if err := validateDefinitionOIDs(CatalogRead{owner: e, state: next}); err != nil {
		t.Fatal("combined identities remain colliding", err)
	}
}

func TestInheritanceControlsRetainedChecksAndConstraintCollisions(t *testing.T) {
	e := New()
	temp := inheritanceControlsTemporary(e, 20000, "tp")
	tempChild := inheritanceControlsTemporary(e, 20001, "tc", temp.address)
	source := New()
	parent := inheritanceLifecycleTable(source, 20000, "p")
	child := inheritanceLifecycleTable(source, 20002, "c", parent.address)
	installDefinitions := func(owner *Engine, p, c *relationDescriptor) {
		usage := testUsage(owner, sqltype.Bool)
		for _, table := range []*relationDescriptor{p, c} {
			table.columns = []ColumnDescriptor{{Name: "b", Usage: usage, Nullable: false, NullabilityKnown: true, OriginTable: uint32(table.address.ObjectID), OriginAttribute: 1}}
			table.attributes = []int32{1}
			table.attributeHighWater = 1
			table.index = map[string]int{"b": 0}
		}
		p.notNullConstraints = []notNullConstraint{{OID: 23000, Name: "nn", Attribute: 1}}
		c.notNullConstraints = []notNullConstraint{{OID: 23001, Name: "nn", Attribute: 1}}
		for _, table := range []*relationDescriptor{p, c} {
			ref := ObjectRef{owner: owner.domain, Epoch: owner.CatalogRead().Epoch(), Address: table.address}
			ref.Address.SubID = 1
			table.checkConstraints = []checkConstraint{{OID: 23002 + table.address.ObjectID - p.address.ObjectID, Name: "ck", expression: columnExpression{expressionInfo: expressionInfo{usage: usage}, origin: ref, scope: 1, frame: 1, slot: 0}}}
		}
		c.inheritance.columns[1] = inheritedColumn{origins: []inheritanceOrigin{{parent: p.address, attribute: 1}}}
		c.inheritance.notNull[1] = inheritedConstraint{origins: []inheritanceOrigin{{parent: p.address, attribute: 1, constraint: p.notNullConstraints[0].OID}}}
		c.inheritance.checks["ck"] = inheritedConstraint{origins: []inheritanceOrigin{{parent: p.address, constraint: p.checkConstraints[0].OID}}}
	}
	installDefinitions(e, temp, tempChild)
	installDefinitions(source, parent, child)
	inheritanceLifecycleInstall(source, parent, child)
	next := cloneCatalog(source.state)
	storage := map[ObjectAddress]*relationStorage{parent.address: {}, child.address: {}}
	retainedStorage := e.storage[tempChild.address]
	e.mu.Lock()
	err := e.preserveTemporaryLocked(context.Background(), next, storage, map[ObjectAddress]*sequenceGenerator{}, nil, nil)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if next.descriptors[temp.address] != temp || next.descriptors[tempChild.address] != tempChild || storage[tempChild.address] != retainedStorage || temp.notNullConstraints[0].OID != 23000 || temp.checkConstraints[0].OID != 23002 {
		t.Fatal("retained TEMP graph, storage or definition identity changed")
	}
	imported := next.descriptors[child.address].(*relationDescriptor)
	mappedParent := next.descriptors[imported.inheritance.parents[0]].(*relationDescriptor)
	if mappedParent.address == parent.address || mappedParent.notNullConstraints[0].OID == 23000 || mappedParent.checkConstraints[0].OID == 23002 {
		t.Fatal("imported identities did not yield to retained TEMP definitions")
	}
	if imported.inheritance.notNull[1].origins[0].constraint != mappedParent.notNullConstraints[0].OID || imported.inheritance.checks["ck"].origins[0].constraint != mappedParent.checkConstraints[0].OID {
		t.Fatal("imported provenance retained old definition IDs")
	}
	if err := validateDefinitionOIDs(CatalogRead{owner: e, state: next}); err != nil {
		t.Fatal(err)
	}
	for _, table := range []*relationDescriptor{tempChild, imported} {
		if err := validateInheritance(CatalogRead{owner: e, state: next}, table); err != nil {
			t.Fatal(table.name, err)
		}
	}
	if parent.address != child.inheritance.parents[0] || parent.notNullConstraints[0].OID != 23000 || parent.checkConstraints[0].OID != 23002 {
		t.Fatal("replacement mutated imported source definitions")
	}
}

func TestInheritanceControlsRetainedExclusionDefinitionCollision(t *testing.T) {
	e := New()
	temp := inheritanceControlsTemporary(e, 20000, "tp")
	tempChild := inheritanceControlsTemporary(e, 20001, "tc", temp.address)
	source := New()
	parent := inheritanceLifecycleTable(source, 22000, "p")
	child := inheritanceLifecycleTable(source, 20002, "c", parent.address)
	install := func(owner *Engine, p, c *relationDescriptor) {
		usage := testUsage(owner, sqltype.Int4)
		for _, table := range []*relationDescriptor{p, c} {
			table.columns = []ColumnDescriptor{{Name: "a", Usage: usage, Nullable: false, NullabilityKnown: true, OriginTable: uint32(table.address.ObjectID), OriginAttribute: 1}, {Name: "b", Usage: testUsage(owner, sqltype.Bool), Nullable: true, NullabilityKnown: true, OriginTable: uint32(table.address.ObjectID), OriginAttribute: 2}}
			table.attributes = []int32{1, 2}
			table.attributeHighWater = 2
			table.index = map[string]int{"a": 0, "b": 1}
			ref := ObjectRef{owner: owner.domain, Epoch: owner.CatalogRead().Epoch(), Address: table.address}
			ref.Address.SubID = 2
			table.checkConstraints = []checkConstraint{{OID: 23002 + table.address.ObjectID - p.address.ObjectID, Name: "ck", expression: columnExpression{expressionInfo: expressionInfo{usage: table.columns[1].Usage}, origin: ref, scope: 1, frame: 1, slot: 1}}}
		}
		p.notNullConstraints = []notNullConstraint{{OID: 23000, Name: "nn", Attribute: 1}}
		c.notNullConstraints = []notNullConstraint{{OID: 23001, Name: "nn", Attribute: 1}}
		c.inheritance.columns[1] = inheritedColumn{origins: []inheritanceOrigin{{parent: p.address, attribute: 1}}}
		c.inheritance.notNull[1] = inheritedConstraint{origins: []inheritanceOrigin{{parent: p.address, attribute: 1, constraint: p.notNullConstraints[0].OID}}}
		c.inheritance.columns[2] = inheritedColumn{origins: []inheritanceOrigin{{parent: p.address, attribute: 2}}}
		c.inheritance.checks["ck"] = inheritedConstraint{origins: []inheritanceOrigin{{parent: p.address, constraint: p.checkConstraints[0].OID}}}
	}
	install(e, temp, tempChild)
	install(source, parent, child)
	class, ok := sqltype.LookupIndexClass("int4_ops")
	if !ok {
		t.Fatal("missing actual int4 btree class")
	}
	operator, err := sqltype.BindExclusionOperator(tempChild.columns[0].Usage, class, "=")
	if err != nil {
		t.Fatal(err)
	}
	index := inheritanceLifecycleTable(e, 24000, "temp_ex")
	index.namespace = temp.namespace
	index.relationKind = indexRelation
	constraint := exclusionConstraint{OID: parent.address.ObjectID, Name: index.name, Index: index.address, Operators: []sqltype.ExclusionOperator{operator}}
	tempChild.exclusionConstraints = []exclusionConstraint{constraint}
	tempChild.hasIndexes = true
	index.sqlIndex = &indexDefinition{method: btreeMethod, parent: tempChild.address, keys: []indexKey{{attribute: 1, class: class}}, constraint: exclusionBacking(&constraint), exclusion: constraint.Operators, ready: true, valid: true, live: true}
	inheritanceLifecycleInstall(e, index)
	inheritanceLifecycleInstall(source, parent, child)
	if err := validateExclusions(e.CatalogRead(), tempChild, map[string]bool{}); err != nil {
		t.Fatal("invalid real exclusion fixture", err)
	}
	before := e.state
	retainedParentStorage, retainedChildStorage := e.storage[temp.address], e.storage[tempChild.address]
	originalIndex := index.sqlIndex
	next := cloneCatalog(source.state)
	parentStorage, childStorage := &relationStorage{}, &relationStorage{}
	storage := map[ObjectAddress]*relationStorage{parent.address: parentStorage, child.address: childStorage}
	e.mu.Lock()
	err = e.preserveTemporaryLocked(context.Background(), next, storage, map[ObjectAddress]*sequenceGenerator{}, nil, nil)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	imported := next.descriptors[child.address].(*relationDescriptor)
	mapped := imported.inheritance.parents[0]
	if mapped == parent.address || next.descriptors[mapped].Name() != parent.name || storage[mapped] != parentStorage || storage[imported.address] != childStorage {
		t.Fatal("imported descriptor/storage did not yield to retained exclusion", mapped)
	}
	if imported.inheritance.columns[1].origins[0].parent != mapped || imported.inheritance.columns[2].origins[0].parent != mapped || imported.inheritance.notNull[1].origins[0].parent != mapped || imported.inheritance.checks["ck"].origins[0].parent != mapped {
		t.Fatal("imported inheritance origins did not follow parent mapping")
	}
	mappedParent := next.descriptors[mapped].(*relationDescriptor)
	if mappedParent.columns[0].OriginTable != uint32(mapped.ObjectID) || mappedParent.columns[1].OriginTable != uint32(mapped.ObjectID) || imported.inheritance.notNull[1].origins[0].constraint != mappedParent.notNullConstraints[0].OID || imported.inheritance.checks["ck"].origins[0].constraint != mappedParent.checkConstraints[0].OID {
		t.Fatal("imported row/constraint origins did not follow mapping")
	}
	if e.state != before || next.descriptors[temp.address] != temp || next.descriptors[tempChild.address] != tempChild || next.descriptors[index.address] != index || storage[temp.address] != retainedParentStorage || storage[tempChild.address] != retainedChildStorage || index.sqlIndex != originalIndex || !reflect.DeepEqual(tempChild.exclusionConstraints[0], constraint) || !backingConstraintPresent(tempChild, index) {
		t.Fatal("retained TEMP descriptors, exclusion attachment, storage or source authority changed")
	}
	if source.state.descriptors[parent.address] != parent || source.state.descriptors[child.address] != child || child.inheritance.parents[0] != parent.address || child.inheritance.columns[1].origins[0].parent != parent.address || parent.columns[0].OriginTable != uint32(parent.address.ObjectID) || child.inheritance.notNull[1].origins[0].constraint != 23000 {
		t.Fatal("imported source authority mutated")
	}
	if err := validateDefinitionOIDs(CatalogRead{owner: e, state: next}); err != nil {
		t.Fatal("combined definition identities invalid", err)
	}
	for _, table := range []*relationDescriptor{tempChild, imported} {
		if err := validateInheritance(CatalogRead{owner: e, state: next}, table); err != nil {
			t.Fatal(table.name, err)
		}
	}
}
