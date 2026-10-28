package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
)

func triggerModeEventOwnership(t *testing.T) {
	e, s := triggerModeUnitSetup(t)
	sessionSQL(t, s, "ALTER TABLE mode_child ADD CONSTRAINT mode_second_fk FOREIGN KEY(pid) REFERENCES mode_parent(id)")
	child := e.CatalogRead().MustRelation(enginetest.RoleTableRef(t, e, "mode_child").Address)
	constraint := child.Constraints.ForeignKeys[0].OID
	capture := func(sql string) dml.ForeignActionTransition {
		t.Helper()
		op, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		scope, err := e.StartStatement(op)
		if err != nil {
			op.Done()
			t.Fatal(err)
		}
		if _, err := scope.Execute(enginetest.Statement(t, sql), 0, nil); err != nil {
			scope.Finish(err)
			op.Done()
			t.Fatal(err)
		}
		events := core.ScopeForeignActionsOrZero(scope).Transitions
		if len(events) != 1 || events[0].Relation != child.Address() {
			scope.Finish(nil)
			op.Done()
			t.Fatal("missing actual RI event", events)
		}
		event := events[0]
		err = scope.Finish(nil)
		op.Done()
		if err != nil {
			t.Fatal(err)
		}
		return event
	}
	sessionSQL(t, s, "SET session_replication_role=replica")
	first := capture("INSERT INTO mode_child VALUES(1,99)")
	if first.TriggerFires(constraint, 2) || len(first.Suppressed) != 2 {
		t.Fatal("replica event did not capture check suppression", first)
	}
	for i, suppression := range first.Suppressed {
		if first.TriggerFires(suppression.Constraint, 2) || !first.TriggerFires(suppression.Constraint, 0) ||
			i > 0 && first.Suppressed[i-1].Constraint >= suppression.Constraint {
			t.Fatal("event capture lost sorted per-slot eligibility", first)
		}
	}
	firstID := first.Suppressed[0].Constraint
	backing := &first.Suppressed[0]
	sessionSQL(t, s, "SET session_replication_role=origin")
	sessionSQL(t, s, "ALTER TABLE mode_child DISABLE TRIGGER ALL")
	second := capture("INSERT INTO mode_child VALUES(2,98)")
	if second.TriggerFires(constraint, 2) || &second.Suppressed[0] == backing {
		t.Fatal("catalog transition reused mutable event eligibility")
	}
	sessionSQL(t, s, "ALTER TABLE mode_child ENABLE TRIGGER ALL")
	sessionSQL(t, s, "INSERT INTO mode_parent VALUES(100,5)")
	third := capture("INSERT INTO mode_child VALUES(3,100)")
	if !third.TriggerFires(constraint, 2) || len(third.Suppressed) != 0 {
		t.Fatal("fresh enabled event retained suppression")
	}
	if first.TriggerFires(constraint, 2) || &first.Suppressed[0] != backing || first.Suppressed[0].Constraint != firstID {
		t.Fatal("later settings/catalog changes mutated captured eligibility")
	}
}
