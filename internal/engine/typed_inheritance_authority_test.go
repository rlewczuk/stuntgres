package engine

import (
	"errors"
	"reflect"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestTypedInheritanceRetainedConstraintAndDefaultAuthority(t *testing.T) {
	for _, kind := range []string{"inherited", "local", "other_parent"} {
		t.Run(kind, func(t *testing.T) {
			e := New()
			parent := inheritanceUnitRelation(e, "p", 22000, "id")
			child := inheritanceUnitRelation(e, "c", 22001, "id")
			child.attributes, child.attributeHighWater = []int32{7}, 7
			child.columns[0].OriginAttribute = 7
			child.columns[0].Nullable = false
			boolean := testUsage(e, sqltype.Bool)
			predicate := literalExpression{expressionInfo: expressionInfo{Usage: boolean}, value: sqltype.Boolean(boolean.Type, true)}
			integer := testUsage(e, sqltype.Int4)
			value, err := sqltype.Integer(integer.Type, 7)
			if err != nil {
				t.Fatal(err)
			}
			defaultExpression := literalExpression{expressionInfo: expressionInfo{Usage: integer}, value: value}
			child.notNullConstraints = []notNullConstraint{{OID: 23001, Name: "c_id_not_null", Attribute: 7}}
			child.checkConstraints = []checkConstraint{{OID: 23003, Name: "positive", expression: predicate}}
			child.defaults = map[int32]columnDefault{7: {OID: 23005, expression: defaultExpression}}
			column := inheritedColumn{local: kind != "other_parent", origins: []inheritanceOrigin{{parent: parent.address, attribute: 1}}}
			nn := inheritedConstraint{local: kind == "local", origins: []inheritanceOrigin{{parent: parent.address, attribute: 1, constraint: 23000}}}
			check := inheritedConstraint{local: kind == "local", origins: []inheritanceOrigin{{parent: parent.address, constraint: 23002}}}
			parents := []ids.ObjectAddress{parent.address}
			var other *relationDescriptor
			if kind == "other_parent" {
				other = inheritanceUnitRelation(e, "q", 22002, "id")
				other.columns[0].Nullable = false
				other.notNullConstraints = []notNullConstraint{{OID: 23010, Name: "q_id_not_null", Attribute: 1}}
				other.checkConstraints = []checkConstraint{{OID: 23011, Name: "positive", expression: predicate}}
				other.defaults = map[int32]columnDefault{1: {OID: 23012, expression: defaultExpression}}
				parents = append(parents, other.address)
				column.origins = append(column.origins, inheritanceOrigin{parent: other.address, attribute: 1})
				nn.origins = append(nn.origins, inheritanceOrigin{parent: other.address, attribute: 1, constraint: 23010})
				check.origins = append(check.origins, inheritanceOrigin{parent: other.address, constraint: 23011})
			}
			child.inheritance = &tableInheritance{parents: parents, columns: map[int32]inheritedColumn{7: column}, notNull: map[int32]inheritedConstraint{7: nn}, checks: map[string]inheritedConstraint{"positive": check}}
			// The parent's attribute and its owned constraints/default are retired;
			// the child attribute remains independently live.
			parent.columns, parent.attributes, parent.index = nil, nil, map[string]int{}
			relations := []*relationDescriptor{parent, child}
			if other != nil {
				relations = append(relations, other)
			}
			view := inheritanceUnitView(e, relations...)
			actions, renames, graph, err := typedInheritedActions(view, child, nil)
			if err != nil || len(actions) != 0 || len(renames) != 0 {
				t.Fatal(actions, renames, err)
			}
			next := *child
			finishTypedInheritance(view, &next, graph)
			if err := validateInheritance(view, &next); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(next.notNullConstraints, child.notNullConstraints) || !reflect.DeepEqual(next.checkConstraints, child.checkConstraints) || !reflect.DeepEqual(next.defaults, child.defaults) || next.columns[0].Nullable {
				t.Fatal("retained physical definition changed", next)
			}
			if other == nil {
				if !graph.notNull[7].local || !graph.checks["positive"].local || len(graph.notNull[7].origins) != 0 || len(graph.checks["positive"].origins) != 0 || !graph.columns[7].explicitDefault {
					t.Fatal("last provider did not become replayable local authority", graph)
				}
			} else if graph.notNull[7].local || graph.checks["positive"].local || graph.columns[7].explicitDefault || !reflect.DeepEqual(graph.notNull[7].origins, []inheritanceOrigin{{parent: other.address, attribute: 1, constraint: 23010}}) || !reflect.DeepEqual(graph.checks["positive"].origins, []inheritanceOrigin{{parent: other.address, constraint: 23011}}) {
				t.Fatal("surviving provider authority was lost", graph)
			}
			if len(child.inheritance.notNull[7].origins) != len(nn.origins) || child.inheritance.columns[7].explicitDefault {
				t.Fatal("captured graph mutated")
			}
		})
	}
}

func TestTypedSameUsageTypeActionUsesIndependentAttributes(t *testing.T) {
	e, source, table := typedUnitCatalog(t, false)
	actions, _ := typedShapeActions(source, source, table, map[int32]bool{2: true})
	if len(actions) != 1 || actions[0].action.Action != parser.AlterColumnType || actions[0].action.Column.Text != "id" || *actions[0].usage != table.columns[0].Usage {
		t.Fatal("same-usage source TYPE disappeared", actions)
	}
	child := inheritanceUnitRelation(e, "c", 22002, "id", "note")
	child.columns = append([]ColumnDescriptor(nil), table.columns...)
	child.attributes = []int32{3, 8}
	child.attributeHighWater = 8
	for i, attribute := range child.attributes {
		child.columns[i].OriginTable, child.columns[i].OriginAttribute = uint32(child.address.ObjectID), int16(attribute)
	}
	child.inheritance = &tableInheritance{parents: []ids.ObjectAddress{table.address}, columns: map[int32]inheritedColumn{3: {origins: []inheritanceOrigin{{parent: table.address, attribute: 1}}}, 8: {origins: []inheritanceOrigin{{parent: table.address, attribute: 7}}}}}
	actions, _, _, err := typedInheritedActions(inheritanceUnitView(e, table, child), child, map[ids.ObjectAddress]map[int32]bool{table.address: {1: true}})
	if err != nil || len(actions) != 1 || actions[0].action.Action != parser.AlterColumnType || actions[0].action.Column.Text != "id" {
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
	auth, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(auth.catalog)
	defer e.DiscardCatalogStage(stage)
	next := *table
	next.revision++
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	store := e.storage[table.address]
	ref := e.domain.Ref(auth.catalog.Epoch(), table.address)
	ref.Address.SubID = 1
	node := columnExpression{expressionInfo: expressionInfo{Usage: table.columns[0].Usage}, origin: ref, scope: 1, frame: 1, slot: 0}
	mutation := typedRelationMutation{old: table, next: &next, transforms: []columnTransformation{{attribute: 1, expression: node}}}
	if err := e.stageTypedRows(scope, auth, stage, mutation); err != nil {
		t.Fatal(err)
	}
	if len(stage.rowShapes) != 0 || e.storage[table.address] != store {
		t.Fatal("binary-identical TYPE copied physical rows")
	}
}
