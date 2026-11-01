package core_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/stored"
)

// Observe a real nested body parked at its admitted relation wait. Neither a
// timer nor an injected marker stands in for the executing trigger call chain.
func deferredAncestryWait(t *testing.T, e *core.Engine, ref ids.ObjectRef, done <-chan struct{}) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	stacks := make([]byte, 64<<10)
	for {
		select {
		case <-done:
			t.Fatal("nested trigger completed before the admitted relation wait")
		default:
		}
		pending := false
		if state := e.Locks.Inspect().Resources[lock.Resource{ObjectRef: ref}]; state != nil {
			for _, waiter := range state.Pending {
				if waiter.Mode == lock.RowExclusive && !waiter.Terminal {
					pending = true
					break
				}
			}
		}
		n := runtime.Stack(stacks, true)
		if n == len(stacks) {
			stacks = make([]byte, len(stacks)*2)
			continue
		}
		parked := false
		for _, stack := range bytes.Split(stacks[:n], []byte("\n\n")) {
			if bytes.Contains(stack, []byte("(*relationRequest).wait")) && bytes.Contains(stack, []byte("(*StatementScope).fireConstraintTrigger")) {
				parked = true
				break
			}
		}
		if pending && parked {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("nested trigger never parked at its admitted relation wait")
		}
		runtime.Gosched()
	}
}

func TestTriggerDeferredActiveAncestry(t *testing.T) {
	for _, outcome := range []string{"success", "caught_error", "cancellation"} {
		t.Run(outcome, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			outer := "BEGIN INSERT INTO deferred_nested_b VALUES(NEW.id,NEW.v); INSERT INTO deferred_outer_gate VALUES(NEW.id,NEW.v); RETURN NEW; END"
			inner := "BEGIN INSERT INTO deferred_lock_helper VALUES(NEW.id,NEW.v); RETURN NEW; END"
			if outcome == "caught_error" {
				outer = `DECLARE caught integer:=0; BEGIN
				 BEGIN INSERT INTO deferred_nested_b VALUES(NEW.id,NEW.v);
				 EXCEPTION WHEN division_by_zero THEN caught:=caught+1; END;
				 IF caught<>1 THEN RAISE EXCEPTION 'nested failure was not caught'; END IF;
				 INSERT INTO deferred_outer_gate VALUES(NEW.id,caught); RETURN NEW; END`
				inner = `DECLARE caught integer:=0; BEGIN
				 BEGIN PERFORM 1/0; EXCEPTION WHEN division_by_zero THEN caught:=caught+1; END;
				 IF caught<>1 THEN RAISE EXCEPTION 'inner failure was not caught'; END IF;
				 INSERT INTO deferred_lock_helper VALUES(NEW.id,caught); PERFORM 1/0; RETURN NEW; END`
			}
			e, scope, read, relation, a := triggerUnitFixtureContext(t, ctx, outer)
			bRelation := deferredUnitNamedHelper(t, e, relation, "deferred_nested_b", 9500030)
			innerGate := deferredUnitHelper(t, e, relation)
			outerGate := deferredUnitNamedHelper(t, e, relation, "deferred_outer_gate", 9500040)
			trigger, _ := read.LookupType(2279)
			b := triggerUnitProcedure(t, e, scope, "deferred_nested_b_body", 9500005, trigger, proc.DMLTriggerInvocation, inner)
			p := deferredUnitDefinition(t, read, bRelation, b).Parts()
			p.OID, p.Constraint.OID = 9500060, 9500061
			p.Constraint.InitiallyDeferred = false
			bDefinition, err := stored.NewTrigger(p)
			if err != nil {
				t.Fatal(err)
			}
			bRelation = triggerUnitInstall(t, e, bRelation, bDefinition)
			relation = triggerUnitInstall(t, e, relation, deferredUnitDefinition(t, read, relation, a))
			triggerUnitAdmit(t, scope, relation)
			deferredUnitAccept(t, scope, read, relation, 1)
			payload := core.ScopeIndexesSlot(scope).ConstraintModes.Events[0].UserTrigger
			queued := scope.RelationPendingPreflight(e.CatalogRead(), relation.Address(), "ALTER TABLE")
			if queued == nil || queued.Error() != `stuntgres: cannot ALTER TABLE "trigger_unit_table" because it has pending trigger events` {
				t.Fatal("queued-only event acquired active-query diagnostic", queued)
			}
			innerHolder := enginetest.StartSession(t, e, nil)
			outerHolder := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, innerHolder, "BEGIN")
			enginetest.SessionSQL(t, outerHolder, "BEGIN")
			defer enginetest.SessionSQL(t, innerHolder, "ROLLBACK")
			defer enginetest.SessionSQL(t, outerHolder, "ROLLBACK")
			enginetest.SessionSQL(t, innerHolder, "LOCK TABLE deferred_lock_helper IN ACCESS EXCLUSIVE MODE")
			enginetest.SessionSQL(t, outerHolder, "LOCK TABLE deferred_outer_gate IN ACCESS EXCLUSIVE MODE")
			done := make(chan struct{})
			var firingErr error
			go func() {
				defer close(done)
				firingErr = core.ScopeCheckConstraintEvents(scope, true, nil)
			}()
			defer func() {
				cancel()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("nested trigger worker did not join")
				}
			}()
			deferredAncestryWait(t, e, e.Catalog.Domain.Ref(read.Epoch(), innerGate.Address()), done)
			active := core.ScopeActiveConstraintTriggersTest(scope)
			if len(active) != 2 || active[0] != relation.Address() || active[1] != bRelation.Address() || !scope.HasPendingConstraintEvents(relation.Address()) || !scope.HasPendingConstraintEvents(bRelation.Address()) {
				t.Fatal("nested invocation hid an active constraint ancestor", active)
			}
			if len(core.ScopeIndexesSlot(scope).ConstraintModes.Events) != 0 {
				t.Fatal("witness failed to claim outer event before nested callback")
			}
			for _, frame := range core.ScopeAfterTestFrames(scope) {
				if frame.RowCursor != frame.Rows {
					t.Fatal("unconsumed frame events mask the active-ancestry witness")
				}
			}
			for _, command := range []string{"ALTER TABLE", "TRUNCATE", "DROP TABLE", "CLUSTER"} {
				for _, target := range []struct {
					address ids.ObjectAddress
					name    string
				}{
					{relation.Address(), relation.Name()}, {bRelation.Address(), bRelation.Name()},
				} {
					err := scope.RelationPendingPreflight(e.CatalogRead(), target.address, command)
					var diagnostic interface{ SQLState() string }
					want := fmt.Sprintf(`stuntgres: cannot %s "%s" because it is being used by active queries in this session`, command, target.name)
					if !errors.As(err, &diagnostic) || diagnostic.SQLState() != "55006" || err.Error() != want {
						t.Fatal("active invocation did not precede pending-event diagnostic", err)
					}
				}
			}
			if outcome == "cancellation" {
				cancel()
			} else {
				enginetest.SessionSQL(t, innerHolder, "ROLLBACK")
				deferredAncestryWait(t, e, e.Catalog.Domain.Ref(read.Epoch(), outerGate.Address()), done)
				remaining := core.ScopeActiveConstraintTriggersTest(scope)
				if len(remaining) != 1 || remaining[0] != relation.Address() || !scope.HasPendingConstraintEvents(relation.Address()) || scope.HasPendingConstraintEvents(bRelation.Address()) {
					t.Fatal("nested unwind lost outer ancestry or resurrected inner invocation", remaining)
				}
				wantInner := 1
				if outcome == "caught_error" {
					wantInner = 0
				}
				if len(scope.Stage.Rows[bRelation.Address()]) != wantInner || len(scope.Stage.Rows[innerGate.Address()]) != wantInner || len(scope.Stage.Rows[relation.Address()]) != 1 {
					t.Fatal("caught-error restoration lost actual outer/inner stage ownership")
				}
				enginetest.SessionSQL(t, outerHolder, "ROLLBACK")
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("nested trigger completion did not unwind")
			}
			if outcome == "cancellation" {
				if !errors.Is(firingErr, context.Canceled) {
					t.Fatal(firingErr)
				}
			} else if firingErr != nil {
				t.Fatal(firingErr)
			}
			if len(core.ScopeActiveConstraintTriggersTest(scope)) != 0 || scope.HasPendingConstraintEvents(relation.Address()) || scope.HasPendingConstraintEvents(bRelation.Address()) || active[0] != (ids.ObjectAddress{}) || active[1] != (ids.ObjectAddress{}) || core.ScopeActiveSQLRoutineDepth(scope) != 0 {
				t.Fatal("completion retained active ancestry or rewound a consumed event")
			}
			if payload != nil && payload.Definition.Parts().Relation.Address != relation.Address() {
				t.Fatal("active ancestry altered immutable event provenance")
			}
			if outcome != "cancellation" {
				deferredUnitAccept(t, scope, read, relation, 2)
				if err := core.ScopeCheckConstraintEvents(scope, true, nil); err != nil {
					t.Fatal(err)
				}
				reused := core.ScopeActiveConstraintTriggersTest(scope)
				if cap(reused) < 2 || &reused[:cap(reused)][0] != &active[0] {
					t.Fatal("sequential invocations did not reuse transient ancestry backing")
				}
			}
			if err := scope.Finish(errors.New("discard ownership candidate")); err == nil {
				t.Fatal("missing candidate veto")
			}
			if core.ScopeActiveConstraintTriggersTest(scope) != nil {
				t.Fatal("Finish retained ancestry backing")
			}
		})
	}
}
