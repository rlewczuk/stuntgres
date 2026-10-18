package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestQueryRecursiveCTELimitZeroAndEmptyAnchor(t *testing.T) {
	owner, state, node := recursiveUnitFixture(t, true, []int64{1}, 3)
	defer owner.close()
	int8, _ := New().CatalogRead().LookupType(sqltype.Int8)
	zero, err := sqltype.Integer(int8, 0)
	if err != nil {
		t.Fatal(err)
	}
	limit := &limitNode{querySchema: node.Schema(), child: node, count: literalExpression{Info: expr.Info{Usage: int8.Usage()}, value: zero}}
	stage, err := openLimitNode(context.Background(), state, limit)
	if err != nil {
		t.Fatal(err)
	}
	if stage.count() != 0 || owner.producers != nil || len(stage.schema) != 1 {
		t.Fatal("LIMIT0 opened recursion or lost schema")
	}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	emptyOwner, emptyState, emptyNode := recursiveUnitFixture(t, true, nil, 3)
	defer emptyOwner.close()
	empty, err := openCTEScan(context.Background(), emptyState, emptyNode)
	if err != nil {
		t.Fatal(err)
	}
	if _, present, err := empty.nextOccurrence(context.Background()); present || err != nil {
		t.Fatal(present, err)
	}
	runtime := emptyOwner.producers[emptyNode.cte].recursion
	if runtime.phase != 2 || runtime.source.flatCount != 0 || runtime.opened {
		t.Fatal("empty anchor opened a recursive generation")
	}
}

func TestQueryRecursiveCTEPrimaryErrorAndRequiredClose(t *testing.T) {
	for _, primaryFailure := range []bool{false, true} {
		owner, state, node := recursiveUnitFixture(t, true, []int64{1}, 3)
		stage, err := openCTEScan(context.Background(), state, node)
		if err != nil {
			t.Fatal(err)
		}
		cteUnitNext(t, &stage)
		producer := owner.producers[node.cte]
		cleanup := errors.New("required recursive child close failed")
		primary := errors.New("recursive term failed")
		closes := 0
		if primaryFailure {
			n := node.query.Root.(*projectNode).child.(*recursiveUnionNode)
			n.rightExpressions = []expr.Expr{queryProbe(n.Schema()[0].Usage, func(*EvalContext) (sqltype.Value, error) {
				producer.recursion.arm.inner.finish = func() error { closes++; return cleanup }
				return sqltype.Value{}, primary
			})}
		} else {
			producer.recursion.arm.inner.finish = func() error { closes++; return cleanup }
			primary = cleanup
		}
		if _, _, err := stage.nextOccurrence(context.Background()); err != primary {
			t.Fatal("primary/close precedence", err, primary)
		}
		if err := owner.close(); err != primary || closes != 1 {
			t.Fatal(err, closes)
		}
		if _, err := producer.ensure(context.Background(), 0); err != primary {
			t.Fatal("failure not sticky on replay", err)
		}
		if err := owner.close(); err != primary || closes != 1 {
			t.Fatal("closed recursive child twice", err, closes)
		}
	}
}

func TestQueryRecursiveCTELongIterationAndFreshInvocation(t *testing.T) {
	initial, base, node := recursiveUnitFixture(t, true, []int64{1}, 2000)
	defer initial.close()
	for range 2 {
		stateCopy := *base
		owner := &cteInvocation{frame: 1, state: stateCopy}
		stateCopy.ctes = owner
		state := &stateCopy
		stage, err := openCTEScan(context.Background(), state, node)
		if err != nil {
			t.Fatal(err)
		}
		for want := int64(1); want <= 2000; want++ {
			row := cteUnitNext(t, &stage)
			got, err := row[0].Integer()
			if err != nil || got != want {
				t.Fatal(got, want, err)
			}
		}
		if _, present, err := stage.nextOccurrence(context.Background()); present || err != nil {
			t.Fatal(present, err)
		}
		if err := owner.close(); err != nil {
			t.Fatal(err)
		}
		if owner.producers[node.cte].recursion.seen != nil {
			t.Fatal("ALL retained seen state")
		}
	}
}
