package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

func TestPgStatStatementsCountersNested(t *testing.T) {
	for _, mode := range []string{"top","all"} {
		for _, volatility := range []string{"VOLATILE","IMMUTABLE"} {
			t.Run(mode+"_"+volatility,func(t *testing.T) {
				e := engine.New()
				s := enginetest.StartSession(t,e,nil)
				for _, q := range []string{"SET pg_stat_statements.track='none'","CREATE TABLE counter_nested(k integer)","INSERT INTO counter_nested VALUES(1)","CREATE FUNCTION counter_nested_fn() RETURNS integer LANGUAGE sql "+volatility+" AS 'SELECT k FROM counter_nested AS counter_nested_child'","SET pg_stat_statements.track_utility=off","SET pg_stat_statements.track_planning=on","SET pg_stat_statements.track='"+mode+"'"} { pgssOwnerExec(t,e,s,q) }
				// SQLRoutineExpr is FoldNever: enclosing immutable conversion
				// may evaluate it during preflight, but does not make its
				// nonconstant child a persistent fold input. A separate literal
				// conversion supplies actual accepted fold backing in both modes.
				const query = "SELECT counter_nested_fn()::text AS counter_nested_parent, 7::text AS counter_nested_fold"
				portal, command := pgssCounterPortal(t, e, s, query)
				binding := core.PayloadOf(s).PreparedSQL.Binding("counter_portal")
				root, folds := binding.Resource.Root, *binding.Folds
				entry := enginetest.PreparedFoldEntry(t, binding.Entry.Description, sqltype.Text, "7")
				if len(folds.Entries) != 1 || &binding.Folds.Entries[0] != &folds.Entries[0] {
					t.Fatal("missing genuine independent literal fold backing")
				}
				parent := pgssPhaseEntry(t, e, "counter_nested_parent")
				if parent.Planning.Count != 1 || parent.Execution.Count != 0 {
					t.Fatal("actual parent planning acceptance", parent)
				}
				wantPlan := int64(0)
				if volatility == "IMMUTABLE" {
					wantPlan = 1
				}
				if _, folded := root.Expressions[0].(plan.LiteralExpr); folded != (volatility == "IMMUTABLE") {
					t.Fatal("actual routine conversion preflight phase", volatility, root.Expressions[0])
				}
				if parent.Counters.SharedRead+parent.Counters.SharedHit != wantPlan {
					t.Fatal("actual Bind planning work", parent)
				}
				command(func(scope *core.StatementScope) error {
					_, err := scope.ExecuteProtocolPortal("counter_portal", 0, func([]session.ProtocolField) error { return nil }, nil, nil, core.CopyProtocolHooks{})
					if err != nil {
						return err
					}
					pending := core.PortalStatementCounters(portal)
					if pending.SharedRead+pending.SharedHit != 1-wantPlan {
						t.Fatal("nested work entered wrong phase", parent, pending)
					}
					return nil
				})
				child := pgssPhaseEntry(t, e, "counter_nested_child")
				if mode == "all" && (child.Execution.Count != 1 || child.Counters.SharedRead+child.Counters.SharedHit != 1) || mode == "top" && child.Execution.Count != 0 {
					t.Fatal("tracked/untracked child admission", mode, child)
				}
				if binding.Resource.Root != root || binding.Folds.Generation != folds.Generation ||
					&binding.Folds.Entries[0] != &folds.Entries[0] ||
					!enginetest.SamePublishedFold(entry, binding.Folds.Entries[0]) ||
					!sqltype.SameDatum(entry.Value, binding.Folds.Entries[0].Value) {
					t.Fatal("execution replaced actual accepted graph/fold provenance")
				}
				command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("counter_portal") })
				parent = pgssPhaseEntry(t, e, "counter_nested_parent")
				if parent.Execution.Count != 1 || parent.Counters.SharedRead+parent.Counters.SharedHit != 1 {
					t.Fatal("inclusive event was duplicated at child/parent terminal", parent)
				}
			})
		}
	}
}
