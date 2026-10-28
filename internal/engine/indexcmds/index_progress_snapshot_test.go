package indexcmds_test

import (
	"context"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/indexcmds"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
)

func snapshotProgressFacts(t *testing.T, e *core.Engine, op *session.Operation) []pgcatalog.CreateIndexProgressFact {
	t.Helper()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	r := core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: auth})
	var facts []pgcatalog.CreateIndexProgressFact
	if err := r.VisitCreateIndexProgress(op.Context(), func(f pgcatalog.CreateIndexProgressFact) error {
		facts = append(facts, f)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return facts
}

func snapshotProgressAwait(t *testing.T, e *core.Engine, observer *session.Operation, predicate func(pgcatalog.CreateIndexProgressFact) bool) pgcatalog.CreateIndexProgressFact {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		facts := snapshotProgressFacts(t, e, observer)
		if len(facts) == 1 && predicate(facts[0]) {
			return facts[0]
		}
		if time.Now().After(deadline) {
			t.Fatal("actual snapshot progress boundary not reached", facts)
		}
		runtime.Gosched()
	}
}

// Admit and publish the real concurrent command's initial invalid descriptor,
// then isolate its actual old-snapshot wait seam. No counter setters or fake
// progress source drive this test; the public test covers the whole dispatcher.
func snapshotProgressScope(t *testing.T, e *core.Engine, builder *session.Session) (*core.StatementScope, *session.Operation, ids.ObjectAddress, uint64, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	op, err := builder.Acquire(ctx)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		cancel()
		op.Done()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		if !scope.Finished {
			_ = scope.Finish(context.Canceled)
		}
		op.Done()
	})
	syntax := enginetest.Statement(t, "CREATE INDEX CONCURRENTLY snapshot_progress_pending ON snapshot_progress_table(n)").(*parser.CreateIndex)
	if err := scope.Gate(syntax); err != nil {
		t.Fatal(err)
	}
	if err := scope.AcquireRelation(syntax, true); err != nil {
		t.Fatal(err)
	}
	var stage *core.CatalogStage
	defer func() {
		if stage != nil {
			e.DiscardCatalogStage(stage)
		}
	}()
	var address ids.ObjectAddress
	func() {
		e.Mu.Lock()
		defer e.Mu.Unlock()
		auth, err := e.StatementAuthorizationLocked(op, syntax)
		if err != nil {
			t.Fatal(err)
		}
		if err := scope.CaptureIdentityLocked(auth); err != nil {
			t.Fatal(err)
		}
		parent := auth.Catalog.MustRelation(scope.LockedRef.Address)
		stage = e.BeginCatalogStage(auth.Catalog)
		stage.AuthorizeLocked(auth, op.Session)
		stage.Indexes().Output = scope.Output
		host := tablecmds.TableHost{DDLHost: core.DDLHost{E: e, Op: op, CatalogStage: stage}}
		created, err := index.PrepareCreate(host, auth, parent, syntax, scope.Input, nil)
		if err != nil {
			t.Fatal(err)
		}
		address = created.Relation.Address()
		stage.Indexes().Progress = scope.BeginCreateIndexProgressLocked(parent.Address(), address)
		scope.IndexProgressCommand("CREATE INDEX CONCURRENTLY", 0)
		if err := e.VisibilityWriteLocked(scope.TxRef); err != nil {
			t.Fatal(err)
		}
		if err := index.StageCreate(host, parent, created.Relation, syntax.Only); err != nil {
			t.Fatal(err)
		}
	}()
	prepared, err := e.ValidateCatalogStage(ctx, stage)
	if err == nil {
		_, err = e.PublishCatalogOperation(op, scope, prepared, core.PublicationToken{Epoch: e.CatalogRead().Epoch()})
	}
	if err != nil {
		t.Fatal(err)
	}
	scope.IndexProgressCommand("CREATE INDEX CONCURRENTLY", address.ObjectID)
	var xmin uint64
	func() {
		e.Mu.Lock()
		defer e.Mu.Unlock()
		e.Visibility.ResetPhaseLocked(scope.TxRef)
		if err := e.VisibilityWriteLocked(scope.TxRef); err != nil {
			t.Fatal(err)
		}
		var err error
		xmin, err = e.VisibilitySnapshotLocked(scope.TxRef, true)
		if err != nil {
			t.Fatal(err)
		}
	}()
	scope.IndexProgressPhase("waiting for old snapshots")
	return scope, op, address, xmin, cancel
}

func TestCreateIndexProgressOldSnapshotCohortRecheck(t *testing.T) {
	for _, disappear := range []bool{true, false} {
		name := "successive_waits"
		n := 2
		if disappear {
			name, n = "remaining_disappear", 3
		}
		t.Run(name, func(t *testing.T) {
			e := engine.New()
			admin := sessionStart(t, e, nil)
			builder := sessionStart(t, e, nil)
			sessionSQL(t, admin, "CREATE TABLE snapshot_progress_table(n INT)")
			sessionSQL(t, admin, "CREATE TABLE snapshot_progress_unrelated(n INT)")
			sessionSQL(t, admin, "INSERT INTO snapshot_progress_unrelated VALUES(1)")
			holders := make(map[int64]*session.Session, n)
			ended := make(map[int64]bool, n)
			for range n {
				holder := sessionStart(t, e, nil)
				sessionSQL(t, holder, "BEGIN ISOLATION LEVEL REPEATABLE READ")
				rows := sessionSQL(t, holder, "SELECT n FROM snapshot_progress_unrelated")
				rows.Rows.Close()
				pid := int64(holder.Token().ID())
				holders[pid] = holder
				t.Cleanup(func() {
					if !ended[pid] {
						sessionSQL(t, holder, "ROLLBACK")
					}
				})
			}
			scope, op, address, xmin, cancel := snapshotProgressScope(t, e, builder)
			observer, err := admin.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer observer.Done()
			e.Mu.Lock()
			cohort := e.Visibility.OlderSnapshotsLocked(scope.TxRef, xmin)
			e.Mu.Unlock()
			if len(cohort) != n {
				t.Fatal("real endangered snapshot cohort", cohort, xmin)
			}
			done := make(chan error, 1)
			joined := false
			go func() { done <- indexcmds.WaitOlderIndexSnapshots(scope, xmin, address) }()
			defer func() {
				cancel()
				if !joined {
					select {
					case <-done:
					case <-time.After(6 * time.Second):
						t.Error("snapshot wait did not terminate")
					}
				}
			}()
			first := snapshotProgressAwait(t, e, observer, func(f pgcatalog.CreateIndexProgressFact) bool {
				return f.LockersTotal == int64(n) && f.LockersDone == 0 && holders[f.CurrentLockerPID] != nil
			})
			// The reported participant must really own the snapshot and have a
			// pending completion wait. No ordering of the frozen map is assumed.
			concurrentUnitCompletion(t, e, holders[first.CurrentLockerPID].Transactions.Ticket, builder)
			end := func(pid int64) {
				sessionSQL(t, holders[pid], "ROLLBACK")
				ended[pid] = true
			}
			lastPID := first.CurrentLockerPID
			if disappear {
				for pid := range holders {
					if pid != first.CurrentLockerPID {
						end(pid)
					}
				}
				still := snapshotProgressFacts(t, e, observer)
				if len(still) != 1 || still[0].LockersTotal != 3 || still[0].LockersDone != 0 || still[0].CurrentLockerPID != first.CurrentLockerPID {
					t.Fatal("remaining holders ending fabricated completed wait", still)
				}
				end(first.CurrentLockerPID)
			} else {
				end(first.CurrentLockerPID)
				next := snapshotProgressAwait(t, e, observer, func(f pgcatalog.CreateIndexProgressFact) bool {
					return f.LockersTotal == 2 && f.LockersDone == 1 && f.CurrentLockerPID != first.CurrentLockerPID && holders[f.CurrentLockerPID] != nil
				})
				lastPID = next.CurrentLockerPID
				concurrentUnitCompletion(t, e, holders[lastPID].Transactions.Ticket, builder)
				end(lastPID)
			}
			select {
			case err := <-done:
				joined = true
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(6 * time.Second):
				t.Fatal("actual old-snapshot wait did not finish")
			}
			final := snapshotProgressFacts(t, e, observer)
			if len(final) != 1 || final[0].Phase != "waiting for old snapshots" || final[0].LockersTotal != int64(n) || final[0].LockersDone != 2 || final[0].CurrentLockerPID != lastPID {
				t.Fatal("original total, reached ordinal, or final PID not retained", final)
			}
			retained := final[0]
			_ = scope.Finish(context.Canceled)
			op.Done()
			if facts := snapshotProgressFacts(t, e, observer); len(facts) != 0 {
				t.Fatal("terminal command retained progress", facts)
			}
			if final[0] != retained {
				t.Fatal("terminal cleanup mutated retained snapshot", final, retained)
			}
		})
	}
}
