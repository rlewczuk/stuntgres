package enginetests_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestIntervalAvgSlotsAndMetadata(t *testing.T) {
	_, q, source := queryCoreFixture(t, "SELECT avg(INTERVAL '1 day'),avg(INTERVAL '1 day'),count(*),bool_and(f) FROM t")
	defer source.Close()
	node := q.Root.(*plan.ProjectNode).Child.(*plan.AggregateNode)
	if len(node.Calls) != 3 {
		t.Fatal("operation sharing", len(node.Calls))
	}
	if node.Calls[0].Operation != functions.AggregateIntervalAvg {
		t.Fatal("wrong operation")
	}
	for _, c := range q.Root.Schema()[:2] {
		if !c.NullabilityKnown || !c.Nullable || c.OriginTable != 0 || c.OriginAttribute != 0 || c.Usage.Typmod != -1 || c.Usage.Type.ID() != sqltype.Interval {
			t.Fatal(c)
		}
	}
	// The actual provider remains execution-owned even when immutable calls share.
	rows, n, err := exec.Finalize(context.Background(), q.Root, source, nil)
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	defer rows.Close()
	values := make([]sqltype.Value, 4)
	present, err := rows.Next(context.Background(), values)
	if !present || err != nil {
		t.Fatal(present, err)
	}
	for _, v := range values[:2] {
		m, d, u, err := v.Interval()
		if err != nil || m != 0 || d != 1 || u != 0 {
			t.Fatal(m, d, u, err)
		}
	}
	_, volatile, owner := queryCoreFixture(t, "SELECT avg(CASE WHEN nextval($1)>0 THEN INTERVAL '1 day' ELSE INTERVAL '0' END),avg(CASE WHEN nextval($1)>0 THEN INTERVAL '1 day' ELSE INTERVAL '0' END) FROM t")
	defer owner.Close()
	if len(volatile.Root.(*plan.ProjectNode).Child.(*plan.AggregateNode).Calls) != 2 {
		t.Fatal("volatile average arguments shared")
	}
}
