package core_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

// Readers retain only actual old immutable artifacts, never a live transaction
// owner or mutable storage header. The channels bound their entire lifetime;
// every success, veto and testing failure closes and joins both readers.
func enumIndexGenerationReaders(t *testing.T, receipt enumIndexGenerationReceipt) func() {
	t.Helper()
	stop := make(chan struct{})
	started := make(chan struct{}, 2)
	finished := make(chan error, 2)
	for range 2 {
		go func() {
			started <- struct{}{}
			for {
				if !reflect.DeepEqual(receipt.root.Entries, receipt.entries) || !reflect.DeepEqual(receipt.writes, receipt.writeCopy) || !reflect.DeepEqual(receipt.versions, receipt.versionCopy) || !reflect.DeepEqual(receipt.codec.EnumMembers(), receipt.members) || !reflect.DeepEqual(receipt.journal.Clone(), receipt.journalCopy) || !reflect.DeepEqual(*receipt.parent, receipt.parentCopy) || !reflect.DeepEqual(*receipt.descriptor, receipt.indexCopy) {
					finished <- fmt.Errorf("concurrent reader observed mutation of retained enum/index backing")
					return
				}
				select {
				case <-stop:
					finished <- nil
					return
				default:
				}
			}
		}()
	}
	<-started
	<-started
	var once sync.Once
	join := func() {
		once.Do(func() {
			close(stop)
			for range 2 {
				if err := <-finished; err != nil {
					t.Error(err)
				}
			}
		})
	}
	t.Cleanup(join)
	return join
}

func enumIndexGenerationPublicationFixture(t *testing.T, e *core.Engine, s *session.Session) ids.TxRef {
	t.Helper()
	for _, q := range []string{"CREATE TYPE enum_generation_mood AS ENUM('pending')", "CREATE TABLE enum_generation_heap(status enum_generation_mood,id INT)", "INSERT INTO enum_generation_heap VALUES('pending',1),('pending',2)", "CREATE TABLE enum_generation_unrelated(n INT)", "INSERT INTO enum_generation_unrelated VALUES(90)", "CREATE INDEX enum_generation_unrelated_idx ON enum_generation_unrelated(n)"} {
		enginetest.SessionSQL(t, s, q)
	}
	tx := enginetest.TransactionBegin(t, s)
	for _, q := range []string{"INSERT INTO enum_generation_heap VALUES('pending',3)", "CREATE INDEX enum_generation_idx ON enum_generation_heap(status)", "UPDATE enum_generation_heap SET id=4 WHERE id=3", "CREATE TABLE enum_generation_private_sibling(n INT)", "CREATE INDEX enum_generation_private_sibling_idx ON enum_generation_private_sibling(n)"} {
		enginetest.SessionSQL(t, s, q)
	}
	return tx
}

func TestEnumIndexLayoutGenerationAtomicPublication(t *testing.T) {
	for _, statement := range []string{"enum", "column"} {
		for _, veto := range []string{"none", "cancel", "epoch", "caller", "source-replaced", "source-generation", "source-ends", "source-length", "source-descriptor", "terminal"} {
			t.Run(statement+"/"+veto, func(t *testing.T) {
				e := engine.New()
				s := enginetest.StartSession(t, e, nil)
				tx := enumIndexGenerationPublicationFixture(t, e, s)
				if statement == "column" {
					enginetest.SessionSQL(t, s, "ALTER TYPE enum_generation_mood ADD VALUE 'inhibited'")
				}
				before := enumIndexGenerationCapture(t, e, s)
				enumIndexGenerationOutputs(t, before, 0, 1)
				join := enumIndexGenerationReaders(t, before)
				unrelated, unrelatedRoot := btreeRoot(t, e, "enum_generation_unrelated_idx")
				unrelatedEntries := transactionalIndexSnapshot(unrelatedRoot)
				view := e.PrivateCatalogLocked(before.journal)
				sibling := enginetest.CatalogRef(t, e, view, "enum_generation_private_sibling_idx").Address
				siblingState := index.JournalIndexesOrZero(before.journal)
				siblingRoot, siblingCandidate := siblingState.UniqueRoots[sibling], siblingState.Candidates[sibling]
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				op, err := s.Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				scope, err := e.StartStatement(op)
				if err != nil {
					op.Done()
					t.Fatal(err)
				}
				defer func() { scope.Finish(errors.New("unit cleanup")); op.Done() }()
				q := "ALTER TYPE enum_generation_mood ADD VALUE 'inhibited'"
				if statement == "column" {
					q = "ALTER TABLE enum_generation_heap ADD COLUMN extra TEXT"
				}
				if _, err := scope.Execute(enginetest.Statement(t, q), 0, nil); err != nil {
					t.Fatal(err)
				}
				// Execute is the deterministic completed-producer barrier. Finish
				// has not yet adopted either the enum or the column journal.
				if scope.Catalog == nil || scope.Catalog == before.journal || s.Transactions.Catalog != before.journal || e.Catalog.State.Get(before.descriptor.Address()) != nil || e.Storage.IndexRoots[before.descriptor.Address()] != nil {
					t.Fatal("completed statement leaked/adopted its private candidate before Finish")
				}
				staged := index.JournalIndexesOrZero(scope.Catalog)
				if staged.UniqueRoots[before.descriptor.Address()] == nil || staged.Candidates[before.descriptor.Address()].Root != staged.UniqueRoots[before.descriptor.Address()] {
					t.Fatal("completed actual enum/column producer did not retain its real root receipt")
				}
				var primary error
				switch veto {
				case "cancel":
					cancel()
				case "epoch":
					if err := engine.Reset(t.Context(), e); err != nil {
						t.Fatal(err)
					}
				case "caller":
					primary = errors.New("caller rejected completed enum utility")
				case "source-replaced", "source-generation", "source-ends", "source-length":
					e.Mu.Lock()
					current := e.Storage.Relations[before.parent.Address()]
					replacement := *current
					switch veto {
					case "source-generation":
						replacement.Generation++
					case "source-ends":
						replacement.Ends = &storage.DeletionRoot{}
					case "source-length":
						replacement.Versions = slices.Clone(current.Versions[:len(current.Versions)-1])
					}
					e.Storage.InstallRelationLocked(before.parent.Address(), &replacement)
					e.Mu.Unlock()
				case "source-descriptor":
					base := e.CatalogRead()
					stage := e.BeginCatalogStage(base)
					defer e.DiscardCatalogStage(stage)
					replacement := *base.MustRelation(before.parent.Address())
					replacement.ObjectRevision++
					if err := stage.Replace(&replacement); err != nil {
						t.Fatal(err)
					}
					prepared, err := e.ValidateCatalogStage(t.Context(), stage)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: base.Epoch()}); err != nil {
						t.Fatal(err)
					}
				case "terminal":
					s.RequestEnd(session.Terminated)
				}
				accepted := indexMaintenanceCapture(e)
				frontier, row, version := e.Visibility.CommitSeq(), e.Storage.RowFrontier(), e.Storage.VersionFrontier()
				err = scope.Finish(primary)
				if veto != "none" {
					if err == nil {
						t.Fatal("Finish accepted a canceled/stale/rejected/terminal enum producer")
					}
					if veto == "cancel" && !errors.Is(err, context.Canceled) || veto == "caller" && !errors.Is(err, primary) || veto == "epoch" && !errors.Is(err, ids.ErrCatalogReplaced) {
						t.Fatal("Finish lost the primary publication veto", err)
					}
					indexMaintenanceUnchanged(t, e, accepted)
					if e.Visibility.CommitSeq() != frontier || e.Storage.RowFrontier() != row || e.Storage.VersionFrontier() != version {
						t.Fatal("rejected Finish advanced physical/publication frontiers")
					}
					before.immutable(t)
					op.Done()
					if veto == "terminal" {
						if err := s.WaitEnd(t.Context()); err != nil {
							t.Fatal(err)
						}
					} else {
						enginetest.TransactionEnd(t, s, tx, false)
					}
				} else {
					if err != nil {
						t.Fatal("Finish rejected its live genuine producer", err)
					}
					adopted := enumIndexGenerationCapture(t, e, s)
					enumIndexGenerationOutputs(t, adopted, 0, 1)
					enumIndexGenerationOutputTransport(t, before, adopted)
					enumIndexGenerationPhysical(t, adopted.descriptor, adopted.parent, adopted.root, adopted.store)
					j := index.JournalIndexesOrZero(adopted.journal)
					if j.UniqueRoots[sibling] != siblingRoot || j.Candidates[sibling] != siblingCandidate || adopted.codec == before.codec && statement == "enum" {
						t.Fatal("Finish lost sibling authority or failed to adopt the new enum generation")
					}
					if err := scope.Finish(nil); err != nil || s.Transactions.Catalog != adopted.journal {
						t.Fatal("repeated Finish adopted the producer twice", err)
					}
					op.Done()
					enginetest.TransactionEnd(t, s, tx, true)
					published, root := btreeRoot(t, e, "enum_generation_idx")
					parent := e.Catalog.State.MustRelation(published.SQLIndex.Parent)
					enumIndexGenerationPhysical(t, published, parent, root, e.Storage.Relations[parent.Address()])
					if len(root.Entries) != 3 || e.Visibility.CommitSeq() != frontier+1 {
						t.Fatal("completed producer did not publish actual rows/root at one frontier")
					}
					before.immutable(t)
				}
				join()
				if !reflect.DeepEqual(unrelatedRoot.Entries, unrelatedEntries) || veto != "epoch" && e.Storage.IndexRoots[unrelated.Address()] != unrelatedRoot {
					t.Fatal("enum publication/veto mutated an unrelated committed root")
				}
				if index.JournalIndexesOrZero(before.journal).UniqueRoots[sibling] != siblingRoot || index.JournalIndexesOrZero(before.journal).Candidates[sibling] != siblingCandidate {
					t.Fatal("publication/veto mutated retained private sibling authority")
				}
				if s.Transactions.Catalog != nil || s.Transactions.Writes != nil || s.Transactions.Deletes != nil {
					t.Fatal("terminal transaction retained enum root/output authority")
				}
				enginetest.LockEmpty(t, e)
			})
		}
	}
}

func TestEnumIndexLayoutGenerationCommitFences(t *testing.T) {
	for _, veto := range []string{"none", "cancel", "frontier", "source-replaced"} {
		t.Run(veto, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			tx := enumIndexGenerationPublicationFixture(t, e, s)
			for _, q := range []string{"ALTER TYPE enum_generation_mood ADD VALUE 'inhibited'", "ALTER TABLE enum_generation_heap ADD COLUMN extra TEXT"} {
				enginetest.SessionSQL(t, s, q)
			}
			receipt := enumIndexGenerationCapture(t, e, s)
			join := enumIndexGenerationReaders(t, receipt)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			op, err := s.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			switch veto {
			case "cancel":
				cancel()
			case "frontier":
				e.Mu.Lock()
				e.Visibility.SetCommitSeqLocked(ids.CommitSeq(^uint64(0)))
				e.Mu.Unlock()
			case "source-replaced":
				e.Mu.Lock()
				replacement := *e.Storage.Relations[receipt.parent.Address()]
				e.Storage.InstallRelationLocked(receipt.parent.Address(), &replacement)
				e.Mu.Unlock()
			}
			accepted := indexMaintenanceCapture(e)
			frontier, row, version := e.Visibility.CommitSeq(), e.Storage.RowFrontier(), e.Storage.VersionFrontier()
			_, err = e.Commit(op, tx)
			op.Done()
			if veto == "none" {
				if err != nil || e.Visibility.CommitSeq() != frontier+1 {
					t.Fatal("live successor did not commit exactly once", err)
				}
				published, root := btreeRoot(t, e, "enum_generation_idx")
				parent := e.Catalog.State.MustRelation(published.SQLIndex.Parent)
				enumIndexGenerationPhysical(t, published, parent, root, e.Storage.Relations[parent.Address()])
			} else {
				if err == nil || veto == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatal("commit accepted stale/canceled/exhausted publication", err)
				}
				indexMaintenanceUnchanged(t, e, accepted)
				if e.Visibility.CommitSeq() != frontier || e.Storage.RowFrontier() != row || e.Storage.VersionFrontier() != version {
					t.Fatal("rejected commit advanced publication or tuple frontiers")
				}
			}
			join()
			receipt.immutable(t)
			if s.TransactionState() != session.TxIdle || s.Transactions.Catalog != nil || s.Transactions.Writes != nil || s.Transactions.Deletes != nil {
				t.Fatal("completed commit retained terminal generation authority")
			}
			enginetest.LockEmpty(t, e)
		})
	}
}
