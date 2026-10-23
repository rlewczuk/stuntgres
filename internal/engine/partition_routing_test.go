package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Descriptor/storage fixture: no SQL parser or public-driver scenario is
// duplicated here. Root and intermediate deliberately have no storage object.
func partitionRoutingUnitEngine(t *testing.T) (*Engine, *catalog.Relation, *catalog.Relation, *catalog.Relation, *catalog.Relation) {
	t.Helper()
	e, _, root, source := hashPartitionUnitEngine(t)
	owner := inheritanceLifecycleTable(e, 26000, "owner").Owner
	root.Owner, source.Owner = owner, owner
	root.ObjectRevision, source.ObjectRevision = 1, 1
	mid := *root
	mid.ObjectAddress.ObjectID += 10
	mid.ObjectName = "routing_mid"
	mid.Membership = &catalog.PartitionMembership{Parent: root.Address(), Strategy: parser.PartitionHash, Modulus: 1}
	midDefinition := *root.Partition
	mid.Partition = &midDefinition
	source.Membership = &catalog.PartitionMembership{Parent: mid.Address(), Strategy: parser.PartitionHash, Modulus: 4, Remainder: 0}
	destination := *source
	destination.ObjectAddress.ObjectID += 20
	destination.ObjectName = "routing_destination"
	destination.Membership = &catalog.PartitionMembership{Parent: mid.Address(), Strategy: parser.PartitionHash, Modulus: 4, Remainder: 2}
	mid.Partition.Children = []ids.ObjectAddress{source.Address(), destination.Address()}
	root.Partition.Children = []ids.ObjectAddress{mid.Address()}
	inheritanceLifecycleInstall(e, root, &mid, source, &destination)
	delete(e.storage.Relations, root.Address())
	delete(e.storage.Relations, mid.Address())
	value, err := sqltype.Integer(root.Columns[0].Usage.Type, 1)
	if err != nil {
		t.Fatal(err)
	}
	e.storage.Relations[source.Address()].Versions = []storage.RowVersion{{Row: 11, Version: 21, Born: 1, Values: []sqltype.Value{value}}}
	e.storage.SetFrontierLocked(11, 21)
	e.visibility.SetCommitSeqLocked(1)
	return e, root, &mid, source, &destination
}

func partitionRoutingUnitScope(t *testing.T, e *Engine, target *catalog.Relation, only bool) (*StatementScope, analyze.Binding, *exec.Source) {
	t.Helper()
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(done)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !scope.finished {
			scope.Finish(errors.New("abandoned routing unit"))
		}
	})
	syntax := &parser.Update{Only: only}
	if err := scope.gate(syntax); err != nil {
		t.Fatal(err)
	}
	scope.statement = syntax
	ref := e.catalog.Domain.Ref(e.CatalogRead().Epoch(), target.Address())
	scope.lockedRef, scope.lockedDescriptor = ref, target
	scope.lockedStorage = e.storage.Relations[target.Address()]
	if err := e.locks.Acquire(op.Context(), scope.ticket, lock.Resource{ObjectRef: ref}, lock.RowExclusive); err != nil {
		t.Fatal(err)
	}
	if !only {
		if err := scope.acquirePartitionDescendants(ref, lock.RowExclusive); err != nil {
			t.Fatal(err)
		}
	}
	if err := scope.acquireInheritedMutationTargets(syntax); err != nil {
		t.Fatal(err)
	}
	auth, err := operationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	scope.routineAuth = auth
	e.mu.Lock()
	err = scope.capturePartitionInsertLocked(e, auth, target)
	var source *exec.Source
	b := analyze.Binding{Ref: ref, Relation: target, Storage: scope.lockedStorage}
	if err == nil {
		view, captureErr := scope.acquireDataViewsLocked(e, scope.Command.TargetRefs(ref))
		err = captureErr
		if err == nil {
			source, err = scope.Command.CaptureSource(op.Context(), view, b)
		}
	}
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(source.Close)
	return scope, b, source
}

func TestPartitionRoutingCapture(t *testing.T) {
	t.Run("reordered_layout", partitionRoutingCaptureReorderedLayout)
	t.Run("reordered_destination", partitionRoutingCaptureReorderedDestination)
	t.Run("admitted_topology_growth", partitionRoutingCaptureTopologyGrowth)
	for _, targetKind := range []string{"root", "mid", "only", "empty", "ordinary"} {
		t.Run(targetKind, func(t *testing.T) {
			e, root, mid, leaf, _ := partitionRoutingUnitEngine(t)
			target := root
			only := targetKind == "only"
			if targetKind == "mid" {
				target = mid
			}
			if targetKind == "empty" {
				root.Partition.Children = nil
			}
			if targetKind == "ordinary" {
				target = leaf
				leaf.Membership = nil
			}
			scope, b, source := partitionRoutingUnitScope(t, e, target, only)
			want := 1
			if only || targetKind == "empty" {
				want = 0
			}
			if source.Count() != want || source.View == nil || source.View.Load() == nil {
				t.Fatal("invalid retained capture", source.Count())
			}
			if e.storage.Relations[root.Address()] != nil || e.storage.Relations[mid.Address()] != nil {
				t.Fatal("virtual heap manufactured")
			}
			if targetKind == "ordinary" {
				if source.Inheritance != nil || source.Ref != b.Ref {
					t.Fatal("ordinary physical fast path changed")
				}
			} else {
				seen := make(map[ids.ObjectAddress]bool)
				for _, segment := range source.Inheritance {
					if segment.Leaf.Storage == nil || segment.Leaf.Relation.Partition != nil || seen[segment.Leaf.Ref.Address] || segment.Prefix.View.Data() != source.View.Data() {
						t.Fatal("invalid physical manifest", segment.Leaf)
					}
					seen[segment.Leaf.Ref.Address] = true
				}
			}
			source.Close()
			if scope.view.Load() == nil {
				t.Fatal("capture consumed statement view")
			}
			if err := scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPartitionRoutingLatestRow(t *testing.T) {
	e, _, _, leaf, _ := partitionRoutingUnitEngine(t)
	store := e.storage.Relations[leaf.Address()]
	row := store.Versions[0]
	request := dml.RowRequest{Row: row.Row, Storage: store, Hint: 0}
	for _, moved := range []bool{false, true} {
		store.Ends = &storage.DeletionRoot{Rows: map[ids.RowID]storage.RowEnd{row.Row: {Version: row.Version, End: 2, Moved: moved}}}
		source := dml.LatestRowSource{Store: store, CommitSeq: 2}
		for _, read := range []func(dml.LatestRowSource, dml.RowRequest) (dml.LatestRow, error){dml.LatestUpdateRow, dml.LatestDeleteRow} {
			latest, err := read(source, request)
			if !moved {
				if err != nil || latest.Live {
					t.Fatal("ordinary deletion not skipped", latest, err)
				}
			} else {
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "40001" || diagnostic.Message != "tuple to be locked was already moved to another partition due to concurrent update" {
					t.Fatal("movement not distinguished", err)
				}
			}
		}
	}
}

func partitionRoutingUnitAssignment(t *testing.T, b *analyze.Binding, value int64) {
	t.Helper()
	v, err := sqltype.Integer(b.Relation.Columns[0].Usage.Type, value)
	if err != nil {
		t.Fatal(err)
	}
	b.Targets = []int{0}
	b.Update = &analyze.UpdateBinding{Assignments: []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: v.Type().Usage()}, Value: v}}}
}

func partitionRoutingCaptureReorderedLayout(t *testing.T) {
	e := New()
	named := inheritanceQueryUnitColumns(e, 26000, "k", "payload")
	leaf := inheritanceQueryUnitColumns(e, 26001, "payload", "k")
	layout, err := catalog.InheritanceQueryLayout(named, leaf)
	if err != nil {
		t.Fatal(err)
	}
	old := storage.RowVersion{Row: 1, Version: 1, Values: []sqltype.Value{inheritanceQueryInt(e, 9), inheritanceQueryInt(e, 1)}}
	segment := &exec.InheritanceQuerySegment{Layout: layout, Scratch: make([]sqltype.Value, 2)}
	physical := exec.QueryPhysicalRow{Leaf: exec.Leaf{Relation: leaf}, Segment: segment, Version: old}
	before := physical.Values(old)
	proposal := []sqltype.Value{inheritanceQueryInt(e, 2), inheritanceQueryInt(e, 9)}
	row := dml.ScatterMutationRow(physical, old, proposal, []int{0})
	if !reflect.DeepEqual(row, []sqltype.Value{inheritanceQueryInt(e, 9), inheritanceQueryInt(e, 2)}) || !reflect.DeepEqual(before, []sqltype.Value{inheritanceQueryInt(e, 1), inheritanceQueryInt(e, 9)}) {
		t.Fatal("physical scatter changed OLD", row, before)
	}
}

func partitionRoutingCaptureReorderedDestination(t *testing.T) {
	e, root, mid, sourceRelation, destination := partitionRoutingUnitEngine(t)
	usage := root.Columns[0].Usage
	for _, relation := range []*catalog.Relation{root, mid, sourceRelation, destination} {
		relation.Columns = append([]catalog.Column(nil), relation.Columns...)
		relation.Columns = append(relation.Columns, catalog.Column{Name: "payload", Usage: usage, Nullable: true})
		relation.Attributes = []int32{1, 2}
		relation.Index = map[string]int{"k": 0, "payload": 1}
		relation.ColumnMeta.HighWater = 2
	}
	destination.Columns[0], destination.Columns[1] = destination.Columns[1], destination.Columns[0]
	destination.Attributes = []int32{2, 1}
	destination.Index = map[string]int{"payload": 0, "k": 1}
	payload, err := sqltype.Integer(usage.Type, 99)
	if err != nil {
		t.Fatal(err)
	}
	store := e.storage.Relations[sourceRelation.Address()]
	store.Versions[0].Values = append(store.Versions[0].Values, payload)
	old := store.Versions[0]
	scope, b, source := partitionRoutingUnitScope(t, e, root, false)
	partitionRoutingUnitAssignment(t, &b, 2)
	out, err := dml.ExecuteUpdate(scope.writeHost(), dml.UpdateInput{Context: scope.op.Context(), Binding: b, Source: source, Schema: "public"})
	if err != nil || out.Count != 1 {
		t.Fatal(out, err)
	}
	physical := scope.stage.rows[destination.Address()][0].Values
	two, err := sqltype.Integer(usage.Type, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(physical, []sqltype.Value{payload, two}) || !reflect.DeepEqual(scope.PartitionInsert.NamedRow, []sqltype.Value{two, payload}) {
		t.Fatal("destination layout lost finalized logical NEW", physical, scope.PartitionInsert.NamedRow)
	}
	one, err := sqltype.Integer(usage.Type, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(old.Values, []sqltype.Value{one, payload}) {
		t.Fatal("destination projection mutated OLD", old.Values)
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
}
