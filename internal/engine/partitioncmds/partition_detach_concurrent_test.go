package partitioncmds_test

import (
	"context"
	"errors"
	"runtime"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/partitioncmds"
	"stuntgres/internal/parser"
	"testing"
	"time"
)

func detachConcurrentUnitTree(t *testing.T) partitionDetachUnit {
	t.Helper()
	f := partitionDetachUnitTree(t, false)
	stage := f.e.BeginCatalogStage(f.e.CatalogRead())
	defer f.e.DiscardCatalogStage(stage)
	if err := partition.StageDetach(stage, stage.Base, f.parent, f.def); err != nil {
		t.Fatal(err)
	}
	detachConcurrentUnitPublish(t, f.e, stage)
	f.parent = f.e.Catalog.State.MustRelation(f.parent.Address())
	f.child = f.e.Catalog.State.MustRelation(f.child.Address())
	return f
}

func detachConcurrentUnitPublish(t *testing.T, e *core.Engine, stage *core.CatalogStage) {
	t.Helper()
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publishCatalog(e, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
}

func detachConcurrentUnitPending(t *testing.T, f partitionDetachUnit) *catalog.Relation {
	t.Helper()
	stage := f.e.BeginCatalogStage(f.e.CatalogRead())
	defer f.e.DiscardCatalogStage(stage)
	f.e.Mu.Lock()
	revision, err := f.e.Catalog.RevisionLocked()
	f.e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := partition.StagePending(stage, f.parent, f.child, f.child, revision); err != nil {
		t.Fatal(err)
	}
	detachConcurrentUnitPublish(t, f.e, stage)
	return f.e.Catalog.State.MustRelation(f.child.Address())
}

func TestPartitionDetachConcurrentPending(t *testing.T) {
	f := detachConcurrentUnitTree(t)
	before := f.e.Catalog.State
	store := f.e.Storage.Relations[f.child.Address()]
	pending := detachConcurrentUnitPending(t, f)
	if before.Get(f.child.Address()) != f.child || f.child.Membership.DetachPending || pending == f.child || !pending.Membership.DetachPending || pending.Membership.Parent != f.parent.Address() {
		t.Fatal("pending publication mutated/removed original edge")
	}
	if f.e.Storage.Relations[f.child.Address()] != store || len(f.parent.Partition.Children) != 1 {
		t.Fatal("metadata phase changed storage/topology")
	}
	if !partition.PendingCompatible(f.child, pending) || pending.History.DetachPredecessor != f.child || pending.History.DetachPublication != pending.Revision() {
		t.Fatal("missing exact publication ownership")
	}
	stage := f.e.BeginCatalogStage(f.e.CatalogRead())
	defer f.e.DiscardCatalogStage(stage)
	if err := partition.StageDetach(stage, stage.Base, f.parent, pending); err != nil {
		t.Fatal(err)
	}
	detachConcurrentUnitPublish(t, f.e, stage)
	completed := f.e.Catalog.State.MustRelation(f.child.Address())
	if completed.Membership != nil || completed.History.DetachPredecessor != nil || completed.History.DetachPublication != 0 || completed.History.DetachRoot != (ids.ObjectAddress{}) || f.e.Storage.Relations[f.child.Address()] != store {
		t.Fatal("final teardown retained admission proof or lost storage")
	}
	// Impossible pending DEFAULT and two pending immediate children reject stage
	// validation; neither can replace the committed source.
	for _, bad := range []string{"default", "second_pending"} {
		t.Run(bad, func(t *testing.T) {
			fixture := partitionDetachUnitTree(t, false)
			if bad == "second_pending" {
				// Establish two ordinary disjoint ranges first, so this case
				// cannot pass merely by detecting a DEFAULT-bearing parent.
				preparation := fixture.e.BeginCatalogStage(fixture.e.CatalogRead())
				defer fixture.e.DiscardCatalogStage(preparation)
				parent, definition := *fixture.parent, *fixture.parent.Partition
				definition.DefaultChild = ids.ObjectAddress{}
				parent.Partition = &definition
				parent.ObjectRevision++
				other := *fixture.def
				other.Membership = partitionAttachUnitRange(t, &parent, 3, 5)
				other.ObjectRevision++
				if err := preparation.Replace(&parent); err != nil {
					t.Fatal(err)
				}
				if err := preparation.Replace(&other); err != nil {
					t.Fatal(err)
				}
				detachConcurrentUnitPublish(t, fixture.e, preparation)
				fixture.parent = fixture.e.Catalog.State.MustRelation(parent.Address())
				fixture.def = fixture.e.Catalog.State.MustRelation(other.Address())
			}
			base := fixture.e.Catalog.State
			stage := fixture.e.BeginCatalogStage(fixture.e.CatalogRead())
			defer fixture.e.DiscardCatalogStage(stage)
			next, membership := *fixture.child, *fixture.child.Membership
			membership.DetachPending = true
			next.Membership = &membership
			next.ObjectRevision++
			if err := stage.Replace(&next); err != nil {
				t.Fatal(err)
			}
			if bad == "second_pending" {
				other, bound := *fixture.def, *fixture.def.Membership
				bound.DetachPending = true
				other.Membership = &bound
				other.ObjectRevision++
				if err := stage.Replace(&other); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := fixture.e.ValidateCatalogStage(context.Background(), stage); err == nil {
				t.Fatal("invalid pending graph validated")
			}
			if fixture.e.Catalog.State != base {
				t.Fatal("failed stage published")
			}
		})
	}
}

func TestPartitionDetachConcurrentSnapshots(t *testing.T) {
	f := detachConcurrentUnitTree(t)
	original := f.e.Catalog.State
	detachConcurrentUnitPending(t, f)
	current := f.e.CatalogRead()
	if got := partition.EffectiveChildren(current, f.parent, nil); len(got) != 0 {
		t.Fatal("fresh topology retained pending child", got)
	}
	got := partition.EffectiveChildren(current, f.parent, original)
	if len(got) != 1 || got[0] != f.child.Address() || &got[0] != &f.parent.Partition.Children[0] {
		t.Fatal("stable topology lost immutable attached backing", got)
	}
	if len(current.MustRelation(f.parent.Address()).Partition.Children) != 1 || !current.MustRelation(f.child.Address()).Membership.DetachPending {
		t.Fatal("effective topology rewrote canonical dependencies")
	}
	foreign := catalog.CloneState(original)
	foreign.SetEpoch(original.Epoch() + 1)
	if got := partition.EffectiveChildren(current, f.parent, foreign); len(got) != 0 {
		t.Fatal("foreign epoch snapshot admitted", got)
	}
	stage := f.e.BeginCatalogStage(current)
	defer f.e.DiscardCatalogStage(stage)
	if err := partition.StageDetach(stage, stage.Base, f.parent, current.MustRelation(f.child.Address())); err != nil {
		t.Fatal(err)
	}
	detachConcurrentUnitPublish(t, f.e, stage)
	if got := partition.EffectiveChildren(f.e.CatalogRead(), f.e.Catalog.State.MustRelation(f.parent.Address()), original); len(got) != 0 {
		t.Fatal("fully removed edge resurrected", got)
	}
}

func TestPartitionDetachConcurrentFences(t *testing.T) {
	f := detachConcurrentUnitTree(t)
	pending := detachConcurrentUnitPending(t, f)
	for _, change := range []string{"revision", "name", "bound", "import", "stamp", "finalize"} {
		t.Run(change, func(t *testing.T) {
			next := *pending
			switch change {
			case "revision":
				next.ObjectRevision++
			case "name":
				next.ObjectName = "replacement"
			case "bound":
				membership := *next.Membership
				membership.Lower = append([]catalog.PartitionDatum(nil), membership.Lower...)
				next.Membership = &membership
			case "import":
				next.History.DetachPredecessor = nil
			case "stamp":
				next.History.DetachPublication = 0
			case "finalize":
				next.Membership = nil
			}
			if partition.PendingCompatible(f.child, &next) {
				t.Fatal("unrelated replacement crossed narrow pending fence", change)
			}
		})
	}
	owner := sessionStart(t, f.e, nil)
	scope, _ := partitionAttachUnitScope(t, f.e, owner)
	ref := f.e.Catalog.Domain.Ref(f.e.Catalog.State.Epoch()+1, f.child.Address())
	f.e.Mu.Lock()
	err := scope.ObservePartitionRelationLocked(f.e, ref)
	f.e.Mu.Unlock()
	if !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("epoch fence", err)
	}
}

func TestPartitionDetachConcurrentPublication(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "cancel_after_first"}[cancel], func(t *testing.T) {
			f := detachConcurrentUnitTree(t)
			before := f.e.Catalog.State
			store := f.e.Storage.Relations[f.child.Address()]
			holder, _ := partitionAttachUnitScope(t, f.e, sessionStart(t, f.e, nil))
			parentRef := f.e.Catalog.Domain.Ref(before.Epoch(), f.parent.Address())
			partitionAttachUnitGrant(t, holder, parentRef, lock.AccessShare)
			worker, op := partitionAttachUnitScope(t, f.e, sessionStart(t, f.e, nil))
			worker.LockedRef = parentRef
			worker.LockedDescriptor = f.parent
			partitionAttachUnitGrant(t, worker, parentRef, lock.ShareUpdateExclusive)
			raw := &parser.AlterTableDetachPartition{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "public"}, {Text: f.parent.Name()}}}, Partition: parser.ObjectName{Parts: []parser.Identifier{{Text: "public"}, {Text: f.child.Name()}}}, Mode: parser.DetachConcurrent}
			done := make(chan error, 1)
			joined := false
			t.Cleanup(func() {
				op.Cancel(context.Canceled)
				if !joined {
					select {
					case <-done:
					case <-time.After(time.Second):
						t.Error("detach publication worker failed to join")
					}
				}
			})
			go func() {
				f.e.Mu.Lock()
				auth, err := f.e.StatementAuthorizationLocked(op, raw)
				if err == nil {
					_, err = partitioncmds.DetachPartitionLocked(f.e, worker, op, auth, raw, 0)
				} else {
					f.e.Mu.Unlock()
				}
				err = worker.Finish(err)
				op.Done()
				done <- err
			}()
			ctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
			defer stop()
			var pending *catalog.Relation
			for {
				select {
				case err := <-done:
					joined = true
					t.Fatal("completed before first-phase wait", err)
				case <-ctx.Done():
					t.Fatal("no pending completion wait", ctx.Err())
				default:
				}
				f.e.Mu.Lock()
				pending = f.e.Catalog.State.MustRelation(f.child.Address())
				f.e.Mu.Unlock()
				snapshot := f.e.Locks.Inspect()
				completion := snapshot.Resources[lock.CompletionOf(holder.LockTicket)]
				observed := false
				if pending.Membership != nil && pending.Membership.DetachPending && completion != nil {
					for _, wait := range completion.Pending {
						if wait.Ticket == worker.LockTicket && !wait.Terminal && wait.Policy.CompletionPurpose == lock.CompletionVirtual {
							observed = true
						}
					}
				}
				if observed {
					break
				}
				runtime.Gosched()
			}
			if before.Get(f.child.Address()) != f.child || f.child.Membership.DetachPending || f.e.Storage.Relations[f.child.Address()] != store {
				t.Fatal("first publication changed immutable predecessor/storage")
			}
			snapshot := f.e.Locks.Inspect()
			for _, ref := range []ids.ObjectRef{parentRef, f.e.Catalog.Domain.Ref(before.Epoch(), f.child.Address())} {
				if entry := snapshot.Resources[lock.Resource{ObjectRef: ref}]; entry != nil {
					if entry.Holders[worker.LockTicket].Mode&(lock.ShareUpdateExclusive|lock.AccessExclusive) != 0 {
						t.Fatal("phase-one lock retained during cohort wait")
					}
				}
			}
			if cancel {
				op.Cancel(context.Canceled)
			} else {
				if err := holder.Finish(nil); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-done:
				joined = true
				if cancel && !errors.Is(err, context.Canceled) || !cancel && err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("detach failed to finish", ctx.Err())
			}
			current := f.e.Catalog.State.MustRelation(f.child.Address())
			if cancel {
				if current != pending || !current.Membership.DetachPending {
					t.Fatal("failure rolled back or completed first committed phase")
				}
			} else if current.Membership != nil || len(f.e.Catalog.State.MustRelation(f.parent.Address()).Partition.Children) != 0 {
				t.Fatal("second publication did not complete exact edge")
			}
		})
	}
}

func TestPartitionDetachConcurrentFinalizeSnapshot(t *testing.T) {
	f := detachConcurrentUnitTree(t)
	detachConcurrentUnitPending(t, f)
	worker, op := partitionAttachUnitScope(t, f.e, sessionStart(t, f.e, nil))
	worker.LockedRef = f.e.Catalog.Domain.Ref(f.e.Catalog.State.Epoch(), f.parent.Address())
	worker.LockedDescriptor = f.parent
	partitionAttachUnitGrant(t, worker, worker.LockedRef, lock.ShareUpdateExclusive)
	raw := &parser.AlterTableDetachPartition{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "public"}, {Text: f.parent.Name()}}}, Partition: parser.ObjectName{Parts: []parser.Identifier{{Text: "public"}, {Text: f.child.Name()}}}, Mode: parser.DetachFinalize}
	f.e.Mu.Lock()
	_, err := f.e.VisibilitySnapshotLocked(worker.TxRef, false)
	if err != nil {
		f.e.Mu.Unlock()
		t.Fatal(err)
	}
	before := f.e.Visibility.Entries[worker.TxRef]
	auth, err := f.e.StatementAuthorizationLocked(op, raw)
	if err == nil {
		_, err = partitioncmds.DetachPartitionLocked(f.e, worker, op, auth, raw, 0)
	} else {
		f.e.Mu.Unlock()
	}
	if err != nil {
		t.Fatal(err)
	}
	f.e.Mu.Lock()
	after := f.e.Visibility.Entries[worker.TxRef]
	f.e.Mu.Unlock()
	if !before.Snapshot || !after.Snapshot || after.Xmin != before.Xmin {
		t.Fatal("FINALIZE retired its owner's advertised snapshot before transaction retirement")
	}
	if err := worker.Finish(nil); err != nil {
		t.Fatal(err)
	}
}
