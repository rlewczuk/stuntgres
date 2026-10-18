package engine

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

type partitionConstraintUnit struct {
	e                         *Engine
	root, leaf, parent, child *relationDescriptor
	view                      CatalogRead
}

func partitionConstraintDescriptors(t *testing.T) partitionConstraintUnit {
	t.Helper()
	root, index, catalog, types := indexClausesFixture(t)
	e := New()
	state := *e.state
	state.descriptors = make(map[ids.ObjectAddress]Descriptor, len(e.state.descriptors)+4)
	state.names = make(map[ids.NameKey]ids.ObjectAddress, len(e.state.names)+4)
	for a, d := range e.state.descriptors {
		state.descriptors[a] = d
	}
	for k, a := range e.state.names {
		state.names[k] = a
	}
	root.name = "unit_root"
	root.relationKind = partitionedRelation
	keyClass, _ := sqltype.DefaultIndexClass(types[sqltype.Int4])
	comparison, err := sqltype.BindIndexOrdering(root.columns[0].Usage, keyClass)
	if err != nil {
		t.Fatal(err)
	}
	root.partition = &partitionDefinition{keys: []partitionKeyEntry{{attribute: 10, usage: root.columns[0].Usage, class: keyClass, comparison: comparison, label: "a"}}}
	index.name = "unit_parent"
	index.sqlIndex.unique = true
	index.sqlIndex.valid = false
	leaf := *root
	leaf.address = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20002}
	leaf.name = "unit_leaf"
	leaf.partition = nil
	leaf.relationKind = tableRelation
	leaf.membership = &partitionMembership{parent: root.address, defaultBound: true}
	leaf.columns = []ColumnDescriptor{root.columns[1], root.columns[0], root.columns[2]}
	leaf.attributes = []int32{11, 22, 33}
	leaf.attributeHighWater = 33
	leaf.index = map[string]int{"b": 0, "a": 1, "p": 2}
	root.partition.children = []ids.ObjectAddress{leaf.address}
	root.partition.defaultChild = leaf.address
	for a, d := range catalog.descriptors {
		state.descriptors[a] = d
	}
	state.descriptors[leaf.address] = &leaf
	view := CatalogRead{owner: e, state: &state}
	mapped, err := mapPartitionIndex(context.Background(), view, root, &leaf, index.sqlIndex)
	if err != nil {
		t.Fatal(err)
	}
	mapped.valid = true
	child := *index
	child.address = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20003}
	child.name = "unit_child"
	child.sqlIndex = mapped
	child.columns = slices.Clone(index.columns)
	child.attributes = slices.Clone(index.attributes)
	state.descriptors[child.address] = &child
	for _, d := range []*relationDescriptor{root, &leaf, index, &child} {
		state.names[descriptorNameKey(d)] = d.address
	}
	e.state = &state
	return partitionConstraintUnit{e: e, root: root, leaf: &leaf, parent: index, child: &child, view: view}
}

func TestPartitionConstraintsCoverage(t *testing.T) {
	f := partitionConstraintDescriptors(t)
	if err := partitionUniqueCoverage(f.root, f.parent.sqlIndex); err != nil {
		t.Fatal(err)
	}
	definition := *f.parent.sqlIndex
	definition.keys = []indexKey{definition.keys[1]}
	definition.include = []int32{10, 30}
	err := partitionUniqueCoverage(f.root, &definition)
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "unique constraint on partitioned table must include all partitioning columns", Detail: `UNIQUE constraint on table "unit_root" lacks column "a" which is part of the partition key.`}
	if !reflect.DeepEqual(err, want) {
		t.Fatal(err, want)
	}
	if err := partitionUniqueCoverage(f.leaf, &definition); err != nil {
		t.Fatal("independent leaf coverage", err)
	}
	definition.constraint = &backingConstraint{Primary: true}
	want.Detail = `PRIMARY KEY constraint on table "unit_root" lacks column "a" which is part of the partition key.`
	if err := partitionUniqueCoverage(f.root, &definition); !reflect.DeepEqual(err, want) {
		t.Fatal(err)
	}
}

func TestPartitionConstraintsCloneMapping(t *testing.T) {
	f := partitionConstraintDescriptors(t)
	mapped, err := mapPartitionIndex(context.Background(), f.view, f.root, f.leaf, f.parent.sqlIndex)
	if err != nil {
		t.Fatal(err)
	}
	if mapped.parent != f.leaf.address || mapped.keys[0].attribute != 22 || mapped.keys[1].attribute != 11 || !slices.Equal(mapped.include, []int32{33}) || !mapped.keys[1].desc || !mapped.keys[1].nullsFirst {
		t.Fatal(mapped)
	}
	mapped.keys[0].attribute = 99
	if f.parent.sqlIndex.keys[0].attribute != 10 {
		t.Fatal("mapped key aliases parent")
	}
}

func TestPartitionConstraintsAttachmentAdmission(t *testing.T) {
	for _, mode := range []string{"valid", "sort", "nnd", "bare_constraint", "nullable_primary", "unrelated", "second", "other_parent"} {
		t.Run(mode, func(t *testing.T) {
			f := partitionConstraintDescriptors(t)
			op, done := roleOperation(t, f.e, "postgres")
			defer done()
			code := ""
			switch mode {
			case "sort":
				f.child.sqlIndex.keys[1].desc = false
				f.child.sqlIndex.keys[1].nullsFirst = false
			case "nnd":
				f.child.sqlIndex.nullsNotDistinct = true
				code = "42P17"
			case "bare_constraint":
				f.parent.sqlIndex.constraint = &backingConstraint{OID: 30000, Name: f.parent.name, Primary: false, kind: uniqueBackingKind}
				code = "42P17"
			case "nullable_primary":
				f.parent.sqlIndex.constraint = &backingConstraint{OID: 30000, Name: f.parent.name, Primary: true, kind: uniqueBackingKind}
				f.child.sqlIndex.constraint = &backingConstraint{OID: 30001, Name: f.child.name, Primary: true, kind: uniqueBackingKind}
				f.leaf.columns[1].Nullable = true
				code = "42P16"
			case "unrelated":
				f.leaf.membership = nil
				code = "55000"
			case "second":
				other := *f.child
				definition := *f.child.sqlIndex
				other.address.ObjectID = 20004
				definition.partitionParent = f.parent.address
				other.sqlIndex = &definition
				f.view.state.descriptors[other.address] = &other
				code = "55000"
			case "other_parent":
				f.child.sqlIndex.partitionParent = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20004}
				code = "55000"
			}
			err := partitionIndexAttachCheck(op, f.view, f.parent, f.child)
			if code == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != code {
				t.Fatal(err, code)
			}
			if diagnostic.Severity != "ERROR" || diagnostic.SeverityUnlocalized != "ERROR" || diagnostic.Position != "" {
				t.Fatal(diagnostic)
			}
		})
	}
}

func TestPartitionConstraintsDescriptor(t *testing.T) {
	for _, mode := range []string{"valid", "orphan", "duplicate", "mapping", "nnd", "validity"} {
		t.Run(mode, func(t *testing.T) {
			f := partitionConstraintDescriptors(t)
			f.child.sqlIndex.partitionParent = f.parent.address
			f.parent.sqlIndex.valid = true
			switch mode {
			case "orphan":
				f.child.sqlIndex.partitionParent.ObjectID = 40000
			case "duplicate":
				other := *f.child
				other.address.ObjectID = 20004
				f.view.state.descriptors[other.address] = &other
			case "mapping":
				f.child.sqlIndex.keys[0].attribute = 11
			case "nnd":
				f.child.sqlIndex.nullsNotDistinct = true
			case "validity":
				f.child.sqlIndex.valid = false
			}
			err := validateIndexDefinition(f.view, f.child)
			if err == nil {
				err = validateIndexDefinition(f.view, f.parent)
			}
			if (err == nil) != (mode == "valid") {
				t.Fatal(mode, err)
			}
		})
	}
}

func TestPartitionConstraintsVirtualPublication(t *testing.T) {
	f := partitionConstraintDescriptors(t)
	next := *f.view.state
	next.descriptors = make(map[ids.ObjectAddress]Descriptor, len(f.view.state.descriptors))
	for a, d := range f.view.state.descriptors {
		if a != f.child.address {
			next.descriptors[a] = d
		}
	}
	roots, err := f.e.prepareIndexPublication(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1}, &catalogState{descriptors: map[ids.ObjectAddress]Descriptor{}}, &next, nil, nil, nil, nil, nil, true, sqltype.OutputEnvironment{}, nil, nil, nil)
	if err != nil || roots[f.parent.address] != nil {
		t.Fatal("virtual root required physical storage", roots, err)
	}
	if f.parent.sqlIndex.valid || f.e.state != f.view.state || f.child.sqlIndex.partitionParent != (ids.ObjectAddress{}) {
		t.Fatal("publication mutated predecessors")
	}
}

func TestPartitionConstraintsAttachPublication(t *testing.T) {
	f := partitionConstraintDescriptors(t)
	op, done := roleOperation(t, f.e, "postgres")
	defer done()
	stage := f.e.BeginCatalogStage(f.view)
	defer f.e.DiscardCatalogStage(stage)
	if err := f.e.stagePartitionIndexAttach(op, stage, f.parent, f.child); err != nil {
		t.Fatal(err)
	}
	next := f.e.stageRead(stage)
	child := next.state.descriptors[f.child.address].(*relationDescriptor)
	parent := next.state.descriptors[f.parent.address].(*relationDescriptor)
	if child.sqlIndex.partitionParent != f.parent.address || !parent.sqlIndex.valid {
		t.Fatal("candidate attachment missing")
	}
	if f.child.sqlIndex.partitionParent != (ids.ObjectAddress{}) || f.parent.sqlIndex.valid || f.e.state != f.view.state {
		t.Fatal("private attachment published early")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.e.prepareIndexPublication(&EvalContext{Context: ctx, Scope: 1, Frame: 1}, f.view.state, next.state, map[ids.ObjectAddress]*relationStorage{f.leaf.address: {}}, nil, nil, nil, nil, true, sqltype.OutputEnvironment{}, nil, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled candidate accepted", err)
	}
	if f.child.sqlIndex.partitionParent != (ids.ObjectAddress{}) || f.parent.sqlIndex.valid {
		t.Fatal("cancellation changed old edge")
	}
}

func TestPartitionConstraintsDumpIndexDeclarationOrder(t *testing.T) {
	f := partitionConstraintDescriptors(t)
	f.parent.sqlIndex.include = nil
	rootKey := uniqueConstraint{OID: 30000, Name: f.parent.name, Attributes: []int32{10, 20}, Index: f.parent.address, IsLocal: true}
	f.root.uniqueConstraints = []uniqueConstraint{rootKey}
	f.parent.sqlIndex.constraint = uniqueBacking(&rootKey)
	local := uniqueConstraint{OID: 30001, Name: f.child.name, Attributes: []int32{22, 11}, Index: f.child.address, IsLocal: true, NullsNotDistinct: true}
	f.child.sqlIndex.include = nil
	f.child.sqlIndex.nullsNotDistinct = true
	f.child.sqlIndex.constraint = uniqueBacking(&local)
	inheritedIndex, inheritedDefinition := *f.child, *f.child.sqlIndex
	inheritedIndex.address.ObjectID = 50000
	inheritedIndex.name = "unit_inherited"
	inheritedIndex.sqlIndex = &inheritedDefinition
	inheritedDefinition.nullsNotDistinct = false
	inheritedDefinition.partitionParent = f.parent.address
	inherited := uniqueConstraint{OID: 30002, Name: inheritedIndex.name, Attributes: local.Attributes, Index: inheritedIndex.address, Parent: rootKey.OID}
	inheritedDefinition.constraint = uniqueBacking(&inherited)
	f.view.state.descriptors[inheritedIndex.address] = &inheritedIndex
	// Native binding retains local declarations first. Replay creates the
	// inherited backing first regardless of that slice or destination OIDs.
	f.leaf.uniqueConstraints = []uniqueConstraint{local, inherited}
	delayedIndex, delayedDefinition := *f.child, *f.child.sqlIndex
	delayedIndex.address.ObjectID = 20006
	delayedIndex.name = "unit_delayed"
	delayedIndex.sqlIndex = &delayedDefinition
	delayedDefinition.include = []int32{33}
	delayed := uniqueConstraint{OID: 30003, Name: delayedIndex.name, Attributes: local.Attributes, Index: delayedIndex.address, IsLocal: true}
	delayedDefinition.constraint = uniqueBacking(&delayed)
	f.leaf.uniqueConstraints = append(f.leaf.uniqueConstraints, delayed)
	f.view.state.descriptors[delayedIndex.address] = &delayedIndex
	remapped, remappedDefinition := *f.child, *f.child.sqlIndex
	remapped.address.ObjectID = 60000
	remapped.name = "unit_standalone_remapped"
	remapped.sqlIndex = &remappedDefinition
	remappedDefinition.constraint = nil
	retained, retainedDefinition := remapped, remappedDefinition
	retained.address.ObjectID = 20007
	retained.name = "unit_standalone_retained"
	retained.sqlIndex = &retainedDefinition
	f.view.state.descriptors[remapped.address] = &remapped
	f.view.state.descriptors[retained.address] = &retained
	f.view.state.dumpPriorities = map[ids.ObjectAddress]ids.OID{remapped.address: 20005}
	before := slices.Clone(f.leaf.uniqueConstraints)
	order := partitionIndexDeclarationOrder(f.view, f.leaf)
	want := []*relationDescriptor{&inheritedIndex, f.child, &remapped, &delayedIndex, &retained}
	if !slices.Equal(order, want) || !reflect.DeepEqual(f.leaf.uniqueConstraints, before) {
		t.Fatal("inherited/local/prebuilt/source priority order changed", order, want)
	}
	f.view.state.dumpPriorities = nil
	order = partitionIndexDeclarationOrder(f.view, f.leaf)
	want = []*relationDescriptor{&inheritedIndex, f.child, &delayedIndex, &retained, &remapped}
	if !slices.Equal(order, want) || f.view.state.dumpPriorities != nil {
		t.Fatal("ordinary standalone ordering allocated provenance or changed OIDs", order, want)
	}
}

func TestPartitionConstraintsDumpLogicalOrder(t *testing.T) {
	// Retaining a TEMP table can remint only the automatic tree's root above
	// every manual node. Neither that root nor its descendants may move in Dump.
	for _, root := range []ids.OID{10, 90000} {
		graph := map[dumpEventKey][]dumpEventKey{{oid: root}: nil, {oid: 20}: {{oid: root}}, {oid: 30}: {{oid: 20}}, {oid: 40}: nil, {oid: 50}: {{oid: 40}}, {oid: 60}: {{oid: 50}}}
		priorities := map[ids.OID]ids.OID{root: 10}
		order, err := viewDumpOrder(context.Background(), graph, priorities)
		if err != nil {
			t.Fatal(err)
		}
		want := []ids.OID{root, 20, 30, 40, 50, 60}
		if !reflect.DeepEqual(dumpDeclarationIDs(order), want) || !reflect.DeepEqual(graph[dumpEventKey{oid: 20}], []dumpEventKey{{oid: root}}) {
			t.Fatal("source order or captured dependency changed", order, graph)
		}
	}
}

func TestPartitionConstraintsDumpPriorityLifecycle(t *testing.T) {
	f := partitionConstraintDescriptors(t)
	if f.e.state.dumpPriorities != nil || cloneCatalog(f.e.state).dumpPriorities != nil {
		t.Fatal("ordinary catalog allocated replay provenance")
	}
	source := f.root.address
	f.view.state.dumpPriorities = map[ids.ObjectAddress]ids.OID{source: 10}
	next := cloneCatalog(f.view.state)
	next.dumpPriorities[source] = 11
	if f.view.state.dumpPriorities[source] != 10 {
		t.Fatal("candidate mutated retained ordering provenance")
	}
	stage := f.e.BeginCatalogStage(f.view)
	defer f.e.DiscardCatalogStage(stage)
	if f.e.stageRead(stage).state.dumpPriorities[source] != 10 {
		t.Fatal("stage lost source priority")
	}
	changes := &catalogChanges{descriptors: map[ids.ObjectAddress]Descriptor{source: nil}}
	private := f.e.privateCatalogLocked(changes)
	if _, exists := private.state.dumpPriorities[source]; exists {
		t.Fatal("private removal retained obsolete priority")
	}
	delete(next.descriptors, source)
	if _, exists := cloneCatalog(next).dumpPriorities[source]; exists {
		t.Fatal("retired address retained replay provenance")
	}
	graph := map[dumpEventKey][]dumpEventKey{{oid: 90}: nil, {oid: 20}: nil, {oid: 30}: {{oid: 90}}}
	order, err := viewDumpOrder(context.Background(), graph, nil)
	if err != nil || !reflect.DeepEqual(dumpDeclarationIDs(order), []ids.OID{20, 90, 30}) {
		t.Fatal("ordinary source order changed", order, err)
	}
	order, err = viewDumpOrder(context.Background(), graph, map[ids.OID]ids.OID{90: 10})
	if err != nil || !reflect.DeepEqual(dumpDeclarationIDs(order), []ids.OID{90, 20, 30}) {
		t.Fatal("collision priority lost or bypassed dependencies", order, err)
	}
}

func TestPartitionConstraintsDumpCollisionProvenance(t *testing.T) {
	e := New()
	temp := inheritanceControlsTemporary(e, 20000, "retained")
	source := New()
	parent := inheritanceLifecycleTable(source, 20000, "z_auto")
	child := inheritanceLifecycleTable(source, 20001, "z_child", parent.address)
	manual := inheritanceLifecycleTable(source, 20002, "a_manual")
	inheritanceLifecycleInstall(source, parent, child, manual)
	e.nextOID = 30000
	for range 2 {
		next := cloneCatalog(source.state)
		storage := map[ids.ObjectAddress]*relationStorage{parent.address: {}, child.address: {}, manual.address: {}}
		e.mu.Lock()
		err := e.preserveTemporaryLocked(context.Background(), next, storage, map[ids.ObjectAddress]*sequenceGenerator{}, nil, nil)
		e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		imported := next.descriptors[child.address].(*relationDescriptor)
		mapped := imported.inheritance.parents[0]
		if next.descriptors[temp.address] != temp || next.descriptors[manual.address] == nil || mapped == parent.address {
			t.Fatal("retained/noncolliding identity contract changed")
		}
		if next.dumpPriorities[mapped] != parent.address.ObjectID || next.dumpPriorities[child.address] != 0 || next.dumpPriorities[manual.address] != 0 {
			t.Fatal("collision provenance missing or broadened", next.dumpPriorities)
		}
		graph := map[dumpEventKey][]dumpEventKey{{oid: mapped.ObjectID}: nil, {oid: child.address.ObjectID}: {{oid: mapped.ObjectID}}, {oid: manual.address.ObjectID}: nil}
		order, err := viewDumpOrder(context.Background(), graph, map[ids.OID]ids.OID{mapped.ObjectID: next.dumpPriorities[mapped]})
		if err != nil || !reflect.DeepEqual(dumpDeclarationIDs(order), []ids.OID{mapped.ObjectID, child.address.ObjectID, manual.address.ObjectID}) {
			t.Fatal("repeated replacement lost original declaration order", order, err)
		}
		if source.state.dumpPriorities != nil || source.state.descriptors[parent.address] != parent {
			t.Fatal("replacement mutated source provenance")
		}
		e.state = next
		e.storage = storage
	}
}

func partitionConstraintBackings(f partitionConstraintUnit) {
	root := uniqueConstraint{OID: f.child.address.ObjectID, Name: f.parent.name, Attributes: []int32{10, 20}, Index: f.parent.address, IsLocal: true}
	child := uniqueConstraint{OID: f.parent.address.ObjectID, Name: f.child.name, Attributes: []int32{22, 11}, Index: f.child.address, Parent: root.OID}
	f.root.uniqueConstraints = []uniqueConstraint{root}
	f.leaf.uniqueConstraints = []uniqueConstraint{child}
	f.parent.sqlIndex.constraint = uniqueBacking(&root)
	f.child.sqlIndex.constraint = uniqueBacking(&child)
	f.child.sqlIndex.partitionParent = f.parent.address
	f.parent.sqlIndex.valid = true
}

func TestPartitionConstraintsDropClosure(t *testing.T) {
	f := partitionConstraintDescriptors(t)
	partitionConstraintBackings(f)
	local := uniqueConstraint{OID: 30004, Name: "independent_leaf", Index: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 30005}, IsLocal: true}
	f.leaf.uniqueConstraints = append(f.leaf.uniqueConstraints, local)
	independent := *f.child
	definition := *f.child.sqlIndex
	definition.partitionParent = ids.ObjectAddress{}
	definition.constraint = uniqueBacking(&local)
	independent.address = local.Index
	independent.name = local.Name
	independent.sqlIndex = &definition
	f.view.state.descriptors[independent.address] = &independent
	stage := f.e.BeginCatalogStage(f.view)
	defer f.e.DiscardCatalogStage(stage)
	if err := f.e.stagePartitionConstraintDrop(stage, f.root.uniqueConstraints[0].OID); err != nil {
		t.Fatal(err)
	}
	if err := f.e.stagePartitionIndexDrop(stage, f.parent); err != nil {
		t.Fatal(err)
	}
	candidate := f.e.stageRead(stage)
	if candidate.state.descriptors[f.child.address] != nil || candidate.state.descriptors[f.parent.address] != nil || candidate.state.descriptors[independent.address] != &independent {
		t.Fatal("drop closure changed independent identity")
	}
	leaf := candidate.state.descriptors[f.leaf.address].(*relationDescriptor)
	if !reflect.DeepEqual(leaf.uniqueConstraints, []uniqueConstraint{local}) || len(f.leaf.uniqueConstraints) != 2 || f.e.state != f.view.state {
		t.Fatal("drop closure published early or removed independent key")
	}
}

func TestPartitionConstraintsDefinitionRemapping(t *testing.T) {
	f := partitionConstraintDescriptors(t)
	partitionConstraintBackings(f)
	f.root.notNullConstraints = []notNullConstraint{{OID: f.root.address.ObjectID, Name: "nn", Attribute: 10}}
	f.leaf.notNullConstraints = []notNullConstraint{{OID: 30008, Name: "nn", Attribute: 22, partitionLocal: true}}
	next := *f.view.state
	next.descriptors = make(map[ids.ObjectAddress]Descriptor, len(f.view.state.descriptors))
	for a, d := range f.view.state.descriptors {
		next.descriptors[a] = d
	}
	oldRoot, oldChild := f.root.uniqueConstraints[0].OID, f.leaf.uniqueConstraints[0].OID
	f.e.mu.Lock()
	err := f.e.remapDefinitionOIDsLocked(&next)
	f.e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	root := next.descriptors[f.root.address].(*relationDescriptor)
	leaf := next.descriptors[f.leaf.address].(*relationDescriptor)
	parent := next.descriptors[f.parent.address].(*relationDescriptor)
	child := next.descriptors[f.child.address].(*relationDescriptor)
	if root.uniqueConstraints[0].OID == oldRoot || leaf.uniqueConstraints[0].OID == oldChild || leaf.uniqueConstraints[0].Parent != root.uniqueConstraints[0].OID {
		t.Fatal("constraint identity collision/ancestry not remapped")
	}
	if parent.sqlIndex.constraint.OID != root.uniqueConstraints[0].OID || child.sqlIndex.constraint.OID != leaf.uniqueConstraints[0].OID || child.sqlIndex.partitionParent != parent.address {
		t.Fatal("backing/edge remapping disagrees")
	}
	if !root.uniqueConstraints[0].IsLocal || leaf.uniqueConstraints[0].IsLocal || f.root.uniqueConstraints[0].OID != oldRoot || f.leaf.uniqueConstraints[0].OID != oldChild {
		t.Fatal("remapping changed provenance or live predecessor")
	}
	if root.notNullConstraints[0].OID == f.root.address.ObjectID || root.notNullConstraints[0].partitionLocal || !leaf.notNullConstraints[0].partitionLocal || !f.leaf.notNullConstraints[0].partitionLocal {
		t.Fatal("NN remapping lost explicit merged provenance")
	}
}

func TestPartitionConstraintsDumpCollisionDefinitionReservation(t *testing.T) {
	f := partitionConstraintDescriptors(t)
	rootKey := uniqueConstraint{OID: 30000, Name: f.parent.name, Attributes: []int32{10, 20}, Index: f.parent.address, IsLocal: true}
	childKey := uniqueConstraint{OID: 30001, Name: f.child.name, Attributes: []int32{22, 11}, Index: f.child.address, Parent: rootKey.OID}
	f.root.uniqueConstraints = []uniqueConstraint{rootKey}
	f.leaf.uniqueConstraints = []uniqueConstraint{childKey}
	f.parent.sqlIndex.constraint = uniqueBacking(&rootKey)
	f.child.sqlIndex.constraint = uniqueBacking(&childKey)
	f.child.sqlIndex.partitionParent = f.parent.address
	destination := New()
	retained := inheritanceControlsTemporary(destination, f.parent.address.ObjectID, "retained_backing_collision")
	// The next descriptor replacement candidate is an imported child constraint,
	// not a descriptor address. It must be reserved in the same identity space.
	destination.nextOID = childKey.OID
	old := destination.state
	next := cloneCatalog(f.view.state)
	storage := map[ids.ObjectAddress]*relationStorage{f.leaf.address: {}}
	destination.mu.Lock()
	err := destination.preserveTemporaryLocked(context.Background(), next, storage, map[ids.ObjectAddress]*sequenceGenerator{}, nil, nil)
	destination.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	root := next.descriptors[f.root.address].(*relationDescriptor)
	leaf := next.descriptors[f.leaf.address].(*relationDescriptor)
	parent := next.descriptors[root.uniqueConstraints[0].Index].(*relationDescriptor)
	child := next.descriptors[f.child.address].(*relationDescriptor)
	if parent.address == f.parent.address || parent.address.ObjectID == rootKey.OID || parent.address.ObjectID == childKey.OID {
		t.Fatal("replacement backing stole a noncolliding owned definition identity", parent.address)
	}
	if root.uniqueConstraints[0].OID != rootKey.OID || leaf.uniqueConstraints[0].OID != childKey.OID || leaf.uniqueConstraints[0].Parent != rootKey.OID {
		t.Fatal("noncolliding constraint identities or immediate ancestry changed", root.uniqueConstraints, leaf.uniqueConstraints)
	}
	if parent.sqlIndex.constraint.OID != rootKey.OID || child.sqlIndex.constraint.OID != childKey.OID || child.sqlIndex.partitionParent != parent.address {
		t.Fatal("reminted backing and constraint identities disagree")
	}
	if next.descriptors[retained.address] != retained || destination.state != old || f.view.state.descriptors[f.parent.address] != f.parent || !reflect.DeepEqual(f.root.uniqueConstraints[0], rootKey) {
		t.Fatal("candidate allocation changed retained TEMP or immutable source")
	}
}

func TestPartitionConstraintsFutureCloneValidity(t *testing.T) {
	f := partitionConstraintDescriptors(t)
	delete(f.view.state.descriptors, f.child.address)
	delete(f.view.state.names, descriptorNameKey(f.child))
	op, done := roleOperation(t, f.e, "postgres")
	defer done()
	stage := f.e.BeginCatalogStage(f.view)
	defer f.e.DiscardCatalogStage(stage)
	f.e.mu.Lock()
	clone, err := f.e.stagePartitionIndexChild(op, stage, f.root, f.parent, f.leaf, true)
	f.e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if !clone.sqlIndex.valid || clone.sqlIndex.partitionParent != f.parent.address || f.parent.sqlIndex.valid {
		t.Fatal("future physical clone inherited virtual invalidity")
	}
	if f.e.state.descriptors[clone.address] != nil {
		t.Fatal("clone published before catalog overlay")
	}
}

func TestPartitionConstraintsVirtualIndexHistory(t *testing.T) {
	f := partitionConstraintDescriptors(t)
	stage := f.e.BeginCatalogStage(f.view)
	defer f.e.DiscardCatalogStage(stage)
	next := *f.parent
	definition := *f.parent.sqlIndex
	next.sqlIndex = &definition
	next.revision++
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	f.e.mu.Lock()
	err := f.e.markStageIndexHistoryLocked(stage)
	f.e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	candidate := f.e.stageRead(stage)
	root := candidate.state.descriptors[f.root.address].(*relationDescriptor)
	if !root.hasIndexes || root == f.root || f.root.hasIndexes || candidate.state.descriptors[f.parent.address].(*relationDescriptor).sqlIndex.valid {
		t.Fatal("virtual history changed validity or live predecessor")
	}
	replacement := *f.view.state
	replacement.descriptors = make(map[ids.ObjectAddress]Descriptor, len(f.view.state.descriptors))
	for a, d := range f.view.state.descriptors {
		replacement.descriptors[a] = d
	}
	if err := normalizeReplacementIndexHistory(context.Background(), CatalogRead{owner: f.e, state: &replacement}); err != nil {
		t.Fatal(err)
	}
	if !replacement.descriptors[f.root.address].(*relationDescriptor).hasIndexes || !replacement.descriptors[f.leaf.address].(*relationDescriptor).hasIndexes || f.root.hasIndexes || f.leaf.hasIndexes {
		t.Fatal("replacement history mutates retained snapshots")
	}
}

func TestPartitionConstraintsAdmittedCancellation(t *testing.T) {
	for _, query := range []string{"CREATE UNIQUE INDEX new_parent ON root(k)", "ALTER INDEX parent_idx ATTACH PARTITION child_idx", "DROP INDEX parent_idx", "ALTER TABLE keyed DROP CONSTRAINT keyed_pkey"} {
		t.Run(query, func(t *testing.T) {
			e := New()
			holder, utility := sessionStart(t, e, nil), sessionStart(t, e, nil)
			for _, sql := range []string{"CREATE TABLE root(k INT) PARTITION BY RANGE(k)", "CREATE TABLE leaf PARTITION OF root DEFAULT", "CREATE UNIQUE INDEX parent_idx ON ONLY root(k)", "CREATE UNIQUE INDEX child_idx ON leaf(k)", "INSERT INTO root VALUES(1)", "CREATE TABLE keyed(k INT PRIMARY KEY) PARTITION BY RANGE(k)", "CREATE TABLE keyed_leaf PARTITION OF keyed DEFAULT", "INSERT INTO keyed VALUES(1)"} {
				sessionSQL(t, holder, sql)
			}
			if query == "DROP INDEX parent_idx" {
				sessionSQL(t, holder, "ALTER INDEX parent_idx ATTACH PARTITION child_idx")
			}
			resource := "leaf"
			if query == "ALTER TABLE keyed DROP CONSTRAINT keyed_pkey" {
				resource = "keyed_leaf"
			}
			tx := transactionBegin(t, holder)
			if err := e.locks.Acquire(context.Background(), holder.transactions.ticket, lockResource{ObjectRef: roleTableRef(t, e, resource)}, accessExclusive); err != nil {
				t.Fatal(err)
			}
			before := indexMaintenanceCapture(e)
			cancel, await := indexMaintenanceAsync(t, e, utility, query)
			lockPending(t, e, 1)
			cancel()
			if err := await(); !errors.Is(err, context.Canceled) {
				t.Fatal("admitted cancellation", err)
			}
			indexMaintenanceUnchanged(t, e, before)
			transactionEnd(t, holder, tx, false)
			lockEmpty(t, e)
			sessionSQL(t, utility, query)
			lockEmpty(t, e)
		})
	}
}

func TestPartitionConstraintsTransactionOverlay(t *testing.T) {
	for _, operation := range []string{"attach", "index_drop", "constraint_drop"} {
		t.Run(operation, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			for _, query := range []string{"CREATE TABLE root(k INT) PARTITION BY RANGE(k)", "CREATE TABLE leaf PARTITION OF root DEFAULT", "CREATE UNIQUE INDEX parent_idx ON ONLY root(k)", "CREATE UNIQUE INDEX child_idx ON leaf(k)", "CREATE TABLE keyed(k INT PRIMARY KEY) PARTITION BY RANGE(k)", "CREATE TABLE keyed_leaf PARTITION OF keyed DEFAULT", "INSERT INTO keyed VALUES(1)"} {
				sessionSQL(t, s, query)
			}
			query := "ALTER INDEX parent_idx ATTACH PARTITION child_idx"
			child := roleTableRef(t, e, "child_idx")
			parent := roleTableRef(t, e, "parent_idx")
			if operation == "index_drop" {
				sessionSQL(t, s, query)
				query = "DROP INDEX parent_idx"
			}
			if operation == "constraint_drop" {
				query = "ALTER TABLE keyed DROP CONSTRAINT keyed_pkey"
				child = roleTableRef(t, e, "keyed_leaf_pkey")
				parent = roleTableRef(t, e, "keyed_pkey")
			}
			oldChild := e.state.descriptors[child.Address]
			oldRoot := e.indexRoots[child.Address]
			tx := transactionBegin(t, s)
			sessionSQL(t, s, "SAVEPOINT kept")
			prior := s.transactions.catalog
			before := indexMaintenanceCapture(e)
			sessionSQL(t, s, query)
			changes := s.transactions.catalog
			if changes == nil || changes == prior {
				t.Fatal("missing distinct utility overlay")
			}
			e.mu.Lock()
			private := e.privateCatalogLocked(changes)
			e.mu.Unlock()
			if operation == "attach" {
				staged := private.state.descriptors[child.Address].(*relationDescriptor)
				if staged == oldChild || staged.sqlIndex.partitionParent != parent.Address || !private.state.descriptors[parent.Address].(*relationDescriptor).sqlIndex.valid {
					t.Fatal("private attachment absent")
				}
			} else if private.state.descriptors[child.Address] != nil || private.state.descriptors[parent.Address] != nil {
				t.Fatal("private recursive removal absent")
			}
			indexMaintenanceUnchanged(t, e, before)
			copied := cloneCatalogChanges(changes)
			e.mu.Lock()
			copyView := e.privateCatalogLocked(copied)
			e.mu.Unlock()
			if copyView.state.descriptors[child.Address] != private.state.descriptors[child.Address] {
				t.Fatal("overlay copy changed immutable edge authority")
			}
			sessionSQL(t, s, "ROLLBACK TO kept")
			if s.transactions.catalog != prior || e.state.descriptors[child.Address] != oldChild || e.indexRoots[child.Address] != oldRoot {
				t.Fatal("savepoint rebuilt or published prior utility roots")
			}
			sessionSQL(t, s, query)
			transactionEnd(t, s, tx, true)
			if operation == "attach" {
				if e.state.descriptors[child.Address].(*relationDescriptor).sqlIndex.partitionParent != parent.Address || e.indexRoots[child.Address] != oldRoot {
					t.Fatal("commit missed edge or rebuilt unchanged physical key")
				}
			} else if e.state.descriptors[child.Address] != nil || e.state.descriptors[parent.Address] != nil || e.indexRoots[child.Address] != nil {
				t.Fatal("commit missed recursive backing removal")
			}
			lockEmpty(t, e)
		})
	}
}

func TestPartitionConstraintsConstraintAncestryValidation(t *testing.T) {
	for _, mode := range []string{"valid", "orphan_constraint", "missing_backing", "wrong_parent", "local_clone", "bare_parent", "ancestor_cycle"} {
		t.Run(mode, func(t *testing.T) {
			f := partitionConstraintDescriptors(t)
			partitionConstraintBackings(f)
			switch mode {
			case "orphan_constraint":
				f.child.sqlIndex.partitionParent = ids.ObjectAddress{}
			case "missing_backing":
				f.child.sqlIndex.constraint = nil
			case "wrong_parent":
				f.leaf.uniqueConstraints[0].Parent++
			case "local_clone":
				f.leaf.uniqueConstraints[0].IsLocal = true
			case "bare_parent":
				f.parent.sqlIndex.constraint = nil
			case "ancestor_cycle":
				other := *f.parent
				definition := *f.parent.sqlIndex
				other.address.ObjectID = 40000
				definition.partitionParent = f.parent.address
				other.sqlIndex = &definition
				f.parent.sqlIndex.partitionParent = other.address
				f.view.state.descriptors[other.address] = &other
			}
			err := validateIndexDefinition(f.view, f.child)
			if (err == nil) != (mode == "valid") {
				t.Fatal(mode, err)
			}
		})
	}
}

func TestPartitionConstraintsSyntaxOnlyDescribe(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	before := indexMaintenanceCapture(e)
	for _, query := range []string{
		"CREATE TABLE missing(k INT UNIQUE) PARTITION BY RANGE((k+1))",
		"CREATE UNIQUE INDEX missing_idx ON ONLY missing(k)",
		"ALTER INDEX missing_idx ATTACH PARTITION absent_idx",
		"DROP INDEX missing_idx",
		"ALTER TABLE missing DROP CONSTRAINT absent_key",
	} {
		description, err := e.Describe(op, statement(t, query))
		if err != nil || description.Parameters.Count != 0 || len(description.Parameters.Types) != 0 || len(description.Results) != 0 {
			t.Fatal("utility Describe resolved catalog or published metadata", query, description, err)
		}
		indexMaintenanceUnchanged(t, e, before)
		lockEmpty(t, e)
	}
}

func TestPartitionConstraintsBareIndexNonreuse(t *testing.T) {
	f := partitionConstraintDescriptors(t)
	root := uniqueConstraint{OID: 30000, Name: f.parent.name, Attributes: []int32{10, 20}, Index: f.parent.address, IsLocal: true}
	f.root.uniqueConstraints = []uniqueConstraint{root}
	f.parent.sqlIndex.constraint = uniqueBacking(&root)
	op, done := roleOperation(t, f.e, "postgres")
	defer done()
	stage := f.e.BeginCatalogStage(f.view)
	defer f.e.DiscardCatalogStage(stage)
	f.e.mu.Lock()
	clone, err := f.e.stagePartitionIndexChild(op, stage, f.root, f.parent, f.leaf, true)
	f.e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	candidate := f.e.stageRead(stage)
	leaf := candidate.state.descriptors[f.leaf.address].(*relationDescriptor)
	if clone.address == f.child.address || clone.sqlIndex.constraint == nil || clone.sqlIndex.partitionParent != f.parent.address || len(leaf.uniqueConstraints) != 1 {
		t.Fatal("bare child reused as a constraint backing")
	}
	constraint := leaf.uniqueConstraints[0]
	if constraint.Parent != root.OID || constraint.IsLocal || constraint.Index != clone.address || !slices.Equal(constraint.Attributes, []int32{22, 11}) {
		t.Fatal("new constraint lost mapped immediate ancestry")
	}
	if candidate.state.descriptors[f.child.address] != f.child || f.child.sqlIndex.partitionParent != (ids.ObjectAddress{}) || f.child.sqlIndex.constraint != nil || len(f.leaf.uniqueConstraints) != 0 {
		t.Fatal("independent bare child or predecessor mutated")
	}
}

func partitionConstraintAttachStage(t *testing.T, e *Engine, op *Operation) *CatalogStage {
	t.Helper()
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	t.Cleanup(func() { e.DiscardCatalogStage(stage) })
	parent := base.state.descriptors[roleTableRef(t, e, "parent_idx").Address].(*relationDescriptor)
	child := base.state.descriptors[roleTableRef(t, e, "child_idx").Address].(*relationDescriptor)
	stage.indexParents = map[ids.ObjectAddress]*relationDescriptor{parent.sqlIndex.parent: base.state.descriptors[parent.sqlIndex.parent].(*relationDescriptor), child.sqlIndex.parent: base.state.descriptors[child.sqlIndex.parent].(*relationDescriptor)}
	stage.domainBases = []Descriptor{parent, child}
	op.session.mu.Lock()
	stage.authSession, stage.authRevision = op.session, op.session.authorization.revision
	op.session.mu.Unlock()
	e.mu.Lock()
	stage.roleRevision = e.roles.revision
	err := e.stagePartitionIndexAttach(op, stage, parent, child)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return stage
}

func partitionConstraintPublicationTree(t *testing.T) *Engine {
	t.Helper()
	e := New()
	for _, query := range []string{"CREATE TABLE root(k INT) PARTITION BY RANGE(k)", "CREATE TABLE leaf PARTITION OF root DEFAULT", "CREATE UNIQUE INDEX parent_idx ON ONLY root(k)", "CREATE UNIQUE INDEX child_idx ON leaf(k)", "INSERT INTO root VALUES(1)", "CREATE ROLE actor"} {
		run(t, e, query)
	}
	return e
}

func TestPartitionConstraintsPublicationFences(t *testing.T) {
	for _, change := range []string{"parent_revision", "child_recreate", "role_registry", "session_identity", "epoch"} {
		t.Run(change, func(t *testing.T) {
			e := partitionConstraintPublicationTree(t)
			op, done := roleOperation(t, e, "postgres")
			defer done()
			stage := partitionConstraintAttachStage(t, e, op)
			prepared, err := e.ValidateCatalogStage(context.Background(), stage)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "parent_revision":
				run(t, e, "ALTER INDEX parent_idx RENAME TO renamed")
			case "child_recreate":
				run(t, e, "DROP INDEX child_idx")
				run(t, e, "CREATE UNIQUE INDEX child_idx ON leaf(k)")
			case "role_registry":
				run(t, e, "CREATE ROLE unrelated")
			case "session_identity":
				roleExecute(t, e, op, "SET ROLE actor")
			case "epoch":
				if err := e.Reset(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			before := indexMaintenanceCapture(e)
			_, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.base.Epoch()})
			switch change {
			case "epoch":
				if !errors.Is(err, ErrCatalogReplaced) {
					t.Fatal("replacement fence", err)
				}
			case "role_registry", "session_identity":
				if err == nil || err.Error() != "stuntgres: authorization changed before publication" {
					t.Fatal("authority fence", err)
				}
			default:
				if _, ok := err.(CatalogConflict); !ok {
					t.Fatal("exact descriptor/incarnation fence", err)
				}
			}
			indexMaintenanceUnchanged(t, e, before)
		})
	}
}

func TestPartitionConstraintsPreservesNewestPhysicalRoot(t *testing.T) {
	e := partitionConstraintPublicationTree(t)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	stage := partitionConstraintAttachStage(t, e, op)
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	child := roleTableRef(t, e, "child_idx")
	original := e.indexRoots[child.Address]
	run(t, e, "INSERT INTO root VALUES(2)")
	latest := e.indexRoots[child.Address]
	if latest == original {
		t.Fatal("fixture did not publish a newer key root")
	}
	if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: stage.base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	if e.indexRoots[child.Address] != latest || e.indexRoots[roleTableRef(t, e, "parent_idx").Address] != nil {
		t.Fatal("metadata attachment rebuilt/lost latest physical root or installed a virtual root")
	}
	btreeWantKeys(t, e, "child_idx", "1", "2")
}

func TestPartitionConstraintsFinalPublicationCancellation(t *testing.T) {
	first := partitionConstraintPublicationTree(t)
	op, done := roleOperation(t, first, "postgres")
	defer done()
	measured, cancel := context.WithCancel(context.Background())
	defer cancel()
	counting := &cancelAtErrCheck{Context: measured, cancel: cancel}
	stage := partitionConstraintAttachStage(t, first, op)
	prepared, err := first.ValidateCatalogStage(counting, stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.PublishCatalog(prepared, PublicationToken{Epoch: stage.base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	polls := counting.checks.Load()
	if polls < 4 {
		t.Fatal("missing genuine preparation/publication boundaries", polls)
	}
	second := partitionConstraintPublicationTree(t)
	other, release := roleOperation(t, second, "postgres")
	defer release()
	canceled, veto := context.WithCancel(context.Background())
	defer veto()
	final := &cancelAtErrCheck{Context: canceled, cancel: veto, at: polls}
	candidate := partitionConstraintAttachStage(t, second, other)
	prepared, err = second.ValidateCatalogStage(final, candidate)
	if err != nil {
		t.Fatal("canceled before final publication boundary", err)
	}
	before := indexMaintenanceCapture(second)
	_, err = second.PublishCatalog(prepared, PublicationToken{Epoch: candidate.base.Epoch()})
	if !errors.Is(err, context.Canceled) || final.checks.Load() != polls {
		t.Fatal("final cancellation not fenced", polls, final.checks.Load(), err)
	}
	indexMaintenanceUnchanged(t, second, before)
}

func TestPartitionConstraintsRecursiveBuildFailure(t *testing.T) {
	e := New()
	for _, query := range []string{"CREATE TABLE root(k INT) PARTITION BY RANGE(k)", "CREATE TABLE a PARTITION OF root FOR VALUES FROM(1) TO(3)", "CREATE TABLE b PARTITION OF root DEFAULT", "INSERT INTO root VALUES(1),(3),(3)"} {
		run(t, e, query)
	}
	before := indexMaintenanceCapture(e)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	result, err := e.Execute(op, statement(t, "CREATE UNIQUE INDEX parent_idx ON root(k)"), 0, nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23505" || result.Rows != nil {
		t.Fatal("late recursive build did not fail atomically", result, err)
	}
	indexMaintenanceUnchanged(t, e, before)
	if len(e.indexRoots) != 0 {
		t.Fatal("failed recursive build retained cold physical installation roots")
	}
	for _, d := range e.state.descriptors {
		if relation, ok := d.(*relationDescriptor); ok && (relation.name == "parent_idx" || relation.name == "a_k_idx" || relation.name == "b_k_idx") {
			t.Fatal("failed recursive build leaked definition", relation.name)
		}
	}
	run(t, e, "DROP TABLE b")
	run(t, e, "CREATE TABLE b PARTITION OF root DEFAULT")
	run(t, e, "INSERT INTO b VALUES(3)")
	run(t, e, "CREATE UNIQUE INDEX parent_idx ON root(k)")
	btreeWantKeys(t, e, "a_k_idx", "1")
	btreeWantKeys(t, e, "b_k_idx", "3")
	if e.indexRoots[roleTableRef(t, e, "parent_idx").Address] != nil {
		t.Fatal("healthy recursive retry installed virtual storage")
	}
}

func TestPartitionConstraintsIncompleteReplacementAdmission(t *testing.T) {
	e := New()
	for _, query := range []string{"CREATE TABLE root(k INT) PARTITION BY RANGE(k)", "CREATE TABLE mid PARTITION OF root DEFAULT PARTITION BY RANGE(k)", "CREATE TABLE leaf PARTITION OF mid DEFAULT", "CREATE UNIQUE INDEX parent_idx ON ONLY root(k)", "CREATE UNIQUE INDEX mid_idx ON ONLY mid(k)", "CREATE UNIQUE INDEX leaf_idx ON leaf(k)", "ALTER INDEX parent_idx ATTACH PARTITION mid_idx", "INSERT INTO root VALUES(1)"} {
		run(t, e, query)
	}
	copiedStorage := func() map[ids.ObjectAddress]*relationStorage {
		out := make(map[ids.ObjectAddress]*relationStorage, len(e.storage))
		for address, store := range e.storage {
			copy := *store
			copy.versions = slices.Clone(store.versions)
			out[address] = &copy
		}
		return out
	}
	before := indexMaintenanceCapture(e)
	snapshot, err := captureIndexSnapshot(context.Background(), e.CatalogRead(), e.storage, e.indexRoots, e.commitSeq)
	if err != nil {
		t.Fatal(err)
	}
	roots, _, _, err := e.prepareRemintedStorage(context.Background(), e.state, copiedStorage(), snapshot)
	if err != nil {
		t.Fatal("representable nested incomplete topology rejected", err)
	}
	if roots[roleTableRef(t, e, "parent_idx").Address] != nil || roots[roleTableRef(t, e, "mid_idx").Address] != nil || roots[roleTableRef(t, e, "leaf_idx").Address] == nil {
		t.Fatal("replacement physical/virtual ownership mismatch")
	}
	indexMaintenanceUnchanged(t, e, before)
	for _, flag := range []string{"physical_invalid", "virtual_not_ready", "virtual_not_live"} {
		t.Run(flag, func(t *testing.T) {
			next := *e.state
			next.descriptors = make(map[ids.ObjectAddress]Descriptor, len(e.state.descriptors))
			for address, d := range e.state.descriptors {
				next.descriptors[address] = d
			}
			name := "mid_idx"
			if flag == "physical_invalid" {
				name = "leaf_idx"
			}
			address := roleTableRef(t, e, name).Address
			copy := *next.descriptors[address].(*relationDescriptor)
			definition := *copy.sqlIndex
			copy.sqlIndex = &definition
			switch flag {
			case "physical_invalid":
				definition.valid = false
			case "virtual_not_ready":
				definition.ready = false
			case "virtual_not_live":
				definition.live = false
			}
			next.descriptors[address] = &copy
			if _, _, _, err := e.prepareRemintedStorage(context.Background(), &next, copiedStorage(), snapshot); err == nil {
				t.Fatal("unrepresentable publication state imported", flag)
			}
			indexMaintenanceUnchanged(t, e, before)
		})
	}
}

func TestPartitionConstraintsStructuralEquivalence(t *testing.T) {
	for _, mode := range []string{"mapped_expression", "different_expression", "method", "membership_before_equivalence"} {
		t.Run(mode, func(t *testing.T) {
			e := New()
			expression := "lower(payload)"
			if mode == "different_expression" || mode == "membership_before_equivalence" {
				expression = "upper(payload)"
			}
			// Bind supported ordinary expression indexes, then construct private
			// immutable candidate keys. UNIQUE expression SQL remains unsupported.
			for _, query := range []string{"CREATE TABLE root(k INT,payload TEXT)", "CREATE TABLE leaf(k INT,payload TEXT)", "CREATE INDEX parent_idx ON root(k,(lower(payload))) WHERE payload IS NOT NULL", "CREATE INDEX child_idx ON leaf(k,(" + expression + ")) WHERE payload IS NOT NULL"} {
				run(t, e, query)
			}
			base := e.CatalogRead()
			next := *base.state
			next.descriptors = make(map[ids.ObjectAddress]Descriptor, len(base.state.descriptors))
			for address, d := range base.state.descriptors {
				next.descriptors[address] = d
			}
			root := *next.descriptors[roleTableRef(t, e, "root").Address].(*relationDescriptor)
			leaf := *next.descriptors[roleTableRef(t, e, "leaf").Address].(*relationDescriptor)
			typ, _ := base.LookupType(sqltype.Int4)
			class, _ := sqltype.DefaultIndexClass(typ)
			comparison, err := sqltype.BindIndexOrdering(root.columns[0].Usage, class)
			if err != nil {
				t.Fatal(err)
			}
			root.relationKind = partitionedRelation
			root.partition = &partitionDefinition{keys: []partitionKeyEntry{{attribute: root.attributes[0], usage: root.columns[0].Usage, class: class, comparison: comparison, label: "k"}}, children: []ids.ObjectAddress{leaf.address}, defaultChild: leaf.address}
			leaf.membership = &partitionMembership{parent: root.address, defaultBound: true}
			next.descriptors[root.address], next.descriptors[leaf.address] = &root, &leaf
			parent := *next.descriptors[roleTableRef(t, e, "parent_idx").Address].(*relationDescriptor)
			child := *next.descriptors[roleTableRef(t, e, "child_idx").Address].(*relationDescriptor)
			parentDefinition, childDefinition := *parent.sqlIndex, *child.sqlIndex
			parentDefinition.unique, childDefinition.unique = true, true
			parent.sqlIndex, child.sqlIndex = &parentDefinition, &childDefinition
			if mode == "method" {
				childDefinition.method = hashMethod
			}
			if mode == "membership_before_equivalence" {
				leaf.membership = nil
			}
			next.descriptors[parent.address], next.descriptors[child.address] = &parent, &child
			view := CatalogRead{owner: e, state: &next}
			before := indexMaintenanceCapture(e)
			op, done := roleOperation(t, e, "postgres")
			defer done()
			err = partitionIndexAttachCheck(op, view, &parent, &child)
			indexMaintenanceUnchanged(t, e, before)
			if mode == "mapped_expression" {
				if err != nil {
					t.Fatal("equivalent cooked expressions did not remap", err)
				}
				return
			}
			code := "42P17"
			if mode == "membership_before_equivalence" {
				code = "55000"
			}
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != code {
				t.Fatal(mode, err)
			}
		})
	}
}

func TestPartitionConstraintsOwnedProviderReplay(t *testing.T) {
	e := New()
	// Roles are live engine authority, not definitions imported by Restore.
	run(t, e, "CREATE USER actor")
	before := indexMaintenanceCapture(e)
	err := e.Restore(context.Background(), strings.NewReader("CREATE USER forbidden_import_actor"))
	var boundary *sqlstate.Error
	if !errors.As(err, &boundary) || boundary.Code != "0A000" {
		t.Fatal("role-import boundary widened", err)
	}
	if _, exists := e.Roles().Lookup("forbidden_import_actor"); exists {
		t.Fatal("failed replacement imported live role authority")
	}
	indexMaintenanceUnchanged(t, e, before)
	script := "CREATE SCHEMA owned AUTHORIZATION actor; SET SESSION AUTHORIZATION 'actor'; CREATE TYPE owned.userkey AS ENUM('a','b'); CREATE TABLE owned.root(k owned.userkey,payload INT,provider REGTYPE DEFAULT 'owned.userkey'::regtype,CONSTRAINT root_key UNIQUE(k,payload)) PARTITION BY RANGE(k); CREATE TABLE owned.mid PARTITION OF owned.root DEFAULT PARTITION BY RANGE(k); CREATE TABLE owned.leaf PARTITION OF owned.mid DEFAULT; INSERT INTO owned.root(k,payload) VALUES('a',1);"
	if err := e.Restore(context.Background(), strings.NewReader(script)); err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err := e.Dump(context.Background(), &dump); err != nil {
		t.Fatal(err)
	}
	if err := e.Restore(context.Background(), bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var redump bytes.Buffer
	if err := e.Dump(context.Background(), &redump); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatal("owned provider replay not deterministic")
	}
	root := e.state.descriptors[roleTableRef(t, e, "owned.root").Address].(*relationDescriptor)
	mid := e.state.descriptors[roleTableRef(t, e, "owned.mid").Address].(*relationDescriptor)
	leaf := e.state.descriptors[roleTableRef(t, e, "owned.leaf").Address].(*relationDescriptor)
	actor, exists := e.Roles().Lookup("actor")
	if !exists || root.owner != actor.Ref {
		t.Fatal("replay lost declared actor ownership")
	}
	if root.owner != mid.owner || root.owner != leaf.owner || len(root.uniqueConstraints) != 1 || len(mid.uniqueConstraints) != 1 || len(leaf.uniqueConstraints) != 1 || mid.uniqueConstraints[0].Parent != root.uniqueConstraints[0].OID || leaf.uniqueConstraints[0].Parent != mid.uniqueConstraints[0].OID {
		t.Fatal("owner/key ancestry not restored")
	}
	for _, table := range []*relationDescriptor{root, mid, leaf} {
		index := e.state.descriptors[table.uniqueConstraints[0].Index].(*relationDescriptor)
		if index.owner != table.owner {
			t.Fatal("index owner detached from actual table owner")
		}
	}
	op, done := roleOperation(t, e, "postgres")
	defer done()
	checkDefault := func() {
		t.Helper()
		rows := roleExecute(t, e, op, "SELECT provider='owned.userkey'::regtype AS matching FROM owned.root")
		values := tuples(rows)
		if rows.Rows != nil {
			rows.Rows.Close()
		}
		if len(values) != 1 || len(values[0]) != 1 || values[0][0] != true {
			t.Fatal("restored owned regtype default lost actual provider identity", values)
		}
	}
	checkDefault()
	result, err := e.Execute(op, statement(t, "INSERT INTO owned.mid(k,payload) VALUES('a',1)"), 0, nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23505" || result.Rows != nil {
		t.Fatal("reminted provider lost physical equality", err)
	}
	roleExecute(t, e, op, "INSERT INTO owned.leaf(k,payload) VALUES('b',1)")
	roleExecute(t, e, op, "DROP TABLE owned.leaf")
	roleExecute(t, e, op, "CREATE TABLE owned.future PARTITION OF owned.mid DEFAULT")
	future := e.state.descriptors[roleTableRef(t, e, "owned.future").Address].(*relationDescriptor)
	if len(future.uniqueConstraints) != 1 || future.uniqueConstraints[0].Parent != mid.uniqueConstraints[0].OID {
		t.Fatal("future clone lost reminted immediate provider")
	}
	roleExecute(t, e, op, "INSERT INTO owned.root(k,payload) VALUES('a',1)")
	checkDefault()
	result, err = e.Execute(op, statement(t, "INSERT INTO owned.future(k,payload) VALUES('a',1)"), 0, nil)
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23505" || result.Rows != nil {
		t.Fatal("future clone lost reminted provider equality", err)
	}
}

func partitionConstraintDropPublicationStage(t *testing.T, key bool) (*Engine, *CatalogStage) {
	t.Helper()
	e := New()
	names := []string{"parent_idx", "child_idx"}
	if key {
		for _, query := range []string{"CREATE TABLE root(k INT,payload INT,CONSTRAINT root_key PRIMARY KEY(k)) PARTITION BY RANGE(k)", "CREATE TABLE leaf PARTITION OF root(CONSTRAINT local_key UNIQUE(payload)) DEFAULT", "INSERT INTO root VALUES(1,7)"} {
			run(t, e, query)
		}
		names = []string{"root_key", "leaf_pkey"}
	} else {
		e = partitionConstraintPublicationTree(t)
		run(t, e, "ALTER INDEX parent_idx ATTACH PARTITION child_idx")
	}
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	t.Cleanup(func() { e.DiscardCatalogStage(stage) })
	stage.indexParents = make(map[ids.ObjectAddress]*relationDescriptor)
	for _, name := range names {
		index := base.state.descriptors[roleTableRef(t, e, name).Address].(*relationDescriptor)
		stage.domainBases = append(stage.domainBases, index)
		stage.indexParents[index.sqlIndex.parent] = base.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
	}
	op.session.mu.Lock()
	stage.authSession, stage.authRevision = op.session, op.session.authorization.revision
	op.session.mu.Unlock()
	e.mu.Lock()
	stage.roleRevision = e.roles.revision
	e.mu.Unlock()
	if key {
		root := base.state.descriptors[roleTableRef(t, e, "root_key").Address].(*relationDescriptor)
		if err := e.stagePartitionConstraintDrop(stage, root.sqlIndex.constraint.OID); err != nil {
			t.Fatal(err)
		}
		if err := e.stagePartitionIndexDrop(stage, root); err != nil {
			t.Fatal(err)
		}
	} else {
		for _, name := range names {
			index := base.state.descriptors[roleTableRef(t, e, name).Address].(*relationDescriptor)
			if err := e.stagePartitionIndexDrop(stage, index); err != nil {
				t.Fatal(err)
			}
		}
	}
	return e, stage
}

func TestPartitionConstraintsDropFinalPublicationCancellation(t *testing.T) {
	for _, key := range []bool{false, true} {
		name := "index"
		if key {
			name = "key"
		}
		t.Run(name, func(t *testing.T) {
			first, stage := partitionConstraintDropPublicationStage(t, key)
			measured, cancel := context.WithCancel(context.Background())
			defer cancel()
			counting := &cancelAtErrCheck{Context: measured, cancel: cancel}
			prepared, err := first.ValidateCatalogStage(counting, stage)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = first.PublishCatalog(prepared, PublicationToken{Epoch: stage.base.Epoch()}); err != nil {
				t.Fatal(err)
			}
			polls := counting.checks.Load()
			if polls == 0 {
				t.Fatal("drop has no cancellation observation")
			}
			second, candidate := partitionConstraintDropPublicationStage(t, key)
			canceled, veto := context.WithCancel(context.Background())
			defer veto()
			final := &cancelAtErrCheck{Context: canceled, cancel: veto, at: polls}
			prepared, err = second.ValidateCatalogStage(final, candidate)
			if err != nil {
				t.Fatal("drop canceled before final boundary", err)
			}
			before := indexMaintenanceCapture(second)
			_, err = second.PublishCatalog(prepared, PublicationToken{Epoch: candidate.base.Epoch()})
			if !errors.Is(err, context.Canceled) {
				t.Fatal("late drop cancellation not fenced", err)
			}
			indexMaintenanceUnchanged(t, second, before)
			// Test consumer-visible state, not an incidental number of polls.
			op, done := roleOperation(t, second, "postgres")
			defer done()
			insert := "INSERT INTO root VALUES(1)"
			if key {
				insert = "INSERT INTO root VALUES(1,8)"
			}
			result, err := second.Execute(op, statement(t, insert), 0, nil)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "23505" || result.Rows != nil {
				t.Fatal("canceled drop stopped enforcing old keys", err)
			}
			rows := roleExecute(t, second, op, "SELECT k FROM root")
			values := tuples(rows)
			if rows.Rows != nil {
				rows.Rows.Close()
			}
			if len(values) != 1 || len(values[0]) != 1 || values[0][0] != int64(1) {
				t.Fatal("canceled drop changed actual row bag", values)
			}
			drop := "DROP INDEX parent_idx"
			if key {
				drop = "ALTER TABLE root DROP CONSTRAINT root_key"
			}
			roleExecute(t, second, op, drop)
			roleExecute(t, second, op, insert)
			rows = roleExecute(t, second, op, "SELECT k FROM root ORDER BY k")
			values = tuples(rows)
			if rows.Rows != nil {
				rows.Rows.Close()
			}
			if len(values) != 2 || values[0][0] != int64(1) || values[1][0] != int64(1) {
				t.Fatal("healthy drop retry did not remove selected key", values)
			}
			if key {
				result, err = second.Execute(op, statement(t, "INSERT INTO root VALUES(2,7)"), 0, nil)
				if !errors.As(err, &diagnostic) || diagnostic.Code != "23505" || result.Rows != nil {
					t.Fatal("healthy key drop removed independent local key", err)
				}
			}
		})
	}
}
