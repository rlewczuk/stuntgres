package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestCountExpressionVolatileCalls(t *testing.T) {
	e := engine.New()
	query, source := enginetest.QueryFixture(t, e, "SELECT count(random()),count(random()) FROM t")
	node := query.Root.(*plan.ProjectNode).Child.(*plan.AggregateNode)
	if len(node.Calls) != 2 {
		t.Fatal("volatile calls shared", len(node.Calls))
	}
	// The low-level source fixture has no execution scope. Random requires
	// the same real session/statement authority as ordinary query execution.
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(context.Canceled)
	core.BorrowQueryAuthority(source, scope)
	rows, count, err := exec.Finalize(context.Background(), query.Root, source, nil)
	if err != nil || count != 1 || rows == nil {
		t.Fatal(rows, count, err)
	}
	defer rows.Close()
	values := make([]sqltype.Value, 2)
	present, err := rows.Next(context.Background(), values)
	if !present || err != nil {
		t.Fatal(present, err)
	}
	for _, value := range values {
		n, _ := value.Integer()
		if n != 4 {
			t.Fatal(n)
		}
	}
}
