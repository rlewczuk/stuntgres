package engine

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

type partitionConstraintUnit struct {
	e                         *Engine
	root, leaf, parent, child *catalog.Relation
	view                      catalog.Read
}

func partitionConstraintDescriptors(t *testing.T) partitionConstraintUnit {
	t.Helper()
	root, idx, catalogSnapshot, types := indexClausesFixture(t)
	e := New()
	state := *e.catalog.state
	state.ResetMaps(e.catalog.state.DescriptorCount() + 4)
	for a, d := range e.catalog.state.Descriptors() {
		state.SetDescriptor(a, d)
	}
	for k, a := range e.catalog.state.Names() {
		state.BindName(k, a)
	}
	root.ObjectName = "unit_root"
	root.RelationKind = catalog.PartitionedRelation
	keyClass, _ := sqltype.DefaultIndexClass(types[sqltype.Int4])
	comparison, err := sqltype.BindIndexOrdering(root.Columns[0].Usage, keyClass)
	if err != nil {
		t.Fatal(err)
	}
	root.Partition = &catalog.PartitionDefinition{Keys: []catalog.PartitionKeyEntry{{Attribute: 10, Usage: root.Columns[0].Usage, Class: keyClass, Comparison: comparison, Label: "a"}}}
	idx.ObjectName = "unit_parent"
	idx.SQLIndex.Unique = true
	idx.SQLIndex.Valid = false
	leaf := *root
	leaf.ObjectAddress = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20002}
	leaf.ObjectName = "unit_leaf"
	leaf.Partition = nil
	leaf.RelationKind = catalog.TableRelation
	leaf.Membership = &catalog.PartitionMembership{Parent: root.Address(), DefaultBound: true}
	leaf.Columns = []catalog.Column{root.Columns[1], root.Columns[0], root.Columns[2]}
	leaf.Attributes = []int32{11, 22, 33}
	leaf.ColumnMeta.HighWater = 33
	leaf.Index = map[string]int{"b": 0, "a": 1, "p": 2}
	root.Partition.Children = []ids.ObjectAddress{leaf.Address()}
	root.Partition.DefaultChild = leaf.Address()
	for a, d := range catalogSnapshot.Descriptors() {
		state.SetDescriptor(a, d)
	}
	state.SetDescriptor(leaf.Address(), &leaf)
	view := e.catalog.read(&state)
	mapped, err := index.MapPartitionIndex(context.Background(), view, root, &leaf, idx.SQLIndex)
	if err != nil {
		t.Fatal(err)
	}
	mapped.Valid = true
	child := *idx
	child.ObjectAddress = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20003}
	child.ObjectName = "unit_child"
	child.SQLIndex = mapped
	child.Columns = slices.Clone(idx.Columns)
	child.Attributes = slices.Clone(idx.Attributes)
	state.SetDescriptor(child.Address(), &child)
	for _, d := range []*catalog.Relation{root, &leaf, idx, &child} {
		state.BindName(catalog.DescriptorNameKey(d), d.Address())
	}
	e.catalog.state = &state
	return partitionConstraintUnit{e: e, root: root, leaf: &leaf, parent: idx, child: &child, view: view}
}

func TestPartitionConstraintsCoverage(t *testing.T) {
	f := partitionConstraintDescriptors(t)
	if err := index.PartitionUniqueCoverage(f.root, f.parent.SQLIndex); err != nil {
		t.Fatal(err)
	}
	definition := *f.parent.SQLIndex
	definition.Keys = []catalog.IndexKey{definition.Keys[1]}
	definition.Include = []int32{10, 30}
	err := index.PartitionUniqueCoverage(f.root, &definition)
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "unique constraint on partitioned table must include all partitioning columns", Detail: `UNIQUE constraint on table "unit_root" lacks column "a" which is part of the partition key.`}
	if !reflect.DeepEqual(err, want) {
		t.Fatal(err, want)
	}
	if err := index.PartitionUniqueCoverage(f.leaf, &definition); err != nil {
		t.Fatal("independent leaf coverage", err)
	}
	definition.Constraint = &catalog.BackingConstraint{Primary: true}
	want.Detail = `PRIMARY KEY constraint on table "unit_root" lacks column "a" which is part of the partition key.`
	if err := index.PartitionUniqueCoverage(f.root, &definition); !reflect.DeepEqual(err, want) {
		t.Fatal(err)
	}
}

func TestPartitionConstraintsCloneMapping(t *testing.T) {
	f := partitionConstraintDescriptors(t)
	mapped, err := index.MapPartitionIndex(context.Background(), f.view, f.root, f.leaf, f.parent.SQLIndex)
	if err != nil {
		t.Fatal(err)
	}
	if mapped.Parent != f.leaf.Address() || mapped.Keys[0].Attribute != 22 || mapped.Keys[1].Attribute != 11 || !slices.Equal(mapped.Include, []int32{33}) || !mapped.Keys[1].Desc || !mapped.Keys[1].NullsFirst {
		t.Fatal(mapped)
	}
	mapped.Keys[0].Attribute = 99
	if f.parent.SQLIndex.Keys[0].Attribute != 10 {
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
				f.child.SQLIndex.Keys[1].Desc = false
				f.child.SQLIndex.Keys[1].NullsFirst = false
			case "nnd":
				f.child.SQLIndex.NullsNotDistinct = true
				code = "42P17"
			case "bare_constraint":
				f.parent.SQLIndex.Constraint = &catalog.BackingConstraint{OID: 30000, Name: f.parent.Name(), Primary: false, Kind: catalog.UniqueBackingKind}
				code = "42P17"
			case "nullable_primary":
				f.parent.SQLIndex.Constraint = &catalog.BackingConstraint{OID: 30000, Name: f.parent.Name(), Primary: true, Kind: catalog.UniqueBackingKind}
				f.child.SQLIndex.Constraint = &catalog.BackingConstraint{OID: 30001, Name: f.child.Name(), Primary: true, Kind: catalog.UniqueBackingKind}
				f.leaf.Columns[1].Nullable = true
				code = "42P16"
			case "unrelated":
				f.leaf.Membership = nil
				code = "55000"
			case "second":
				other := *f.child
				definition := *f.child.SQLIndex
				other.ObjectAddress.ObjectID = 20004
				definition.PartitionParent = f.parent.Address()
				other.SQLIndex = &definition
				f.view.State().SetDescriptor(other.Address(), &other)
				code = "55000"
			case "other_parent":
				f.child.SQLIndex.PartitionParent = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20004}
				code = "55000"
			}
			err := index.PartitionIndexAttachCheck(op, f.view, f.parent, f.child)
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
			f.child.SQLIndex.PartitionParent = f.parent.Address()
			f.parent.SQLIndex.Valid = true
			switch mode {
			case "orphan":
				f.child.SQLIndex.PartitionParent.ObjectID = 40000
			case "duplicate":
				other := *f.child
				other.ObjectAddress.ObjectID = 20004
				f.view.State().SetDescriptor(other.Address(), &other)
			case "mapping":
				f.child.SQLIndex.Keys[0].Attribute = 11
			case "nnd":
				f.child.SQLIndex.NullsNotDistinct = true
			case "validity":
				f.child.SQLIndex.Valid = false
			}
			err := index.ValidateIndexDefinition(f.view, f.child)
			if err == nil {
				err = index.ValidateIndexDefinition(f.view, f.parent)
			}
			if (err == nil) != (mode == "valid") {
				t.Fatal(mode, err)
			}
		})
	}
}

func TestPartitionConstraintsVirtualPublication(t *testing.T) {
	f := partitionConstraintDescriptors(t)
	next := *f.view.State()
	next.ResetDescriptors(f.view.DescriptorCount())
	for a, d := range f.view.Descriptors() {
		if a != f.child.Address() {
			next.SetDescriptor(a, d)
		}
	}
	roots, err := f.e.prepareIndexRoots(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, catalog.NewState(0), &next, nil, nil, nil, nil, nil, true, sqltype.OutputEnvironment{}, nil, nil, nil)
	if err != nil || roots[f.parent.Address()] != nil {
		t.Fatal("virtual root required physical storage", roots, err)
	}
	if f.parent.SQLIndex.Valid || f.e.catalog.state != f.view.State() || f.child.SQLIndex.PartitionParent != (ids.ObjectAddress{}) {
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
	child := next.MustRelation(f.child.Address())
	parent := next.MustRelation(f.parent.Address())
	if child.SQLIndex.PartitionParent != f.parent.Address() || !parent.SQLIndex.Valid {
		t.Fatal("candidate attachment missing")
	}
	if f.child.SQLIndex.PartitionParent != (ids.ObjectAddress{}) || f.parent.SQLIndex.Valid || f.e.catalog.state != f.view.State() {
		t.Fatal("private attachment published early")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.e.prepareIndexRoots(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, f.view.State(), next.State(), map[ids.ObjectAddress]*storage.Relation{f.leaf.Address(): {}}, nil, nil, nil, nil, true, sqltype.OutputEnvironment{}, nil, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled candidate accepted", err)
	}
	if f.child.SQLIndex.PartitionParent != (ids.ObjectAddress{}) || f.parent.SQLIndex.Valid {
		t.Fatal("cancellation changed old edge")
	}
}

func TestPartitionConstraintsDumpIndexDeclarationOrder(t *testing.T) {
	f := partitionConstraintDescriptors(t)
	f.parent.SQLIndex.Include = nil
	rootKey := catalog.UniqueConstraint{OID: 30000, Name: f.parent.Name(), Attributes: []int32{10, 20}, Index: f.parent.Address(), IsLocal: true}
	f.root.Constraints.Unique = []catalog.UniqueConstraint{rootKey}
	f.parent.SQLIndex.Constraint = index.UniqueBacking(&rootKey)
	local := catalog.UniqueConstraint{OID: 30001, Name: f.child.Name(), Attributes: []int32{22, 11}, Index: f.child.Address(), IsLocal: true, NullsNotDistinct: true}
	f.child.SQLIndex.Include = nil
	f.child.SQLIndex.NullsNotDistinct = true
	f.child.SQLIndex.Constraint = index.UniqueBacking(&local)
	inheritedIndex, inheritedDefinition := *f.child, *f.child.SQLIndex
	inheritedIndex.ObjectAddress.ObjectID = 50000
	inheritedIndex.ObjectName = "unit_inherited"
	inheritedIndex.SQLIndex = &inheritedDefinition
	inheritedDefinition.NullsNotDistinct = false
	inheritedDefinition.PartitionParent = f.parent.Address()
	inherited := catalog.UniqueConstraint{OID: 30002, Name: inheritedIndex.Name(), Attributes: local.Attributes, Index: inheritedIndex.Address(), Parent: rootKey.OID}
	inheritedDefinition.Constraint = index.UniqueBacking(&inherited)
	f.view.State().SetDescriptor(inheritedIndex.Address(), &inheritedIndex)
	// Native binding retains local declarations first. Replay creates the
	// inherited backing first regardless of that slice or destination OIDs.
	f.leaf.Constraints.Unique = []catalog.UniqueConstraint{local, inherited}
	delayedIndex, delayedDefinition := *f.child, *f.child.SQLIndex
	delayedIndex.ObjectAddress.ObjectID = 20006
	delayedIndex.ObjectName = "unit_delayed"
	delayedIndex.SQLIndex = &delayedDefinition
	delayedDefinition.Include = []int32{33}
	delayed := catalog.UniqueConstraint{OID: 30003, Name: delayedIndex.Name(), Attributes: local.Attributes, Index: delayedIndex.Address(), IsLocal: true}
	delayedDefinition.Constraint = index.UniqueBacking(&delayed)
	f.leaf.Constraints.Unique = append(f.leaf.Constraints.Unique, delayed)
	f.view.State().SetDescriptor(delayedIndex.Address(), &delayedIndex)
	remapped, remappedDefinition := *f.child, *f.child.SQLIndex
	remapped.ObjectAddress.ObjectID = 60000
	remapped.ObjectName = "unit_standalone_remapped"
	remapped.SQLIndex = &remappedDefinition
	remappedDefinition.Constraint = nil
	retained, retainedDefinition := remapped, remappedDefinition
	retained.ObjectAddress.ObjectID = 20007
	retained.ObjectName = "unit_standalone_retained"
	retained.SQLIndex = &retainedDefinition
	f.view.State().SetDescriptor(remapped.Address(), &remapped)
	f.view.State().SetDescriptor(retained.Address(), &retained)
	f.view.State().SetDumpPriorities(map[ids.ObjectAddress]ids.OID{remapped.Address(): 20005})
	before := slices.Clone(f.leaf.Constraints.Unique)
	order := index.PartitionIndexDeclarationOrder(f.view, f.leaf)
	want := []*catalog.Relation{&inheritedIndex, f.child, &remapped, &delayedIndex, &retained}
	if !slices.Equal(order, want) || !reflect.DeepEqual(f.leaf.Constraints.Unique, before) {
		t.Fatal("inherited/local/prebuilt/source priority order changed", order, want)
	}
	f.view.State().SetDumpPriorities(nil)
	order = index.PartitionIndexDeclarationOrder(f.view, f.leaf)
	want = []*catalog.Relation{&inheritedIndex, f.child, &delayedIndex, &retained, &remapped}
	if !slices.Equal(order, want) || f.view.State().HasDumpPriorities() {
		t.Fatal("ordinary standalone ordering allocated provenance or changed OIDs", order, want)
	}
}

func TestPartitionConstraintsDumpLogicalOrder(t *testing.T) {
	// Retaining a TEMP table can remint only the automatic tree's root above
	// every manual node. Neither that root nor its descendants may move in Dump.
	for _, root := range []ids.OID{10, 90000} {
		graph := dump.Graph{{OID: root}: nil, {OID: 20}: {{OID: root}}, {OID: 30}: {{OID: 20}}, {OID: 40}: nil, {OID: 50}: {{OID: 40}}, {OID: 60}: {{OID: 50}}}
		priorities := map[ids.OID]ids.OID{root: 10}
		order, err := dump.Order(context.Background(), graph, priorities)
		if err != nil {
			t.Fatal(err)
		}
		want := []ids.OID{root, 20, 30, 40, 50, 60}
		if !reflect.DeepEqual(dump.DeclarationIDs(order), want) || !reflect.DeepEqual(graph[dump.Event{OID: 20}], []dump.Event{{OID: root}}) {
			t.Fatal("source order or captured dependency changed", order, graph)
		}
	}
}

func TestPartitionConstraintsDumpPriorityLifecycle(t *testing.T) {
	f := partitionConstraintDescriptors(t)
	if f.e.catalog.state.HasDumpPriorities() || catalog.CloneState(f.e.catalog.state).HasDumpPriorities() {
		t.Fatal("ordinary catalog allocated replay provenance")
	}
	source := f.root.Address()
	f.view.State().SetDumpPriorities(map[ids.ObjectAddress]ids.OID{source: 10})
	next := catalog.CloneState(f.view.State())
	next.SetDumpPriority(source, 11)
	if f.view.State().DumpPriority(source) != 10 {
		t.Fatal("candidate mutated retained ordering provenance")
	}
	stage := f.e.BeginCatalogStage(f.view)
	defer f.e.DiscardCatalogStage(stage)
	if f.e.stageRead(stage).State().DumpPriority(source) != 10 {
		t.Fatal("stage lost source priority")
	}
	changes := &session.Journal{Descriptors: map[ids.ObjectAddress]catalog.Descriptor{source: nil}}
	private := f.e.privateCatalogLocked(changes)
	if _, exists := private.State().LookupDumpPriority(source); exists {
		t.Fatal("private removal retained obsolete priority")
	}
	next.RemoveDescriptor(source)
	if _, exists := catalog.CloneState(next).LookupDumpPriority(source); exists {
		t.Fatal("retired address retained replay provenance")
	}
	graph := dump.Graph{{OID: 90}: nil, {OID: 20}: nil, {OID: 30}: {{OID: 90}}}
	order, err := dump.Order(context.Background(), graph, nil)
	if err != nil || !reflect.DeepEqual(dump.DeclarationIDs(order), []ids.OID{20, 90, 30}) {
		t.Fatal("ordinary source order changed", order, err)
	}
	order, err = dump.Order(context.Background(), graph, map[ids.OID]ids.OID{90: 10})
	if err != nil || !reflect.DeepEqual(dump.DeclarationIDs(order), []ids.OID{90, 20, 30}) {
		t.Fatal("collision priority lost or bypassed dependencies", order, err)
	}
}

func TestPartitionConstraintsDumpCollisionProvenance(t *testing.T) {
	e := New()
	temp := inheritanceControlsTemporary(e, 20000, "retained")
	source := New()
	parent := inheritanceLifecycleTable(source, 20000, "z_auto")
	child := inheritanceLifecycleTable(source, 20001, "z_child", parent.Address())
	manual := inheritanceLifecycleTable(source, 20002, "a_manual")
	inheritanceLifecycleInstall(source, parent, child, manual)
	e.catalog.nextOID = 30000
	for range 2 {
		next := catalog.CloneState(source.catalog.state)
		relations := map[ids.ObjectAddress]*storage.Relation{parent.Address(): {}, child.Address(): {}, manual.Address(): {}}
		e.mu.Lock()
		err := e.preserveTemporaryLocked(context.Background(), next, relations, map[ids.ObjectAddress]*storage.SequenceGenerator{}, nil, nil)
		e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		imported := next.MustRelation(child.Address())
		mapped := imported.Inheritance.Parents[0]
		if next.Get(temp.Address()) != temp || next.Get(manual.Address()) == nil || mapped == parent.Address() {
			t.Fatal("retained/noncolliding identity contract changed")
		}
		if next.DumpPriority(mapped) != parent.Address().ObjectID || next.DumpPriority(child.Address()) != 0 || next.DumpPriority(manual.Address()) != 0 {
			t.Fatal("collision provenance missing or broadened", maps.Collect(next.DumpPriorities()))
		}
		graph := dump.Graph{{OID: mapped.ObjectID}: nil, {OID: child.Address().ObjectID}: {{OID: mapped.ObjectID}}, {OID: manual.Address().ObjectID}: nil}
		order, err := dump.Order(context.Background(), graph, map[ids.OID]ids.OID{mapped.ObjectID: next.DumpPriority(mapped)})
		if err != nil || !reflect.DeepEqual(dump.DeclarationIDs(order), []ids.OID{mapped.ObjectID, child.Address().ObjectID, manual.Address().ObjectID}) {
			t.Fatal("repeated replacement lost original declaration order", order, err)
		}
		if source.catalog.state.HasDumpPriorities() || source.catalog.state.Get(parent.Address()) != parent {
			t.Fatal("replacement mutated source provenance")
		}
		e.catalog.state = next
		e.mu.Lock()
		e.storage.ReplaceRelationsLocked(relations)
		e.mu.Unlock()
	}
}

func partitionConstraintBackings(f partitionConstraintUnit) {
	root := catalog.UniqueConstraint{OID: f.child.Address().ObjectID, Name: f.parent.Name(), Attributes: []int32{10, 20}, Index: f.parent.Address(), IsLocal: true}
	child := catalog.UniqueConstraint{OID: f.parent.Address().ObjectID, Name: f.child.Name(), Attributes: []int32{22, 11}, Index: f.child.Address(), Parent: root.OID}
	f.root.Constraints.Unique = []catalog.UniqueConstraint{root}
	f.leaf.Constraints.Unique = []catalog.UniqueConstraint{child}
	f.parent.SQLIndex.Constraint = index.UniqueBacking(&root)
	f.child.SQLIndex.Constraint = index.UniqueBacking(&child)
	f.child.SQLIndex.PartitionParent = f.parent.Address()
	f.parent.SQLIndex.Valid = true
}

func TestPartitionConstraintsDropClosure(t *testing.T) {
	f := partitionConstraintDescriptors(t)
	partitionConstraintBackings(f)
	local := catalog.UniqueConstraint{OID: 30004, Name: "independent_leaf", Index: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 30005}, IsLocal: true}
	f.leaf.Constraints.Unique = append(f.leaf.Constraints.Unique, local)
	independent := *f.child
	definition := *f.child.SQLIndex
	definition.PartitionParent = ids.ObjectAddress{}
	definition.Constraint = index.UniqueBacking(&local)
	independent.ObjectAddress = local.Index
	independent.ObjectName = local.Name
	independent.SQLIndex = &definition
	f.view.State().SetDescriptor(independent.Address(), &independent)
	stage := f.e.BeginCatalogStage(f.view)
	defer f.e.DiscardCatalogStage(stage)
	if err := f.e.stagePartitionConstraintDrop(stage, f.root.Constraints.Unique[0].OID); err != nil {
		t.Fatal(err)
	}
	if err := f.e.stagePartitionIndexDrop(stage, f.parent); err != nil {
		t.Fatal(err)
	}
	candidate := f.e.stageRead(stage)
	if candidate.Get(f.child.Address()) != nil || candidate.Get(f.parent.Address()) != nil || candidate.Get(independent.Address()) != &independent {
		t.Fatal("drop closure changed independent identity")
	}
	leaf := candidate.MustRelation(f.leaf.Address())
	if !reflect.DeepEqual(leaf.Constraints.Unique, []catalog.UniqueConstraint{local}) || len(f.leaf.Constraints.Unique) != 2 || f.e.catalog.state != f.view.State() {
		t.Fatal("drop closure published early or removed independent key")
	}
}

func TestPartitionConstraintsDefinitionRemapping(t *testing.T) {
	f := partitionConstraintDescriptors(t)
	partitionConstraintBackings(f)
	f.root.Constraints.NotNull = []catalog.NotNullConstraint{{OID: f.root.Address().ObjectID, Name: "nn", Attribute: 10}}
	f.leaf.Constraints.NotNull = []catalog.NotNullConstraint{{OID: 30008, Name: "nn", Attribute: 22, PartitionLocal: true}}
	next := *f.view.State()
	next.ResetDescriptors(f.view.DescriptorCount())
	for a, d := range f.view.Descriptors() {
		next.SetDescriptor(a, d)
	}
	oldRoot, oldChild := f.root.Constraints.Unique[0].OID, f.leaf.Constraints.Unique[0].OID
	f.e.mu.Lock()
	err := f.e.remapDefinitionOIDsLocked(&next)
	f.e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	root := next.MustRelation(f.root.Address())
	leaf := next.MustRelation(f.leaf.Address())
	parent := next.MustRelation(f.parent.Address())
	child := next.MustRelation(f.child.Address())
	if root.Constraints.Unique[0].OID == oldRoot || leaf.Constraints.Unique[0].OID == oldChild || leaf.Constraints.Unique[0].Parent != root.Constraints.Unique[0].OID {
		t.Fatal("constraint identity collision/ancestry not remapped")
	}
	if parent.SQLIndex.Constraint.OID != root.Constraints.Unique[0].OID || child.SQLIndex.Constraint.OID != leaf.Constraints.Unique[0].OID || child.SQLIndex.PartitionParent != parent.Address() {
		t.Fatal("backing/edge remapping disagrees")
	}
	if !root.Constraints.Unique[0].IsLocal || leaf.Constraints.Unique[0].IsLocal || f.root.Constraints.Unique[0].OID != oldRoot || f.leaf.Constraints.Unique[0].OID != oldChild {
		t.Fatal("remapping changed provenance or live predecessor")
	}
	if root.Constraints.NotNull[0].OID == f.root.Address().ObjectID || root.Constraints.NotNull[0].PartitionLocal || !leaf.Constraints.NotNull[0].PartitionLocal || !f.leaf.Constraints.NotNull[0].PartitionLocal {
		t.Fatal("NN remapping lost explicit merged provenance")
	}
}

func TestPartitionConstraintsDumpCollisionDefinitionReservation(t *testing.T) {
	f := partitionConstraintDescriptors(t)
	rootKey := catalog.UniqueConstraint{OID: 30000, Name: f.parent.Name(), Attributes: []int32{10, 20}, Index: f.parent.Address(), IsLocal: true}
	childKey := catalog.UniqueConstraint{OID: 30001, Name: f.child.Name(), Attributes: []int32{22, 11}, Index: f.child.Address(), Parent: rootKey.OID}
	f.root.Constraints.Unique = []catalog.UniqueConstraint{rootKey}
	f.leaf.Constraints.Unique = []catalog.UniqueConstraint{childKey}
	f.parent.SQLIndex.Constraint = index.UniqueBacking(&rootKey)
	f.child.SQLIndex.Constraint = index.UniqueBacking(&childKey)
	f.child.SQLIndex.PartitionParent = f.parent.Address()
	destination := New()
	retained := inheritanceControlsTemporary(destination, f.parent.Address().ObjectID, "retained_backing_collision")
	// The next descriptor replacement candidate is an imported child constraint,
	// not a descriptor address. It must be reserved in the same identity space.
	destination.catalog.nextOID = childKey.OID
	old := destination.catalog.state
	next := catalog.CloneState(f.view.State())
	relations := map[ids.ObjectAddress]*storage.Relation{f.leaf.Address(): {}}
	destination.mu.Lock()
	err := destination.preserveTemporaryLocked(context.Background(), next, relations, map[ids.ObjectAddress]*storage.SequenceGenerator{}, nil, nil)
	destination.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	root := next.MustRelation(f.root.Address())
	leaf := next.MustRelation(f.leaf.Address())
	parent := next.MustRelation(root.Constraints.Unique[0].Index)
	child := next.MustRelation(f.child.Address())
	if parent.Address() == f.parent.Address() || parent.Address().ObjectID == rootKey.OID || parent.Address().ObjectID == childKey.OID {
		t.Fatal("replacement backing stole a noncolliding owned definition identity", parent.Address())
	}
	if root.Constraints.Unique[0].OID != rootKey.OID || leaf.Constraints.Unique[0].OID != childKey.OID || leaf.Constraints.Unique[0].Parent != rootKey.OID {
		t.Fatal("noncolliding constraint identities or immediate ancestry changed", root.Constraints.Unique, leaf.Constraints.Unique)
	}
	if parent.SQLIndex.Constraint.OID != rootKey.OID || child.SQLIndex.Constraint.OID != childKey.OID || child.SQLIndex.PartitionParent != parent.Address() {
		t.Fatal("reminted backing and constraint identities disagree")
	}
	if next.Get(retained.Address()) != retained || destination.catalog.state != old || f.view.Get(f.parent.Address()) != f.parent || !reflect.DeepEqual(f.root.Constraints.Unique[0], rootKey) {
		t.Fatal("candidate allocation changed retained TEMP or immutable source")
	}
}

func TestPartitionConstraintsFutureCloneValidity(t *testing.T) {
	f := partitionConstraintDescriptors(t)
	f.view.State().RemoveDescriptor(f.child.Address())
	f.view.State().UnbindName(catalog.DescriptorNameKey(f.child))
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
	if !clone.SQLIndex.Valid || clone.SQLIndex.PartitionParent != f.parent.Address() || f.parent.SQLIndex.Valid {
		t.Fatal("future physical clone inherited virtual invalidity")
	}
	if f.e.catalog.state.Get(clone.Address()) != nil {
		t.Fatal("clone published before catalog overlay")
	}
}

func TestPartitionConstraintsVirtualIndexHistory(t *testing.T) {
	f := partitionConstraintDescriptors(t)
	stage := f.e.BeginCatalogStage(f.view)
	defer f.e.DiscardCatalogStage(stage)
	next := *f.parent
	definition := *f.parent.SQLIndex
	next.SQLIndex = &definition
	next.ObjectRevision++
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
	root := candidate.MustRelation(f.root.Address())
	if !root.History.HasIndexes || root == f.root || f.root.History.HasIndexes || candidate.MustRelation(f.parent.Address()).SQLIndex.Valid {
		t.Fatal("virtual history changed validity or live predecessor")
	}
	replacement := *f.view.State()
	replacement.ResetDescriptors(f.view.DescriptorCount())
	for a, d := range f.view.Descriptors() {
		replacement.SetDescriptor(a, d)
	}
	if err := normalizeReplacementIndexHistory(context.Background(), f.e.catalog.read(&replacement)); err != nil {
		t.Fatal(err)
	}
	if !replacement.MustRelation(f.root.Address()).History.HasIndexes || !replacement.MustRelation(f.leaf.Address()).History.HasIndexes || f.root.History.HasIndexes || f.leaf.History.HasIndexes {
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
			if err := e.locks.Acquire(context.Background(), holder.Transactions.Ticket, lock.Resource{ObjectRef: roleTableRef(t, e, resource)}, lock.AccessExclusive); err != nil {
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
			oldChild := e.catalog.state.Get(child.Address)
			oldRoot := e.storage.IndexRoots[child.Address]
			tx := transactionBegin(t, s)
			sessionSQL(t, s, "SAVEPOINT kept")
			prior := s.Transactions.Catalog
			before := indexMaintenanceCapture(e)
			sessionSQL(t, s, query)
			changes := s.Transactions.Catalog
			if changes == nil || changes == prior {
				t.Fatal("missing distinct utility overlay")
			}
			e.mu.Lock()
			private := e.privateCatalogLocked(changes)
			e.mu.Unlock()
			if operation == "attach" {
				staged := private.MustRelation(child.Address)
				if staged == oldChild || staged.SQLIndex.PartitionParent != parent.Address || !private.MustRelation(parent.Address).SQLIndex.Valid {
					t.Fatal("private attachment absent")
				}
			} else if private.Get(child.Address) != nil || private.Get(parent.Address) != nil {
				t.Fatal("private recursive removal absent")
			}
			indexMaintenanceUnchanged(t, e, before)
			copied := changes.Clone()
			e.mu.Lock()
			copyView := e.privateCatalogLocked(copied)
			e.mu.Unlock()
			if copyView.Get(child.Address) != private.Get(child.Address) {
				t.Fatal("overlay copy changed immutable edge authority")
			}
			sessionSQL(t, s, "ROLLBACK TO kept")
			if s.Transactions.Catalog != prior || e.catalog.state.Get(child.Address) != oldChild || e.storage.IndexRoots[child.Address] != oldRoot {
				t.Fatal("savepoint rebuilt or published prior utility roots")
			}
			sessionSQL(t, s, query)
			transactionEnd(t, s, tx, true)
			if operation == "attach" {
				if e.catalog.state.MustRelation(child.Address).SQLIndex.PartitionParent != parent.Address || e.storage.IndexRoots[child.Address] != oldRoot {
					t.Fatal("commit missed edge or rebuilt unchanged physical key")
				}
			} else if e.catalog.state.Get(child.Address) != nil || e.catalog.state.Get(parent.Address) != nil || e.storage.IndexRoots[child.Address] != nil {
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
				f.child.SQLIndex.PartitionParent = ids.ObjectAddress{}
			case "missing_backing":
				f.child.SQLIndex.Constraint = nil
			case "wrong_parent":
				f.leaf.Constraints.Unique[0].Parent++
			case "local_clone":
				f.leaf.Constraints.Unique[0].IsLocal = true
			case "bare_parent":
				f.parent.SQLIndex.Constraint = nil
			case "ancestor_cycle":
				other := *f.parent
				definition := *f.parent.SQLIndex
				other.ObjectAddress.ObjectID = 40000
				definition.PartitionParent = f.parent.Address()
				other.SQLIndex = &definition
				f.parent.SQLIndex.PartitionParent = other.Address()
				f.view.State().SetDescriptor(other.Address(), &other)
			}
			err := index.ValidateIndexDefinition(f.view, f.child)
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
	root := catalog.UniqueConstraint{OID: 30000, Name: f.parent.Name(), Attributes: []int32{10, 20}, Index: f.parent.Address(), IsLocal: true}
	f.root.Constraints.Unique = []catalog.UniqueConstraint{root}
	f.parent.SQLIndex.Constraint = index.UniqueBacking(&root)
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
	leaf := candidate.MustRelation(f.leaf.Address())
	if clone.Address() == f.child.Address() || clone.SQLIndex.Constraint == nil || clone.SQLIndex.PartitionParent != f.parent.Address() || len(leaf.Constraints.Unique) != 1 {
		t.Fatal("bare child reused as a constraint backing")
	}
	constraint := leaf.Constraints.Unique[0]
	if constraint.Parent != root.OID || constraint.IsLocal || constraint.Index != clone.Address() || !slices.Equal(constraint.Attributes, []int32{22, 11}) {
		t.Fatal("new constraint lost mapped immediate ancestry")
	}
	if candidate.Get(f.child.Address()) != f.child || f.child.SQLIndex.PartitionParent != (ids.ObjectAddress{}) || f.child.SQLIndex.Constraint != nil || len(f.leaf.Constraints.Unique) != 0 {
		t.Fatal("independent bare child or predecessor mutated")
	}
}

func partitionConstraintAttachStage(t *testing.T, e *Engine, op *session.Operation) *CatalogStage {
	t.Helper()
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	t.Cleanup(func() { e.DiscardCatalogStage(stage) })
	parent := base.MustRelation(roleTableRef(t, e, "parent_idx").Address)
	child := base.MustRelation(roleTableRef(t, e, "child_idx").Address)
	stage.indexes().Parents = map[ids.ObjectAddress]*catalog.Relation{parent.SQLIndex.Parent: base.MustRelation(parent.SQLIndex.Parent), child.SQLIndex.Parent: base.MustRelation(child.SQLIndex.Parent)}
	stage.domainBases = []catalog.Descriptor{parent, child}
	op.Session.Mu.Lock()
	stage.authSession, stage.authRevision = op.Session, op.Session.Authorization.Revision
	op.Session.Mu.Unlock()
	e.mu.Lock()
	stage.roleRevision = e.roles.published.Revision
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
			_, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()})
			switch change {
			case "epoch":
				if !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal("replacement fence", err)
				}
			case "role_registry", "session_identity":
				if err == nil || err.Error() != "stuntgres: authorization changed before publication" {
					t.Fatal("authority fence", err)
				}
			default:
				if _, ok := err.(catalog.Conflict); !ok {
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
	original := e.storage.IndexRoots[child.Address]
	run(t, e, "INSERT INTO root VALUES(2)")
	latest := e.storage.IndexRoots[child.Address]
	if latest == original {
		t.Fatal("fixture did not publish a newer key root")
	}
	if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	if e.storage.IndexRoots[child.Address] != latest || e.storage.IndexRoots[roleTableRef(t, e, "parent_idx").Address] != nil {
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
	if _, err := first.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
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
	_, err = second.PublishCatalog(prepared, PublicationToken{Epoch: candidate.Base.Epoch()})
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
	if len(e.storage.IndexRoots) != 0 {
		t.Fatal("failed recursive build retained cold physical installation roots")
	}
	for _, d := range e.catalog.state.Descriptors() {
		if relation, ok := d.(*catalog.Relation); ok && (relation.Name() == "parent_idx" || relation.Name() == "a_k_idx" || relation.Name() == "b_k_idx") {
			t.Fatal("failed recursive build leaked definition", relation.Name())
		}
	}
	run(t, e, "DROP TABLE b")
	run(t, e, "CREATE TABLE b PARTITION OF root DEFAULT")
	run(t, e, "INSERT INTO b VALUES(3)")
	run(t, e, "CREATE UNIQUE INDEX parent_idx ON root(k)")
	btreeWantKeys(t, e, "a_k_idx", "1")
	btreeWantKeys(t, e, "b_k_idx", "3")
	if e.storage.IndexRoots[roleTableRef(t, e, "parent_idx").Address] != nil {
		t.Fatal("healthy recursive retry installed virtual storage")
	}
}

func TestPartitionConstraintsIncompleteReplacementAdmission(t *testing.T) {
	e := New()
	for _, query := range []string{"CREATE TABLE root(k INT) PARTITION BY RANGE(k)", "CREATE TABLE mid PARTITION OF root DEFAULT PARTITION BY RANGE(k)", "CREATE TABLE leaf PARTITION OF mid DEFAULT", "CREATE UNIQUE INDEX parent_idx ON ONLY root(k)", "CREATE UNIQUE INDEX mid_idx ON ONLY mid(k)", "CREATE UNIQUE INDEX leaf_idx ON leaf(k)", "ALTER INDEX parent_idx ATTACH PARTITION mid_idx", "INSERT INTO root VALUES(1)"} {
		run(t, e, query)
	}
	copiedStorage := func() map[ids.ObjectAddress]*storage.Relation {
		out := make(map[ids.ObjectAddress]*storage.Relation, len(e.storage.Relations))
		for address, store := range e.storage.Relations {
			copy := *store
			copy.Versions = slices.Clone(store.Versions)
			out[address] = &copy
		}
		return out
	}
	before := indexMaintenanceCapture(e)
	snapshot, err := e.captureIndexSnapshot(context.Background(), e.CatalogRead(), e.storage.Relations, e.storage.IndexRoots, e.visibility.CommitSeq())
	if err != nil {
		t.Fatal(err)
	}
	roots, _, _, err := e.prepareRemintedStorage(context.Background(), e.catalog.state, copiedStorage(), snapshot)
	if err != nil {
		t.Fatal("representable nested incomplete topology rejected", err)
	}
	if roots[roleTableRef(t, e, "parent_idx").Address] != nil || roots[roleTableRef(t, e, "mid_idx").Address] != nil || roots[roleTableRef(t, e, "leaf_idx").Address] == nil {
		t.Fatal("replacement physical/virtual ownership mismatch")
	}
	indexMaintenanceUnchanged(t, e, before)
	for _, flag := range []string{"physical_invalid", "virtual_not_ready", "virtual_not_live"} {
		t.Run(flag, func(t *testing.T) {
			next := *e.catalog.state
			next.ResetDescriptors(e.catalog.state.DescriptorCount())
			for address, d := range e.catalog.state.Descriptors() {
				next.SetDescriptor(address, d)
			}
			name := "mid_idx"
			if flag == "physical_invalid" {
				name = "leaf_idx"
			}
			address := roleTableRef(t, e, name).Address
			copy := *next.MustRelation(address)
			definition := *copy.SQLIndex
			copy.SQLIndex = &definition
			switch flag {
			case "physical_invalid":
				definition.Valid = false
			case "virtual_not_ready":
				definition.Ready = false
			case "virtual_not_live":
				definition.Live = false
			}
			next.SetDescriptor(address, &copy)
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
			next := *base.State()
			next.ResetDescriptors(base.DescriptorCount())
			for address, d := range base.Descriptors() {
				next.SetDescriptor(address, d)
			}
			root := *next.MustRelation(roleTableRef(t, e, "root").Address)
			leaf := *next.MustRelation(roleTableRef(t, e, "leaf").Address)
			typ, _ := base.LookupType(sqltype.Int4)
			class, _ := sqltype.DefaultIndexClass(typ)
			comparison, err := sqltype.BindIndexOrdering(root.Columns[0].Usage, class)
			if err != nil {
				t.Fatal(err)
			}
			root.RelationKind = catalog.PartitionedRelation
			root.Partition = &catalog.PartitionDefinition{Keys: []catalog.PartitionKeyEntry{{Attribute: root.Attributes[0], Usage: root.Columns[0].Usage, Class: class, Comparison: comparison, Label: "k"}}, Children: []ids.ObjectAddress{leaf.Address()}, DefaultChild: leaf.Address()}
			leaf.Membership = &catalog.PartitionMembership{Parent: root.Address(), DefaultBound: true}
			next.SetDescriptor(root.Address(), &root)
			next.SetDescriptor(leaf.Address(), &leaf)
			parent := *next.MustRelation(roleTableRef(t, e, "parent_idx").Address)
			child := *next.MustRelation(roleTableRef(t, e, "child_idx").Address)
			parentDefinition, childDefinition := *parent.SQLIndex, *child.SQLIndex
			parentDefinition.Unique, childDefinition.Unique = true, true
			parent.SQLIndex, child.SQLIndex = &parentDefinition, &childDefinition
			if mode == "method" {
				childDefinition.Method = catalog.HashMethod
			}
			if mode == "membership_before_equivalence" {
				leaf.Membership = nil
			}
			next.SetDescriptor(parent.Address(), &parent)
			next.SetDescriptor(child.Address(), &child)
			view := e.catalog.read(&next)
			before := indexMaintenanceCapture(e)
			op, done := roleOperation(t, e, "postgres")
			defer done()
			err = index.PartitionIndexAttachCheck(op, view, &parent, &child)
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
	root := e.catalog.state.MustRelation(roleTableRef(t, e, "owned.root").Address)
	mid := e.catalog.state.MustRelation(roleTableRef(t, e, "owned.mid").Address)
	leaf := e.catalog.state.MustRelation(roleTableRef(t, e, "owned.leaf").Address)
	actor, exists := e.Roles().Lookup("actor")
	if !exists || root.Owner != actor.Ref {
		t.Fatal("replay lost declared actor ownership")
	}
	if root.Owner != mid.Owner || root.Owner != leaf.Owner || len(root.Constraints.Unique) != 1 || len(mid.Constraints.Unique) != 1 || len(leaf.Constraints.Unique) != 1 || mid.Constraints.Unique[0].Parent != root.Constraints.Unique[0].OID || leaf.Constraints.Unique[0].Parent != mid.Constraints.Unique[0].OID {
		t.Fatal("owner/key ancestry not restored")
	}
	for _, table := range []*catalog.Relation{root, mid, leaf} {
		index := e.catalog.state.MustRelation(table.Constraints.Unique[0].Index)
		if index.Owner != table.Owner {
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
	future := e.catalog.state.MustRelation(roleTableRef(t, e, "owned.future").Address)
	if len(future.Constraints.Unique) != 1 || future.Constraints.Unique[0].Parent != mid.Constraints.Unique[0].OID {
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
	stage.indexes().Parents = make(map[ids.ObjectAddress]*catalog.Relation)
	for _, name := range names {
		index := base.MustRelation(roleTableRef(t, e, name).Address)
		stage.domainBases = append(stage.domainBases, index)
		stage.indexes().Parents[index.SQLIndex.Parent] = base.MustRelation(index.SQLIndex.Parent)
	}
	op.Session.Mu.Lock()
	stage.authSession, stage.authRevision = op.Session, op.Session.Authorization.Revision
	op.Session.Mu.Unlock()
	e.mu.Lock()
	stage.roleRevision = e.roles.published.Revision
	e.mu.Unlock()
	if key {
		root := base.MustRelation(roleTableRef(t, e, "root_key").Address)
		if err := e.stagePartitionConstraintDrop(stage, root.SQLIndex.Constraint.OID); err != nil {
			t.Fatal(err)
		}
		if err := e.stagePartitionIndexDrop(stage, root); err != nil {
			t.Fatal(err)
		}
	} else {
		for _, name := range names {
			index := base.MustRelation(roleTableRef(t, e, name).Address)
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
			if _, err = first.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
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
			_, err = second.PublishCatalog(prepared, PublicationToken{Epoch: candidate.Base.Epoch()})
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
