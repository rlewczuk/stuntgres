package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/lock"
)

// A real middle candidate changes during exact-key readmission. The failed
// batched window must accept nothing, replay just that source occurrence, and
// preserve both unrelated pending targetlists and preprojected RETURNING cells.
func TestPgsurgeryFreezeMergeStageRecheck(t *testing.T) {
	for _, variant := range []string{"one", "zero", "two"} {
		t.Run(variant, func(t *testing.T) {
			e := engine.New()
			worker := enginetest.StartSession(t, e, nil)
			peer := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "INSERT INTO t VALUES(1,10),(2,20),(3,30)", "CREATE SEQUENCE merge_rhs"} {
				enginetest.SessionSQL(t, worker, q)
			}
			ctx, cancel := context.WithTimeout(context.Background(), enginetest.Liveness)
			defer cancel()
			query := "MERGE INTO t USING (VALUES(1),(2),(3)) s(id) ON t.id=s.id"
			if variant == "two" {
				query += " AND t.v<100"
			}
			query += " WHEN MATCHED"
			if variant == "zero" {
				query += " AND t.v<100"
			}
			query += " THEN UPDATE SET v=t.v+nextval('merge_rhs')::int"
			if variant == "two" {
				query += " WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=t.v+1000 WHEN NOT MATCHED BY TARGET THEN INSERT(id,v) VALUES(s.id,nextval('merge_rhs')::int)"
			}
			query += " RETURNING v"
			op, scope, binding, target, inputs := updateFromBoundSources(t, e, worker, ctx, query)
			var state *exec.State
			var rows *dml.MergeRows
			var done chan error
			peerActive := false
			t.Cleanup(func() {
				cancel()
				defer op.Done()
				defer func() {
					if done != nil {
						<-done
					}
					if rows != nil {
						rows.ReleaseProducer()
						if rows.Returning != nil {
							_ = rows.Returning.Close()
						}
					}
					if state != nil {
						state.ReleaseMemberships()
					}
					if !scope.Finished {
						_ = scope.Finish(context.Canceled)
					}
				}()
				if peerActive {
					enginetest.SessionSQL(t, peer, "ROLLBACK")
				}
			})
			h := scope.WriteHost()
			unique, err := dml.BindUniqueWrite(h, binding.Ref)
			if err != nil {
				t.Fatal(err)
			}
			if err := exec.SelectQuerySources(ctx, inputs); err != nil {
				t.Fatal(err)
			}
			state = &exec.State{Source: inputs, SourceMode: exec.SourceReusable, Input: scope.Input, Output: scope.Output}
			cursor, err := exec.OpenNode(ctx, state, binding.Merge.Source)
			if err != nil {
				t.Fatal(err)
			}
			rows, err = dml.AssembleMergeRows(h, core.ScopeMergeInput(scope, ctx, binding, target, nil, nil, "public"), &cursor, state, len(unique.Keys) != 0 || unique.Checked)
			if err != nil {
				t.Fatal(err)
			}
			// Proposal construction is not an updating heap event. These
			// genuine grants have no BEFORE lock-only work in this fixture.
			facts := make([]lock.TupleMaintenance, len(rows.Predecessors))
			for i, predecessor := range rows.Predecessors {
				facts[i].Tuple = lock.MultiTuple{Resource: lock.Resource{ObjectRef: binding.Ref, Row: predecessor.Row}, Version: predecessor.Version}
			}
			if err := e.Locks.CaptureTupleMaintenance(ctx, scope.LockTicket, facts); err != nil {
				t.Fatal(err)
			}
			for _, fact := range facts {
				if fact.Present {
					t.Fatal("unaccepted MERGE proposal published an updater", fact)
				}
			}
			if len(scope.Stage.Rows[binding.Ref.Address]) != 0 || len(rows.Actions) != 3 {
				t.Fatal("MERGE crossed its explicit producer/Stage boundary", rows.Actions, scope.Stage.Rows)
			}
			original := rows.Predecessors[1]
			key := freezeTupleRepair(t, e, binding.Storage, original, binding.Ref)
			enginetest.SessionSQL(t, peer, "BEGIN")
			peerActive = true
			enginetest.SessionSQL(t, peer, "UPDATE t SET v=200 WHERE id=2")
			done = make(chan error, 1)
			go func() { done <- dml.StageMergeRows(scope.ActivateRuntime(ctx), h, rows, binding.Ref); close(done) }()
			freezeTupleWait(t, e, scope.LockTicket, key, done)
			e.Mu.Lock()
			acceptedEarly := len(scope.Stage.Rows[binding.Ref.Address])
			e.Mu.Unlock()
			if acceptedEarly != 0 {
				t.Fatal("failed batched window accepted a prefix before actual readmission")
			}
			enginetest.SessionSQL(t, peer, "COMMIT")
			peerActive = false
			freezeTupleJoin(t, done)
			staged := scope.Stage.Rows[binding.Ref.Address]
			wanted := []int64{11, 204, 33}
			final := wanted
			if variant == "zero" {
				wanted, final = []int64{11, 33}, []int64{11, 200, 33}
			}
			if variant == "two" {
				wanted, final = []int64{11, 1200, 4, 33}, []int64{11, 4, 1200, 33}
			}
			if len(staged) != len(wanted) || rows.StagedActions != len(wanted) {
				t.Fatal("lost or duplicated actual MERGE actions", rows.StagedActions, staged)
			}
			for i, want := range wanted {
				got, err := staged[i].Values[1].Integer()
				if err != nil || got != want {
					t.Fatal("replayed an unrelated pending targetlist or retained stale middle RHS", i, got, want, err)
				}
			}
			if variant != "zero" && (staged[1].PrivateHistory == nil || staged[1].PrivateHistory.Version.Version == original.Version) {
				t.Fatal("middle proposal retained its original predecessor")
			}
			returning := enginetest.Tuples(core.Result{Rows: rows.Returning})
			if len(returning) != len(wanted) {
				t.Fatal("pending RETURNING count", returning)
			}
			for i, want := range wanted {
				if returning[i][0] != want {
					t.Fatal("pending RETURNING replacement/order", i, returning, wanted)
				}
			}
			if err := scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
			op.Done()
			enginetest.TransactionRows(t, worker, "SELECT v FROM t ORDER BY id,v", final...)
		})
	}
}
