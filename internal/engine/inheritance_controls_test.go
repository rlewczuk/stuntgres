package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqltype"
	"testing"
)

func inheritanceControlsTemporary(e *Engine, id ids.OID, name string, parents ...ids.ObjectAddress) *catalog.Relation {
	ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 21000}
	if e.catalog.State.Get(ns) == nil {
		e.catalog.PublishLocked(catalog.CloneState(e.catalog.State))
		d := &catalog.Namespace{ObjectAddress: ns, ObjectName: "pg_temp_inheritance_unit", ObjectRevision: 1, Owner: e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Temporary: e.catalog.Domain.Session(99)}
		e.catalog.State.SetDescriptor(ns, d)
		e.catalog.State.BindName(catalog.DescriptorNameKey(d), ns)
	}
	table := inheritanceLifecycleTable(e, id, name, parents...)
	table.ObjectNamespace = ns
	inheritanceLifecycleInstall(e, table)
	return table
}

func TestInheritanceControlsTemporaryReplacementRejection(t *testing.T) {
	e := New()
	p := inheritanceLifecycleTable(e, 20000, "p")
	inheritanceLifecycleInstall(e, p)
	temp := inheritanceControlsTemporary(e, 20001, "tc", p.Address())
	state, oid, revision, epoch, stored := e.catalog.State, e.catalog.NextOID(), e.catalog.NextRevision(), e.catalog.State.Epoch(), e.storage.Relations[temp.Address()]
	next := catalog.CloneState(New().catalog.State)
	e.mu.Lock()
	err := e.preserveTemporaryLocked(context.Background(), next, map[ids.ObjectAddress]*storage.Relation{}, map[ids.ObjectAddress]*storage.SequenceGenerator{}, nil, nil, nil, &catalogOIDReservation{Base: e.catalog.NextOID(), Next: e.catalog.NextOID()})
	e.mu.Unlock()
	if err == nil || err.Error() != control.TemporaryInheritanceReplacementError {
		t.Fatal(err)
	}
	if e.catalog.State != state || e.catalog.NextOID() != oid || e.catalog.NextRevision() != revision || e.catalog.State.Epoch() != epoch || e.storage.Relations[temp.Address()] != stored {
		t.Fatal("rejection changed target/allocator")
	}
	// Reset uses the same final replacement fence, not just a helper-only guard.
	if err = e.Reset(context.Background()); err == nil || err.Error() != control.TemporaryInheritanceReplacementError {
		t.Fatal(err)
	}
	if e.catalog.State != state || e.catalog.NextOID() != oid || e.catalog.NextRevision() != revision || e.storage.Relations[temp.Address()] != stored {
		t.Fatal("Reset rejection changed state")
	}
}

func TestInheritanceControlsRetainedTemporaryCollision(t *testing.T) {
	e := New()
	temp := inheritanceControlsTemporary(e, 20000, "tp")
	tempChild := inheritanceControlsTemporary(e, 20004, "tc", temp.Address())
	retainedStorage := e.storage.Relations[temp.Address()]
	source := New()
	parent := inheritanceLifecycleTable(source, 20000, "p")
	child := inheritanceLifecycleTable(source, 20001, "c", parent.Address())
	inheritanceLifecycleInstall(source, parent, child)
	next := catalog.CloneState(source.catalog.State)
	relations := map[ids.ObjectAddress]*storage.Relation{parent.Address(): {}, child.Address(): {}}
	e.catalog.InstallOIDFrontierLocked(30000)
	e.mu.Lock()
	err := e.preserveTemporaryLocked(context.Background(), next, relations, map[ids.ObjectAddress]*storage.SequenceGenerator{}, nil, nil, replacementProcedurePolicyFixture(source, e), &catalogOIDReservation{Base: e.catalog.NextOID(), Next: e.catalog.NextOID()})
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if next.Get(temp.Address()) != temp || next.Get(tempChild.Address()) != tempChild || relations[temp.Address()] != retainedStorage {
		t.Fatal("TEMP identities changed")
	}
	imported := next.MustRelation(child.Address())
	mapped := imported.Inheritance.Parents[0].Relation
	if mapped == parent.Address() || mapped.ObjectID <= child.Address().ObjectID || next.Get(mapped).Name() != "p" {
		t.Fatal("imported parent not remapped", mapped)
	}
	if child.Inheritance.Parents[0].Relation != parent.Address() || imported == child {
		t.Fatal("source graph mutated")
	}
	predecessor, err := control.Resolver{}.Predecessor(e.catalog.Read(next), mapped, child.Address())
	if err != nil {
		t.Fatal(err)
	}
	order, err := dump.Order(context.Background(), dump.Graph{{OID: child.Address().ObjectID}: {{OID: predecessor}}, {OID: mapped.ObjectID}: nil}, nil)
	if err != nil || !reflect.DeepEqual(dump.DeclarationIDs(order), []ids.OID{mapped.ObjectID, child.Address().ObjectID}) {
		t.Fatal(order, err)
	}
}

func TestInheritanceControlsConstraintOriginRemap(t *testing.T) {
	e := New()
	parent := inheritanceLifecycleTable(e, 20000, "p")
	child := inheritanceLifecycleTable(e, 20001, "c", parent.Address())
	parent.Constraints.NotNull = []catalog.NotNullConstraint{{OID: 22000, Name: "nn", Attribute: 1}}
	child.Inheritance.NotNull[1] = catalog.InheritedConstraint{Origins: []catalog.InheritanceOrigin{{Parent: parent.Address(), Attribute: 1, Constraint: 22000}}}
	old := map[ids.ObjectAddress]catalog.Descriptor{parent.Address(): parent, child.Address(): child}
	changed := *parent
	changed.Constraints.NotNull = []catalog.NotNullConstraint{{OID: 30000, Name: "nn", Attribute: 1}}
	next := catalog.CloneState(e.catalog.State)
	next.SetDescriptor(parent.Address(), &changed)
	next.SetDescriptor(child.Address(), child)
	if err := table.RemapInheritanceConstraintOrigins(next, old); err != nil {
		t.Fatal(err)
	}
	current := next.MustRelation(child.Address())
	if current.Inheritance.NotNull[1].Origins[0].Constraint != 30000 || child.Inheritance.NotNull[1].Origins[0].Constraint != 22000 {
		t.Fatal("constraint origin not independently remapped")
	}
}

func TestInheritanceControlsRetainedDefinitionCollision(t *testing.T) {
	e := New()
	temp := inheritanceControlsTemporary(e, 20000, "tp")
	temp.Columns = []catalog.Column{{Name: "a", Usage: testUsage(e, sqltype.Int4), Nullable: false, NullabilityKnown: true}}
	temp.Attributes = []int32{1}
	temp.ColumnMeta.HighWater = 1
	temp.Index = map[string]int{"a": 0}
	temp.Constraints.NotNull = []catalog.NotNullConstraint{{OID: 22000, Name: "tp_a_nn", Attribute: 1}}
	source := New()
	parent := inheritanceLifecycleTable(source, 22000, "p")
	child := inheritanceLifecycleTable(source, 20001, "c", parent.Address())
	inheritanceLifecycleInstall(source, parent, child)
	next := catalog.CloneState(source.catalog.State)
	relations := map[ids.ObjectAddress]*storage.Relation{parent.Address(): {}, child.Address(): {}}
	e.mu.Lock()
	err := e.preserveTemporaryLocked(context.Background(), next, relations, map[ids.ObjectAddress]*storage.SequenceGenerator{}, nil, nil, replacementProcedurePolicyFixture(source, e), &catalogOIDReservation{Base: e.catalog.NextOID(), Next: e.catalog.NextOID()})
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	mapped := next.MustRelation(child.Address()).Inheritance.Parents[0].Relation
	if mapped.ObjectID == 22000 || next.Get(mapped).Name() != "p" {
		t.Fatal("imported object did not yield to retained definition identity", mapped)
	}
	if next.Get(temp.Address()) != temp || temp.Constraints.NotNull[0].OID != 22000 {
		t.Fatal("retained definition or descriptor changed")
	}
	if err := catalog.ValidateDefinitionOIDs(e.catalog.Read(next)); err != nil {
		t.Fatal("combined identities remain colliding", err)
	}
}

func TestInheritanceControlsRetainedChecksAndConstraintCollisions(t *testing.T) {
	e := New()
	temp := inheritanceControlsTemporary(e, 20000, "tp")
	tempChild := inheritanceControlsTemporary(e, 20001, "tc", temp.Address())
	source := New()
	parent := inheritanceLifecycleTable(source, 20000, "p")
	child := inheritanceLifecycleTable(source, 20002, "c", parent.Address())
	installDefinitions := func(owner *Engine, p, c *catalog.Relation) {
		usage := testUsage(owner, sqltype.Bool)
		for _, table := range []*catalog.Relation{p, c} {
			table.Columns = []catalog.Column{{Name: "b", Usage: usage, Nullable: false, NullabilityKnown: true, OriginTable: uint32(table.Address().ObjectID), OriginAttribute: 1}}
			table.Attributes = []int32{1}
			table.ColumnMeta.HighWater = 1
			table.Index = map[string]int{"b": 0}
		}
		p.Constraints.NotNull = []catalog.NotNullConstraint{{OID: 23000, Name: "nn", Attribute: 1}}
		c.Constraints.NotNull = []catalog.NotNullConstraint{{OID: 23001, Name: "nn", Attribute: 1}}
		for _, table := range []*catalog.Relation{p, c} {
			ref := owner.catalog.Domain.Ref(owner.CatalogRead().Epoch(), table.Address())
			ref.Address.SubID = 1
			table.Constraints.Check = []catalog.CheckConstraint{{OID: 23002 + table.Address().ObjectID - p.Address().ObjectID, Name: "ck", Expression: plan.ColumnExpr{Info: expr.Info{Usage: usage}, Origin: ref, Scope: 1, Frame: 1, Slot: 0}}}
		}
		c.Inheritance.Columns[1] = catalog.InheritedColumn{Origins: []catalog.InheritanceOrigin{{Parent: p.Address(), Attribute: 1}}}
		c.Inheritance.NotNull[1] = catalog.InheritedConstraint{Origins: []catalog.InheritanceOrigin{{Parent: p.Address(), Attribute: 1, Constraint: p.Constraints.NotNull[0].OID}}}
		c.Inheritance.Checks["ck"] = catalog.InheritedConstraint{Origins: []catalog.InheritanceOrigin{{Parent: p.Address(), Constraint: p.Constraints.Check[0].OID}}}
	}
	installDefinitions(e, temp, tempChild)
	installDefinitions(source, parent, child)
	inheritanceLifecycleInstall(source, parent, child)
	next := catalog.CloneState(source.catalog.State)
	relations := map[ids.ObjectAddress]*storage.Relation{parent.Address(): {}, child.Address(): {}}
	retainedStorage := e.storage.Relations[tempChild.Address()]
	e.mu.Lock()
	err := e.preserveTemporaryLocked(context.Background(), next, relations, map[ids.ObjectAddress]*storage.SequenceGenerator{}, nil, nil, replacementProcedurePolicyFixture(source, e), &catalogOIDReservation{Base: e.catalog.NextOID(), Next: e.catalog.NextOID()})
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if next.Get(temp.Address()) != temp || next.Get(tempChild.Address()) != tempChild || relations[tempChild.Address()] != retainedStorage || temp.Constraints.NotNull[0].OID != 23000 || temp.Constraints.Check[0].OID != 23002 {
		t.Fatal("retained TEMP graph, storage or definition identity changed")
	}
	imported := next.MustRelation(child.Address())
	mappedParent := next.MustRelation(imported.Inheritance.Parents[0].Relation)
	if mappedParent.Address() == parent.Address() || mappedParent.Constraints.NotNull[0].OID == 23000 || mappedParent.Constraints.Check[0].OID == 23002 {
		t.Fatal("imported identities did not yield to retained TEMP definitions")
	}
	if imported.Inheritance.NotNull[1].Origins[0].Constraint != mappedParent.Constraints.NotNull[0].OID || imported.Inheritance.Checks["ck"].Origins[0].Constraint != mappedParent.Constraints.Check[0].OID {
		t.Fatal("imported provenance retained old definition IDs")
	}
	if err := catalog.ValidateDefinitionOIDs(e.catalog.Read(next)); err != nil {
		t.Fatal(err)
	}
	for _, tbl := range []*catalog.Relation{tempChild, imported} {
		if err := table.ValidateInheritance(e.catalog.Read(next), tbl); err != nil {
			t.Fatal(tbl.Name(), err)
		}
	}
	if parent.Address() != child.Inheritance.Parents[0].Relation || parent.Constraints.NotNull[0].OID != 23000 || parent.Constraints.Check[0].OID != 23002 {
		t.Fatal("replacement mutated imported source definitions")
	}
}

func TestInheritanceControlsRetainedExclusionDefinitionCollision(t *testing.T) {
	e := New()
	temp := inheritanceControlsTemporary(e, 20000, "tp")
	tempChild := inheritanceControlsTemporary(e, 20001, "tc", temp.Address())
	source := New()
	parent := inheritanceLifecycleTable(source, 22000, "p")
	child := inheritanceLifecycleTable(source, 20002, "c", parent.Address())
	install := func(owner *Engine, p, c *catalog.Relation) {
		usage := testUsage(owner, sqltype.Int4)
		for _, table := range []*catalog.Relation{p, c} {
			table.Columns = []catalog.Column{{Name: "a", Usage: usage, Nullable: false, NullabilityKnown: true, OriginTable: uint32(table.Address().ObjectID), OriginAttribute: 1}, {Name: "b", Usage: testUsage(owner, sqltype.Bool), Nullable: true, NullabilityKnown: true, OriginTable: uint32(table.Address().ObjectID), OriginAttribute: 2}}
			table.Attributes = []int32{1, 2}
			table.ColumnMeta.HighWater = 2
			table.Index = map[string]int{"a": 0, "b": 1}
			ref := owner.catalog.Domain.Ref(owner.CatalogRead().Epoch(), table.Address())
			ref.Address.SubID = 2
			table.Constraints.Check = []catalog.CheckConstraint{{OID: 23002 + table.Address().ObjectID - p.Address().ObjectID, Name: "ck", Expression: plan.ColumnExpr{Info: expr.Info{Usage: table.Columns[1].Usage}, Origin: ref, Scope: 1, Frame: 1, Slot: 1}}}
		}
		p.Constraints.NotNull = []catalog.NotNullConstraint{{OID: 23000, Name: "nn", Attribute: 1}}
		c.Constraints.NotNull = []catalog.NotNullConstraint{{OID: 23001, Name: "nn", Attribute: 1}}
		c.Inheritance.Columns[1] = catalog.InheritedColumn{Origins: []catalog.InheritanceOrigin{{Parent: p.Address(), Attribute: 1}}}
		c.Inheritance.NotNull[1] = catalog.InheritedConstraint{Origins: []catalog.InheritanceOrigin{{Parent: p.Address(), Attribute: 1, Constraint: p.Constraints.NotNull[0].OID}}}
		c.Inheritance.Columns[2] = catalog.InheritedColumn{Origins: []catalog.InheritanceOrigin{{Parent: p.Address(), Attribute: 2}}}
		c.Inheritance.Checks["ck"] = catalog.InheritedConstraint{Origins: []catalog.InheritanceOrigin{{Parent: p.Address(), Constraint: p.Constraints.Check[0].OID}}}
	}
	install(e, temp, tempChild)
	install(source, parent, child)
	class, ok := sqltype.LookupIndexClass("int4_ops")
	if !ok {
		t.Fatal("missing actual int4 btree class")
	}
	operator, err := operatorFamilyTestExclusion(tempChild.Columns[0].Usage, class, "=")
	if err != nil {
		t.Fatal(err)
	}
	idx := inheritanceLifecycleTable(e, 24000, "temp_ex")
	idx.ObjectNamespace = temp.Namespace()
	idx.RelationKind = catalog.IndexRelation
	constraint := catalog.ExclusionConstraint{OID: parent.Address().ObjectID, Name: idx.Name(), Index: idx.Address(), Operators: []sqltype.ExclusionOperator{operator}}
	tempChild.Constraints.Exclusion = []catalog.ExclusionConstraint{constraint}
	tempChild.History.HasIndexes = true
	idx.SQLIndex = &catalog.IndexDefinition{Method: catalog.BtreeMethod, MethodOID: 403, Parent: tempChild.Address(), Keys: []catalog.IndexKey{{Attribute: 1, Class: class}}, Constraint: index.ExclusionBacking(&constraint), Exclusion: constraint.Operators, Ready: true, Valid: true, Live: true}
	inheritanceLifecycleInstall(e, idx)
	inheritanceLifecycleInstall(source, parent, child)
	if err := index.ValidateExclusions(e.CatalogRead(), tempChild, map[string]bool{}); err != nil {
		t.Fatal("invalid real exclusion fixture", err)
	}
	before := e.catalog.State
	retainedParentStorage, retainedChildStorage := e.storage.Relations[temp.Address()], e.storage.Relations[tempChild.Address()]
	originalIndex := idx.SQLIndex
	next := catalog.CloneState(source.catalog.State)
	parentStorage, childStorage := &storage.Relation{}, &storage.Relation{}
	relations := map[ids.ObjectAddress]*storage.Relation{parent.Address(): parentStorage, child.Address(): childStorage}
	e.mu.Lock()
	err = e.preserveTemporaryLocked(context.Background(), next, relations, map[ids.ObjectAddress]*storage.SequenceGenerator{}, nil, nil, replacementProcedurePolicyFixture(source, e), &catalogOIDReservation{Base: e.catalog.NextOID(), Next: e.catalog.NextOID()})
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	imported := next.MustRelation(child.Address())
	mapped := imported.Inheritance.Parents[0].Relation
	if mapped == parent.Address() || next.Get(mapped).Name() != parent.Name() || relations[mapped] != parentStorage || relations[imported.Address()] != childStorage {
		t.Fatal("imported descriptor/storage did not yield to retained exclusion", mapped)
	}
	if imported.Inheritance.Columns[1].Origins[0].Parent != mapped || imported.Inheritance.Columns[2].Origins[0].Parent != mapped || imported.Inheritance.NotNull[1].Origins[0].Parent != mapped || imported.Inheritance.Checks["ck"].Origins[0].Parent != mapped {
		t.Fatal("imported inheritance origins did not follow parent mapping")
	}
	mappedParent := next.MustRelation(mapped)
	if mappedParent.Columns[0].OriginTable != uint32(mapped.ObjectID) || mappedParent.Columns[1].OriginTable != uint32(mapped.ObjectID) || imported.Inheritance.NotNull[1].Origins[0].Constraint != mappedParent.Constraints.NotNull[0].OID || imported.Inheritance.Checks["ck"].Origins[0].Constraint != mappedParent.Constraints.Check[0].OID {
		t.Fatal("imported row/constraint origins did not follow mapping")
	}
	if e.catalog.State != before || next.Get(temp.Address()) != temp || next.Get(tempChild.Address()) != tempChild || next.Get(idx.Address()) != idx || relations[temp.Address()] != retainedParentStorage || relations[tempChild.Address()] != retainedChildStorage || idx.SQLIndex != originalIndex || !reflect.DeepEqual(tempChild.Constraints.Exclusion[0], constraint) || !index.BackingConstraintPresent(tempChild, idx) {
		t.Fatal("retained TEMP descriptors, exclusion attachment, storage or source authority changed")
	}
	if source.catalog.State.Get(parent.Address()) != parent || source.catalog.State.Get(child.Address()) != child || child.Inheritance.Parents[0].Relation != parent.Address() || child.Inheritance.Columns[1].Origins[0].Parent != parent.Address() || parent.Columns[0].OriginTable != uint32(parent.Address().ObjectID) || child.Inheritance.NotNull[1].Origins[0].Constraint != 23000 {
		t.Fatal("imported source authority mutated")
	}
	if err := catalog.ValidateDefinitionOIDs(e.catalog.Read(next)); err != nil {
		t.Fatal("combined definition identities invalid", err)
	}
	for _, tbl := range []*catalog.Relation{tempChild, imported} {
		if err := table.ValidateInheritance(e.catalog.Read(next), tbl); err != nil {
			t.Fatal(tbl.Name(), err)
		}
	}
}
