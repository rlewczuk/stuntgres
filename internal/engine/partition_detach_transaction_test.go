package engine

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
)

func TestPartitionDetachTransactionOverlay(t *testing.T) {
	for _, end := range []string{"commit", "rollback", "savepoint"} {
		t.Run(end, func(t *testing.T) {
			f := partitionDetachUnitTree(t, false)
			owner := sessionStart(t, f.e, nil)
			ref := f.e.catalog.Domain.Ref(f.e.catalog.state.Epoch(), f.child.Address())
			parentRef := ref
			parentRef.Address = f.parent.Address()
			partitionAttachUnitWrite(t, f.e, owner, ref, 1)
			tx := partitionAttachUnitBegin(t, f.e, owner, ReadCommitted)
			partitionAttachUnitWrite(t, f.e, owner, ref, 2)
			mark, markOp := partitionAttachUnitScope(t, f.e, owner)
			partitionAttachUnitGrant(t, mark, parentRef, lock.AccessShare)
			control := &parser.SavepointControl{Kind: parser.CreateSavepoint, Name: parser.Identifier{Text: "before_detach"}}
			if _, err := mark.executeSavepoint(control, 0); err != nil {
				t.Fatal(err)
			}
			if err := mark.Finish(nil); err != nil {
				t.Fatal(err)
			}
			markOp.Done()
			beforeRows := maps.Clone(owner.transactions.roots)
			detach, op := partitionAttachUnitScope(t, f.e, owner)
			partitionAttachUnitGrant(t, detach, parentRef, lock.AccessExclusive)
			partitionAttachUnitGrant(t, detach, ref, lock.AccessExclusive)
			stage := f.e.BeginCatalogStage(detach.ResolutionCatalog())
			defer f.e.DiscardCatalogStage(stage)
			if err := f.e.stagePartitionDetach(stage, f.parent, f.child); err != nil {
				t.Fatal(err)
			}
			if err := f.e.stageCatalogOverlay(detach, stage, detach.owner.catalog.Clone()); err != nil {
				t.Fatal(err)
			}
			if err := detach.Finish(nil); err != nil {
				t.Fatal(err)
			}
			op.Done()
			private := f.e.privateCatalogLocked(owner.transactions.catalog)
			if private.MustRelation(ref.Address).Membership != nil || f.e.catalog.state.Get(ref.Address) != f.child {
				t.Fatal("detach overlay did not remain private")
			}
			if !reflect.DeepEqual(beforeRows, owner.transactions.roots) {
				t.Fatal("metadata-only detach changed private row roots")
			}
			partitionAttachUnitWrite(t, f.e, owner, ref, 4)
			if end == "savepoint" {
				rollback, rollbackOp := partitionAttachUnitScope(t, f.e, owner)
				control.Kind = parser.RollbackSavepoint
				if _, err := rollback.executeSavepoint(control, 0); err != nil {
					t.Fatal(err)
				}
				if err := rollback.Finish(nil); err != nil {
					t.Fatal(err)
				}
				rollbackOp.Done()
				if f.e.privateCatalogLocked(owner.transactions.catalog).Get(ref.Address) != f.child {
					t.Fatal("savepoint did not restore exact attached descriptor")
				}
				lockState := f.e.locks.Inspect()
				entry := lockState.Resources[lock.Resource{ObjectRef: parentRef}]
				mode := entry.Holders[owner.transactions.ticket].Mode
				childEntry := lockState.Resources[lock.Resource{ObjectRef: ref}]
				childHeld := childEntry != nil && childEntry.Holders[owner.transactions.ticket].Mode != 0
				if mode != lock.AccessShare || childHeld {
					t.Fatal("post-savepoint AX contributions survived", mode, childHeld)
				}
			}
			finishOp, err := owner.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if end == "rollback" {
				_, err = f.e.Rollback(finishOp, tx)
			} else {
				_, err = f.e.Commit(finishOp, tx)
			}
			finishOp.Done()
			if err != nil {
				t.Fatal(err)
			}
			standalone := f.e.catalog.state.MustRelation(ref.Address).Membership == nil
			if standalone != (end == "commit") {
				t.Fatal("wrong end graph")
			}
			reader, readOp := partitionAttachUnitScope(t, f.e, sessionStart(t, f.e, nil))
			view, err := reader.AcquireDataView(ref)
			if err != nil {
				t.Fatal(err)
			}
			want := []int64{1}
			if end == "commit" {
				want = []int64{1, 2, 4}
			}
			if end == "savepoint" {
				want = []int64{1, 2}
			}
			if got := partitionAttachUnitValues(t, view, ref); !reflect.DeepEqual(got, want) {
				t.Fatal("private row roots lost/retained wrong writes", got, want)
			}
			if err := reader.Finish(nil); err != nil {
				t.Fatal(err)
			}
			readOp.Done()
			lockEmpty(t, f.e)
		})
	}
}

func TestPartitionDetachLockFences(t *testing.T) {
	for _, target := range []string{"parent", "default", "child", "descendant"} {
		for _, veto := range []string{"cancel", "epoch", "revision"} {
			if veto == "revision" && (target == "parent" || target == "descendant") {
				continue
			}
			t.Run(target+"/"+veto, func(t *testing.T) {
				f := partitionDetachUnitTree(t, target == "descendant")
				victim := f.parent
				switch target {
				case "default":
					victim = f.def
				case "child":
					victim = f.child
				case "descendant":
					victim = f.leaf
				}
				ref := f.e.catalog.Domain.Ref(f.e.catalog.state.Epoch(), victim.Address())
				// A real explicit holder retains relation locks without retaining
				// implicit row-work replacement admission.
				holderSession := sessionStart(t, f.e, nil)
				holderRef := partitionAttachUnitBegin(t, f.e, holderSession, ReadCommitted)
				holder, holderOp := partitionAttachUnitScope(t, f.e, holderSession)
				partitionAttachUnitGrant(t, holder, ref, lock.AccessShare)
				raw := &parser.AlterTableDetachPartition{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: f.parent.Name()}}}, Partition: parser.ObjectName{Parts: []parser.Identifier{{Text: f.child.Name()}}}}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				op, err := sessionStart(t, f.e, nil).Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				worker, err := f.e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if !worker.finished {
						_ = worker.Finish(context.Canceled)
					}
				}()
				if err := worker.gate(raw); err != nil {
					t.Fatal(err)
				}
				if err := worker.snapshotPhase(raw); err != nil {
					t.Fatal(err)
				}
				before := f.e.catalog.state
				join := partitionAttachUnitWorker(t, worker, func() error {
					if err := worker.acquireRelation(raw, true); err != nil {
						return err
					}
					f.e.mu.Lock()
					auth, err := f.e.statementAuthorizationLocked(op, raw)
					if err != nil {
						f.e.mu.Unlock()
						return err
					}
					_, err = f.e.detachPartitionLocked(worker, op, auth, raw, 0)
					return err
				})
				partitionAttachUnitQueued(t, f.e, ref, worker.ticket, lock.AccessExclusive)
				switch veto {
				case "cancel":
					op.cancel(context.Canceled)
				case "epoch":
					if err := f.e.Reset(context.Background()); err != nil {
						t.Fatal(err)
					}
				case "revision":
					f.e.mu.Lock()
					next := catalog.CloneState(f.e.catalog.state)
					changed := *victim
					changed.ObjectRevision++
					changed.ObjectName += "_new"
					next.UnbindName(catalog.DescriptorNameKey(victim))
					next.BindName(catalog.DescriptorNameKey(&changed), changed.Address())
					next.SetDescriptor(changed.Address(), &changed)
					f.e.catalog.state = next
					f.e.mu.Unlock()
					if err := holder.Finish(nil); err != nil {
						t.Fatal(err)
					}
					holderOp.Done()
					transactionEnd(t, holderSession, holderRef, false)
				}
				err = join()
				if veto == "cancel" {
					if !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
				} else if !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal("grant fence did not reject original operation", err)
				}
				_ = worker.Finish(err)
				op.Done()
				if veto == "cancel" && f.e.catalog.state != before {
					t.Fatal("canceled late admission changed catalog")
				}
				if veto != "epoch" && f.e.catalog.state.MustRelation(f.child.Address()).Membership == nil {
					t.Fatal("wait failure partially detached")
				}
				_ = holder.Finish(nil)
				holderOp.Done()
				if holderSession.TransactionState() != TxIdle {
					transactionEnd(t, holderSession, holderRef, false)
				}
				lockEmpty(t, f.e)
			})
		}
	}
}
