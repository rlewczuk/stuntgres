package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func arrayUnitInteger(n int64) parser.Expr {
	return parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: n}}
}
func arrayUnitParameter(n int) parser.Expr {
	return parser.ParameterExpr{Number: n, Pos: parser.Position{Character: n + 10}}
}

func TestArrayExpressionCommonTypeAndExplicitTarget(t *testing.T) {
	e := engine.New()
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := parser.ArrayExpr{Elements: []parser.Expr{arrayUnitInteger(1), parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}, arrayUnitInteger(2147483648)}}
	node, err := b.BindArray(raw, sqltype.Usage{})
	if err != nil {
		t.Fatal(err)
	}
	if node.ResultUsage().Type.Element().ID() != sqltype.Int8 || node.Properties().RowDependent {
		t.Fatal(node.ResultUsage(), node.Properties())
	}
	value, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
	if err != nil || value.ArrayLen() != 3 || !value.ArrayElement(1).IsNull() || value.ArrayElement(1).Type() != b.Type(sqltype.Int8) {
		t.Fatal(value, err)
	}
	_, err = b.BindArray(parser.ArrayExpr{Pos: parser.Position{Character: 8}}, sqltype.Usage{})
	var state interface{ SQLState() string }
	if !errors.As(err, &state) || state.SQLState() != "42P18" {
		t.Fatal(err)
	}
	empty, err := b.BindArray(parser.ArrayExpr{}, b.Type(b.Type(sqltype.Int4).AssociatedArray()).Usage())
	if err != nil {
		t.Fatal(err)
	}
	value, err = empty.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
	if err != nil || value.IsNull() || value.ArrayLen() != 0 {
		t.Fatal(value, err)
	}
}

func TestArrayExpressionParameterManifestAndTraversal(t *testing.T) {
	e := engine.New()
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	intArray := b.Type(b.Type(sqltype.Int4).AssociatedArray())
	base := parser.CastExpr{Child: arrayUnitParameter(1), TypeName: parser.TypeName{Name: "integer", ArrayBounds: []int32{0}}, Syntax: parser.PostfixCast}
	raw := parser.SubscriptExpr{Base: base, Subscripts: []parser.ArraySubscript{{Lower: arrayUnitParameter(2), Upper: arrayUnitParameter(3), Slice: true}}}
	node, err := b.BindSubscript(raw)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := b.Finish()
	if err != nil || manifest.Count != 3 || manifest.Types[0].Type != intArray || manifest.Types[1].Type.ID() != sqltype.Int4 || manifest.Types[2].Type.ID() != sqltype.Int4 {
		t.Fatal(manifest, err)
	}
	var numbers []int
	plan.WalkExpression(node, func(child expr.Expr) {
		if p, ok := child.(plan.ParameterExpr); ok {
			numbers = append(numbers, p.Number)
		}
	})
	if !reflect.DeepEqual(numbers, []int{1, 2, 3}) {
		t.Fatal("bound traversal omitted base/bounds", numbers)
	}
	if !node.Properties().ParameterDependent || node.ResultUsage().Type != intArray {
		t.Fatal(node.Properties(), node.ResultUsage())
	}
	input, err := intArray.Input(context.Background(), "[0:2]={7,8,9}", -1)
	if err != nil {
		t.Fatal(err)
	}
	lower, _ := sqltype.Integer(b.Type(sqltype.Int4), 0)
	upper, _ := sqltype.Integer(b.Type(sqltype.Int4), 1)
	value, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Parameters: []sqltype.Value{input, lower, upper}}})
	if err != nil {
		t.Fatal(err)
	}
	text, err := intArray.Output(context.Background(), value)
	if err != nil || text != "{7,8}" {
		t.Fatal(text, err)
	}
}

func TestArrayExpressionImmutableRewriteAndGeneration(t *testing.T) {
	e := engine.New()
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := parser.SubscriptExpr{Base: parser.ArrayExpr{Elements: []parser.Expr{arrayUnitInteger(7), arrayUnitInteger(8)}}, Subscripts: []parser.ArraySubscript{{Upper: arrayUnitInteger(1)}}}
	original, err := b.BindSubscript(raw)
	if err != nil {
		t.Fatal(err)
	}
	node := original.(plan.SubscriptExpr)
	saved := node.Subscripts[0].Upper
	replacement := plan.LiteralExpr{Info: b.Info(b.Type(sqltype.Int4).Usage(), parser.Position{}, expr.Properties{}), Value: sqltype.Null(b.Type(sqltype.Int4))}
	mapped, changed, err := plan.MapSubscript(node, func(child expr.Expr) (expr.Expr, bool, error) {
		if child == saved {
			return replacement, true, nil
		}
		return child, false, nil
	})
	if err != nil || !changed || node.Subscripts[0].Upper != saved || mapped.Subscripts[0].Upper != replacement {
		t.Fatal("rewrite modified original/omitted bound", changed, err)
	}
	other := engine.New()
	read := other.CatalogRead()
	retagged, changed, err := stored.RetagTypes(read, original, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if d == nil {
			return nil
		}
		next, _ := read.LookupType(d.ID())
		return next
	})
	if err != nil || !changed || retagged.ResultUsage().Type == original.ResultUsage().Type {
		t.Fatal(retagged, changed, err)
	}
	value, err := retagged.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
	if err != nil {
		t.Fatal(err)
	}
	n, err := value.Integer()
	if err != nil || n != 7 {
		t.Fatal(n, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := original.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: canceled}}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

type arrayDemandExpression struct {
	expr.Info
	evaluate func(*exec.Context) (sqltype.Value, error)
}

func (n arrayDemandExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	return n.evaluate(evalContext(ctx))
}

func TestArrayExpressionRuntimeDemandOrder(t *testing.T) {
	e := engine.New()
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	typ := b.Type(sqltype.Int4)
	arr := b.Type(typ.AssociatedArray())
	ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	one, _ := sqltype.Integer(typ, 1)
	input, err := arr.Input(ctx.Context, "{{7}}", -1)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	child := func(label string, value sqltype.Value) expr.Expr {
		return arrayDemandExpression{b.Info(value.Type().Usage(), parser.Position{}, expr.Properties{}), func(*exec.Context) (sqltype.Value, error) { order = append(order, label); return value, nil }}
	}
	node := plan.SubscriptExpr{Info: b.Info(arr.Usage(), parser.Position{}, expr.Properties{}), Base: child("base", input), Slice: true, Subscripts: []plan.BoundArraySubscript{{Lower: child("l1", one), Upper: child("u1", one), Slice: true}, {Lower: child("l2", one), Upper: child("u2", one), Slice: true}}}
	if _, err := node.Eval(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"base", "u1", "u2", "l1", "l2"}) {
		t.Fatal(order)
	}
	order = nil
	node.Base = child("base", sqltype.Null(arr))
	if value, err := node.Eval(ctx); err != nil || !value.IsNull() || !reflect.DeepEqual(order, []string{"base"}) {
		t.Fatal(value, order, err)
	}
	coercionPlan, err := sqltype.PlanCoercion(arr.Usage(), arr.Usage(), sqltype.Assignment)
	if err != nil {
		t.Fatal(err)
	}
	target := plan.ArrayAssignmentTarget{Node: node, Reapply: coercionPlan}
	order = nil
	if _, err := target.ApplyExpression(ctx, input, child("rhs", input)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"u1", "u2", "l1", "l2", "rhs"}) {
		t.Fatal(order)
	}
	order = nil
	target.Node.Subscripts[0].Upper = child("u1", sqltype.Null(typ))
	_, err = target.ApplyExpression(ctx, input, child("rhs", input))
	var state interface{ SQLState() string }
	if !errors.As(err, &state) || state.SQLState() != "22004" || !reflect.DeepEqual(order, []string{"u1", "u2", "l1", "l2"}) {
		t.Fatal(order, err)
	}
	order = nil
	stop := errors.New("lower evaluation failed")
	target.Node.Subscripts[1].Lower = arrayDemandExpression{b.Info(typ.Usage(), parser.Position{}, expr.Properties{}), func(*exec.Context) (sqltype.Value, error) { order = append(order, "l2"); return sqltype.Value{}, stop }}
	_, err = target.ApplyExpression(ctx, input, child("rhs", input))
	if !errors.Is(err, stop) || !reflect.DeepEqual(order, []string{"u1", "u2", "l1", "l2"}) {
		t.Fatal("bound error must precede NULL check and RHS", order, err)
	}
}

func TestArrayExpressionColumnUseAndTargetManifest(t *testing.T) {
	e := engine.New()
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	intArray := b.Type(b.Type(sqltype.Int4).AssociatedArray())
	b.Target.Relation = &catalog.Relation{Columns: []catalog.Column{{Name: "a", Usage: intArray.Usage()}, {Name: "hidden", Usage: b.Type(sqltype.Int4).Usage()}}, Attributes: []int32{3, 7}, Index: map[string]int{"a": 0, "hidden": 1}}
	bindTestTargetNamespace(b)
	column := func(name string) parser.Expr {
		return parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}}
	}
	raw := []parser.ArraySubscript{{Upper: column("hidden")}}
	node, err := b.BindSubscript(parser.SubscriptExpr{Base: column("a"), Subscripts: raw})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(b.Target.Uses, []int{0, 1}) {
		t.Fatal("base and bound must contribute actual SELECT uses", b.Target.Uses)
	}
	if _, physical := node.(plan.ColumnExpr); physical {
		t.Fatal("derived access has physical origin")
	}
	b.Target.Uses = nil
	target, err := b.BindArrayTarget(b.Target.Relation.Columns[0], raw, parser.Position{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(b.Target.Uses, []int{1}) {
		t.Fatal("partial target must read bounds, not the implicit old container", b.Target.Uses)
	}
	var slots []int
	var storage [13]expr.Expr
	for _, child := range plan.SubscriptChildren(target.Node, &storage) {
		if col, ok := child.(plan.ColumnExpr); ok {
			slots = append(slots, col.Slot)
		}
	}
	if !reflect.DeepEqual(slots, []int{1}) {
		t.Fatal("target traversal must retain bound column references", slots)
	}
}

func TestArrayExpressionNullCountComposition(t *testing.T) {
	e := engine.New()
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	for _, tc := range []struct {
		name, position string
		character      int
		count          int64
	}{
		{"num_nulls", "27", 27, 0}, {"num_nonnulls", "30", 30, 1},
	} {
		array := parser.ArrayExpr{Pos: parser.Position{Character: tc.character}, Elements: []parser.Expr{arrayUnitInteger(1), parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}}}
		raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: tc.name}}}, Args: []parser.Expr{array}}
		node, err := b.BindFunction(raw)
		if err != nil {
			t.Fatal(err)
		}
		value, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
		if err != nil {
			t.Fatal(err)
		}
		count, err := value.Integer()
		if err != nil || count != tc.count || value.Type() != b.Type(sqltype.Int4) {
			t.Fatal(count, value.Type(), err)
		}
		raw.Variadic = true
		_, err = b.BindFunction(raw)
		nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "0A000", Message: "VARIADIC arrays in null-count arguments are unsupported", Position: tc.position})
		raw.Args = []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1, Pos: array.Pos}}}
		_, err = b.BindFunction(raw)
		nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42804", Message: "VARIADIC argument must be an array", Position: tc.position})
	}
}

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

func nullCountUnitDiagnostic(t *testing.T, err error, want sqlstate.Error) {
	t.Helper()
	want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
	var got *sqlstate.Error
	if !errors.As(err, &got) || *got != want {
		t.Fatalf("got %#v (%v), want %#v", got, err, want)
	}
}
