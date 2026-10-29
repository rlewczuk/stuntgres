package tablecmds_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

func TestAlterConstraintInheritEvents(t *testing.T) {
	e := engine.New()
	p := inheritanceUnitRelation(e, "p", 22000, "a")
	other := inheritanceUnitRelation(e, "other", 22001, "a")
	child := inheritanceUnitRelation(e, "child", 22002, "a")
	p.Columns[0].Nullable = false
	other.Columns[0].Nullable = false
	child.Columns[0].Nullable = false
	p.Constraints.NotNull = []catalog.NotNullConstraint{{OID: 23000, Name: "parent_nn", Attribute: 1}}
	other.Constraints.NotNull = []catalog.NotNullConstraint{{OID: 23001, Name: "other_nn", Attribute: 1}}
	child.Constraints.NotNull = []catalog.NotNullConstraint{{OID: 23002, Name: "child_nn", Attribute: 1, NotValid: true}}
	po := catalog.InheritanceOrigin{Parent: p.Address(), Attribute: 1, Constraint: 23000}
	oo := catalog.InheritanceOrigin{Parent: other.Address(), Attribute: 1, Constraint: 23001}
	child.Inheritance = &catalog.TableInheritance{Parents: []catalog.InheritanceParent{{Relation: p.Address(), Seqno: 1}, {Relation: other.Address(), Seqno: 2}}, Columns: map[int32]catalog.InheritedColumn{1: {Origins: []catalog.InheritanceOrigin{{Parent: p.Address(), Attribute: 1}, {Parent: other.Address(), Attribute: 1}}}}, Checks: map[string]catalog.InheritedConstraint{}, NotNull: map[int32]catalog.InheritedConstraint{1: {Origins: []catalog.InheritanceOrigin{po, oo}}}}
	stage := e.BeginCatalogStage(e.CatalogRead())
	for _, d := range []*catalog.Relation{p, other, child} {
		if err := stage.Create(d); err != nil {
			t.Fatal(err)
		}
	}
	queryCreatePublish(t, e, stage)
	view := e.CatalogRead()
	plan := &table.InheritanceAlterPlan{Root: p, Only: true, Events: map[ids.ObjectAddress][]table.InheritanceAlterEvent{}}
	a := parser.AlterTableAction{Action: parser.AlterConstraintInherit, Constraint: parser.Identifier{Text: "parent_nn"}, InheritabilitySet: true, NoInherit: true}
	next, err := table.AlterConstraintInheritCandidate(p, "parent_nn", true)
	if err != nil {
		t.Fatal(err)
	}
	plan.RecordAction(p, next, a)
	plan.Descendant = true
	actions, graph, err := table.InheritedAlterActions(plan, view, view, child)
	if err != nil || len(actions) != 0 {
		t.Fatal("detach must not relay SET or DROP", actions, err)
	}
	fact := graph.NotNull[1]
	if !fact.Local || !reflect.DeepEqual(fact.Origins, []catalog.InheritanceOrigin{oo}) || child.Inheritance.NotNull[1].Local || len(child.Inheritance.NotNull[1].Origins) != 2 {
		t.Fatal("detach lost exact other-parent contribution or changed source", fact)
	}
	plan.Graph = graph
	detached, err := table.FinishInheritanceAlterCandidate(plan, view, child, child)
	if err != nil || detached == child || &detached.Constraints.NotNull[0] != &child.Constraints.NotNull[0] || detached.Constraints.NotNull[0].OID != 23002 || !detached.Constraints.NotNull[0].NotValid || detached.Columns[0].Nullable {
		t.Fatal("localization changed fact or backing", err)
	}
	if len(plan.Events[child.Address()]) != 0 {
		t.Fatal("detach propagated below direct child")
	}
	plan.Descendant = false
	a.NoInherit = false
	restored, err := table.AlterConstraintInheritCandidate(next, "parent_nn", false)
	if err != nil {
		t.Fatal(err)
	}
	plan.Events = map[ids.ObjectAddress][]table.InheritanceAlterEvent{}
	plan.RecordAction(next, restored, a)
	plan.Descendant = true
	actions, graph, err = table.InheritedAlterActions(plan, view, view, detached)
	if err != nil || len(actions) != 1 || actions[0].Action != parser.SetNotNull || !actions[0].InheritabilitySet {
		t.Fatal(actions, err)
	}
	op, done := roleOperation(t, e, "postgres")
	defer done()
	merged, err := table.NotNullCandidate(op, view, detached, actions[0], table.NotNullInherited)
	if err != nil || merged != detached {
		t.Fatal("existing invalid fact was validated/replaced", err)
	}
	plan.RecordAction(detached, merged, actions[0])
	if len(plan.Events[child.Address()]) != 0 {
		t.Fatal("compatible child did not stop branch")
	}
	if graph.NotNull[1].Count() != 2 || !graph.NotNull[1].Local || len(detached.Inheritance.NotNull[1].Origins) != 1 {
		t.Fatal("merge lost locality/multiplicity or mutated predecessor")
	}
}

func TestAlterConstraintInheritCreatedIdentity(t *testing.T) {
	e := engine.New()
	p := inheritanceUnitRelation(e, "p", 22000, "a")
	p.Columns[0].Nullable = false
	p.Constraints.NotNull = []catalog.NotNullConstraint{{OID: 23000, Name: "nn", Attribute: 1, NoInherit: true}}
	stage := e.BeginCatalogStage(e.CatalogRead())
	if err := stage.Create(p); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, stage)
	view := e.CatalogRead()
	plan := &table.InheritanceAlterPlan{Root: p, Only: true, Events: map[ids.ObjectAddress][]table.InheritanceAlterEvent{}}
	a := parser.AlterTableAction{Action: parser.AlterConstraintInherit, Constraint: parser.Identifier{Text: "nn"}, InheritabilitySet: true}
	next, err := table.AlterConstraintInheritCandidate(p, "nn", false)
	if err != nil {
		t.Fatal(err)
	}
	plan.RecordAction(p, next, a)
	child := inheritanceUnitRelation(e, "fresh", 22001, "a")
	child.Inheritance = &catalog.TableInheritance{Parents: []catalog.InheritanceParent{{Relation: p.Address(), Seqno: 1}}, Columns: map[int32]catalog.InheritedColumn{1: {Origins: []catalog.InheritanceOrigin{{Parent: p.Address(), Attribute: 1}}}}, Checks: map[string]catalog.InheritedConstraint{}, NotNull: map[int32]catalog.InheritedConstraint{}}
	plan.Descendant = true
	actions, _, err := table.InheritedAlterActions(plan, view, view, child)
	if err != nil || len(actions) != 1 {
		t.Fatal(actions, err)
	}
	op, done := roleOperation(t, e, "postgres")
	defer done()
	created, err := table.NotNullCandidate(op, view, child, actions[0], table.NotNullInherited)
	if err != nil || created == child || created.Columns[0].Nullable || len(child.Constraints.NotNull) != 0 {
		t.Fatal("constructor mutated source", err)
	}
	plan.RecordAction(child, created, actions[0])
	e.Mu.Lock()
	err = table.AssignDefinitionOIDs(core.DDLHost{E: e}, created)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	plan.RecordAssignedConstraintModes(created)
	events := plan.Events[child.Address()]
	if len(events) != 1 || events[0].Constraint == 0 || events[0].Constraint != created.Constraints.NotNull[0].OID || events[0].Attribute != 1 {
		t.Fatal("recursion lost assigned NN identity", events)
	}
	leaf := inheritanceUnitRelation(e, "leaf", 22002, "a")
	leaf.Inheritance = &catalog.TableInheritance{Parents: []catalog.InheritanceParent{{Relation: child.Address(), Seqno: 1}}, Columns: map[int32]catalog.InheritedColumn{1: {Origins: []catalog.InheritanceOrigin{{Parent: child.Address(), Attribute: 1}}}}, Checks: map[string]catalog.InheritedConstraint{}, NotNull: map[int32]catalog.InheritedConstraint{}}
	actions, graph, err := table.InheritedAlterActions(plan, view, view, leaf)
	if err != nil || len(actions) != 1 || graph.NotNull[1].Count() != 1 || graph.NotNull[1].Origins[0].Constraint != events[0].Constraint {
		t.Fatal("new fact did not recurse with exact source identity", actions, graph, err)
	}
}
