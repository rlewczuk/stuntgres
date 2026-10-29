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

func TestAlterConstraintInheritCandidateOwnership(t *testing.T) {
	old := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 22000}, ObjectName: "t", ObjectRevision: 7, RelationKind: catalog.TableRelation, Attributes: []int32{1}, Columns: []catalog.Column{{Name: "a", Nullable: false}}, Index: map[string]int{"a": 0}, Constraints: catalog.RelationConstraints{NotNull: []catalog.NotNullConstraint{{OID: 23000, Name: "nn", Attribute: 1, NotValid: true}}, Check: []catalog.CheckConstraint{{OID: 23001, Name: "ck"}}, Unique: []catalog.UniqueConstraint{{OID: 23002, Name: "pk", Primary: true, Attributes: []int32{1}}}}}
	before := *old
	next, err := table.AlterConstraintInheritCandidate(old, "nn", true)
	if err != nil || next == old || !reflect.DeepEqual(old, &before) {
		t.Fatal("candidate changed predecessor", err)
	}
	if &next.Constraints.NotNull[0] == &old.Constraints.NotNull[0] || &next.Columns[0] != &old.Columns[0] || &next.Attributes[0] != &old.Attributes[0] || &next.Constraints.Unique[0] != &old.Constraints.Unique[0] || &next.Constraints.Check[0] != &old.Constraints.Check[0] {
		t.Fatal("wrong immutable backing detached")
	}
	fact := next.Constraints.NotNull[0]
	fact.NoInherit = false
	if !reflect.DeepEqual(fact, old.Constraints.NotNull[0]) || next.Revision() != old.Revision() {
		t.Fatal("candidate changed identity/validity/revision")
	}
	again, err := table.AlterConstraintInheritCandidate(next, "nn", true)
	if err != nil || again != next {
		t.Fatal("no-op replaced predecessor", err)
	}
	restored, err := table.AlterConstraintInheritCandidate(next, "nn", false)
	if err != nil || !reflect.DeepEqual(restored, old) {
		t.Fatal("mode roundtrip changed persistent facts", err)
	}
	for _, tc := range []struct{ name, code string }{{"ck", "42809"}, {"missing", "42704"}} {
		got, err := table.AlterConstraintInheritCandidate(old, tc.name, true)
		var diagnostic *sqlstate.Error
		if got != old || !errors.As(err, &diagnostic) || diagnostic.Code != tc.code {
			t.Fatal("selection changed source", got, err)
		}
	}
	// Live and retired contributions both block detach; locality is independent.
	inherited := *old
	inherited.Inheritance = &catalog.TableInheritance{NotNull: map[int32]catalog.InheritedConstraint{1: {Local: true, Origins: []catalog.InheritanceOrigin{{Parent: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 22001}, Attribute: 1, Constraint: 23003}}}}}
	got, err := table.AlterConstraintInheritCandidate(&inherited, "nn", true)
	var diagnostic *sqlstate.Error
	if got != &inherited || !errors.As(err, &diagnostic) || diagnostic.Code != "55000" {
		t.Fatal("inherited detach admitted", err)
	}
	got, err = table.AlterConstraintInheritCandidate(&inherited, "nn", false)
	if err != nil || got != &inherited {
		t.Fatal("matching inherited INHERIT rejected", err)
	}
	inherited.Inheritance.NotNull[1] = catalog.InheritedConstraint{Local: true, Retired: []catalog.RetiredInheritanceOrigin{{Attribute: 1, Constraint: 23003, Own: 23000, Detached: true, Archive: 1, Lifetime: 1}}}
	got, err = table.AlterConstraintInheritCandidate(&inherited, "nn", true)
	if got != &inherited || !errors.As(err, &diagnostic) || diagnostic.Code != "55000" {
		t.Fatal("retired contribution ignored", err)
	}
	a := parser.AlterTableAction{Action: parser.AlterConstraintInherit, Constraint: parser.Identifier{Text: "nn"}, InheritabilitySet: true}
	if table.AlterActionPhase(a.Action) != 10 || !table.InheritanceRecursiveAction(old, a, true) || table.AlterTableLockMode(&parser.AlterTable{Actions: []parser.AlterTableAction{a}}) != lock.AccessExclusive {
		t.Fatal("mode escaped misc/AX/direct-child pipeline")
	}
	noop, err := table.ConstraintModeNoOp(old, []parser.AlterTableAction{a}, false)
	if err != nil || !noop {
		t.Fatal("matching mode lost no-journal path", err)
	}
	a.NoInherit = true
	noop, err = table.ConstraintModeNoOp(old, []parser.AlterTableAction{a}, false)
	if err != nil || noop {
		t.Fatal("changed mode elided", err)
	}
}

func TestRestoreConstraintModeAdmission(t *testing.T) {
	valid := func() *parser.AlterTable {
		return &parser.AlterTable{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "public"}, {Text: "t"}}}, Only: true, Actions: []parser.AlterTableAction{{Action: parser.AlterConstraintInherit, Constraint: parser.Identifier{Text: "nn"}, InheritabilitySet: true, NoInherit: true}}}
	}
	if err := table.RestoreAlterConstraints(valid()); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*parser.AlterTable){
		"unqualified":  func(s *parser.AlterTable) { s.Name.Parts = s.Name.Parts[1:] },
		"conditional":  func(s *parser.AlterTable) { s.IfExists = true },
		"recursive":    func(s *parser.AlterTable) { s.Only = false },
		"star":         func(s *parser.AlterTable) { s.Star = true },
		"materialized": func(s *parser.AlterTable) { s.Materialized = true },
		"multiple":     func(s *parser.AlterTable) { s.Actions = append(s.Actions, s.Actions[0]) },
		"inherit":      func(s *parser.AlterTable) { s.Actions[0].NoInherit = false },
		"bare": func(s *parser.AlterTable) {
			s.Actions[0].Action = parser.AlterConstraint
			s.Actions[0].InheritabilitySet = false
		},
		"timing":      func(s *parser.AlterTable) { s.Actions[0].DeferrabilitySet = true },
		"enforcement": func(s *parser.AlterTable) { s.Actions[0].EnforcementSet = true },
		"old_set":     func(s *parser.AlterTable) { s.Actions[0].Action = parser.SetNotNull },
	} {
		t.Run(name, func(t *testing.T) {
			s := valid()
			mutate(s)
			if err := table.RestoreAlterConstraints(s); err == nil {
				t.Fatal("noncanonical mode replay admitted")
			}
		})
	}
	t.Run("invalid_constructor", func(t *testing.T) {
		build := func() *parser.AlterTable {
			s := valid()
			s.Only = false
			s.Actions[0] = parser.AlterTableAction{Action: parser.AddNotNull, Constraint: parser.Identifier{Text: "nn"}, Column: parser.Identifier{Text: "a"}, NotValid: true, NoInherit: true}
			return s
		}
		if err := table.RestoreAlterConstraints(build()); err != nil {
			t.Fatal(err)
		}
		for name, mutate := range map[string]func(*parser.AlterTable){
			"only":        func(s *parser.AlterTable) { s.Only = true },
			"star":        func(s *parser.AlterTable) { s.Star = true },
			"conditional": func(s *parser.AlterTable) { s.IfExists = true },
			"unqualified": func(s *parser.AlterTable) { s.Name.Parts = s.Name.Parts[1:] },
			"multiple":    func(s *parser.AlterTable) { s.Actions = append(s.Actions, s.Actions[0]) },
			"valid":       func(s *parser.AlterTable) { s.Actions[0].NotValid = false },
			"timing":      func(s *parser.AlterTable) { s.Actions[0].DeferrabilitySet = true },
			"enforcement": func(s *parser.AlterTable) { s.Actions[0].EnforcementSet = true },
		} {
			t.Run(name, func(t *testing.T) {
				s := build()
				mutate(s)
				if err := table.RestoreAlterConstraints(s); err == nil {
					t.Fatal("invalid constructor shape admitted")
				}
			})
		}
	})
}
