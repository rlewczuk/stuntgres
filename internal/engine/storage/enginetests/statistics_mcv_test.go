package enginetests

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func mcvTypes() map[sqltype.ID]*sqltype.Descriptor {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, d := range sqltype.Builtins() {
		types[d.ID()] = d
	}
	return types
}

func mcvCollector(t *testing.T, usage sqltype.Usage, width, target int, kinds string) *storage.ExtendedCollector {
	t.Helper()
	keys := make([]int32, width)
	columns := make([]storage.StatisticsColumn, width)
	for i := range keys {
		keys[i] = int32(2*i + 1)
		columns[i] = storage.StatisticsColumn{Key: storage.StatisticsKey{Attribute: keys[i]}, Usage: usage, Slot: i}
	}
	selection, err := catalog.NewStatisticsSelection(keys, 0, kinds)
	if err != nil {
		t.Fatal(err)
	}
	types := mcvTypes()
	c, err := storage.NewExtendedCollector(storage.ExtendedInput{Definition: &catalog.Statistics{Selection: selection}, Columns: columns, Type: types[sqltype.PgNDistinct], DependenciesType: types[sqltype.PgDependencies], MCVType: types[sqltype.PgMCVList], MCVTarget: target})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func mcvFinish(t *testing.T, c *storage.ExtendedCollector) *storage.ExtendedStatistics {
	t.Helper()
	fact, err := c.Finish(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return fact
}

func TestStatisticsMCVCollection(t *testing.T) {
	ctx := context.Background()
	types := mcvTypes()
	integer := types[sqltype.Int4]
	add := func(c *storage.ExtendedCollector, a, b int64) {
		x, _ := sqltype.Integer(integer, a)
		y, _ := sqltype.Integer(integer, b)
		if err := c.Add(ctx, []sqltype.Value{x, y}); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("acceptance and immutable combined facts", func(t *testing.T) {
		c := mcvCollector(t, integer.Usage(), 2, 100, "dfm")
		add(c, 1, 1)
		add(c, 1, 1)
		add(c, 2, 2)
		if err := c.Add(ctx, []sqltype.Value{sqltype.Null(integer), sqltype.Null(integer)}); err != nil {
			t.Fatal(err)
		}
		fact := mcvFinish(t, c)
		if fact.MCVData.Rows != 4 || len(fact.MCVData.Items) != 3 || len(fact.Items) != 1 || len(fact.DependencyItems) != 2 || fact.MCV.Type().ID() != sqltype.PgMCVList || !fact.MCV.ValidResult(fact.MCV.Type().Usage()) {
			t.Fatal(fact)
		}
		for i, count := range []uint64{2, 1, 1} {
			item := fact.MCVData.Items[i]
			if item.Count != count || item.MarginalCounts[0] != count || item.MarginalCounts[1] != count || item.Nulls[0] != (i == 2) || item.Nulls[1] != (i == 2) {
				t.Fatal(i, item)
			}
		}
		before := *fact.MCVData
		add(c, 2, 2)
		updated := mcvFinish(t, c)
		if updated.MCVData.Rows != 5 || !reflect.DeepEqual(before, *fact.MCVData) || fact.MCVData.Items[0].Count != 2 {
			t.Fatal("old retained fact mutated")
		}
		base := &storage.RelationStatistics{Extended: map[storage.ExtendedStatisticsKey]*storage.ExtendedStatistics{{Inherited: true}: fact}}
		merged := storage.MergeExtendedStatistics(base, map[storage.ExtendedStatisticsKey]*storage.ExtendedStatistics{{}: updated})
		if merged.Extended[storage.ExtendedStatisticsKey{Inherited: true}] != fact || base.Extended[storage.ExtendedStatisticsKey{}] != nil {
			t.Fatal("unselected backing replaced")
		}
	})
	t.Run("full marginals before cap", func(t *testing.T) {
		for _, target := range []int{1, 2, 10000} {
			c := mcvCollector(t, integer.Usage(), 2, target, "m")
			for range 4 {
				add(c, 1, 1)
			}
			for range 3 {
				add(c, 1, 2)
			}
			for range 2 {
				add(c, 2, 2)
			}
			if err := c.Add(ctx, []sqltype.Value{sqltype.Null(integer), sqltype.Null(integer)}); err != nil {
				t.Fatal(err)
			}
			fact := mcvFinish(t, c)
			want := min(target, 4)
			if len(fact.MCVData.Items) != want || fact.Value.Type() != nil || fact.Dependencies.Type() != nil || len(fact.Items) != 0 {
				t.Fatal("m-only ownership", fact)
			}
			item := fact.MCVData.Items[0]
			if fact.MCVData.Rows != 10 || item.Count != 4 || item.MarginalCounts[0] != 7 || item.MarginalCounts[1] != 4 {
				t.Fatal(item)
			}
		}
	})
	t.Run("empty null width and deterministic ties", func(t *testing.T) {
		c := mcvCollector(t, integer.Usage(), 8, 1, "m")
		if mcvFinish(t, c) != nil {
			t.Fatal("invented empty fact")
		}
		row := make([]sqltype.Value, 8)
		for i := range row {
			row[i] = sqltype.Null(integer)
		}
		if err := c.Add(ctx, row); err != nil {
			t.Fatal(err)
		}
		fact := mcvFinish(t, c)
		for i := range row {
			if !fact.MCVData.Items[0].Nulls[i] || fact.MCVData.Items[0].MarginalCounts[i] != 1 || len(fact.MCVData.Dictionaries[i]) != 0 {
				t.Fatal(fact.MCVData)
			}
		}
		c = mcvCollector(t, integer.Usage(), 2, 1, "m")
		add(c, 2, 2)
		add(c, 1, 1)
		a, b := mcvFinish(t, c), mcvFinish(t, c)
		n, err := a.MCVData.Dictionaries[0][0].Integer()
		if err != nil || n != 2 || !reflect.DeepEqual(a.MCV, b.MCV) {
			t.Fatal("unstable tie", n, err)
		}
	})
	t.Run("typed equality and genuine collision chains", func(t *testing.T) {
		cube, err := sqltype.Cube(45000, "cube", "public.cube")
		if err != nil {
			t.Fatal(err)
		}
		mood, err := sqltype.Enum(45001, "mood", "public.mood", []string{"z", "a"})
		if err != nil {
			t.Fatal(err)
		}
		domain, err := sqltype.Domain(45002, "wrapped", "public.wrapped", integer.Usage())
		if err != nil {
			t.Fatal(err)
		}
		bpUsage := types[sqltype.BPChar].Usage()
		bpUsage.Collation, err = sqltype.RegisteredCollation(100, 950)
		if err != nil {
			t.Fatal(err)
		}
		for _, test := range []struct {
			usage  sqltype.Usage
			texts  []string
			counts []uint64
		}{
			{cube.Usage(), []string{"(1)", "(2)", "(1)", "(3)"}, []uint64{2, 1, 1}},
			{mood.Usage(), []string{"z", "a", "z"}, []uint64{2, 1}},
			{domain.Usage(), []string{"1", "2", "1"}, []uint64{2, 1}},
			{types[1007].Usage(), []string{"{1,NULL}", "{2}", "{1,NULL}"}, []uint64{2, 1}},
			{bpUsage, []string{"x", "x ", "y"}, []uint64{2, 1}},
			{types[sqltype.Float8].Usage(), []string{"0", "-0", "NaN", "NaN"}, []uint64{2, 2}},
		} {
			c := mcvCollector(t, test.usage, 2, 100, "m")
			var firstHash uint64
			for i, text := range test.texts {
				v, err := test.usage.Type.Input(ctx, text, -1)
				if err != nil {
					t.Fatal(err)
				}
				if test.usage.Type == cube {
					g, err := sqltype.BindGrouping(test.usage)
					if err != nil {
						t.Fatal(err)
					}
					h, err := g.Hash(ctx, v)
					if err != nil {
						t.Fatal(err)
					}
					if i == 0 {
						firstHash = h
					} else if h != firstHash {
						t.Fatal("cube witness no longer collides")
					}
				}
				if err := c.Add(ctx, []sqltype.Value{v, v}); err != nil {
					t.Fatal(err)
				}
			}
			fact := mcvFinish(t, c)
			if len(fact.MCVData.Items) != len(test.counts) {
				t.Fatal(test.texts, fact.MCVData)
			}
			for i, count := range test.counts {
				if fact.MCVData.Items[i].Count != count || fact.MCVData.Items[i].MarginalCounts[0] != count {
					t.Fatal(test.texts, fact.MCVData)
				}
			}
		}
	})
	t.Run("canceled adapter and collection return no fact", func(t *testing.T) {
		c := mcvCollector(t, integer.Usage(), 2, 1, "m")
		add(c, 1, 1)
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if fact, err := c.Finish(canceled); fact != nil || !errors.Is(err, context.Canceled) {
			t.Fatal(fact, err)
		}
		one, _ := sqltype.Integer(integer, 1)
		if err := c.Add(canceled, []sqltype.Value{one, one}); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		fact := mcvFinish(t, c)
		if fact.MCVData.Rows != 1 {
			t.Fatal("canceled add counted a row")
		}
		if value, err := storage.BuildExtendedMCV(canceled, types[sqltype.PgMCVList], []sqltype.Usage{integer.Usage(), integer.Usage()}, fact.MCVData); value.Type() != nil || !errors.Is(err, context.Canceled) {
			t.Fatal(value, err)
		}
	})
	t.Run("maximum target bounds retained dictionaries", func(t *testing.T) {
		c := mcvCollector(t, integer.Usage(), 2, 10000, "m")
		for n := int64(0); n < 10001; n++ {
			add(c, n, n)
		}
		fact := mcvFinish(t, c)
		if fact.MCVData.Rows != 10001 || len(fact.MCVData.Items) != 10000 || len(fact.MCVData.Dictionaries[0]) != 10000 || len(fact.MCVData.Dictionaries[1]) != 10000 {
			t.Fatal("maximum target not applied")
		}
		last, err := fact.MCVData.Dictionaries[0][9999].Integer()
		if err != nil || last != 9999 || fact.MCVData.Items[9999].MarginalCounts[0] != 1 {
			t.Fatal("tie order or full marginal count", last, err)
		}
	})
	t.Run("in-flight cancellation owns no prefix", func(t *testing.T) {
		c := mcvCollector(t, integer.Usage(), 2, 10000, "m")
		for n := int64(0); n < 32; n++ {
			add(c, n, n)
		}
		before := mcvFinish(t, c)
		for _, after := range []int{3, 35, 70} {
			live, cancel := context.WithCancel(ctx)
			boundary := &mcvCancelContext{Context: live, remaining: after, cancel: cancel}
			fact, err := c.Finish(boundary)
			cancel()
			if fact != nil || !errors.Is(err, context.Canceled) {
				t.Fatal(after, fact, err)
			}
			if before.MCVData.Rows != 32 || len(before.MCVData.Items) != 32 || before.MCVData.Items[0].Count != 1 {
				t.Fatal("held fact mutated")
			}
		}
	})
}

type mcvCancelContext struct {
	context.Context
	remaining int
	cancel    context.CancelFunc
}

func (c *mcvCancelContext) Err() error {
	c.remaining--
	if c.remaining == 0 {
		c.cancel()
	}
	return c.Context.Err()
}
