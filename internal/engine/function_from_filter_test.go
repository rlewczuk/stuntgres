package engine

import (
	"context"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestJSONEachPredecessorFilterPlacement(t *testing.T) {
	types := jsonObjectUnitTypes()
	left := &valuesNode{querySchema: querySchema{{Usage: types[sqltype.Bool].Usage()}}}
	right := &functionFromNode{querySchema: querySchema{{Usage: types[sqltype.Text].Usage()}, {Usage: types[sqltype.JSON].Usage()}}, correlated: true, frame: 7}
	join := &joinNode{querySchema: append(append(querySchema(nil), left.Schema()...), right.Schema()...), left: left, right: right, leftWidth: 1, rightWidth: 2, kind: parser.JoinInner, correlatedRight: true, frame: 7}
	predicate := columnExpression{Info: expressionInfo{Usage: types[sqltype.Bool].Usage(), Property: ExprProperties{RowDependent: true}}, frame: 7, scope: 1, slot: 0}
	moved := functionFromWhere(join, predicate)
	if moved == nil || moved == join || moved.right != right || moved.frame != join.frame || moved.leftWidth != 1 || moved.rightWidth != 2 {
		t.Fatal("incorrect copied join", moved)
	}
	filter, ok := moved.left.(*filterNode)
	if !ok || filter.child != left || filter.predicate != predicate || len(filter.Schema()) != 1 || join.left != left {
		t.Fatal("predicate duplicated or retained tree mutated", moved)
	}
	unchanged := func(label string, root LogicalNode, predicate BoundExpr) {
		t.Helper()
		if functionFromWhere(root, predicate) != nil {
			t.Fatal("unsafe predicate moved", label)
		}
	}
	for _, kind := range []parser.JoinKind{parser.JoinLeft, parser.JoinRight, parser.JoinFull} {
		copy := *join
		copy.kind = kind
		unchanged("outer", &copy, predicate)
	}
	copy := *join
	copy.correlatedRight = false
	unchanged("uncorrelated", &copy, predicate)
	copy = *join
	copy.merges = []BoundExpr{predicate}
	unchanged("merged", &copy, predicate)
	copy = *join
	copy.right = &derivedNode{}
	unchanged("subquery boundary", &copy, predicate)
	for _, property := range []ExprProperties{{RowDependent: true, Volatility: Volatile}, {RowDependent: true, StageDependent: true}, {RowDependent: true, RuntimeDomainConversion: true}, {RowDependent: true, DomainDependent: true}} {
		unsafe := predicate
		unsafe.Info.Property = property
		unchanged("expression barrier", join, unsafe)
	}
	foreign := predicate
	foreign.frame = 9
	unchanged("outer frame", join, foreign)
	rhs := predicate
	rhs.slot = 1
	unchanged("right reference", join, rhs)
	unsupported := &queryProbeExpression{Info: expressionInfo{Usage: types[sqltype.Bool].Usage()}, eval: func(*EvalContext) (sqltype.Value, error) {
		t.Fatal("placement evaluated predicate")
		return sqltype.Value{}, nil
	}}
	unchanged("unsupported dependency", join, unsupported)
	unchanged("nonjoin", left, predicate)
}

func TestJSONElementsPredecessorConjunctDemand(t *testing.T) {
	types := jsonObjectUnitTypes()
	usage := types[sqltype.Bool].Usage()
	left := &valuesNode{querySchema: querySchema{{Usage: usage}}}
	right := &functionFromNode{querySchema: querySchema{{Usage: usage}}, frame: 7}
	join := &joinNode{querySchema: querySchema{{Usage: usage}, {Usage: usage}}, left: left, right: right, leftWidth: 1, rightWidth: 1, kind: parser.JoinInner, frame: 7}
	column := columnExpression{Info: expressionInfo{Usage: usage, Property: ExprProperties{RowDependent: true}}, frame: 7, scope: 1, slot: 0}
	calls := 0
	volatile := &queryProbeExpression{Info: expressionInfo{Usage: usage, Property: ExprProperties{Volatility: Volatile}}, eval: func(*EvalContext) (sqltype.Value, error) {
		calls++
		return sqltype.Boolean(usage.Type, true), nil
	}}
	predicate := logicalExpression{Info: expressionInfo{Usage: usage, Property: mergeExpressionProperties(column.Properties(), volatile.Properties())}, op: parser.LogicalAnd, left: column, right: volatile}
	root, ok := functionFromConjuncts(join, predicate).(*filterNode)
	if !ok || root.predicate != volatile {
		t.Fatal("volatile residual lost", root)
	}
	moved, ok := root.child.(*joinNode)
	if !ok || moved == join || moved.right != right || moved.correlatedRight || moved.frame != join.frame {
		t.Fatal("uncorrelated function join not copied", root.child)
	}
	predecessor, ok := moved.left.(*filterNode)
	if !ok || predecessor.child != left || predecessor.predicate != column || join.left != left || predicate.left != column || predicate.right != volatile {
		t.Fatal("predicate duplicated or canonical tree mutated", moved)
	}
	// FALSE and UNKNOWN predecessor rows both reject before expansion. Only
	// the TRUE row reaches three members, evaluating the volatile residual
	// three times rather than once per source or once per unfiltered member.
	for _, value := range []sqltype.Value{sqltype.Boolean(usage.Type, true), sqltype.Boolean(usage.Type, false), sqltype.Null(usage.Type)} {
		evaluation := &EvalContext{Context: context.Background(), Scope: 1, Frame: 7, Row: []sqltype.Value{value}}
		selected, err := predecessor.predicate.Eval(evaluation)
		if err != nil {
			t.Fatal(err)
		}
		truth, err := TruthOf(selected)
		if err != nil {
			t.Fatal(err)
		}
		if truth != True {
			continue
		}
		for range 3 {
			if _, err := root.predicate.Eval(evaluation); err != nil {
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
	left := &valuesNode{querySchema: querySchema{{Usage: usage}}}
	right := &functionFromNode{querySchema: querySchema{{Usage: usage}}, frame: 7}
	join := &joinNode{querySchema: querySchema{{Usage: usage}, {Usage: usage}}, left: left, right: right, leftWidth: 1, rightWidth: 1, kind: parser.JoinInner, frame: 7}
	column := columnExpression{Info: expressionInfo{Usage: usage, Property: ExprProperties{RowDependent: true}}, frame: 7, scope: 1, slot: 0}
	and := func(a, b BoundExpr) logicalExpression {
		return logicalExpression{Info: expressionInfo{Usage: usage, Property: mergeExpressionProperties(a.Properties(), b.Properties())}, op: parser.LogicalAnd, left: a, right: b}
	}
	var unsafe []BoundExpr
	for _, properties := range []ExprProperties{{RowDependent: true, Volatility: Volatile}, {RowDependent: true, StageDependent: true}, {RowDependent: true, DomainDependent: true}, {RowDependent: true, RuntimeDomainConversion: true}} {
		copy := column
		copy.Property = properties
		unsafe = append(unsafe, copy)
	}
	foreign := column
	foreign.frame = 9
	rhs := column
	rhs.slot = 1
	virtual := column
	virtual.virtual = literalExpression{Info: expressionInfo{Usage: usage}, value: sqltype.Boolean(usage.Type, true)}
	or := logicalExpression{Info: column.Info, op: parser.LogicalOr, left: column, right: column}
	not := logicalExpression{Info: column.Info, op: parser.LogicalNot, left: column}
	query := and(column, column)
	query.queryRight = true
	unsupported := &queryProbeExpression{Info: expressionInfo{Usage: usage}, eval: func(*EvalContext) (sqltype.Value, error) {
		t.Fatal("partition evaluated expression")
		return sqltype.Value{}, nil
	}}
	unsafe = append(unsafe, foreign, rhs, virtual, or, not, query, unsupported)
	for _, barrier := range unsafe {
		before, after := partitionFunctionConjuncts(join, and(column, barrier))
		if before != column || after != barrier {
			t.Fatalf("unsafe conjunct moved: %T", barrier)
		}
		if functionFromConjuncts(join, and(barrier, barrier)) != nil {
			t.Fatalf("no-safe-conjunct expression moved: %T", barrier)
		}
	}
	for _, kind := range []parser.JoinKind{parser.JoinLeft, parser.JoinRight, parser.JoinFull} {
		copy := *join
		copy.kind = kind
		if functionFromConjuncts(&copy, and(column, column)) != nil {
			t.Fatal("outer join boundary crossed")
		}
	}
	copy := *join
	copy.merges = []BoundExpr{column}
	if functionFromConjuncts(&copy, and(column, column)) != nil {
		t.Fatal("merged join boundary crossed")
	}
	copy = *join
	copy.right = &derivedNode{}
	if functionFromConjuncts(&copy, and(column, column)) != nil || functionFromConjuncts(left, and(column, column)) != nil {
		t.Fatal("non-function boundary crossed")
	}
	// A fully eligible conjunction moves as a whole, without a residual or
	// mutation of the original expression and join.
	if moved, ok := functionFromConjuncts(join, and(column, column)).(*joinNode); !ok || moved == join || moved.right != right || join.left != left {
		t.Fatal("safe conjunction not moved immutably")
	}
}
