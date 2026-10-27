package storage

import (
	"context"
	"math"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestStatisticsDependenciesTypedGrouping(t *testing.T) {
	types := statisticsTypes()
	ctx := context.Background()
	bp := types[sqltype.BPChar]
	usage := bp.Usage()
	var err error
	usage.Collation, err = sqltype.RegisteredCollation(100, 950)
	if err != nil {
		t.Fatal(err)
	}
	floats := []sqltype.Value{}
	for _, f := range []float64{0, math.Copysign(0, -1), math.NaN(), math.NaN()} {
		v, err := sqltype.Float(types[sqltype.Float8], f)
		if err != nil {
			t.Fatal(err)
		}
		floats = append(floats, v)
	}
	for _, test := range []struct {
		usage  sqltype.Usage
		values []sqltype.Value
	}{
		{usage, []sqltype.Value{sqltype.String(bp, "x"), sqltype.String(bp, "x "), sqltype.String(bp, "y")}},
		{types[sqltype.Float8].Usage(), floats},
	} {
		selection, err := catalog.NewStatisticsSelection([]int32{1, 3}, 0, "df")
		if err != nil {
			t.Fatal(err)
		}
		input := ExtendedInput{Definition: &catalog.Statistics{Selection: selection}, Columns: []StatisticsColumn{{Key: StatisticsKey{Attribute: 1}, Usage: test.usage, Slot: 0}, {Key: StatisticsKey{Attribute: 3}, Usage: test.usage, Slot: 1}}, Type: types[sqltype.PgNDistinct], DependenciesType: types[sqltype.PgDependencies]}
		c, err := NewExtendedCollector(input)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range test.values {
			if err := c.Add(ctx, []sqltype.Value{v, v}); err != nil {
				t.Fatal(err)
			}
		}
		fact, err := c.Finish(ctx)
		if err != nil || len(fact.DependencyItems) != 2 || fact.DependencyItems[0].Value != 1 || fact.DependencyItems[1].Value != 1 || fact.Items[0].Value != 2 {
			t.Fatal("typed equivalence not reused", fact, err)
		}
	}
	// Deliberately put a different determinant in the same hash bucket. It must
	// be collision-checked rather than falsely merged into a conflicting group.
	c := dependencyUnitCollector(t, 2, "f")
	one, two := int64(1), int64(2)
	dependencyAdd(t, c, []*int64{&one, &one})
	value, _ := sqltype.Integer(c.input.Columns[0].Usage.Type, 2)
	_, hashes, err := c.preparer.prepare(ctx, []sqltype.Value{value, value})
	if err != nil {
		t.Fatal(err)
	}
	for mask := 1; mask < len(c.dependencies.subsets); mask++ {
		hash := uint64(14695981039346656037)
		for i := range c.input.Columns {
			if mask&(1<<i) != 0 {
				hash = (hash ^ hashes[i]) * 1099511628211
			}
		}
		c.dependencies.subsets[mask].buckets[hash] = 1
	}
	dependencyAdd(t, c, []*int64{&two, &two})
	fact, err := c.Finish(ctx)
	if err != nil || len(fact.DependencyItems) != 2 || fact.DependencyItems[0].Value != 1 {
		t.Fatal("hash collision bypassed equality", fact, err)
	}
}
