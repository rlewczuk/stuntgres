package storage

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
	"testing"
)

func dependencyUnitCollector(t *testing.T, width int, kinds string) *ExtendedCollector {
	t.Helper()
	base := ndistinctUnitCollector(t, width, statisticsTypes()[sqltype.Int4].Usage())
	keys := make([]int32, width)
	for i := range keys {
		keys[i] = base.input.Definition.Selection.Key(i)
	}
	selection, err := catalog.NewStatisticsSelection(keys, 0, kinds)
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewExtendedCollector(ExtendedInput{Definition: &catalog.Statistics{Selection: selection}, Columns: base.input.Columns, Type: base.input.Type, DependenciesType: statisticsTypes()[sqltype.PgDependencies]})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func dependencyAdd(t *testing.T, c *ExtendedCollector, rows ...[]*int64) {
	t.Helper()
	for _, row := range rows {
		values := make([]sqltype.Value, len(row))
		for i, v := range row {
			values[i] = sqltype.Null(c.input.Columns[i].Usage.Type)
			if v != nil {
				values[i], _ = sqltype.Integer(c.input.Columns[i].Usage.Type, *v)
			}
		}
		if err := c.Add(context.Background(), values); err != nil {
			t.Fatal(err)
		}
	}
}
func TestStatisticsDependenciesMeasured(t *testing.T) {
	one, two, ten, twenty := int64(1), int64(2), int64(10), int64(20)
	for _, test := range []struct {
		rows    [][]*int64
		degrees []float64
	}{
		{[][]*int64{{&one, &ten}, {&one, &ten}, {&two, &twenty}}, []float64{1, 1}},
		{[][]*int64{{&one, &ten}, {&one, &ten}, {&two, &twenty}, {&one, &twenty}}, []float64{.25, .5}},
		{[][]*int64{{&one, &ten}, {&one, nil}, {&two, &twenty}}, []float64{1.0 / 3, 1}},
		{[][]*int64{{nil, &ten}, {nil, &twenty}, {&one, &ten}}, []float64{1.0 / 3, 1.0 / 3}},
		{[][]*int64{{nil, nil}, {nil, nil}}, []float64{1, 1}},
		{[][]*int64{{&one, &ten}, {&one, &twenty}, {&two, &ten}, {&two, &twenty}}, nil},
	} {
		c := dependencyUnitCollector(t, 2, "f")
		dependencyAdd(t, c, test.rows...)
		fact, err := c.Finish(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		var degrees []float64
		for _, item := range fact.DependencyItems {
			degrees = append(degrees, item.Value)
		}
		if !reflect.DeepEqual(degrees, test.degrees) || fact.Dependencies.IsNull() != (len(degrees) == 0) {
			t.Fatal(degrees, test.degrees)
		}
	}
}
func TestStatisticsDependenciesEnumeration(t *testing.T) {
	one := int64(1)
	for width := 2; width <= 8; width++ {
		c := dependencyUnitCollector(t, width, "f")
		row := make([]*int64, width)
		for i := range row {
			row[i] = &one
		}
		dependencyAdd(t, c, row)
		fact, err := c.Finish(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(fact.DependencyItems) != width*((1<<(width-1))-1) {
			t.Fatal(width, len(fact.DependencyItems))
		}
		// Independently enumerate the set of determinant/dependent candidates
		// by bit membership, then sort by the released ordering contract. Do not
		// use the producer's recursive enumerator to manufacture expectations.
		var expected [][]int16
		for mask := 1; mask < 1<<width; mask++ {
			for dependent := range width {
				if mask&(1<<dependent) != 0 {
					continue
				}
				var attributes []int16
				for dimension := range width {
					if mask&(1<<dimension) != 0 {
						// The fixture declares sparse keys 1,3,5,... .
						attributes = append(attributes, int16(2*dimension+1))
					}
				}
				expected = append(expected, append(attributes, int16(2*dependent+1)))
			}
		}
		slices.SortFunc(expected, func(a, b []int16) int {
			if len(a) != len(b) {
				return len(a) - len(b)
			}
			if order := slices.Compare(a[:len(a)-1], b[:len(b)-1]); order != 0 {
				return order
			}
			return int(a[len(a)-1]) - int(b[len(b)-1])
		})
		if len(expected) != len(fact.DependencyItems) {
			t.Fatal(width, len(expected), len(fact.DependencyItems))
		}
		for index, attributes := range expected {
			item := fact.DependencyItems[index]
			if !slices.Equal(item.Attributes, attributes) || item.Value != 1 {
				t.Fatal(width, index, item, attributes)
			}
		}
	}
}
func TestStatisticsDependenciesCombined(t *testing.T) {
	c := dependencyUnitCollector(t, 2, "df")
	one, two := int64(1), int64(2)
	dependencyAdd(t, c, []*int64{&one, &one}, []*int64{&two, &two})
	fact, err := c.Finish(context.Background())
	if err != nil || len(fact.Items) != 1 || fact.Items[0].Value != 2 || len(fact.DependencyItems) != 2 {
		t.Fatal(fact, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := c.Finish(canceled); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(got, err)
	}
	if err := c.Add(canceled, []sqltype.Value{sqltype.Null(c.input.Columns[0].Usage.Type), sqltype.Null(c.input.Columns[1].Usage.Type)}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	before := fact.Dependencies
	dependencyAdd(t, c, []*int64{&one, &two})
	after, err := c.Finish(context.Background())
	if err != nil || after == fact || !reflect.DeepEqual(before, fact.Dependencies) || fact.DependencyItems[0].Value != 1 {
		t.Fatal("old combined fact mutated", err)
	}
}
