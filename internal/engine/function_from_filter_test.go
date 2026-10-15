package engine

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestJSONEachPredecessorFilterPlacement(t *testing.T) {
	types := jsonObjectUnitTypes()
	left := &valuesNode{querySchema: querySchema{{Usage: types[sqltype.Bool].Usage()}}}
	right := &functionFromNode{querySchema: querySchema{{Usage: types[sqltype.Text].Usage()}, {Usage: types[sqltype.JSON].Usage()}}, correlated: true, frame: 7}
	join := &joinNode{querySchema: append(append(querySchema(nil), left.Schema()...), right.Schema()...), left: left, right: right, leftWidth: 1, rightWidth: 2, kind: parser.JoinInner, correlatedRight: true, frame: 7}
	predicate := columnExpression{expressionInfo: expressionInfo{usage: types[sqltype.Bool].Usage(), properties: ExprProperties{RowDependent: true}}, frame: 7, scope: 1, slot: 0}
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
		unsafe.expressionInfo.properties = property
		unchanged("expression barrier", join, unsafe)
	}
	foreign := predicate
	foreign.frame = 9
	unchanged("outer frame", join, foreign)
	rhs := predicate
	rhs.slot = 1
	unchanged("right reference", join, rhs)
	unsupported := &queryProbeExpression{expressionInfo: expressionInfo{usage: types[sqltype.Bool].Usage()}, eval: func(*EvalContext) (sqltype.Value, error) {
		t.Fatal("placement evaluated predicate")
		return sqltype.Value{}, nil
	}}
	unchanged("unsupported dependency", join, unsupported)
	unchanged("nonjoin", left, predicate)
}
