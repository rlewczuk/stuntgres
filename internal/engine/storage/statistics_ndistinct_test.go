package storage

import (
	"context"
	"errors"
	"math"
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
	"testing"
)

func ndistinctUnitCollector(t *testing.T, width int, usage sqltype.Usage) *NDistinctCollector {
	t.Helper()
	keys := make([]int32, width)
	columns := make([]StatisticsColumn, width)
	for i := range keys {
		keys[i] = int32(i*2 + 1)
		columns[i] = StatisticsColumn{Key: StatisticsKey{Attribute: keys[i]}, Usage: usage, Slot: i, Target: 100}
	}
	selection, err := catalog.NewStatisticsSelection(keys, 0, "d")
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewNDistinctCollector(NDistinctInput{Definition: &catalog.Statistics{Selection: selection}, Columns: columns, Type: statisticsTypes()[sqltype.PgNDistinct]})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestStatisticsNDistinctSubsets(t *testing.T) {
	types := statisticsTypes()
	ctx := context.Background()
	for _, width := range []int{2, 3, 8} {
		c := ndistinctUnitCollector(t, width, types[sqltype.Int4].Usage())
		row := make([]sqltype.Value, width)
		for i := range row {
			row[i], _ = sqltype.Integer(types[sqltype.Int4], 1)
		}
		if err := c.Add(ctx, row); err != nil {
			t.Fatal(err)
		}
		for i := range row {
			row[i], _ = sqltype.Integer(types[sqltype.Int4], 2)
		}
		if err := c.Add(ctx, row); err != nil {
			t.Fatal(err)
		}
		fact, err := c.Finish(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(fact.Items) != (1<<width)-width-1 {
			t.Fatal(width, len(fact.Items))
		}
		for i, item := range fact.Items {
			if item.Value != 2 {
				t.Fatal(width, i, item)
			}
			if i > 0 {
				previous := fact.Items[i-1].Attributes
				if len(previous) > len(item.Attributes) {
					t.Fatal("subset width ordering")
				}
			}
		}
		before := append([]int16(nil), fact.Items[0].Attributes...)
		c.input.Columns[0].Key.Attribute = 99
		if !reflect.DeepEqual(before, fact.Items[0].Attributes) {
			t.Fatal("fact aliases construction keys")
		}
	}
}

func TestStatisticsNDistinctCollection(t *testing.T) {
	types := statisticsTypes()
	ctx := context.Background()
	integer := types[sqltype.Int4]
	one, _ := sqltype.Integer(integer, 1)
	null := sqltype.Null(integer)
	c := ndistinctUnitCollector(t, 2, integer.Usage())
	for _, row := range [][]sqltype.Value{{null, null}, {null, null}, {null, one}, {null, one}, {one, null}, {one, one}} {
		if err := c.Add(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	fact, err := c.Finish(ctx)
	if err != nil || fact.Items[0].Value != 4 {
		t.Fatal(fact, err)
	}
	// Borrowed input slices may be overwritten immediately after each Add.
	row := []sqltype.Value{one, one}
	if err := c.Add(ctx, row); err != nil {
		t.Fatal(err)
	}
	row[0] = null
	fact, err = c.Finish(ctx)
	if err != nil || fact.Items[0].Value != 4 {
		t.Fatal("borrowed row retained", fact, err)
	}
	empty := ndistinctUnitCollector(t, 2, integer.Usage())
	got, err := empty.Finish(ctx)
	if got != nil || err != nil {
		t.Fatal("empty population", got, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := c.Add(canceled, row); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if got, err := c.Finish(canceled); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(got, err)
	}
	float := types[sqltype.Float8]
	f := ndistinctUnitCollector(t, 2, float.Usage())
	for _, v := range []float64{0, math.Copysign(0, -1), math.NaN(), math.NaN()} {
		value, _ := sqltype.Float(float, v)
		if err := f.Add(ctx, []sqltype.Value{value, value}); err != nil {
			t.Fatal(err)
		}
	}
	got, err = f.Finish(ctx)
	if err != nil || got.Items[0].Value != 2 {
		t.Fatal("typed floating grouping", got, err)
	}
	bp := types[sqltype.BPChar]
	usage := bp.Usage()
	usage.Collation, err = sqltype.RegisteredCollation(100, 950)
	if err != nil {
		t.Fatal(err)
	}
	b := ndistinctUnitCollector(t, 2, usage)
	for _, text := range []string{"x", "x "} {
		value := sqltype.String(bp, text)
		if err := b.Add(ctx, []sqltype.Value{value, value}); err != nil {
			t.Fatal(err)
		}
	}
	got, err = b.Finish(ctx)
	if err != nil || got.Items[0].Value != 1 {
		t.Fatal("bpchar grouping", got, err)
	}
	two, _ := sqltype.Integer(integer, 2)
	collision := ndistinctUnitCollector(t, 2, integer.Usage())
	if err := collision.Add(ctx, []sqltype.Value{one, one}); err != nil {
		t.Fatal(err)
	}
	hash := uint64(14695981039346656037)
	for i := range 2 {
		key, err := collision.grouping[i].PrepareKey(ctx, two)
		if err != nil {
			t.Fatal(err)
		}
		part, err := collision.grouping[i].Hash(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		hash = (hash ^ part) * 1099511628211
	}
	collision.subsets[0].buckets[hash] = 1
	for range 2 {
		if err := collision.Add(ctx, []sqltype.Value{two, two}); err != nil {
			t.Fatal(err)
		}
	}
	got, err = collision.Finish(ctx)
	if err != nil || got.Items[0].Value != 2 {
		t.Fatal("hash collision bypassed typed equality", got, err)
	}
	array := types[1007]
	arrays := ndistinctUnitCollector(t, 2, array.Usage())
	for _, value := range []sqltype.Value{one, one, two} {
		datum, err := sqltype.OwnedArrayValue(array, []sqltype.ArrayDimension{{Lower: 1, Length: 2}}, []sqltype.Value{value, null})
		if err != nil {
			t.Fatal(err)
		}
		if err := arrays.Add(ctx, []sqltype.Value{datum, datum}); err != nil {
			t.Fatal(err)
		}
	}
	got, err = arrays.Finish(ctx)
	if err != nil || got.Items[0].Value != 2 {
		t.Fatal("typed array grouping", got, err)
	}
}
