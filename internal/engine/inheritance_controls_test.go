package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func inheritanceControlsTemporary(e *Engine, id ids.OID, name string, parents ...ids.ObjectAddress) *relationDescriptor {
	ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 21000}
	if e.catalog.state.Get(ns) == nil {
		e.catalog.state = cloneCatalog(e.catalog.state)
		d := &namespaceDescriptor{address: ns, name: "pg_temp_inheritance_unit", revision: 1, Owner: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Temporary: e.catalog.Domain.Session(99)}
		e.catalog.state.setDescriptor(ns, d)
		e.catalog.state.bindName(descriptorNameKey(d), ns)
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
	state, oid, revision, epoch, storage := e.catalog.state, e.catalog.nextOID, e.catalog.nextRevision, e.catalog.state.epoch, e.storage[temp.address]
	next := cloneCatalog(New().catalog.state)
	e.mu.Lock()
	err := e.preserveTemporaryLocked(context.Background(), next, map[ids.ObjectAddress]*relationStorage{}, map[ids.ObjectAddress]*sequenceGenerator{}, nil, nil)
	e.mu.Unlock()
	if err == nil || err.Error() != temporaryInheritanceReplacementError {
		t.Fatal(err)
	}
	if e.catalog.state != state || e.catalog.nextOID != oid || e.catalog.nextRevision != revision || e.catalog.state.epoch != epoch || e.storage[temp.address] != storage {
		t.Fatal("rejection changed target/allocator")
	}
	// Reset uses the same final replacement fence, not just a helper-only guard.
	if err = e.Reset(context.Background()); err == nil || err.Error() != temporaryInheritanceReplacementError {
		t.Fatal(err)
	}
	if e.catalog.state != state || e.catalog.nextOID != oid || e.catalog.nextRevision != revision || e.storage[temp.address] != storage {
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
	next := cloneCatalog(source.catalog.state)
	storage := map[ids.ObjectAddress]*relationStorage{parent.address: {}, child.address: {}}
	e.catalog.nextOID = 30000
	e.mu.Lock()
	err := e.preserveTemporaryLocked(context.Background(), next, storage, map[ids.ObjectAddress]*sequenceGenerator{}, nil, nil)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if next.Get(temp.address) != temp || next.Get(tempChild.address) != tempChild || storage[temp.address] != retainedStorage {
		t.Fatal("TEMP identities changed")
	}
	imported := next.MustRelation(child.address)
	mapped := imported.Inheritance.Parents[0]
	if mapped == parent.address || mapped.ObjectID <= child.address.ObjectID || next.Get(mapped).Name() != "p" {
		t.Fatal("imported parent not remapped", mapped)
	}
	if child.Inheritance.Parents[0] != parent.address || imported == child {
		t.Fatal("source graph mutated")
	}
	predecessor, err := dumpPredecessor(e.catalog.read(next), mapped, child.address)
	if err != nil {
		t.Fatal(err)
	}
	order, err := viewDumpOrder(context.Background(), map[dumpEventKey][]dumpEventKey{{oid: child.address.ObjectID}: {{oid: predecessor}}, {oid: mapped.ObjectID}: nil}, nil)
	if err != nil || !reflect.DeepEqual(dumpDeclarationIDs(order), []ids.OID{mapped.ObjectID, child.address.ObjectID}) {
		t.Fatal(order, err)
	}
}

func TestInheritanceControlsConstraintOriginRemap(t *testing.T) {
	e := New()
	parent := inheritanceLifecycleTable(e, 20000, "p")
	child := inheritanceLifecycleTable(e, 20001, "c", parent.address)
	parent.NotNullConstraints = []notNullConstraint{{OID: 22000, Name: "nn", Attribute: 1}}
	child.Inheritance.NotNull[1] = inheritedConstraint{Origins: []inheritanceOrigin{{Parent: parent.address, Attribute: 1, Constraint: 22000}}}
	old := map[ids.ObjectAddress]Descriptor{parent.address: parent, child.address: child}
	changed := *parent
	changed.NotNullConstraints = []notNullConstraint{{OID: 30000, Name: "nn", Attribute: 1}}
	next := cloneCatalog(e.catalog.state)
	next.setDescriptor(parent.address, &changed)
	next.setDescriptor(child.address, child)
	if err := remapInheritanceConstraintOrigins(next, old); err != nil {
		t.Fatal(err)
	}
	current := next.MustRelation(child.address)
	if current.Inheritance.NotNull[1].Origins[0].Constraint != 30000 || child.Inheritance.NotNull[1].Origins[0].Constraint != 22000 {
		t.Fatal("constraint origin not independently remapped")
	}
}

func TestInheritanceControlsRetainedDefinitionCollision(t *testing.T) {
	e := New()
	temp := inheritanceControlsTemporary(e, 20000, "tp")
	temp.Columns = []ColumnDescriptor{{Name: "a", Usage: testUsage(e, sqltype.Int4), Nullable: false, NullabilityKnown: true}}
	temp.Attributes = []int32{1}
	temp.AttributeHighWater = 1
	temp.Index = map[string]int{"a": 0}
	temp.NotNullConstraints = []notNullConstraint{{OID: 22000, Name: "tp_a_nn", Attribute: 1}}
	source := New()
	parent := inheritanceLifecycleTable(source, 22000, "p")
	child := inheritanceLifecycleTable(source, 20001, "c", parent.address)
	inheritanceLifecycleInstall(source, parent, child)
	next := cloneCatalog(source.catalog.state)
	storage := map[ids.ObjectAddress]*relationStorage{parent.address: {}, child.address: {}}
	e.mu.Lock()
	err := e.preserveTemporaryLocked(context.Background(), next, storage, map[ids.ObjectAddress]*sequenceGenerator{}, nil, nil)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	mapped := next.MustRelation(child.address).Inheritance.Parents[0]
	if mapped.ObjectID == 22000 || next.Get(mapped).Name() != "p" {
		t.Fatal("imported object did not yield to retained definition identity", mapped)
	}
	if next.Get(temp.address) != temp || temp.NotNullConstraints[0].OID != 22000 {
		t.Fatal("retained definition or descriptor changed")
	}
	if err := validateDefinitionOIDs(e.catalog.read(next)); err != nil {
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
			table.Columns = []ColumnDescriptor{{Name: "b", Usage: usage, Nullable: false, NullabilityKnown: true, OriginTable: uint32(table.address.ObjectID), OriginAttribute: 1}}
			table.Attributes = []int32{1}
			table.AttributeHighWater = 1
			table.Index = map[string]int{"b": 0}
		}
		p.NotNullConstraints = []notNullConstraint{{OID: 23000, Name: "nn", Attribute: 1}}
		c.NotNullConstraints = []notNullConstraint{{OID: 23001, Name: "nn", Attribute: 1}}
		for _, table := range []*relationDescriptor{p, c} {
			ref := owner.catalog.Domain.Ref(owner.CatalogRead().Epoch(), table.address)
			ref.Address.SubID = 1
			table.CheckConstraints = []checkConstraint{{OID: 23002 + table.address.ObjectID - p.address.ObjectID, Name: "ck", Expression: columnExpression{Info: expr.Info{Usage: usage}, origin: ref, scope: 1, frame: 1, slot: 0}}}
		}
		c.Inheritance.Columns[1] = inheritedColumn{Origins: []inheritanceOrigin{{Parent: p.address, Attribute: 1}}}
		c.Inheritance.NotNull[1] = inheritedConstraint{Origins: []inheritanceOrigin{{Parent: p.address, Attribute: 1, Constraint: p.NotNullConstraints[0].OID}}}
		c.Inheritance.Checks["ck"] = inheritedConstraint{Origins: []inheritanceOrigin{{Parent: p.address, Constraint: p.CheckConstraints[0].OID}}}
	}
	installDefinitions(e, temp, tempChild)
	installDefinitions(source, parent, child)
	inheritanceLifecycleInstall(source, parent, child)
	next := cloneCatalog(source.catalog.state)
	storage := map[ids.ObjectAddress]*relationStorage{parent.address: {}, child.address: {}}
	retainedStorage := e.storage[tempChild.address]
	e.mu.Lock()
	err := e.preserveTemporaryLocked(context.Background(), next, storage, map[ids.ObjectAddress]*sequenceGenerator{}, nil, nil)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if next.Get(temp.address) != temp || next.Get(tempChild.address) != tempChild || storage[tempChild.address] != retainedStorage || temp.NotNullConstraints[0].OID != 23000 || temp.CheckConstraints[0].OID != 23002 {
		t.Fatal("retained TEMP graph, storage or definition identity changed")
	}
	imported := next.MustRelation(child.address)
	mappedParent := next.MustRelation(imported.Inheritance.Parents[0])
	if mappedParent.address == parent.address || mappedParent.NotNullConstraints[0].OID == 23000 || mappedParent.CheckConstraints[0].OID == 23002 {
		t.Fatal("imported identities did not yield to retained TEMP definitions")
	}
	if imported.Inheritance.NotNull[1].Origins[0].Constraint != mappedParent.NotNullConstraints[0].OID || imported.Inheritance.Checks["ck"].Origins[0].Constraint != mappedParent.CheckConstraints[0].OID {
		t.Fatal("imported provenance retained old definition IDs")
	}
	if err := validateDefinitionOIDs(e.catalog.read(next)); err != nil {
		t.Fatal(err)
	}
	for _, table := range []*relationDescriptor{tempChild, imported} {
		if err := validateInheritance(e.catalog.read(next), table); err != nil {
			t.Fatal(table.name, err)
		}
	}
	if parent.address != child.Inheritance.Parents[0] || parent.NotNullConstraints[0].OID != 23000 || parent.CheckConstraints[0].OID != 23002 {
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
			table.Columns = []ColumnDescriptor{{Name: "a", Usage: usage, Nullable: false, NullabilityKnown: true, OriginTable: uint32(table.address.ObjectID), OriginAttribute: 1}, {Name: "b", Usage: testUsage(owner, sqltype.Bool), Nullable: true, NullabilityKnown: true, OriginTable: uint32(table.address.ObjectID), OriginAttribute: 2}}
			table.Attributes = []int32{1, 2}
			table.AttributeHighWater = 2
			table.Index = map[string]int{"a": 0, "b": 1}
			ref := owner.catalog.Domain.Ref(owner.CatalogRead().Epoch(), table.address)
			ref.Address.SubID = 2
			table.CheckConstraints = []checkConstraint{{OID: 23002 + table.address.ObjectID - p.address.ObjectID, Name: "ck", Expression: columnExpression{Info: expr.Info{Usage: table.Columns[1].Usage}, origin: ref, scope: 1, frame: 1, slot: 1}}}
		}
		p.NotNullConstraints = []notNullConstraint{{OID: 23000, Name: "nn", Attribute: 1}}
		c.NotNullConstraints = []notNullConstraint{{OID: 23001, Name: "nn", Attribute: 1}}
		c.Inheritance.Columns[1] = inheritedColumn{Origins: []inheritanceOrigin{{Parent: p.address, Attribute: 1}}}
		c.Inheritance.NotNull[1] = inheritedConstraint{Origins: []inheritanceOrigin{{Parent: p.address, Attribute: 1, Constraint: p.NotNullConstraints[0].OID}}}
		c.Inheritance.Columns[2] = inheritedColumn{Origins: []inheritanceOrigin{{Parent: p.address, Attribute: 2}}}
		c.Inheritance.Checks["ck"] = inheritedConstraint{Origins: []inheritanceOrigin{{Parent: p.address, Constraint: p.CheckConstraints[0].OID}}}
	}
	install(e, temp, tempChild)
	install(source, parent, child)
	class, ok := sqltype.LookupIndexClass("int4_ops")
	if !ok {
		t.Fatal("missing actual int4 btree class")
	}
	operator, err := sqltype.BindExclusionOperator(tempChild.Columns[0].Usage, class, "=")
	if err != nil {
		t.Fatal(err)
	}
	index := inheritanceLifecycleTable(e, 24000, "temp_ex")
	index.namespace = temp.namespace
	index.RelationKind = indexRelation
	constraint := exclusionConstraint{OID: parent.address.ObjectID, Name: index.name, Index: index.address, Operators: []sqltype.ExclusionOperator{operator}}
	tempChild.ExclusionConstraints = []exclusionConstraint{constraint}
	tempChild.HasIndexes = true
	index.SQLIndex = &indexDefinition{Method: btreeMethod, Parent: tempChild.address, Keys: []indexKey{{Attribute: 1, Class: class}}, Constraint: exclusionBacking(&constraint), Exclusion: constraint.Operators, Ready: true, Valid: true, Live: true}
	inheritanceLifecycleInstall(e, index)
	inheritanceLifecycleInstall(source, parent, child)
	if err := validateExclusions(e.CatalogRead(), tempChild, map[string]bool{}); err != nil {
		t.Fatal("invalid real exclusion fixture", err)
	}
	before := e.catalog.state
	retainedParentStorage, retainedChildStorage := e.storage[temp.address], e.storage[tempChild.address]
	originalIndex := index.SQLIndex
	next := cloneCatalog(source.catalog.state)
	parentStorage, childStorage := &relationStorage{}, &relationStorage{}
	storage := map[ids.ObjectAddress]*relationStorage{parent.address: parentStorage, child.address: childStorage}
	e.mu.Lock()
	err = e.preserveTemporaryLocked(context.Background(), next, storage, map[ids.ObjectAddress]*sequenceGenerator{}, nil, nil)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	imported := next.MustRelation(child.address)
	mapped := imported.Inheritance.Parents[0]
	if mapped == parent.address || next.Get(mapped).Name() != parent.name || storage[mapped] != parentStorage || storage[imported.address] != childStorage {
		t.Fatal("imported descriptor/storage did not yield to retained exclusion", mapped)
	}
	if imported.Inheritance.Columns[1].Origins[0].Parent != mapped || imported.Inheritance.Columns[2].Origins[0].Parent != mapped || imported.Inheritance.NotNull[1].Origins[0].Parent != mapped || imported.Inheritance.Checks["ck"].Origins[0].Parent != mapped {
		t.Fatal("imported inheritance origins did not follow parent mapping")
	}
	mappedParent := next.MustRelation(mapped)
	if mappedParent.Columns[0].OriginTable != uint32(mapped.ObjectID) || mappedParent.Columns[1].OriginTable != uint32(mapped.ObjectID) || imported.Inheritance.NotNull[1].Origins[0].Constraint != mappedParent.NotNullConstraints[0].OID || imported.Inheritance.Checks["ck"].Origins[0].Constraint != mappedParent.CheckConstraints[0].OID {
		t.Fatal("imported row/constraint origins did not follow mapping")
	}
	if e.catalog.state != before || next.Get(temp.address) != temp || next.Get(tempChild.address) != tempChild || next.Get(index.address) != index || storage[temp.address] != retainedParentStorage || storage[tempChild.address] != retainedChildStorage || index.SQLIndex != originalIndex || !reflect.DeepEqual(tempChild.ExclusionConstraints[0], constraint) || !backingConstraintPresent(tempChild, index) {
		t.Fatal("retained TEMP descriptors, exclusion attachment, storage or source authority changed")
	}
	if source.catalog.state.Get(parent.address) != parent || source.catalog.state.Get(child.address) != child || child.Inheritance.Parents[0] != parent.address || child.Inheritance.Columns[1].Origins[0].Parent != parent.address || parent.Columns[0].OriginTable != uint32(parent.address.ObjectID) || child.Inheritance.NotNull[1].Origins[0].Constraint != 23000 {
		t.Fatal("imported source authority mutated")
	}
	if err := validateDefinitionOIDs(e.catalog.read(next)); err != nil {
		t.Fatal("combined definition identities invalid", err)
	}
	for _, table := range []*relationDescriptor{tempChild, imported} {
		if err := validateInheritance(e.catalog.read(next), table); err != nil {
			t.Fatal(table.name, err)
		}
	}
}
