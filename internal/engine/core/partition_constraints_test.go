package core_test

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/sqltype"
)

func TestPartitionConstraintsVirtualPublication(t *testing.T) {
	f := partitionConstraintDescriptors(t)
	next := *f.view.State()
	next.ResetDescriptors()
	for a, d := range f.view.Descriptors() {
		if a != f.child.Address() {
			next.SetDescriptor(a, d)
		}
	}
	roots, err := core.EnginePrepareIndexRoots(f.e, &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, catalog.NewState(0), &next, nil, nil, nil, nil, nil, true, sqltype.OutputEnvironment{}, nil, nil, nil)
	if err != nil || roots[f.parent.Address()] != nil {
		t.Fatal("virtual root required physical storage", roots, err)
	}
	if f.parent.SQLIndex.Valid || f.e.Catalog.State != f.view.State() || f.child.SQLIndex.PartitionParent != (ids.ObjectAddress{}) {
		t.Fatal("publication mutated predecessors")
	}
}

func TestPartitionConstraintsAttachPublication(t *testing.T) {
	f := partitionConstraintDescriptors(t)
	op, done := enginetest.RoleOperation(t, f.e, "postgres")
	defer done()
	stage := f.e.BeginCatalogStage(f.view)
	defer f.e.DiscardCatalogStage(stage)
	if err := index.StagePartitionIndexAttach(tablecmds.TableHost{core.DDLHost{E: f.e, Op: op, CatalogStage: stage}}, f.parent, f.child); err != nil {
		t.Fatal(err)
	}
	next := f.e.StageRead(stage)
	child := next.MustRelation(f.child.Address())
	parent := next.MustRelation(f.parent.Address())
	if child.SQLIndex.PartitionParent != f.parent.Address() || !parent.SQLIndex.Valid {
		t.Fatal("candidate attachment missing")
	}
	if f.child.SQLIndex.PartitionParent != (ids.ObjectAddress{}) || f.parent.SQLIndex.Valid || f.e.Catalog.State != f.view.State() {
		t.Fatal("private attachment published early")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := core.EnginePrepareIndexRoots(f.e, &exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, f.view.State(), next.State(), map[ids.ObjectAddress]*storage.Relation{f.leaf.Address(): {}}, nil, nil, nil, nil, true, sqltype.OutputEnvironment{}, nil, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled candidate accepted", err)
	}
	if f.child.SQLIndex.PartitionParent != (ids.ObjectAddress{}) || f.parent.SQLIndex.Valid {
		t.Fatal("cancellation changed old edge")
	}
}

func TestPartitionConstraintsDumpCollisionProvenance(t *testing.T) {
	e := engine.New()
	temp := inheritanceControlsTemporary(e, 20000, "retained")
	source := engine.New()
	parent := inheritanceLifecycleTable(source, 20000, "z_auto")
	child := inheritanceLifecycleTable(source, 20001, "z_child", parent.Address())
	manual := inheritanceLifecycleTable(source, 20002, "a_manual")
	inheritanceLifecycleInstall(source, parent, child, manual)
	e.Catalog.InstallOIDFrontierLocked(30000)
	for range 2 {
		next := catalog.CloneState(source.Catalog.State)
		relations := map[ids.ObjectAddress]*storage.Relation{parent.Address(): {}, child.Address(): {}, manual.Address(): {}}
		e.Mu.Lock()
		err := core.EnginePreserveTemporaryLocked(e, context.Background(), next, relations, map[ids.ObjectAddress]*storage.SequenceGenerator{}, nil, nil, replacementProcedurePolicyFixture(source, e), &catalog.OIDReservation{Base: e.Catalog.NextOID(), Next: e.Catalog.NextOID()})
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		imported := next.MustRelation(child.Address())
		mapped := imported.Inheritance.Parents[0].Relation
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
		if source.Catalog.State.HasDumpPriorities() || source.Catalog.State.Get(parent.Address()) != parent {
			t.Fatal("replacement mutated source provenance")
		}
		e.Catalog.PublishLocked(next)
		e.Mu.Lock()
		e.Storage.ReplaceRelationsLocked(relations)
		e.Mu.Unlock()
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
	destination := engine.New()
	retained := inheritanceControlsTemporary(destination, f.parent.Address().ObjectID, "retained_backing_collision")
	// The next descriptor replacement candidate is an imported child constraint,
	// not a descriptor address. It must be reserved in the same identity space.
	destination.Catalog.InstallOIDFrontierLocked(childKey.OID)
	old := destination.Catalog.State
	next := catalog.CloneState(f.view.State())
	relations := map[ids.ObjectAddress]*storage.Relation{f.leaf.Address(): {}}
	destination.Mu.Lock()
	err := core.EnginePreserveTemporaryLocked(destination, context.Background(), next, relations, map[ids.ObjectAddress]*storage.SequenceGenerator{}, nil, nil, replacementProcedurePolicyFixture(f.e, destination), &catalog.OIDReservation{Base: destination.Catalog.NextOID(), Next: destination.Catalog.NextOID()})
	destination.Mu.Unlock()
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
	if next.Get(retained.Address()) != retained || destination.Catalog.State != old || f.view.Get(f.parent.Address()) != f.parent || !reflect.DeepEqual(f.root.Constraints.Unique[0], rootKey) {
		t.Fatal("candidate allocation changed retained TEMP or immutable source")
	}
}

func TestPartitionConstraintsIncompleteReplacementAdmission(t *testing.T) {
	e := engine.New()
	for _, query := range []string{"CREATE TABLE root(k INT) PARTITION BY RANGE(k)", "CREATE TABLE mid PARTITION OF root DEFAULT PARTITION BY RANGE(k)", "CREATE TABLE leaf PARTITION OF mid DEFAULT", "CREATE UNIQUE INDEX parent_idx ON ONLY root(k)", "CREATE UNIQUE INDEX mid_idx ON ONLY mid(k)", "CREATE UNIQUE INDEX leaf_idx ON leaf(k)", "ALTER INDEX parent_idx ATTACH PARTITION mid_idx", "INSERT INTO root VALUES(1)"} {
		enginetest.Run(t, e, query)
	}
	copiedStorage := func() map[ids.ObjectAddress]*storage.Relation {
		out := make(map[ids.ObjectAddress]*storage.Relation, len(e.Storage.Relations))
		for address, store := range e.Storage.Relations {
			copy := *store
			copy.Versions = slices.Clone(store.Versions)
			out[address] = &copy
		}
		return out
	}
	before := indexMaintenanceCapture(e)
	snapshot, err := core.EngineCaptureIndexSnapshot(e, context.Background(), e.CatalogRead(), e.Storage.Relations, e.Storage.IndexRoots, e.Visibility.CommitSeq())
	if err != nil {
		t.Fatal(err)
	}
	roots, _, _, err := core.EnginePrepareRemintedStorage(e, context.Background(), e.Catalog.State, copiedStorage(), snapshot, true)
	if err != nil {
		t.Fatal("representable nested incomplete topology rejected", err)
	}
	if roots[roleTableRef(t, e, "parent_idx").Address] != nil || roots[roleTableRef(t, e, "mid_idx").Address] != nil || roots[roleTableRef(t, e, "leaf_idx").Address] == nil {
		t.Fatal("replacement physical/virtual ownership mismatch")
	}
	indexMaintenanceUnchanged(t, e, before)
	for _, flag := range []string{"physical_invalid", "virtual_not_ready", "virtual_not_live"} {
		t.Run(flag, func(t *testing.T) {
			next := *e.Catalog.State
			next.ResetDescriptors()
			for address, d := range e.Catalog.State.Descriptors() {
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
			if _, _, _, err := core.EnginePrepareRemintedStorage(e, context.Background(), &next, copiedStorage(), snapshot, true); err == nil {
				t.Fatal("unrepresentable publication state imported", flag)
			}
			indexMaintenanceUnchanged(t, e, before)
		})
	}
}
