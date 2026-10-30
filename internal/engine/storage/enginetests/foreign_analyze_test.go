package enginetests

import (
	"context"
	"errors"
	"math"
	"testing"

	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func TestForeignAnalyzeSampleStatistics(t *testing.T) {
	var integer *sqltype.Descriptor
	for _, descriptor := range sqltype.Builtins() {
		if descriptor.ID() == sqltype.Int4 {
			integer = descriptor
		}
	}
	for _, test := range []struct {
		name                string
		counts              []int
		nulls               int
		population, encoded float64
	}{
		{"skew", []int{14, 2}, 4, 200, 2},
		{"full", []int{14, 2}, 4, 20, 2},
		{"singletons", []int{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}, 0, 200, -1},
		{"mixed", []int{2, 1, 1}, 0, 40, -.125},
		{"nulls", nil, 20, 200, 0},
		{"noninteger", []int{1, 1, 1, 1}, 0, 200.5, -1},
		{"underestimate", []int{2, 1, 1}, 0, 2, -1.5},
	} {
		t.Run(test.name, func(t *testing.T) {
			key := storage.StatisticsKey{Attribute: 1}
			column := storage.StatisticsColumn{Key: key, Usage: integer.Usage(), Target: 100, SamplePopulation: &test.population}
			var inputRows uint64
			stats, count, err := storage.CollectStatistics(context.Background(), []storage.StatisticsColumn{column}, func(yield func([]sqltype.Value) error) error {
				for group, n := range test.counts {
					value, err := sqltype.Integer(integer, int64(group+1))
					if err != nil {
						return err
					}
					for range n {
						inputRows++
						if err := yield([]sqltype.Value{value}); err != nil {
							return err
						}
					}
				}
				for range test.nulls {
					inputRows++
					if err := yield([]sqltype.Value{sqltype.Null(integer)}); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			fact := stats.Columns[key]
			if count != inputRows || fact.Rows != inputRows || fact.NullCount != uint64(test.nulls) || fact.DistinctCount != uint64(len(test.counts)) {
				t.Fatal("literal sample counts were extrapolated", count, fact)
			}
			encoded, known := fact.EncodedDistinct()
			if !known || math.Abs(encoded-test.encoded) > 1e-12 {
				t.Fatalf("distinct estimate=%g want=%g", encoded, test.encoded)
			}
			for _, mcv := range fact.MCV {
				value, err := mcv.Value.Integer()
				if err != nil {
					t.Fatal(err)
				}
				if mcv.Count != uint64(test.counts[value-1]) {
					t.Fatal("MCV not literal observed count", mcv)
				}
			}
			if fact.NullFraction() != float64(test.nulls)/float64(inputRows) {
				t.Fatal("population used as sample denominator")
			}
		})
	}
	key := storage.StatisticsKey{Attribute: 1}
	column := storage.StatisticsColumn{Key: key, Usage: integer.Usage(), Target: 100, SamplePopulation: new(float64(200))}
	stats, count, err := storage.CollectStatistics(context.Background(), []storage.StatisticsColumn{column}, func(func([]sqltype.Value) error) error { return nil })
	if err != nil || stats != nil || count != 0 {
		t.Fatal("empty sample manufactured distribution", stats, count, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stats, _, err = storage.CollectStatistics(ctx, []storage.StatisticsColumn{column}, func(func([]sqltype.Value) error) error { t.Fatal("canceled collection visited source"); return nil })
	if !errors.Is(err, context.Canceled) || stats != nil {
		t.Fatal(stats, err)
	}
}
