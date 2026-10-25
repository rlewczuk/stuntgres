package exec

import (
	"context"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestJSONEachPredecessorFilterPlacement(t *testing.T) {
	types := jsonObjectUnitTypes()
	left := &plan.ValuesNode{QuerySchema: plan.QuerySchema{{Usage: types[sqltype.Bool].Usage()}}}
	right := &plan.FunctionFromNode{QuerySchema: plan.QuerySchema{{Usage: types[sqltype.Text].Usage()}, {Usage: types[sqltype.JSON].Usage()}}, Correlated: true, Frame: 7}
	join := &plan.JoinNode{QuerySchema: append(append(plan.QuerySchema(nil), left.Schema()...), right.Schema()...), Left: left, Right: right, LeftWidth: 1, RightWidth: 2, Kind: parser.JoinInner, CorrelatedRight: true, Frame: 7}
	predicate := plan.ColumnExpr{Info: expr.Info{Usage: types[sqltype.Bool].Usage(), Property: expr.Properties{RowDependent: true}}, Frame: 7, Scope: 1, Slot: 0}
	moved := FunctionFromWhere(join, predicate)
	if moved == nil || moved == join || moved.Right != right || moved.Frame != join.Frame || moved.LeftWidth != 1 || moved.RightWidth != 2 {
		t.Fatal("incorrect copied join", moved)
	}
	filter, ok := moved.Left.(*plan.FilterNode)
	if !ok || filter.Child != left || filter.Predicate != predicate || len(filter.Schema()) != 1 || join.Left != left {
		t.Fatal("predicate duplicated or retained tree mutated", moved)
	}
	unchanged := func(label string, root plan.Node, predicate expr.Expr) {
		t.Helper()
		if FunctionFromWhere(root, predicate) != nil {
			t.Fatal("unsafe predicate moved", label)
		}
	}
	for _, kind := range []parser.JoinKind{parser.JoinLeft, parser.JoinRight, parser.JoinFull} {
		copy := *join
		copy.Kind = kind
		unchanged("outer", &copy, predicate)
	}
	copy := *join
	copy.CorrelatedRight = false
	unchanged("uncorrelated", &copy, predicate)
	copy = *join
	copy.Merges = []expr.Expr{predicate}
	unchanged("merged", &copy, predicate)
	copy = *join
	copy.Right = &plan.DerivedNode{}
	unchanged("subquery boundary", &copy, predicate)
	for _, property := range []expr.Properties{{RowDependent: true, Volatility: expr.Volatile}, {RowDependent: true, StageDependent: true}, {RowDependent: true, RuntimeDomainConversion: true}, {RowDependent: true, DomainDependent: true}} {
		unsafe := predicate
		unsafe.Info.Property = property
		unchanged("expression barrier", join, unsafe)
	}
	foreign := predicate
	foreign.Frame = 9
	unchanged("outer frame", join, foreign)
	rhs := predicate
	rhs.Slot = 1
	unchanged("right reference", join, rhs)
	unsupported := &queryProbeExpression{Info: expr.Info{Usage: types[sqltype.Bool].Usage()}, eval: func(*Context) (sqltype.Value, error) {
		t.Fatal("placement evaluated predicate")
		return sqltype.Value{}, nil
	}}
	unchanged("unsupported dependency", join, unsupported)
	unchanged("nonjoin", left, predicate)
}

func TestJSONElementsPredecessorConjunctDemand(t *testing.T) {
	types := jsonObjectUnitTypes()
	usage := types[sqltype.Bool].Usage()
	left := &plan.ValuesNode{QuerySchema: plan.QuerySchema{{Usage: usage}}}
	right := &plan.FunctionFromNode{QuerySchema: plan.QuerySchema{{Usage: usage}}, Frame: 7}
	join := &plan.JoinNode{QuerySchema: plan.QuerySchema{{Usage: usage}, {Usage: usage}}, Left: left, Right: right, LeftWidth: 1, RightWidth: 1, Kind: parser.JoinInner, Frame: 7}
	column := plan.ColumnExpr{Info: expr.Info{Usage: usage, Property: expr.Properties{RowDependent: true}}, Frame: 7, Scope: 1, Slot: 0}
	calls := 0
	volatile := &queryProbeExpression{Info: expr.Info{Usage: usage, Property: expr.Properties{Volatility: expr.Volatile}}, eval: func(*Context) (sqltype.Value, error) {
		calls++
		return sqltype.Boolean(usage.Type, true), nil
	}}
	predicate := plan.LogicalExpr{Info: expr.Info{Usage: usage, Property: expr.MergeProperties(column.Properties(), volatile.Properties())}, Op: parser.LogicalAnd, Left: column, Right: volatile}
	root, ok := plan.FunctionFromConjuncts(join, predicate).(*plan.FilterNode)
	if !ok || root.Predicate != volatile {
		t.Fatal("volatile residual lost", root)
	}
	moved, ok := root.Child.(*plan.JoinNode)
	if !ok || moved == join || moved.Right != right || moved.CorrelatedRight || moved.Frame != join.Frame {
		t.Fatal("uncorrelated function join not copied", root.Child)
	}
	predecessor, ok := moved.Left.(*plan.FilterNode)
	if !ok || predecessor.Child != left || predecessor.Predicate != column || join.Left != left || predicate.Left != column || predicate.Right != volatile {
		t.Fatal("predicate duplicated or canonical tree mutated", moved)
	}
	// FALSE and UNKNOWN predecessor rows both reject before expansion. Only
	// the TRUE row reaches three members, evaluating the volatile residual
	// three times rather than once per source or once per unfiltered member.
	for _, value := range []sqltype.Value{sqltype.Boolean(usage.Type, true), sqltype.Boolean(usage.Type, false), sqltype.Null(usage.Type)} {
		evaluation := &Context{EvalStatement: EvalStatement{Context: context.Background()}, EvalFrame: EvalFrame{Scope: 1, Frame: 7, Row: []sqltype.Value{value}}}
		selected, err := predecessor.Predicate.Eval(evaluation)
		if err != nil {
			t.Fatal(err)
		}
		truth, err := plan.TruthOf(selected)
		if err != nil {
			t.Fatal(err)
		}
		if truth != plan.True {
			continue
		}
		for range 3 {
			if _, err := root.Predicate.Eval(evaluation); err != nil {
				t.Fatal(err)
			}
		}
	}
	if calls != 3 {
		t.Fatal("wrong residual demand", calls)
	}
}

func TestJSONElementsPredecessorConjunctBarriers(t *testing.T) {
	types := jsonObjectUnitTypes()
	usage := types[sqltype.Bool].Usage()
	left := &plan.ValuesNode{QuerySchema: plan.QuerySchema{{Usage: usage}}}
	right := &plan.FunctionFromNode{QuerySchema: plan.QuerySchema{{Usage: usage}}, Frame: 7}
	join := &plan.JoinNode{QuerySchema: plan.QuerySchema{{Usage: usage}, {Usage: usage}}, Left: left, Right: right, LeftWidth: 1, RightWidth: 1, Kind: parser.JoinInner, Frame: 7}
	column := plan.ColumnExpr{Info: expr.Info{Usage: usage, Property: expr.Properties{RowDependent: true}}, Frame: 7, Scope: 1, Slot: 0}
	and := func(a, b expr.Expr) plan.LogicalExpr {
		return plan.LogicalExpr{Info: expr.Info{Usage: usage, Property: expr.MergeProperties(a.Properties(), b.Properties())}, Op: parser.LogicalAnd, Left: a, Right: b}
	}
	var unsafe []expr.Expr
	for _, properties := range []expr.Properties{{RowDependent: true, Volatility: expr.Volatile}, {RowDependent: true, StageDependent: true}, {RowDependent: true, DomainDependent: true}, {RowDependent: true, RuntimeDomainConversion: true}} {
		copy := column
		copy.Property = properties
		unsafe = append(unsafe, copy)
	}
	foreign := column
	foreign.Frame = 9
	rhs := column
	rhs.Slot = 1
	virtual := column
	virtual.Virtual = plan.LiteralExpr{Info: expr.Info{Usage: usage}, Value: sqltype.Boolean(usage.Type, true)}
	or := plan.LogicalExpr{Info: column.Info, Op: parser.LogicalOr, Left: column, Right: column}
	not := plan.LogicalExpr{Info: column.Info, Op: parser.LogicalNot, Left: column}
	query := and(column, column)
	query.QueryRight = true
	unsupported := &queryProbeExpression{Info: expr.Info{Usage: usage}, eval: func(*Context) (sqltype.Value, error) {
		t.Fatal("partition evaluated expression")
		return sqltype.Value{}, nil
	}}
	unsafe = append(unsafe, foreign, rhs, virtual, or, not, query, unsupported)
	for _, barrier := range unsafe {
		before, after := plan.PartitionFunctionConjuncts(join, and(column, barrier))
		if before != column || after != barrier {
			t.Fatalf("unsafe conjunct moved: %T", barrier)
		}
		if plan.FunctionFromConjuncts(join, and(barrier, barrier)) != nil {
			t.Fatalf("no-safe-conjunct expression moved: %T", barrier)
		}
	}
	for _, kind := range []parser.JoinKind{parser.JoinLeft, parser.JoinRight, parser.JoinFull} {
		copy := *join
		copy.Kind = kind
		if plan.FunctionFromConjuncts(&copy, and(column, column)) != nil {
			t.Fatal("outer join boundary crossed")
		}
	}
	copy := *join
	copy.Merges = []expr.Expr{column}
	if plan.FunctionFromConjuncts(&copy, and(column, column)) != nil {
		t.Fatal("merged join boundary crossed")
	}
	copy = *join
	copy.Right = &plan.DerivedNode{}
	if plan.FunctionFromConjuncts(&copy, and(column, column)) != nil || plan.FunctionFromConjuncts(left, and(column, column)) != nil {
		t.Fatal("non-function boundary crossed")
	}
	// A fully eligible conjunction moves as a whole, without a residual or
	// mutation of the original expression and join.
	if moved, ok := plan.FunctionFromConjuncts(join, and(column, column)).(*plan.JoinNode); !ok || moved == join || moved.Right != right || join.Left != left {
		t.Fatal("safe conjunction not moved immutably")
	}
}

func jsonObjectUnitTypes() map[sqltype.ID]*sqltype.Descriptor {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, d := range sqltype.Builtins() {
		types[d.ID()] = d
	}
	return types
}

type queryProbeExpression struct {
	expr.Info
	eval func(*Context) (sqltype.Value, error)
}

func (e queryProbeExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	return e.eval(evalContext(ctx))
}

// evalContext recovers the engine evaluation context in test probes. A nil or
// foreign context yields nil.
func evalContext(ctx expr.Context) *Context {
	c, _ := ctx.(*Context)
	return c
}
