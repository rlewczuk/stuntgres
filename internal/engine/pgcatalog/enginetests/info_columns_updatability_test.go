package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func columnsUnitView(f columnsUnitFixture, id ids.OID, source *catalog.Relation, slots []int, automatic bool) *catalog.Relation {
	capture := f.dc.f.e.Catalog.Read(f.state)
	ref := capture.Owner().Domain.Ref(capture.Epoch(), source.Address())
	syntax := &parser.Select{From: &parser.TableReference{}}
	project := &plan.ProjectNode{Child: &plan.SourceNode{Ref: ref, Occurrence: 0}, HasTargetSet: !automatic}
	relation := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}, ObjectNamespace: f.dc.f.schema, ObjectName: "metadata_view", ObjectRevision: 1, Owner: f.dc.f.owner, RelationKind: catalog.ViewRelation}
	for i, slot := range slots {
		column := source.Columns[0]
		if slot >= 0 {
			column = source.Columns[slot]
		}
		relation.Columns = append(relation.Columns, column)
		relation.Attributes = append(relation.Attributes, int32(i+1))
		if slot >= 0 {
			project.Expressions = append(project.Expressions, plan.ColumnExpr{Info: expr.Info{Usage: column.Usage}, Origin: ref, Slot: slot})
		} else {
			project.Expressions = append(project.Expressions, plan.LiteralExpr{Info: expr.Info{Usage: column.Usage}, Value: sqltype.Null(column.Usage.Type)})
		}
	}
	relation.View = stored.NewView(stored.ViewParts{Query: &plan.Query{Root: project}, Syntax: syntax, Sources: []stored.ViewLeaf{{Ref: ref, Attributes: append([]int32(nil), source.Attributes...)}}})
	f.state.SetDescriptor(relation.Address(), relation)
	return relation
}

func TestInfoColumnsUpdatability(t *testing.T) {
	f := newColumnsUnitFixture(t)
	simple := columnsUnitView(f, 22600, f.table, []int{1, -1}, true)
	nested := columnsUnitView(f, 22601, simple, []int{0, 1}, true)
	columnsUnitFilteredShape(t, f)
	check := func(relation *catalog.Relation, attribute int32, want bool) {
		t.Helper()
		got, err := view.ColumnUpdatable(t.Context(), f.dc.f.e.Catalog.Read(f.state), relation, attribute, fdw.UpdatableEvents)
		if err != nil || got != want {
			t.Fatal("column updatability", relation.Address(), attribute, got, want, err)
		}
	}
	check(f.table, 1, true)
	check(simple, 1, true)
	check(simple, 2, false)
	check(nested, 1, true)
	check(nested, 2, false)
	// Stable frozen attr3 remains valid after a physical gap closes and rename.
	changed := *f.table
	changed.Columns = append([]catalog.Column(nil), changed.Columns[1:]...)
	changed.Columns[0].Name = "renamed"
	changed.Attributes = append([]int32(nil), changed.Attributes[1:]...)
	f.state.SetDescriptor(changed.Address(), &changed)
	check(nested, 1, true)
	for _, tc := range []struct {
		events                     []string
		instead, conditional, want bool
	}{
		{[]string{"UPDATE"}, true, false, false}, {[]string{"DELETE"}, true, false, false}, {[]string{"UPDATE", "DELETE"}, true, false, true}, {[]string{"UPDATE", "DELETE"}, false, false, false}, {[]string{"UPDATE", "DELETE"}, true, true, false},
	} {
		nonautomatic := columnsUnitView(f, 22602, &changed, []int{-1}, false)
		var rules []stored.Rule
		for i, event := range tc.events {
			parts := stored.RuleParts{OID: ids.OID(22610 + i), Name: event, Event: event, Mode: catalog.RuleOrigin, Instead: tc.instead}
			if tc.conditional {
				boolean, _ := f.dc.f.e.Catalog.Read(f.state).LookupType(sqltype.Bool)
				parts.Condition = plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, true)}
			}
			rules = append(rules, stored.NewRule(parts))
		}
		nonautomatic.Rules = stored.RulesPayload(rules)
		check(nonautomatic, 1, tc.want)
	}
	// An unconditional UPDATE rule combines with automatic DELETE, including
	// computed outputs, without running or binding the rule action.
	ruled := *simple
	ruled.Rules = stored.RulesPayload([]stored.Rule{stored.NewRule(stored.RuleParts{OID: 22620, Name: "update", Event: "UPDATE", Mode: catalog.RuleOrigin, Instead: true})})
	f.state.SetDescriptor(ruled.Address(), &ruled)
	check(&ruled, 2, true)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := view.ColumnUpdatable(ctx, f.dc.f.e.Catalog.Read(f.state), nested, 1, fdw.UpdatableEvents); !errors.Is(err, context.Canceled) {
		t.Fatal("view cancellation", err)
	}
	walking := &dtpCancelContext{Context: context.Background(), remaining: 3}
	if _, err := view.ColumnUpdatable(walking, f.dc.f.e.Catalog.Read(f.state), nested, 1, fdw.UpdatableEvents); !errors.Is(err, context.Canceled) {
		t.Fatal("recursive view cancellation", err)
	}
	cycle := columnsUnitView(f, 22630, &ruled, []int{0}, true)
	capture := f.dc.f.e.Catalog.Read(f.state)
	own := capture.Owner().Domain.Ref(capture.Epoch(), cycle.Address())
	parts := stored.ViewOf(cycle).Parts()
	parts.Query = &plan.Query{Root: &plan.ProjectNode{Child: &plan.SourceNode{Ref: own, Occurrence: 0}, Expressions: []expr.Expr{plan.ColumnExpr{Info: expr.Info{Usage: cycle.Columns[0].Usage}, Origin: own, Slot: 0}}}}
	parts.Sources = []stored.ViewLeaf{{Ref: own, Attributes: cycle.Attributes}}
	cycle.View = stored.NewView(parts)
	if _, err := view.ColumnUpdatable(t.Context(), capture, cycle, 1, fdw.UpdatableEvents); err == nil {
		t.Fatal("recursive metadata graph accepted")
	}
	columnsUnitForeignCapabilities(t, f)
}

var columnsForeignIO = errors.New("metadata attempted foreign I/O")

type columnsForeignRead struct{ calls *int }

func (r columnsForeignRead) called() error { *r.calls++; return columnsForeignIO }
func (r columnsForeignRead) GetForeignRelSize(context.Context, catalog.ForeignRequest) (catalog.ForeignRelSize, error) {
	return catalog.ForeignRelSize{}, r.called()
}
func (r columnsForeignRead) GetForeignPaths(context.Context, catalog.ForeignRequest, catalog.ForeignRelSize) ([]catalog.ForeignPath, error) {
	return nil, r.called()
}
func (r columnsForeignRead) GetForeignPlan(context.Context, catalog.ForeignRequest, catalog.ForeignPath) (catalog.ForeignPlan, error) {
	return catalog.ForeignPlan{}, r.called()
}
func (r columnsForeignRead) BeginForeignScan(context.Context, catalog.ForeignRequest, catalog.ForeignPlan) (catalog.ForeignScan, error) {
	return nil, r.called()
}

type columnsForeignUpdate struct{ columnsForeignRead }

func (r columnsForeignUpdate) ExecForeignUpdate(context.Context, catalog.ForeignModifyRequest, any, catalog.ForeignRowIdentity, catalog.ForeignRow) (catalog.ForeignRow, bool, error) {
	return nil, false, r.called()
}

type columnsForeignDelete struct{ columnsForeignRead }

func (r columnsForeignDelete) ExecForeignDelete(context.Context, catalog.ForeignModifyRequest, any, catalog.ForeignRowIdentity, catalog.ForeignRow) (catalog.ForeignRow, bool, error) {
	return nil, false, r.called()
}

type columnsForeignBoth struct{ columnsForeignUpdate }

func (r columnsForeignBoth) ExecForeignDelete(context.Context, catalog.ForeignModifyRequest, any, catalog.ForeignRowIdentity, catalog.ForeignRow) (catalog.ForeignRow, bool, error) {
	return nil, false, r.called()
}

func columnsUnitForeignCapabilities(t *testing.T, f columnsUnitFixture) {
	t.Helper()
	calls := 0
	read := columnsForeignRead{calls: &calls}
	for _, tc := range []struct {
		wrapper              catalog.ForeignDataWrapper
		update, remove, want bool
	}{
		{read, false, false, false}, {columnsForeignUpdate{read}, true, false, false}, {columnsForeignDelete{read}, false, true, false}, {columnsForeignBoth{columnsForeignUpdate{read}}, true, true, true},
	} {
		state := catalog.CloneState(f.state)
		procedure := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 22700}, ObjectNamespace: f.dc.f.schema, ObjectRevision: 1, ObjectName: "metadata_handler", Foreign: &catalog.ForeignProcedure{Wrapper: tc.wrapper}}
		wrapper := &catalog.ForeignObject{ObjectAddress: ids.ObjectAddress{ClassID: ids.ForeignWrapperClassID, ObjectID: 22701}, ObjectRevision: 1, ObjectName: "metadata_wrapper", Handler: procedure.Address()}
		server := &catalog.ForeignObject{ObjectAddress: ids.ObjectAddress{ClassID: ids.ForeignServerClassID, ObjectID: 22702}, ObjectRevision: 1, ObjectName: "metadata_server", Wrapper: wrapper.Address()}
		relation := *f.table
		relation.ObjectAddress.ObjectID = 22703
		relation.RelationKind = catalog.ForeignRelation
		relation.Foreign = &catalog.ForeignTableDefinition{Server: server.Address()}
		for _, d := range []catalog.Descriptor{procedure, wrapper, server, &relation} {
			state.SetDescriptor(d.Address(), d)
		}
		capture := f.dc.f.e.Catalog.Read(state)
		update, remove, err := fdw.UpdatableEvents(t.Context(), capture, &relation)
		got, viewErr := view.ColumnUpdatable(t.Context(), capture, &relation, 1, fdw.UpdatableEvents)
		if err != nil || viewErr != nil || update != tc.update || remove != tc.remove || got != tc.want || calls != 0 {
			t.Fatal("real interface capability without callbacks", update, remove, got, err, viewErr, calls)
		}
	}
}

func columnsUnitFilteredShape(t *testing.T, f columnsUnitFixture) {
	t.Helper()
	for depth := 1; depth <= 2; depth++ {
		filtered := columnsUnitView(f, 22640, f.table, []int{0}, true)
		parts := stored.ViewOf(filtered).Parts()
		project := *parts.Query.Root.(*plan.ProjectNode)
		source := project.Child
		boolean, _ := f.dc.f.e.Catalog.Read(f.state).LookupType(sqltype.Bool)
		var expected []expr.Expr
		for i := range depth {
			predicate := plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, i != 0)}
			project.Child = &plan.FilterNode{Child: project.Child, Predicate: predicate}
			expected = append([]expr.Expr{predicate}, expected...)
		}
		parts.Query = &plan.Query{Root: &project}
		filtered.View = stored.NewView(parts)
		writeProject, writeSource, predicates, writeReason := stored.ViewWriteShape(filtered)
		metaProject, metaSource, metaReason := stored.ViewUpdatabilityShape(filtered)
		if writeProject != &project || metaProject != writeProject || writeSource != source || metaSource != writeSource || writeReason != "" || metaReason != writeReason || !reflect.DeepEqual(predicates, expected) {
			t.Fatal("shared filtered shape", depth, predicates, expected, writeReason, metaReason)
		}
		if allocations := testing.AllocsPerRun(10, func() { stored.ViewUpdatabilityShape(filtered) }); allocations != 0 {
			t.Fatal("metadata collected execution predicates", depth, allocations)
		}
		allowed, err := view.ColumnUpdatable(t.Context(), f.dc.f.e.Catalog.Read(f.state), filtered, 1, fdw.UpdatableEvents)
		if err != nil || !allowed {
			t.Fatal("metadata evaluated a false filter", allowed, err)
		}
	}
}
