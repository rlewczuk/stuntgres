package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/lock"
)

// PostgreSQL's old-tuple buffer pin prevents this particular surgery while a
// BEFORE callback is paused. This native proof instead checks the logical
// model's exact Stage/readmission invariant with real owners, physical storage,
// an actual advisory wait and an actual second tuple wait. It is not a claim
// that the impossible pinned PostgreSQL schedule was observed.
func TestPgsurgeryFreezeProducerStageRecheck(t *testing.T) {
	for _, accept := range []bool{true, false} {
		name := "predicate_rejects"
		if accept {
			name = "selected_rhs_without_before_replay"
		}
		t.Run(name, func(t *testing.T) {
			e := engine.New()
			setup := enginetest.StartSession(t, e, nil)
			worker := enginetest.StartSession(t, e, nil)
			peer := enginetest.StartSession(t, e, nil)
			gate := enginetest.StartSession(t, e, nil)
			for _, q := range []string{
				// This proof isolates Stage tuple readmission. An immediate
				// unique index adds a legitimate key-claim wait after BEFORE
				// and before Stage, independently of the repaired tuple key.
				"CREATE TABLE t(id INT, v TEXT)",
				"INSERT INTO t VALUES(1,'A')",
				"CREATE SEQUENCE rhs",
				"CREATE TABLE before_audit(old_v TEXT, new_v TEXT)",
				"CREATE TABLE after_audit(v TEXT)",
				"CREATE FUNCTION freeze_before() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN INSERT INTO before_audit VALUES(OLD.v,NEW.v); NEW.v := NEW.v || ':T'; PERFORM pg_advisory_xact_lock(93217); RETURN NEW; END $$",
				"CREATE TRIGGER freeze_before BEFORE UPDATE ON t FOR EACH ROW WHEN (NEW.v <> 'B') EXECUTE FUNCTION freeze_before()",
				"CREATE FUNCTION freeze_after() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN INSERT INTO after_audit VALUES(NEW.v); RETURN NEW; END $$",
				"CREATE TRIGGER freeze_after AFTER UPDATE ON t FOR EACH ROW EXECUTE FUNCTION freeze_after()",
			} {
				enginetest.SessionSQL(t, setup, q)
			}
			enginetest.SessionSQL(t, gate, "BEGIN")
			enginetest.SessionSQL(t, gate, "SELECT pg_advisory_xact_lock(93217)")
			gateActive, peerActive := true, false
			op := enginetest.TransactionOperation(t, worker)
			ctx, cancel := context.WithTimeout(op.Context(), enginetest.Liveness)
			op.Ctx = ctx
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			var done chan error
			var source *exec.Source
			var result core.Result
			t.Cleanup(func() {
				cancel()
				defer op.Done()
				// Finish owns Owner.Admission. Neither an assertion nor a
				// teardown error may bypass cancellation, real worker join,
				// and the scope finalizer before session shutdown.
				defer func() {
					if done != nil {
						<-done
					}
					if result.Rows != nil {
						_ = result.Rows.Close()
					}
					if source != nil {
						source.Close()
					}
					if !scope.Finished {
						_ = scope.Finish(context.Canceled)
					}
				}()
				if gateActive {
					enginetest.SessionSQL(t, gate, "ROLLBACK")
				}
				if peerActive {
					enginetest.SessionSQL(t, peer, "ROLLBACK")
				}
			})
			q := "UPDATE t SET v=v || ':' || nextval('rhs')::text WHERE id=1"
			if !accept {
				q += " AND v='A'"
			}
			q += " RETURNING v"
			statement := enginetest.Statement(t, q)
			if err := scope.Gate(statement); err != nil {
				t.Fatal(err)
			}
			// Execute may reattach scope fields. Retain the actual admitted
			// ticket before starting it rather than racing on LockTicket.
			ticket := scope.LockTicket
			if _, err := scope.Describe(statement); err != nil {
				t.Fatal(err)
			}
			ref, store := scope.LockedRef, scope.LockedStorage
			view, err := scope.AcquireDataView(ref)
			if err != nil {
				t.Fatal(err)
			}
			source, err = exec.CaptureSource(op.Context(), view, ref)
			if err != nil {
				t.Fatal(err)
			}
			original := source.Committed[0]
			done = make(chan error, 1)
			go func() { var err error; result, err = scope.Execute(statement, 0, nil); done <- err; close(done) }()
			advisory := lock.Resource{Database: e.Catalog.Domain, Kind: lock.AdvisoryBigintResource, Low: 93217}
			// Transaction-scoped advisory work belongs to the transaction
			// ticket, not the session-scoped Advisory.Ticket.
			freezeTupleWait(t, e, ticket, advisory, done)
			ordinary := lock.Resource{ObjectRef: ref, Row: original.Row}
			if !e.Locks.Holds(ticket, ordinary, lock.ForNoKeyUpdate) {
				t.Fatal("BEFORE did not retain the real original row grant")
			}
			key := freezeTupleRepair(t, e, store, original, ref)
			enginetest.SessionSQL(t, peer, "BEGIN")
			peerActive = true
			enginetest.SessionSQL(t, peer, "UPDATE t SET v='B' WHERE id=1")
			enginetest.SessionSQL(t, gate, "COMMIT")
			gateActive = false
			freezeTupleWait(t, e, ticket, key, done)
			peerXID := peer.Transactions.Ticket.AssignedPublicXID()
			workerXID := ticket.AssignedPublicXID()
			if peerXID == 0 || workerXID == 0 || peerXID == workerXID {
				t.Fatal("missing distinct actual event owners", peerXID, workerXID)
			}
			assertOriginalEvents := func() {
				t.Helper()
				facts := []lock.TupleMaintenance{
					{Tuple: lock.MultiTuple{Resource: key, Version: original.Version}},
					{Tuple: lock.MultiTuple{Resource: ordinary, Version: original.Version}},
				}
				if err := e.Locks.CaptureTupleMaintenance(op.Context(), ticket, facts); err != nil {
					t.Fatal(err)
				}
				if !facts[0].Present || facts[0].Multi || facts[0].Xmax != peerXID {
					t.Fatal("proposal changed the genuine peer updater on original A", facts[0], peerXID)
				}
				if !facts[1].Present || facts[1].Multi || facts[1].Xmax != workerXID {
					t.Fatal("repair/readmission changed the genuine original prelock", facts[1], workerXID)
				}
			}
			assertOriginalEvents()
			e.Mu.Lock()
			unaccepted := len(scope.Stage.Rows[ref.Address]) == 0
			originalText, textErr := source.Committed[0].Values[1].Text()
			retained := source.Committed[0].Version == original.Version && textErr == nil && originalText == "A"
			e.Mu.Unlock()
			if !unaccepted || !retained {
				t.Fatal("old proposal was accepted or source storage was overwritten during Stage wait")
			}
			enginetest.SessionSQL(t, peer, "COMMIT")
			peerActive = false
			freezeTupleJoin(t, done)
			// Old A's committed updater and old-key prelock are history,
			// not another updater proposal or a record to prune on retry.
			assertOriginalEvents()
			e.Mu.Lock()
			published := store.Versions[0]
			staged := scope.Stage.Rows[ref.Address]
			if accept {
				if len(staged) != 1 || staged[0].PrivateHistory == nil || staged[0].PrivateHistory.Version.Version != published.Version || published.Version == original.Version {
					e.Mu.Unlock()
					t.Fatal("accepted proposal did not use the real published successor")
				}
				actual, keyErr := exec.TupleResource(ref, staged[0], store.Headers)
				if keyErr != nil || actual != key {
					e.Mu.Unlock()
					t.Fatal("accepted successor lost its admitted anchor", actual, key, keyErr)
				}
			} else if len(staged) != 0 {
				e.Mu.Unlock()
				t.Fatal("rejected latest predicate staged a mutation")
			}
			e.Mu.Unlock()
			wantCount := int64(0)
			if accept {
				wantCount = 1
			}
			if result.Count != wantCount {
				t.Fatal("successful action count", result.Count, wantCount)
			}
			tuples := enginetest.Tuples(result)
			if accept && (len(tuples) != 1 || tuples[0][0] != "B:2") || !accept && len(tuples) != 0 {
				t.Fatal("late RETURNING", tuples)
			}
			if result.Rows != nil {
				result.Rows.Close()
			}
			if err := scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
			op.Done()
			before := enginetest.Tuples(enginetest.SessionSQL(t, setup, "SELECT old_v,new_v FROM before_audit"))
			if len(before) != 1 || before[0][0] != "A" || before[0][1] != "A:1" {
				t.Fatal("BEFORE was replayed or its completed effect was lost", before)
			}
			after := enginetest.Tuples(enginetest.SessionSQL(t, setup, "SELECT v FROM after_audit ORDER BY v"))
			if len(after) != int(wantCount)+1 || after[0][0] != "B" || accept && after[1][0] != "B:2" {
				t.Fatal("AFTER ran for an unaccepted/stale proposal", after)
			}
		})
	}
}
