package core_test

import (
	"errors"
	"maps"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

func nestedConstraintUnitScope(t *testing.T, e *core.Engine, s *session.Session) *core.StatementScope {
	t.Helper()
	op := transactionOperation(t, s)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !scope.Finished {
			_ = scope.Finish(errors.New("unit cleanup"))
		}
	})
	return scope
}

func TestNestedConstraintDescriptionOwnership(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE p(v INT PRIMARY KEY)")
	sessionSQL(t, s, "CREATE TABLE c(v INT CONSTRAINT fk REFERENCES p INITIALLY DEFERRED)")
	transactionBegin(t, s)
	sessionSQL(t, s, "SET CONSTRAINTS fk DEFERRED")
	sessionSQL(t, s, "INSERT INTO c VALUES(9)")
	before := core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Transactions))
	if len(before.Events) != 1 || before.Events[0].ForeignKey == nil {
		t.Fatal("missing actual pending violation")
	}
	payload := before.Events[0].ForeignKey
	savedPayload := *payload
	savedPayload.Next.Values = slices.Clone(payload.Next.Values)
	savedModes := maps.Clone(before.ByConstraint)
	scope := nestedConstraintUnitScope(t, e, s)
	slot, ref, notices := core.ScopeIndexesSlot(scope), scope.TxRef, len(scope.Op.Notices)
	for _, query := range []string{"SET CONSTRAINTS fk IMMEDIATE", "SET CONSTRAINTS missing DEFERRED", "SET CONSTRAINTS ALL IMMEDIATE"} {
		description, err := core.EngineDescribeOperation(e, scope, enginetest.Statement(t, query))
		if err != nil || !reflect.DeepEqual(description, core.Description{}) {
			t.Fatal("nested utility description resolved names/checked work or returned a nonzero signature", description, err)
		}
		current := core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Transactions))
		if core.ScopeIndexesSlot(scope) != slot || scope.TxRef != ref || len(scope.Op.Notices) != notices || !reflect.DeepEqual(current, before) || &current.Events[0] != &before.Events[0] || current.Events[0].ForeignKey != payload || !reflect.DeepEqual(*payload, savedPayload) || !maps.Equal(before.ByConstraint, savedModes) {
			t.Fatal("description changed live or retained timing/event/incarnation ownership")
		}
	}
}

func TestNestedConstraintModeOwnership(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		name := "implicit"
		if explicit {
			name = "explicit"
		}
		t.Run(name, func(t *testing.T) {
			e := engine.New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(v INT,CONSTRAINT u UNIQUE(v) DEFERRABLE)")
			ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
			oid := e.CatalogRead().MustRelation(ref.Address).Constraints.Unique[0].OID
			if explicit {
				transactionBegin(t, s)
			}
			scope := nestedConstraintUnitScope(t, e, s)
			if scope.Owner.InBlock() != explicit || core.ScopeActiveSQLRoutineDepth(scope) != 0 {
				t.Fatal("incorrect actual entry context")
			}
			_, err := scope.Execute(enginetest.Statement(t, "DO $$BEGIN SET CONSTRAINTS u DEFERRED; END$$"), 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			slot := core.ScopeIndexesSlot(scope)
			if slot == nil || !slot.ConstraintsStarted || !slot.ConstraintModes.ByConstraint[oid] || len(scope.Op.Notices) != 0 || core.ScopeActiveSQLRoutineDepth(scope) != 0 || scope.Owner.InBlock() != explicit {
				t.Fatal("actual nested return discarded modes, leaked activation or warned for implicit execution")
			}
			saved := maps.Clone(slot.ConstraintModes.ByConstraint)
			if err := scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
			scope.Op.Done()
			owner := core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Transactions))
			if owner.ByConstraint[oid] != explicit || !maps.Equal(slot.ConstraintModes.ByConstraint, saved) {
				t.Fatal("mode did not follow transaction lifetime or mutated saved scope state")
			}
		})
	}
	t.Run("top-level-warning-is-unchanged", func(t *testing.T) {
		e := engine.New()
		s := sessionStart(t, e, nil)
		sessionSQL(t, s, "CREATE TABLE t(v INT,CONSTRAINT u UNIQUE(v) DEFERRABLE)")
		scope := nestedConstraintUnitScope(t, e, s)
		_, err := core.EngineExecuteStatement(e, scope, enginetest.Statement(t, "SET CONSTRAINTS u DEFERRED"), 0, nil)
		want := sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "SET CONSTRAINTS can only be used in transaction blocks"}
		if err != nil || !reflect.DeepEqual(scope.Op.Notices, []sqlstate.Error{want}) || len(scope.IndexesOrZero().ConstraintModes.ByConstraint) != 0 {
			t.Fatal("top-level outside-block utility changed warning/retention", scope.Op.Notices, err)
		}
	})
}

func TestNestedConstraintSelectedEventOwnership(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE p(v INT PRIMARY KEY)")
	sessionSQL(t, s, "CREATE TABLE a(v INT CONSTRAINT fa REFERENCES p INITIALLY DEFERRED)")
	sessionSQL(t, s, "CREATE TABLE b(v INT CONSTRAINT fb REFERENCES p INITIALLY DEFERRED)")
	sessionSQL(t, s, "INSERT INTO p VALUES(1)")
	transactionBegin(t, s)
	sessionSQL(t, s, "SET CONSTRAINTS ALL DEFERRED")
	sessionSQL(t, s, "INSERT INTO a VALUES(1)")
	sessionSQL(t, s, "INSERT INTO b VALUES(9)")
	before := core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Transactions))
	if len(before.Events) != 2 || before.Events[0].ForeignKey == nil || before.Events[1].ForeignKey == nil {
		t.Fatal("missing actual separately owned pending obligations")
	}
	savedEvents := slices.Clone(before.Events)
	first, second := before.Events[0], before.Events[1]
	scope := nestedConstraintUnitScope(t, e, s)
	_, err := scope.Execute(enginetest.Statement(t, "DO $$BEGIN SET CONSTRAINTS fa IMMEDIATE; END$$"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	current := scope.IndexesOrZero().ConstraintModes
	if len(current.Events) != 1 || current.Events[0].ForeignKey != second.ForeignKey || current.Events[0].Row != second.Row || current.Events[0].Version != second.Version || &current.Events[0] == &before.Events[0] || current.ByConstraint[first.Constraint] || !current.AllSet || !current.AllDeferred || !reflect.DeepEqual(before.Events, savedEvents) {
		t.Fatal("nested IMMEDIATE consumed unselected work or mutated its saved event owner")
	}
	saved := current
	savedMap := maps.Clone(current.ByConstraint)
	// A real PL checkpoint must restore the pending event and reject the failed
	// IMMEDIATE candidate, rather than publishing it when the exception is caught.
	_, err = core.EngineExecuteStatement(e, scope, enginetest.Statement(t, "DO $$BEGIN SET CONSTRAINTS fb IMMEDIATE; EXCEPTION WHEN foreign_key_violation THEN PERFORM 1; END$$"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	restored := scope.IndexesOrZero().ConstraintModes
	if !reflect.DeepEqual(restored, saved) || restored.Events[0].ForeignKey != second.ForeignKey || !maps.Equal(current.ByConstraint, savedMap) || !reflect.DeepEqual(before.Events, savedEvents) || core.ScopeActiveSQLRoutineDepth(scope) != 0 {
		t.Fatal("caught nested failure leaked a candidate mode or changed retained event sources")
	}
}

func TestNestedConstraintProceduralIncarnationOwnership(t *testing.T) {
	for _, boundary := range []string{"COMMIT", "ROLLBACK", "COMMIT AND CHAIN", "ROLLBACK AND CHAIN"} {
		t.Run(boundary, func(t *testing.T) {
			e := engine.New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE p(v INT PRIMARY KEY)")
			sessionSQL(t, s, "CREATE TABLE c(v INT CONSTRAINT fk REFERENCES p INITIALLY DEFERRED)")
			sessionSQL(t, s, "INSERT INTO p VALUES(1),(2)")
			scope := nestedConstraintUnitScope(t, e, s)
			_, err := scope.Execute(enginetest.Statement(t, "DO $$BEGIN SET CONSTRAINTS fk DEFERRED; INSERT INTO c VALUES(1); END$$"), 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			oldSlot := core.ScopeIndexesSlot(scope)
			if oldSlot == nil || len(oldSlot.ConstraintModes.Events) != 1 {
				t.Fatal("missing actual old-segment timing/event owner")
			}
			before, ref := oldSlot.ConstraintModes, scope.TxRef
			payload := before.Events[0].ForeignKey
			saved := *payload
			saved.Next.Values = slices.Clone(payload.Next.Values)
			savedModes := maps.Clone(before.ByConstraint)
			_, err = core.EngineExecuteStatement(e, scope, enginetest.Statement(t, "DO $$BEGIN "+boundary+"; END$$"), 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			owner := core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(scope.Owner))
			if scope.TxRef == ref || scope.Owner.Ref != scope.TxRef || core.ScopeIndexesSlot(scope) != nil || len(owner.Events) != 0 || owner.ByConstraint != nil || owner.AllSet || len(scope.Stage.Rows) != 0 || core.ScopeActiveSQLRoutineDepth(scope) != 0 {
				t.Fatal("rotation reused the old incarnation's scope/owner modes, events or writes")
			}
			_, err = core.EngineExecuteStatement(e, scope, enginetest.Statement(t, "DO $$BEGIN INSERT INTO c VALUES(2); END$$"), 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			next := core.ScopeIndexesSlot(scope)
			if next == nil || next == oldSlot || !next.ConstraintsStarted || len(next.ConstraintModes.Events) != 1 || next.ConstraintModes.ByConstraint != nil || next.ConstraintModes.AllSet {
				t.Fatal("fresh definition default inherited old override/queue state")
			}
			event := next.ConstraintModes.Events[0]
			if event.Row == before.Events[0].Row || event.ForeignKey == payload || &next.ConstraintModes.Events[0] == &before.Events[0] || !reflect.DeepEqual(*payload, saved) || !maps.Equal(before.ByConstraint, savedModes) {
				t.Fatal("fresh enlistment revived old work or overwrote retained source backing/payload/maps")
			}
		})
		t.Run(boundary+"/no-work", func(t *testing.T) {
			e := engine.New()
			s := sessionStart(t, e, nil)
			scope := nestedConstraintUnitScope(t, e, s)
			ref := scope.TxRef
			_, err := scope.Execute(enginetest.Statement(t, "DO $$BEGIN "+boundary+"; END$$"), 0, nil)
			if err != nil || scope.TxRef == ref || core.ScopeIndexesSlot(scope) != nil || len(core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(scope.Owner)).Events) != 0 {
				t.Fatal("empty rotation allocated constraint bookkeeping or failed to rotate", err)
			}
		})
	}
}
