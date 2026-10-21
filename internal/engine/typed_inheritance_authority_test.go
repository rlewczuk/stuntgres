package engine

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestTypedInheritanceRetainedConstraintAndDefaultAuthority(t *testing.T) {
	for _, kind := range []string{"inherited", "local", "other_parent"} {
		t.Run(kind, func(t *testing.T) {
			e := New()
			parent := inheritanceUnitRelation(e, "p", 22000, "id")
			child := inheritanceUnitRelation(e, "c", 22001, "id")
			child.Attributes, child.ColumnMeta.HighWater = []int32{7}, 7
			child.Columns[0].OriginAttribute = 7
			child.Columns[0].Nullable = false
			boolean := testUsage(e, sqltype.Bool)
			predicate := plan.LiteralExpr{Info: expr.Info{Usage: boolean}, Value: sqltype.Boolean(boolean.Type, true)}
			integer := testUsage(e, sqltype.Int4)
			value, err := sqltype.Integer(integer.Type, 7)
			if err != nil {
				t.Fatal(err)
			}
			defaultExpression := plan.LiteralExpr{Info: expr.Info{Usage: integer}, Value: value}
			child.Constraints.NotNull = []catalog.NotNullConstraint{{OID: 23001, Name: "c_id_not_null", Attribute: 7}}
			child.Constraints.Check = []catalog.CheckConstraint{{OID: 23003, Name: "positive", Expression: predicate}}
			child.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{7: {OID: 23005, Expression: defaultExpression}}
			column := catalog.InheritedColumn{Local: kind != "other_parent", Origins: []catalog.InheritanceOrigin{{Parent: parent.Address(), Attribute: 1}}}
			nn := catalog.InheritedConstraint{Local: kind == "local", Origins: []catalog.InheritanceOrigin{{Parent: parent.Address(), Attribute: 1, Constraint: 23000}}}
			check := catalog.InheritedConstraint{Local: kind == "local", Origins: []catalog.InheritanceOrigin{{Parent: parent.Address(), Constraint: 23002}}}
			parents := []ids.ObjectAddress{parent.Address()}
			var other *catalog.Relation
			if kind == "other_parent" {
				other = inheritanceUnitRelation(e, "q", 22002, "id")
				other.Columns[0].Nullable = false
				other.Constraints.NotNull = []catalog.NotNullConstraint{{OID: 23010, Name: "q_id_not_null", Attribute: 1}}
				other.Constraints.Check = []catalog.CheckConstraint{{OID: 23011, Name: "positive", Expression: predicate}}
				other.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{1: {OID: 23012, Expression: defaultExpression}}
				parents = append(parents, other.Address())
				column.Origins = append(column.Origins, catalog.InheritanceOrigin{Parent: other.Address(), Attribute: 1})
				nn.Origins = append(nn.Origins, catalog.InheritanceOrigin{Parent: other.Address(), Attribute: 1, Constraint: 23010})
				check.Origins = append(check.Origins, catalog.InheritanceOrigin{Parent: other.Address(), Constraint: 23011})
			}
			child.Inheritance = &catalog.TableInheritance{Parents: parents, Columns: map[int32]catalog.InheritedColumn{7: column}, NotNull: map[int32]catalog.InheritedConstraint{7: nn}, Checks: map[string]catalog.InheritedConstraint{"positive": check}}
			// The parent's attribute and its owned constraints/default are retired;
			// the child attribute remains independently live.
			parent.Columns, parent.Attributes, parent.Index = nil, nil, map[string]int{}
			relations := []*catalog.Relation{parent, child}
			if other != nil {
				relations = append(relations, other)
			}
			view := inheritanceUnitView(e, relations...)
			actions, renames, graph, err := table.TypedInheritedActions(view, child, nil)
			if err != nil || len(actions) != 0 || len(renames) != 0 {
				t.Fatal(actions, renames, err)
			}
			next := *child
			typedef.FinishTypedInheritance(view, &next, graph)
			if err := table.ValidateInheritance(view, &next); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(next.Constraints.NotNull, child.Constraints.NotNull) || !reflect.DeepEqual(next.Constraints.Check, child.Constraints.Check) || !reflect.DeepEqual(next.ColumnMeta.Defaults, child.ColumnMeta.Defaults) || next.Columns[0].Nullable {
				t.Fatal("retained physical definition changed", next)
			}
			if other == nil {
				if !graph.NotNull[7].Local || !graph.Checks["positive"].Local || len(graph.NotNull[7].Origins) != 0 || len(graph.Checks["positive"].Origins) != 0 || !graph.Columns[7].ExplicitDefault {
					t.Fatal("last provider did not become replayable local authority", graph)
				}
			} else if graph.NotNull[7].Local || graph.Checks["positive"].Local || graph.Columns[7].ExplicitDefault || !reflect.DeepEqual(graph.NotNull[7].Origins, []catalog.InheritanceOrigin{{Parent: other.Address(), Attribute: 1, Constraint: 23010}}) || !reflect.DeepEqual(graph.Checks["positive"].Origins, []catalog.InheritanceOrigin{{Parent: other.Address(), Constraint: 23011}}) {
				t.Fatal("surviving provider authority was lost", graph)
			}
			if len(child.Inheritance.NotNull[7].Origins) != len(nn.Origins) || child.Inheritance.Columns[7].ExplicitDefault {
				t.Fatal("captured graph mutated")
			}
		})
	}
}

func TestTypedSameUsageTypeActionUsesIndependentAttributes(t *testing.T) {
	e, source, tbl := typedUnitCatalog(t, false)
	actions, _ := typedef.TypedShapeActions(source, source, tbl, map[int32]bool{2: true})
	if len(actions) != 1 || actions[0].Action.Action != parser.AlterColumnType || actions[0].Action.Column.Text != "id" || *actions[0].Usage != tbl.Columns[0].Usage {
		t.Fatal("same-usage source TYPE disappeared", actions)
	}
	child := inheritanceUnitRelation(e, "c", 22002, "id", "note")
	child.Columns = append([]catalog.Column(nil), tbl.Columns...)
	child.Attributes = []int32{3, 8}
	child.ColumnMeta.HighWater = 8
	for i, attribute := range child.Attributes {
		child.Columns[i].OriginTable, child.Columns[i].OriginAttribute = uint32(child.Address().ObjectID), int16(attribute)
	}
	child.Inheritance = &catalog.TableInheritance{Parents: []ids.ObjectAddress{tbl.Address()}, Columns: map[int32]catalog.InheritedColumn{3: {Origins: []catalog.InheritanceOrigin{{Parent: tbl.Address(), Attribute: 1}}}, 8: {Origins: []catalog.InheritanceOrigin{{Parent: tbl.Address(), Attribute: 7}}}}}
	actions, _, _, err := table.TypedInheritedActions(inheritanceUnitView(e, tbl, child), child, map[ids.ObjectAddress]map[int32]bool{tbl.Address(): {1: true}})
	if err != nil || len(actions) != 1 || actions[0].Action.Action != parser.AlterColumnType || actions[0].Action.Column.Text != "id" {
		t.Fatal("same-usage inherited TYPE disappeared", actions, err)
	}
}

func TestTypedSameUsageTypePreservesPhysicalRoot(t *testing.T) {
	e, _, table := typedUnitCatalog(t, false)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(errors.New("unit candidate discarded"))
	auth, err := operationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	defer e.DiscardCatalogStage(stage)
	next := *table
	next.ObjectRevision++
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	store := e.storage.Relations[table.Address()]
	ref := e.catalog.Domain.Ref(auth.Catalog.Epoch(), table.Address())
	ref.Address.SubID = 1
	node := plan.ColumnExpr{Info: expr.Info{Usage: table.Columns[0].Usage}, Origin: ref, Scope: 1, Frame: 1, Slot: 0}
	mutation := typedRelationMutation{old: table, next: &next, transforms: []index.ColumnTransformation{{Attribute: 1, Expression: node}}}
	if err := e.stageTypedRows(scope, auth, stage, mutation); err != nil {
		t.Fatal(err)
	}
	if len(stage.rowShapes) != 0 || e.storage.Relations[table.Address()] != store {
		t.Fatal("binary-identical TYPE copied physical rows")
	}
}
