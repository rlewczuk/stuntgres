package enginetests

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func TestPlannerOtherStatisticsTarget(t *testing.T) {
	var integer *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Int4 {
			integer = d
		}
	}
	key := storage.StatisticsKey{Attribute: 1}
	collect := func(target int, counts []int, ctx context.Context, fail error) (*storage.RelationStatistics, error) {
		column := storage.StatisticsColumn{Key: key, Usage: integer.Usage(), Target: target}
		stats, population, err := storage.CollectStatistics(ctx, []storage.StatisticsColumn{column}, func(yield func([]sqltype.Value) error) error {
			for i, count := range counts {
				for range count {
					v, e := sqltype.Integer(integer, int64(i+1))
					if e != nil {
						return e
					}
					if e = yield([]sqltype.Value{v}); e != nil {
						return e
					}
				}
			}
			return fail
		})
		if err == nil {
			want := 0
			for _, n := range counts {
				want += n
			}
			if population != uint64(want) {
				t.Fatal(population, want)
			}
		}
		return stats, err
	}
	for _, tc := range []struct {
		target int
		want   []int64
	}{{1, []int64{1, 8}}, {4, []int64{1, 2, 4, 6, 8}}} {
		stats, err := collect(tc.target, []int{1, 1, 1, 1, 1, 1, 1, 1}, context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		s := stats.Columns[key]
		var got []int64
		for _, v := range s.Histogram {
			n, e := v.Integer()
			if e != nil {
				t.Fatal(e)
			}
			got = append(got, n)
		}
		if s.Rows != 8 || s.NullCount != 0 || s.DistinctCount != 8 || len(s.MCV) != 0 || !reflect.DeepEqual(got, tc.want) {
			t.Fatal(s, got, tc.want)
		}
	}
	var old *storage.RelationStatistics
	for _, target := range []int{1, 3} {
		stats, err := collect(target, []int{4, 3, 2, 1}, context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		s := stats.Columns[key]
		if len(s.MCV) != target || s.Rows != 10 || s.DistinctCount != 4 || s.NullCount != 0 {
			t.Fatal(s)
		}
		for i, mcv := range s.MCV {
			n, _ := mcv.Value.Integer()
			if n != int64(i+1) || mcv.Count != uint64(4-i) {
				t.Fatal(mcv)
			}
		}
		if old == nil {
			old = stats
		}
	}
	failure := errors.New("row producer failure")
	for _, fail := range []error{failure, context.Canceled} {
		stats, err := collect(3, []int{4, 3, 2, 1}, context.Background(), fail)
		if !errors.Is(err, fail) || stats != nil || len(old.Columns[key].MCV) != 1 || old.Columns[key].MCV[0].Count != 4 {
			t.Fatal("failed candidate changed previous publication", stats, err, old)
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	stats, err := collect(3, []int{1}, cancelled, nil)
	if !errors.Is(err, context.Canceled) || stats != nil {
		t.Fatal(stats, err)
	}
}
