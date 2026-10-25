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
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func TestPartitionDetachTransactionOverlay(t *testing.T) {
	for _, end := range []string{"commit", "rollback", "savepoint"} {
		t.Run(end, func(t *testing.T) {
			f := partitionDetachUnitTree(t, false)
			owner := sessionStart(t, f.e, nil)
			ref := f.e.Catalog.Domain.Ref(f.e.Catalog.State.Epoch(), f.child.Address())
			parentRef := ref
			parentRef.Address = f.parent.Address()
			partitionAttachUnitWrite(t, f.e, owner, ref, 1)
			tx := partitionAttachUnitBegin(t, f.e, owner, session.ReadCommitted)
			partitionAttachUnitWrite(t, f.e, owner, ref, 2)
			mark, markOp := partitionAttachUnitScope(t, f.e, owner)
			partitionAttachUnitGrant(t, mark, parentRef, lock.AccessShare)
			control := &parser.SavepointControl{Kind: parser.CreateSavepoint, Name: parser.Identifier{Text: "before_detach"}}
			if _, err := ScopeExecuteSavepoint(mark, control, 0); err != nil {
				t.Fatal(err)
			}
			if err := mark.Finish(nil); err != nil {
				t.Fatal(err)
			}
			markOp.Done()
			beforeRows := maps.Clone(owner.Transactions.Roots)
			detach, op := partitionAttachUnitScope(t, f.e, owner)
			partitionAttachUnitGrant(t, detach, parentRef, lock.AccessExclusive)
			partitionAttachUnitGrant(t, detach, ref, lock.AccessExclusive)
			stage := f.e.BeginCatalogStage(detach.ResolutionCatalog())
			defer f.e.DiscardCatalogStage(stage)
			if err := partition.StageDetach(stage, stage.Base, f.parent, f.child); err != nil {
				t.Fatal(err)
			}
			if err := f.e.StageCatalogOverlay(detach, stage, detach.Owner.Catalog.Clone()); err != nil {
				t.Fatal(err)
			}
			if err := detach.Finish(nil); err != nil {
				t.Fatal(err)
			}
			op.Done()
			private := f.e.PrivateCatalogLocked(owner.Transactions.Catalog)
			if private.MustRelation(ref.Address).Membership != nil || f.e.Catalog.State.Get(ref.Address) != f.child {
				t.Fatal("detach overlay did not remain private")
			}
			if !reflect.DeepEqual(beforeRows, owner.Transactions.Roots) {
				t.Fatal("metadata-only detach changed private row roots")
			}
			partitionAttachUnitWrite(t, f.e, owner, ref, 4)
			if end == "savepoint" {
				rollback, rollbackOp := partitionAttachUnitScope(t, f.e, owner)
				control.Kind = parser.RollbackSavepoint
				if _, err := ScopeExecuteSavepoint(rollback, control, 0); err != nil {
					t.Fatal(err)
				}
				if err := rollback.Finish(nil); err != nil {
					t.Fatal(err)
				}
				rollbackOp.Done()
				if f.e.PrivateCatalogLocked(owner.Transactions.Catalog).Get(ref.Address) != f.child {
					t.Fatal("savepoint did not restore exact attached descriptor")
				}
				lockState := f.e.Locks.Inspect()
				entry := lockState.Resources[lock.Resource{ObjectRef: parentRef}]
				mode := entry.Holders[owner.Transactions.Ticket].Mode
				childEntry := lockState.Resources[lock.Resource{ObjectRef: ref}]
				childHeld := childEntry != nil && childEntry.Holders[owner.Transactions.Ticket].Mode != 0
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
			standalone := f.e.Catalog.State.MustRelation(ref.Address).Membership == nil
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
				ref := f.e.Catalog.Domain.Ref(f.e.Catalog.State.Epoch(), victim.Address())
				// A real explicit holder retains relation locks without retaining
				// implicit row-work replacement admission.
				holderSession := sessionStart(t, f.e, nil)
				holderRef := partitionAttachUnitBegin(t, f.e, holderSession, session.ReadCommitted)
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
					if !worker.Finished {
						_ = worker.Finish(context.Canceled)
					}
				}()
				if err := worker.Gate(raw); err != nil {
					t.Fatal(err)
				}
				if err := ScopeSnapshotPhase(worker, raw); err != nil {
					t.Fatal(err)
				}
				before := f.e.Catalog.State
				join := partitionAttachUnitWorker(t, worker, func() error {
					if err := worker.AcquireRelation(raw, true); err != nil {
						return err
					}
					f.e.Mu.Lock()
					auth, err := f.e.StatementAuthorizationLocked(op, raw)
					if err != nil {
						f.e.Mu.Unlock()
						return err
					}
					_, err = detachPartitionLocked(f.e, worker, op, auth, raw, 0)
					return err
				})
				partitionAttachUnitQueued(t, f.e, ref, worker.LockTicket, lock.AccessExclusive)
				switch veto {
				case "cancel":
					op.Cancel(context.Canceled)
				case "epoch":
					if err := Reset(context.Background(), f.e); err != nil {
						t.Fatal(err)
					}
				case "revision":
					f.e.Mu.Lock()
					next := catalog.CloneState(f.e.Catalog.State)
					changed := *victim
					changed.ObjectRevision++
					changed.ObjectName += "_new"
					next.UnbindName(catalog.DescriptorNameKey(victim))
					next.BindName(catalog.DescriptorNameKey(&changed), changed.Address())
					next.SetDescriptor(changed.Address(), &changed)
					f.e.Catalog.PublishLocked(next)
					f.e.Mu.Unlock()
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
				if veto == "cancel" && f.e.Catalog.State != before {
					t.Fatal("canceled late admission changed catalog")
				}
				if veto != "epoch" && f.e.Catalog.State.MustRelation(f.child.Address()).Membership == nil {
					t.Fatal("wait failure partially detached")
				}
				_ = holder.Finish(nil)
				holderOp.Done()
				if holderSession.TransactionState() != session.TxIdle {
					transactionEnd(t, holderSession, holderRef, false)
				}
				lockEmpty(t, f.e)
			})
		}
	}
}
