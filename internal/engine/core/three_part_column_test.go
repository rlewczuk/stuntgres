package core_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Scalar unit fixtures explicitly admit their input row, just like production owners.
func bindTestTargetNamespace(b *analyze.Binder) {
	relation := b.Target.Relation
	columns := make([]analyze.QueryColumn, len(relation.Columns))
	for i, column := range relation.Columns {
		columns[i] = analyze.QueryColumn{Descriptor: column, Slot: i, Physical: i, Leaf: analyze.QueryTargetLeaf}
	}
	qualifier := analyze.QueryQualifier{Name: relation.Name(), Original: relation.Name(), Ref: b.Target.Ref, Columns: columns}
	if relation.Namespace() != (ids.ObjectAddress{}) {
		qualifier.Schema = b.BindingCatalog().Get(relation.Namespace()).Name()
	}
	b.Query.Namespace = &analyze.QueryNamespace{Columns: columns, Qualifiers: []analyze.QueryQualifier{qualifier}, Frame: relation.Columns}
}

func threePartUnitBinder(t *testing.T) *analyze.Binder {
	t.Helper()
	b := enginetest.ExpressionBinder(t, context.Background(), engine.New(), sqltype.InputEnvironment{}, nil)
	usage := b.Type(sqltype.VarChar).Usage()
	usage.Typmod = 12
	b.Target.Relation = &catalog.Relation{ObjectName: "items", Columns: []catalog.Column{{Name: "v", Usage: usage}}, Attributes: []int32{7}, Index: map[string]int{"v": 0}}
	b.Target.Ref = ids.ObjectRef{Address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20001}, Epoch: 1}
	bindTestTargetNamespace(b)
	b.Query.Namespace.Qualifiers[0].Schema = "qa"
	return b
}

func threePartUnitColumn(parts ...string) parser.ColumnExpr {
	name := parser.ObjectName{}
	for _, part := range parts {
		name.Parts = append(name.Parts, parser.Identifier{Text: part})
	}
	return parser.ColumnExpr{Name: name, Pos: parser.Position{Character: 9}}
}

func TestThreePartColumnNamespace(t *testing.T) {
	b := threePartUnitBinder(t)
	raw := threePartUnitColumn("qa", "items", "v")
	node, err := b.Bind(raw, analyze.ExpressionPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	column := node.(plan.ColumnExpr)
	if column.Slot != 0 || column.Frame != 1 || column.Origin.Address.SubID != 7 || !reflect.DeepEqual(b.Target.Uses, []int{0}) {
		t.Fatal(column, b.Target.Uses)
	}
	for _, namespace := range []*analyze.QueryNamespace{nil, {}} {
		b.Query.Namespace = namespace
		b.Target.InsertTarget = b.Target.Relation
		_, err = b.Bind(raw, analyze.ExpressionPolicy{})
		enginetest.State(t, err, "42P01")
		_, err = b.Bind(threePartUnitColumn("v"), analyze.ExpressionPolicy{})
		enginetest.State(t, err, "42703")
		_, err = b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.AssignmentContext})
		enginetest.State(t, err, "42P01")
		_, err = b.Bind(threePartUnitColumn("v"), analyze.ExpressionPolicy{Context: analyze.AssignmentContext})
		enginetest.State(t, err, "42703")
		_, err = b.BindRoutineDefault(raw, b.Type(sqltype.VarChar).Usage())
		enginetest.State(t, err, "42P01")
	}
	b = threePartUnitBinder(t)
	b.Query.Namespace.Qualifiers[0].Name = "x"
	b.Query.Namespace.Qualifiers[0].Alias = "x"
	_, err = b.Bind(raw, analyze.ExpressionPolicy{})
	enginetest.State(t, err, "42P01")
	if _, err = b.Bind(threePartUnitColumn("x", "v"), analyze.ExpressionPolicy{}); err != nil {
		t.Fatal(err)
	}
	b = threePartUnitBinder(t)
	_, err = b.Bind(threePartUnitColumn("qb", "items", "v"), analyze.ExpressionPolicy{})
	enginetest.State(t, err, "42P01")
}

func TestThreePartColumnFrames(t *testing.T) {
	b := threePartUnitBinder(t)
	b.Params.UpdateParameters = true
	outer := b.Query.Namespace
	leave := b.EnterQuery(false)
	// A second empty frame must not impersonate the retained mutation target.
	leaveInner := b.EnterQuery(false)
	node, err := b.Bind(threePartUnitColumn("qa", "items", "v"), analyze.ExpressionPolicy{})
	if err != nil || node.(plan.ColumnExpr).Frame != 1 {
		t.Fatal(node, err)
	}
	leaveInner()
	inner := *outer
	inner.Qualifiers = append([]analyze.QueryQualifier(nil), outer.Qualifiers...)
	inner.Columns = nil
	inner.Qualifiers[0].Columns = nil
	inner.Qualifiers[0].Schema = "qb"
	b.Query.Namespace = &inner
	if _, err = b.Bind(threePartUnitColumn("qa", "items", "v"), analyze.ExpressionPolicy{}); err != nil {
		t.Fatal("different schema shadowed outer", err)
	}
	_, err = b.Bind(threePartUnitColumn("items", "v"), analyze.ExpressionPolicy{})
	enginetest.State(t, err, "42703")
	inner.Qualifiers[0].Name, inner.Qualifiers[0].Alias = "items", "items"
	if _, err = b.Bind(threePartUnitColumn("qa", "items", "v"), analyze.ExpressionPolicy{}); err != nil {
		t.Fatal("alias shadowed schema-qualified outer", err)
	}
	leave()
	if b.Query.Namespace != outer || b.Query.Frame != 0 || len(b.Query.Scopes) != 0 {
		t.Fatal("query state not restored")
	}
	b.Query.Namespace = nil
	leave = b.EnterQuery(false)
	_, err = b.Bind(threePartUnitColumn("qa", "items", "v"), analyze.ExpressionPolicy{})
	enginetest.State(t, err, "42P01")
	leave()
}

func TestThreePartColumnOrigins(t *testing.T) {
	b := threePartUnitBinder(t)
	var first plan.ColumnExpr
	for i, parts := range [][]string{{"v"}, {"items", "v"}, {"qa", "items", "v"}} {
		node, err := b.Bind(threePartUnitColumn(parts...), analyze.ExpressionPolicy{})
		if err != nil {
			t.Fatal(err)
		}
		column := node.(plan.ColumnExpr)
		if i == 0 {
			first = column
		} else if !reflect.DeepEqual(first, column) {
			t.Fatal("qualification changed descriptor/origin/frame", first, column)
		}
	}
	if first.Usage.Typmod != 12 || first.Origin.Address.SubID != 7 || !reflect.DeepEqual(b.Target.Uses, []int{0}) {
		t.Fatal(first, b.Target.Uses)
	}
	// Source reads belong to their occurrence, not the retained write target.
	b.Target.Uses = nil
	b.Query.Sources = []exec.Leaf{{Ref: b.Target.Ref, Relation: b.Target.Relation}}
	namespace := *b.Query.Namespace
	namespace.Columns = append([]analyze.QueryColumn(nil), namespace.Columns...)
	namespace.Columns[0].Leaf = 0
	namespace.Qualifiers = append([]analyze.QueryQualifier(nil), namespace.Qualifiers...)
	namespace.Qualifiers[0].Columns = namespace.Columns
	b.Query.Namespace = &namespace
	node, err := b.Bind(threePartUnitColumn("qa", "items", "v"), analyze.ExpressionPolicy{})
	if err != nil || node.(plan.ColumnExpr).Origin != first.Origin || len(b.Target.Uses) != 0 || !reflect.DeepEqual(b.Query.Sources[0].Uses, []int{0}) {
		t.Fatal(node, err, b.Target.Uses, b.Query.Sources[0].Uses)
	}
}
