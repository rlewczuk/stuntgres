package engine

import (
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

func TestAnyValueSlotsAndMetadata(t *testing.T) {
	_, q, source := queryCoreFixture(t, "SELECT any_value(v),any_value(v),count(v),any_value(f) FROM t")
	defer source.Close()
	node := q.Root.(*plan.ProjectNode).Child.(*plan.AggregateNode)
	if len(node.Calls) != 3 {
		t.Fatal("aggregate identity sharing", len(node.Calls))
	}
	for i, c := range q.Root.Schema() {
		if !c.NullabilityKnown || c.Nullable != (i != 2) || c.OriginTable != 0 || c.OriginAttribute != 0 || c.Usage.Typmod != -1 {
			t.Fatal("result metadata", c)
		}
	}
	_, volatile, owner := queryCoreFixture(t, "SELECT any_value(nextval($1)),any_value(nextval($1)) FROM t")
	defer owner.Close()
	calls := volatile.Root.(*plan.ProjectNode).Child.(*plan.AggregateNode).Calls
	if len(calls) != 2 {
		t.Fatal("volatile occurrences shared")
	}
	for _, call := range calls {
		if !call.Argument.Properties().ParameterDependent || call.Argument.Properties().Volatility != expr.Volatile {
			t.Fatal(call.Argument.Properties())
		}
	}
}
