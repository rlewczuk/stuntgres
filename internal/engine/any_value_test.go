package engine

import (
	"testing"

	"stuntgres/internal/engine/expr"
)

func TestAnyValueSlotsAndMetadata(t *testing.T) {
	_, q, source := queryCoreFixture(t, "SELECT any_value(v),any_value(v),count(v),any_value(f) FROM t")
	defer source.close()
	node := q.Root.(*projectNode).child.(*aggregateNode)
	if len(node.calls) != 3 {
		t.Fatal("aggregate identity sharing", len(node.calls))
	}
	for i, c := range q.Root.Schema() {
		if !c.NullabilityKnown || c.Nullable != (i != 2) || c.OriginTable != 0 || c.OriginAttribute != 0 || c.Usage.Typmod != -1 {
			t.Fatal("result metadata", c)
		}
	}
	_, volatile, owner := queryCoreFixture(t, "SELECT any_value(nextval($1)),any_value(nextval($1)) FROM t")
	defer owner.close()
	calls := volatile.Root.(*projectNode).child.(*aggregateNode).calls
	if len(calls) != 2 {
		t.Fatal("volatile occurrences shared")
	}
	for _, call := range calls {
		if !call.argument.Properties().ParameterDependent || call.argument.Properties().Volatility != expr.Volatile {
			t.Fatal(call.argument.Properties())
		}
	}
}
