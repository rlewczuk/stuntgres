package engine

import (
	"context"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestIntervalAvgSlotsAndMetadata(t *testing.T) {
	_, q, source := queryCoreFixture(t, "SELECT avg(INTERVAL '1 day'),avg(INTERVAL '1 day'),count(*),bool_and(f) FROM t")
	defer source.close()
	node := q.Root.(*projectNode).child.(*aggregateNode)
	if len(node.calls) != 3 {
		t.Fatal("operation sharing", len(node.calls))
	}
	if node.calls[0].operation != aggregateIntervalAvg {
		t.Fatal("wrong operation")
	}
	for _, c := range q.Root.Schema()[:2] {
		if !c.NullabilityKnown || !c.Nullable || c.OriginTable != 0 || c.OriginAttribute != 0 || c.Usage.Typmod != -1 || c.Usage.Type.ID() != sqltype.Interval {
			t.Fatal(c)
		}
	}
	// The actual provider remains execution-owned even when immutable calls share.
	rows, n, err := finalizeQuery(context.Background(), q.Root, source, nil)
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
	defer owner.close()
	if len(volatile.Root.(*projectNode).child.(*aggregateNode).calls) != 2 {
		t.Fatal("volatile average arguments shared")
	}
}
