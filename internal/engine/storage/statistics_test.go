package storage

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

func statisticsTypes() map[sqltype.ID]*sqltype.Descriptor {
	result := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, d := range sqltype.Builtins() {
		result[d.ID()] = d
	}
	return result
}

func collectStatisticsValues(t *testing.T, usage sqltype.Usage, values []sqltype.Value) *ColumnStatistics {
	t.Helper()
	key := StatisticsKey{Attribute: 7}
	stats, population, err := CollectStatistics(context.Background(), []StatisticsColumn{{Key: key, Usage: usage, Slot: 0}}, func(yield func([]sqltype.Value) error) error {
		for _, value := range values {
			if err := yield([]sqltype.Value{value}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil || population != uint64(len(values)) {
		t.Fatalf("collection: rows=%d err=%v", population, err)
	}
	if stats == nil {
		return nil
	}
	return stats.Columns[key]
}

func TestAnalyzeStatisticsCollection(t *testing.T) {
	types := statisticsTypes()
	integer := types[sqltype.Int4]
	value := func(n int64) sqltype.Value {
		v, err := sqltype.Integer(integer, n)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	t.Run("uniform and bounded endpoints", func(t *testing.T) {
		for _, size := range []int{10, 200} {
			values := make([]sqltype.Value, size)
			for i := range values {
				values[i] = value(int64(i + 1))
			}
			s := collectStatisticsValues(t, integer.Usage(), values)
			encoded, known := s.EncodedDistinct()
			if s.Rows != uint64(size) || s.NullCount != 0 || s.DistinctCount != uint64(size) || !known || encoded != -1 || len(s.MCV) != 0 || len(s.Histogram) != min(size, 101) {
				t.Fatalf("uniform: %#v encoded=%v/%v", s, encoded, known)
			}
			first, _ := s.Histogram[0].Integer()
			last, _ := s.Histogram[len(s.Histogram)-1].Integer()
			if first != 1 || last != int64(size) {
				t.Fatalf("endpoints %d %d", first, last)
			}
			if !reflect.DeepEqual(s, collectStatisticsValues(t, integer.Usage(), values)) {
				t.Fatal("nondeterministic publication")
			}
		}
	})
	t.Run("skew null and encoding", func(t *testing.T) {
		values := make([]sqltype.Value, 20)
		for i := range values {
			if i < 14 {
				values[i] = value(1)
			} else if i < 16 {
				values[i] = value(2)
			} else {
				values[i] = sqltype.Null(integer)
			}
		}
		s := collectStatisticsValues(t, integer.Usage(), values)
		encoded, known := s.EncodedDistinct()
		if s.Rows != 20 || s.NullCount != 4 || s.NullFraction() != .2 || s.DistinctCount != 2 || !known || encoded != 2 || len(s.MCV) != 2 || s.MCV[0].Count != 14 || s.MCV[1].Count != 2 || len(s.Histogram) != 0 {
			t.Fatalf("skew: %#v", s)
		}
	})
	t.Run("all null versus empty and unavailable", func(t *testing.T) {
		s := collectStatisticsValues(t, integer.Usage(), []sqltype.Value{sqltype.Null(integer), sqltype.Null(integer), sqltype.Null(integer), sqltype.Null(integer), sqltype.Null(integer)})
		if s.NullCount != 5 || s.NullFraction() != 1 || !s.DistinctKnown || s.DistinctCount != 0 || len(s.MCV) != 0 || len(s.Histogram) != 0 {
			t.Fatalf("all null: %#v", s)
		}
		if collectStatisticsValues(t, integer.Usage(), nil) != nil {
			t.Fatal("empty created distribution")
		}
		json := types[sqltype.JSON]
		s = collectStatisticsValues(t, json.Usage(), []sqltype.Value{sqltype.Null(json)})
		if s.DistinctKnown || s.NullFraction() != 1 {
			t.Fatalf("unavailable equality: %#v", s)
		}
		if _, known := s.EncodedDistinct(); known {
			t.Fatal("fabricated distinct zero")
		}
	})
	t.Run("bounded MCV residual weighted ranks", func(t *testing.T) {
		var values []sqltype.Value
		for i := int64(1); i <= 103; i++ {
			for j := int64(0); j < i+1; j++ {
				values = append(values, value(i))
			}
		}
		s := collectStatisticsValues(t, integer.Usage(), values)
		if len(s.MCV) != 100 || len(s.Histogram) > 101 || s.DistinctCount != 103 {
			t.Fatalf("bounds: %#v", s)
		}
		for _, boundary := range s.Histogram {
			n, _ := boundary.Integer()
			if n < 1 || n > 3 {
				t.Fatalf("retained MCV leaked into residual: %d", n)
			}
		}
		first, _ := s.Histogram[0].Integer()
		last, _ := s.Histogram[len(s.Histogram)-1].Integer()
		if first != 1 || last != 3 {
			t.Fatalf("residual endpoints %d,%d", first, last)
		}
	})
	t.Run("typed signed zero NaN and bpchar", func(t *testing.T) {
		float := types[sqltype.Float8]
		a, err := float.Input(context.Background(), "NaN", -1)
		if err != nil {
			t.Fatal(err)
		}
		b, err := float.Input(context.Background(), "0", -1)
		if err != nil {
			t.Fatal(err)
		}
		c, err := float.Input(context.Background(), "-0", -1)
		if err != nil {
			t.Fatal(err)
		}
		s := collectStatisticsValues(t, float.Usage(), []sqltype.Value{a, a, b, c})
		if s.DistinctCount != 2 || len(s.MCV) != 2 || s.MCV[0].Count != 2 || s.MCV[1].Count != 2 {
			t.Fatalf("float typed grouping: %#v", s)
		}
		bp := types[sqltype.BPChar]
		usage := bp.Usage()
		collation, err := sqltype.RegisteredCollation(100, 950)
		if err != nil {
			t.Fatal(err)
		}
		usage.Collation = collation
		s = collectStatisticsValues(t, usage, []sqltype.Value{sqltype.String(bp, "a"), sqltype.String(bp, "a ")})
		if s.DistinctCount != 1 || len(s.MCV) != 1 || s.MCV[0].Count != 2 {
			t.Fatalf("bpchar grouping: %#v", s)
		}
	})
	t.Run("selected merge stable attribute compatibility", func(t *testing.T) {
		a := collectStatisticsValues(t, integer.Usage(), []sqltype.Value{value(1)})
		b := collectStatisticsValues(t, integer.Usage(), []sqltype.Value{value(2)})
		ka, kb := StatisticsKey{Attribute: 1}, StatisticsKey{Attribute: 7, Inherited: true}
		old := MergeStatistics(nil, map[StatisticsKey]*ColumnStatistics{ka: a, kb: b})
		next := MergeStatistics(old, map[StatisticsKey]*ColumnStatistics{ka: b})
		if next == old || next.Columns[kb] != b || old.Columns[ka] != a {
			t.Fatal("merge mutated or copied unselected payload")
		}
		relation := &catalog.Relation{Attributes: []int32{7}, Columns: []catalog.Column{{Usage: integer.Usage()}}}
		filtered := CompatibleStatistics(next, relation)
		if len(filtered.Columns) != 1 || filtered.Columns[kb] != b {
			t.Fatal("dropped attribute shifted facts")
		}
		relation.Columns[0].Usage = types[sqltype.Int8].Usage()
		if CompatibleStatistics(filtered, relation) != nil {
			t.Fatal("incompatible type retained")
		}
	})
}

// The context starts cancelling in the first real histogram comparison after
// collection; no collector test hook, provider replacement or fake publication.
type statisticsComparisonCancel struct {
	context.Context
	armed bool
	calls int
}

func (c *statisticsComparisonCancel) Err() error {
	if c.armed {
		c.calls++
		if c.calls >= 2 {
			return context.Canceled
		}
	}
	return nil
}

func TestAnalyzeStatisticsCollectionCancellation(t *testing.T) {
	integer := statisticsTypes()[sqltype.Int4]
	ctx := &statisticsComparisonCancel{Context: context.Background()}
	stats, _, err := CollectStatistics(ctx, []StatisticsColumn{{Key: StatisticsKey{Attribute: 3}, Usage: integer.Usage(), Slot: 0}}, func(yield func([]sqltype.Value) error) error {
		for i := int64(200); i > 0; i-- {
			value, _ := sqltype.Integer(integer, i)
			if err := yield([]sqltype.Value{value}); err != nil {
				return err
			}
		}
		ctx.armed = true
		return nil
	})
	if !errors.Is(err, context.Canceled) || stats != nil || ctx.calls < 2 {
		t.Fatalf("comparison cancellation: stats=%v err=%v calls=%d", stats, err, ctx.calls)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	stats, _, err = CollectStatistics(cancelled, nil, func(func([]sqltype.Value) error) error { called = true; return nil })
	if !errors.Is(err, context.Canceled) || stats != nil || called {
		t.Fatal("cancelled capture invoked rows")
	}
}
