package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

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
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := parser.ArrayExpr{Elements: []parser.Expr{arrayUnitInteger(1), parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}, arrayUnitInteger(2147483648)}}
	node, err := b.bindArray(raw, sqltype.Usage{})
	if err != nil {
		t.Fatal(err)
	}
	if node.ResultUsage().Type.Element().ID() != sqltype.Int8 || node.Properties().RowDependent {
		t.Fatal(node.ResultUsage(), node.Properties())
	}
	value, err := node.Eval(&EvalContext{Context: context.Background()})
	if err != nil || value.ArrayLen() != 3 || !value.ArrayElement(1).IsNull() || value.ArrayElement(1).Type() != b.typ(sqltype.Int8) {
		t.Fatal(value, err)
	}
	_, err = b.bindArray(parser.ArrayExpr{Pos: parser.Position{Character: 8}}, sqltype.Usage{})
	var state interface{ SQLState() string }
	if !errors.As(err, &state) || state.SQLState() != "42P18" {
		t.Fatal(err)
	}
	empty, err := b.bindArray(parser.ArrayExpr{}, b.typ(b.typ(sqltype.Int4).AssociatedArray()).Usage())
	if err != nil {
		t.Fatal(err)
	}
	value, err = empty.Eval(&EvalContext{Context: context.Background()})
	if err != nil || value.IsNull() || value.ArrayLen() != 0 {
		t.Fatal(value, err)
	}
}

func TestArrayExpressionParameterManifestAndTraversal(t *testing.T) {
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	intArray := b.typ(b.typ(sqltype.Int4).AssociatedArray())
	base := parser.CastExpr{Child: arrayUnitParameter(1), TypeName: parser.TypeName{Name: "integer", ArrayBounds: []int32{0}}, Syntax: parser.PostfixCast}
	raw := parser.SubscriptExpr{Base: base, Subscripts: []parser.ArraySubscript{{Lower: arrayUnitParameter(2), Upper: arrayUnitParameter(3), Slice: true}}}
	node, err := b.bindSubscript(raw)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := b.finish()
	if err != nil || manifest.Count != 3 || manifest.Types[0].Type != intArray || manifest.Types[1].Type.ID() != sqltype.Int4 || manifest.Types[2].Type.ID() != sqltype.Int4 {
		t.Fatal(manifest, err)
	}
	var numbers []int
	walkBoundExpression(node, func(child BoundExpr) {
		if p, ok := child.(parameterExpression); ok {
			numbers = append(numbers, p.number)
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
	lower, _ := sqltype.Integer(b.typ(sqltype.Int4), 0)
	upper, _ := sqltype.Integer(b.typ(sqltype.Int4), 1)
	value, err := node.Eval(&EvalContext{Context: context.Background(), Parameters: []sqltype.Value{input, lower, upper}})
	if err != nil {
		t.Fatal(err)
	}
	text, err := intArray.Output(context.Background(), value)
	if err != nil || text != "{7,8}" {
		t.Fatal(text, err)
	}
}

func TestArrayExpressionImmutableRewriteAndGeneration(t *testing.T) {
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := parser.SubscriptExpr{Base: parser.ArrayExpr{Elements: []parser.Expr{arrayUnitInteger(7), arrayUnitInteger(8)}}, Subscripts: []parser.ArraySubscript{{Upper: arrayUnitInteger(1)}}}
	original, err := b.bindSubscript(raw)
	if err != nil {
		t.Fatal(err)
	}
	node := original.(subscriptExpression)
	saved := node.subscripts[0].upper
	replacement := literalExpression{expressionInfo: b.info(b.typ(sqltype.Int4).Usage(), parser.Position{}, ExprProperties{}), value: sqltype.Null(b.typ(sqltype.Int4))}
	mapped, changed, err := mapSubscript(node, func(child BoundExpr) (BoundExpr, bool, error) {
		if child == saved {
			return replacement, true, nil
		}
		return child, false, nil
	})
	if err != nil || !changed || node.subscripts[0].upper != saved || mapped.subscripts[0].upper != replacement {
		t.Fatal("rewrite modified original/omitted bound", changed, err)
	}
	other := New()
	read := other.CatalogRead()
	retagged, changed, err := retagBoundTypes(read, original, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if d == nil {
			return nil
		}
		next, _ := read.LookupType(d.ID())
		return next
	})
	if err != nil || !changed || retagged.ResultUsage().Type == original.ResultUsage().Type {
		t.Fatal(retagged, changed, err)
	}
	value, err := retagged.Eval(&EvalContext{Context: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	n, err := value.Integer()
	if err != nil || n != 7 {
		t.Fatal(n, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := original.Eval(&EvalContext{Context: canceled}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

type arrayDemandExpression struct {
	expressionInfo
	evaluate func(*EvalContext) (sqltype.Value, error)
}

func (n arrayDemandExpression) Eval(c *EvalContext) (sqltype.Value, error) { return n.evaluate(c) }

func TestArrayExpressionRuntimeDemandOrder(t *testing.T) {
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	typ := b.typ(sqltype.Int4)
	arr := b.typ(typ.AssociatedArray())
	ctx := &EvalContext{Context: context.Background()}
	one, _ := sqltype.Integer(typ, 1)
	input, err := arr.Input(ctx.Context, "{{7}}", -1)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	child := func(label string, value sqltype.Value) BoundExpr {
		return arrayDemandExpression{b.info(value.Type().Usage(), parser.Position{}, ExprProperties{}), func(*EvalContext) (sqltype.Value, error) { order = append(order, label); return value, nil }}
	}
	node := subscriptExpression{expressionInfo: b.info(arr.Usage(), parser.Position{}, ExprProperties{}), base: child("base", input), slice: true, subscripts: []boundArraySubscript{{lower: child("l1", one), upper: child("u1", one), slice: true}, {lower: child("l2", one), upper: child("u2", one), slice: true}}}
	if _, err := node.Eval(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"base", "u1", "u2", "l1", "l2"}) {
		t.Fatal(order)
	}
	order = nil
	node.base = child("base", sqltype.Null(arr))
	if value, err := node.Eval(ctx); err != nil || !value.IsNull() || !reflect.DeepEqual(order, []string{"base"}) {
		t.Fatal(value, order, err)
	}
	plan, err := sqltype.PlanCoercion(arr.Usage(), arr.Usage(), sqltype.Assignment)
	if err != nil {
		t.Fatal(err)
	}
	target := arrayAssignmentTarget{node: node, reapply: plan}
	order = nil
	if _, err := target.applyExpression(ctx, input, child("rhs", input)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"u1", "u2", "l1", "l2", "rhs"}) {
		t.Fatal(order)
	}
	order = nil
	target.node.subscripts[0].upper = child("u1", sqltype.Null(typ))
	_, err = target.applyExpression(ctx, input, child("rhs", input))
	var state interface{ SQLState() string }
	if !errors.As(err, &state) || state.SQLState() != "22004" || !reflect.DeepEqual(order, []string{"u1", "u2", "l1", "l2"}) {
		t.Fatal(order, err)
	}
	order = nil
	stop := errors.New("lower evaluation failed")
	target.node.subscripts[1].lower = arrayDemandExpression{b.info(typ.Usage(), parser.Position{}, ExprProperties{}), func(*EvalContext) (sqltype.Value, error) { order = append(order, "l2"); return sqltype.Value{}, stop }}
	_, err = target.applyExpression(ctx, input, child("rhs", input))
	if !errors.Is(err, stop) || !reflect.DeepEqual(order, []string{"u1", "u2", "l1", "l2"}) {
		t.Fatal("bound error must precede NULL check and RHS", order, err)
	}
}

func TestArrayExpressionColumnUseAndTargetManifest(t *testing.T) {
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	intArray := b.typ(b.typ(sqltype.Int4).AssociatedArray())
	b.relation = &relationDescriptor{columns: []ColumnDescriptor{{Name: "a", Usage: intArray.Usage()}, {Name: "hidden", Usage: b.typ(sqltype.Int4).Usage()}}, attributes: []int32{3, 7}, index: map[string]int{"a": 0, "hidden": 1}}
	column := func(name string) parser.Expr {
		return parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}}
	}
	raw := []parser.ArraySubscript{{Upper: column("hidden")}}
	node, err := b.bindSubscript(parser.SubscriptExpr{Base: column("a"), Subscripts: raw})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(b.uses, []int{0, 1}) {
		t.Fatal("base and bound must contribute actual SELECT uses", b.uses)
	}
	if _, physical := node.(columnExpression); physical {
		t.Fatal("derived access has physical origin")
	}
	b.uses = nil
	target, err := b.bindArrayTarget(b.relation.columns[0], raw, parser.Position{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(b.uses, []int{1, 0}) {
		t.Fatal("partial target must read bounds and original container", b.uses)
	}
	var slots []int
	var storage [13]BoundExpr
	for _, child := range subscriptChildren(target.node, &storage) {
		if col, ok := child.(columnExpression); ok {
			slots = append(slots, col.slot)
		}
	}
	if !reflect.DeepEqual(slots, []int{0, 1}) {
		t.Fatal("target traversal omitted a column", slots)
	}
}

func TestArrayExpressionRestoreVisitorModes(t *testing.T) {
	column := parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "a"}}}}
	bound := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "abs"}}}, Args: []parser.Expr{arrayUnitInteger(1)}}
	raw := parser.SubscriptExpr{Base: column, Subscripts: []parser.ArraySubscript{{Upper: bound}}}
	if err := restoreScalar(raw, true); err != nil {
		t.Fatal("stored binder must retain column/function admission", err)
	}
	if err := restoreScalar(raw, false); err == nil {
		t.Fatal("INSERT Restore whitelist expanded")
	}
	position := parser.Position{Character: 49}
	bound.Args = []parser.Expr{parser.ParameterExpr{Number: 1, Pos: position}}
	raw.Subscripts[0].Upper = bound
	err := restoreScalar(raw, true)
	var diagnostic interface{ SQLState() string }
	if !errors.As(err, &diagnostic) || diagnostic.SQLState() != "42P02" {
		t.Fatal(err)
	}
	state, ok := err.(*sqlstate.Error)
	if !ok || state.Message != "parameters are not allowed in restore" || state.Position != "49" {
		t.Fatal(err)
	}
}

func TestArrayExpressionNullCountComposition(t *testing.T) {
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	for _, tc := range []struct {
		name, position string
		character      int
		count          int64
	}{
		{"num_nulls", "27", 27, 0}, {"num_nonnulls", "30", 30, 1},
	} {
		array := parser.ArrayExpr{Pos: parser.Position{Character: tc.character}, Elements: []parser.Expr{arrayUnitInteger(1), parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}}}
		raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: tc.name}}}, Args: []parser.Expr{array}}
		node, err := b.bindFunction(raw)
		if err != nil {
			t.Fatal(err)
		}
		value, err := node.Eval(&EvalContext{Context: context.Background()})
		if err != nil {
			t.Fatal(err)
		}
		count, err := value.Integer()
		if err != nil || count != tc.count || value.Type() != b.typ(sqltype.Int4) {
			t.Fatal(count, value.Type(), err)
		}
		raw.Variadic = true
		_, err = b.bindFunction(raw)
		nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "0A000", Message: "VARIADIC arrays in null-count arguments are unsupported", Position: tc.position})
		raw.Args = []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1, Pos: array.Pos}}}
		_, err = b.bindFunction(raw)
		nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42804", Message: "VARIADIC argument must be an array", Position: tc.position})
	}
}
