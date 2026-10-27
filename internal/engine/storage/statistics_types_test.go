package storage

import (
	"context"
	"testing"

	"stuntgres/internal/sqltype"
)

func TestAnalyzeStatisticsCollectionTypedCapabilities(t *testing.T) {
	ctx := context.Background()
	types := statisticsTypes()
	mood, err := sqltype.Enum(45000, "mood", "public.mood", []string{"z", "a", "m"})
	if err != nil {
		t.Fatal(err)
	}
	domain, err := sqltype.Domain(45001, "wrapped", "public.wrapped", mood.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, descriptor := range []*sqltype.Descriptor{mood, domain} {
		var values []sqltype.Value
		for _, text := range []string{"m", "a", "z"} {
			v, err := descriptor.Input(ctx, text, -1)
			if err != nil {
				t.Fatal(err)
			}
			values = append(values, v)
		}
		s := collectStatisticsValues(t, descriptor.Usage(), values)
		if s.DistinctCount != 3 || len(s.Histogram) != 3 {
			t.Fatalf("enum/domain: %#v", s)
		}
		first, err := descriptor.Output(ctx, s.Histogram[0])
		if err != nil {
			t.Fatal(err)
		}
		last, err := descriptor.Output(ctx, s.Histogram[2])
		if err != nil {
			t.Fatal(err)
		}
		if first != "z" || last != "m" {
			t.Fatalf("enum declaration order lost: %q/%q", first, last)
		}
	}
	t.Run("unordered array retains grouping", func(t *testing.T) {
		array := types[1007]
		if array == nil {
			t.Fatal("missing int4 array")
		}
		var values []sqltype.Value
		for _, text := range []string{"{1,2}", "{1,2}", "{3}"} {
			v, err := array.Input(ctx, text, -1)
			if err != nil {
				t.Fatal(err)
			}
			values = append(values, v)
		}
		s := collectStatisticsValues(t, array.Usage(), values)
		if !s.DistinctKnown || s.DistinctCount != 2 || len(s.MCV) != 1 || s.MCV[0].Count != 2 || len(s.Histogram) != 0 {
			t.Fatalf("unordered array: %#v", s)
		}
	})
	t.Run("collated text retains real authority", func(t *testing.T) {
		text := types[sqltype.Text]
		usage := text.Usage()
		collation, err := sqltype.RegisteredCollation(100, 950)
		if err != nil {
			t.Fatal(err)
		}
		usage.Collation = collation
		s := collectStatisticsValues(t, usage, []sqltype.Value{sqltype.String(text, "b"), sqltype.String(text, "a"), sqltype.String(text, "a")})
		if s.Usage != usage || s.DistinctCount != 2 || len(s.MCV) != 1 || s.MCV[0].Count != 2 || len(s.Histogram) != 1 {
			t.Fatalf("collated text: %#v", s)
		}
	})
	t.Run("hash collision uses typed equality", func(t *testing.T) {
		integer := types[sqltype.Int4]
		one, _ := sqltype.Integer(integer, 1)
		two, _ := sqltype.Integer(integer, 2)
		grouping, err := sqltype.BindGrouping(integer.Usage())
		if err != nil {
			t.Fatal(err)
		}
		hash, err := grouping.Hash(ctx, two)
		if err != nil {
			t.Fatal(err)
		}
		// Seed a collision bucket with a different real datum. No equality or
		// hash provider is replaced; add must inspect and reject its candidate.
		c := statisticsCollector{column: StatisticsColumn{Usage: integer.Usage()}, stats: &ColumnStatistics{Usage: integer.Usage(), Rows: 1, DistinctKnown: true}, grouping: grouping, buckets: map[uint64]int{hash: 1}, groups: []statisticsGroup{{value: one, key: one, count: 1, first: 0}}}
		if err := c.add(ctx, two); err != nil {
			t.Fatal(err)
		}
		if err := c.add(ctx, two); err != nil {
			t.Fatal(err)
		}
		if len(c.groups) != 2 || c.groups[0].count != 1 || c.groups[1].count != 2 {
			t.Fatalf("collision merged different values: %#v", c.groups)
		}
	})
	t.Run("stable attributes map independent slots and flavors", func(t *testing.T) {
		integer := types[sqltype.Int4]
		one, _ := sqltype.Integer(integer, 1)
		two, _ := sqltype.Integer(integer, 2)
		local, inherited := StatisticsKey{Attribute: 9}, StatisticsKey{Attribute: 9, Inherited: true}
		columns := []StatisticsColumn{{Key: local, Usage: integer.Usage(), Slot: 1}, {Key: inherited, Usage: integer.Usage(), Slot: 0}}
		stats, rows, err := CollectStatistics(ctx, columns, func(yield func([]sqltype.Value) error) error { return yield([]sqltype.Value{one, two}) })
		if err != nil || rows != 1 || len(stats.Columns) != 2 {
			t.Fatalf("mapping rows=%d err=%v stats=%v", rows, err, stats)
		}
		a, _ := stats.Columns[local].Histogram[0].Integer()
		b, _ := stats.Columns[inherited].Histogram[0].Integer()
		if a != 2 || b != 1 {
			t.Fatalf("slot/flavor contamination %d/%d", a, b)
		}
	})
}
