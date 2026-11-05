package core_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/statistics"
)

func TestPgStatStatementsCountersRoutineRebase(t *testing.T) {
	for _, boundary := range []string{"COMMIT", "ROLLBACK"} {
		t.Run(boundary, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			for _, q := range []string{
				"SET pg_stat_statements.track='none'",
				"CREATE TABLE counter_rebase_images(k integer,v text)",
				"CREATE PROCEDURE counter_rebase_owner() LANGUAGE plpgsql AS $$BEGIN PERFORM 1; END$$",
				"SET pg_stat_statements.track_utility=on",
				"SET pg_stat_statements.track_planning=off",
				"SET pg_stat_statements.track='top'",
			} {
				pgssOwnerExec(t, e, s, q)
			}
			op, err := s.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			const query = "CALL counter_rebase_owner()"
			op.OriginalSQL, op.SQLAvailable = query, true
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(context.Canceled)
			if _, err = scope.Execute(enginetest.Statement(t, query), 0, nil); err != nil {
				t.Fatal(err)
			}
			// Retain the actual admitted utility owner before root Finish. Read
			// its lexical scratch through the existing test bridge; do not create
			// a frame, replace a sink, or add an inspection API for this regression.
			frame := reflect.ValueOf(core.ScopeSQLRoutineScratch(scope)).FieldByName("statementStats")
			if frame.IsNil() || !frame.Elem().FieldByName("admitted").Bool() || frame.Elem().FieldByName("finished").Bool() {
				t.Fatal("missing live admitted enclosing utility owner")
			}
			parent := frame.Pointer()
			assertBorrowed := func(where string) {
				t.Helper()
				scratch := reflect.ValueOf(core.ScopeSQLRoutineScratch(scope))
				if !scratch.FieldByName("statementStats").IsNil() || scratch.FieldByName("statementStatsParent").Pointer() != parent {
					t.Error(where, "lost nil-current-frame/non-nil-borrowed-parent ownership")
				}
			}
			restoreOuter := scope.ActivateSQLRoutine(nil, e.CatalogRead())
			assertBorrowed("first untracked activation")
			restoreInner := scope.ActivateSQLRoutine(nil, e.CatalogRead())
			assertBorrowed("nested untracked activation")
			before, owner := scope.TxRef, scope.Owner

			// Execute a real PL transaction instruction. This reaches both the
			// current-segment reset and saved-scratch TxRef rebase, not a synthetic
			// scratch reconstruction or a callback that echoes expected counters.
			_, err = core.EngineExecuteStatement(e, scope, enginetest.Statement(t, "DO $$BEGIN "+boundary+"; END$$"), 0, nil)
			if err != nil {
				_ = restoreOuter(restoreInner(err))
				t.Fatal(err)
			}
			if scope.TxRef == before || scope.Owner != owner || scope.Owner.Ref != scope.TxRef {
				t.Fatal("PL instruction did not rotate the actual attached transaction incarnation")
			}
			assertBorrowed("after actual procedural rotation")
			_, err = scope.Execute(enginetest.Statement(t, "INSERT INTO counter_rebase_images VALUES(1,'inner')"), 0, nil)
			if err = restoreInner(err); err != nil {
				_ = restoreOuter(err)
				t.Fatal(err)
			}
			assertBorrowed("after saved untracked scratch restoration")
			_, err = scope.Execute(enginetest.Statement(t, "INSERT INTO counter_rebase_images VALUES(2,'outer')"), 0, nil)
			if err = restoreOuter(err); err != nil {
				t.Fatal(err)
			}
			scratch := reflect.ValueOf(core.ScopeSQLRoutineScratch(scope))
			if scratch.FieldByName("statementStats").Pointer() != parent || !scratch.FieldByName("statementStatsParent").IsNil() {
				t.Error("restoration replaced the enclosing frame or invented an ancestor")
			}
			want := statistics.StatementCounters{SharedDirtied: 2, WALRecords: 2, WALBytes: 144}
			if got := core.ScopeStatementCounters(scope); got != want {
				t.Error("accepted successor images missed or duplicated the actual enclosing owner", got, want)
			}
			if err = scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
			entry := pgssPhaseEntry(t, e, query)
			if entry.Execution.Count != 1 || entry.Rows != 0 || entry.Counters != want || !frame.Elem().FieldByName("finished").Bool() {
				t.Fatal("root terminal lost its successful inclusive sample or retained a live owner", entry)
			}
		})
	}
}
