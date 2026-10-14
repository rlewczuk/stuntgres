package engine

import "testing"

func TestArrayAggregateDescription(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT array_agg(id ORDER BY id),array_agg(NULL::integer[]) FROM t")
	defer source.close()
	for _, column := range query.Root.Schema() {
		if column.Name != "array_agg" || column.Usage.Type.ID() != 1007 || column.Usage.Typmod != -1 || column.OriginTable != 0 || column.OriginAttribute != 0 || !column.Nullable || !column.NullabilityKnown {
			t.Fatal("aggregate description", column)
		}
	}
}
