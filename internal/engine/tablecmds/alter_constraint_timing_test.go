package tablecmds_test

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestAlterConstraintTimingCandidateOwnership(t *testing.T) {
	old := &catalog.Relation{
		ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000},
		ObjectName:    "c", ObjectRevision: 7, RelationKind: catalog.TableRelation,
		Attributes: []int32{1},
		Constraints: catalog.RelationConstraints{
			Check: []catalog.CheckConstraint{{OID: 20001, Name: "ck"}},
			ForeignKeys: []catalog.ForeignKeyConstraint{
				{OID: 20002, Name: "fk", Attributes: []int32{1}, Referenced: []int32{1}, TriggerOIDs: [4]ids.OID{20003, 20004, 20005, 20006}, TriggerModes: [4]catalog.TriggerMode{catalog.TriggerOrigin, catalog.TriggerDisabled, catalog.TriggerReplica, catalog.TriggerAlways}},
				{OID: 20007, Name: "other", NotValid: true, NotEnforced: true},
			},
		},
	}
	before := *old
	original := old.Constraints.ForeignKeys[0]
	for _, flags := range [][2]bool{{true, false}, {true, true}} {
		next, err := table.AlterConstraintTimingCandidate(old, "fk", flags[0], flags[1])
		if err != nil || next == old || !reflect.DeepEqual(old, &before) {
			t.Fatal("candidate changed source", err)
		}
		if &next.Constraints.ForeignKeys[0] == &old.Constraints.ForeignKeys[0] || &next.Constraints.Check[0] != &old.Constraints.Check[0] || &next.Attributes[0] != &old.Attributes[0] {
			t.Fatal("candidate detached wrong payload")
		}
		if next.Address() != old.Address() || next.Revision() != old.Revision() || !reflect.DeepEqual(next.Constraints.ForeignKeys[1], old.Constraints.ForeignKeys[1]) {
			t.Fatal("candidate changed unselected identity or flags")
		}
		changed := next.Constraints.ForeignKeys[0]
		if changed.Deferrable != flags[0] || changed.InitiallyDeferred != flags[1] {
			t.Fatal("candidate lost timing defaults")
		}
		changed.Deferrable, changed.InitiallyDeferred = false, false
		if !reflect.DeepEqual(changed, original) {
			t.Fatal("candidate changed trigger identities/modes or FK predicate")
		}
		again, err := table.AlterConstraintTimingCandidate(next, "fk", flags[0], flags[1])
		if err != nil || again != next {
			t.Fatal("no-op replaced descriptor", err)
		}
		restored, err := table.AlterConstraintTimingCandidate(next, "fk", false, false)
		if err != nil || !reflect.DeepEqual(restored, old) {
			t.Fatal("timing round trip lost descriptor facts", err)
		}
		checks, err := table.ResolveAlterForeignKeys(next, []table.AlterForeignKeyScan{{ForeignKeyConstraint: original}}, nil)
		if err != nil || len(checks) != 1 || checks[0].Child != next || !reflect.DeepEqual(checks[0].Key, original) {
			t.Fatal("timing alteration lost historical validation", checks, err)
		}
	}
	for _, tc := range []struct{ name, code, message string }{
		{"ck", "42809", `constraint "ck" of relation "c" is not a foreign key constraint`},
		{"missing", "42704", `constraint "missing" of relation "c" does not exist`},
	} {
		next, err := table.AlterConstraintTimingCandidate(old, tc.name, true, false)
		var diagnostic *sqlstate.Error
		if next != old || !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != tc.message {
			t.Fatal("selection diagnostic", next, err)
		}
	}
	action := parser.AlterTableAction{Action: parser.AlterConstraintTiming, Constraint: parser.Identifier{Text: "fk"}}
	if table.AlterActionPhase(action.Action) != 10 || table.InheritanceRecursiveAction(old, action, false) || table.AlterTableLockMode(&parser.AlterTable{Actions: []parser.AlterTableAction{action}}) != lock.AccessExclusive {
		t.Fatal("timing action escaped root-only locked MISC phase")
	}
}
