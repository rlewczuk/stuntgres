package storage

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

type dependencyGroup struct {
	values    [8]sqltype.Value
	rows      uint64
	conflicts uint8
	next      int
}

type dependencySubset struct {
	mask    uint8
	buckets map[uint64]int
	groups  []dependencyGroup
}

// DependenciesCollector measures row-weighted, all-or-nothing support of each
// determinant group. It captures logical rows, not PostgreSQL physical samples
// or TOAST width filtering. NULL participates through typed grouping equality.
type DependenciesCollector struct {
	input    ExtendedInput
	grouping [8]sqltype.Grouping
	subsets  []dependencySubset
	rows     uint64
}

// visitDependencyDirections uses PostgreSQL's width/combination/dependent order.
// The scratch attributes must be copied by consumers retaining them.
func visitDependencyDirections(selection catalog.StatisticsSelection, visit func(uint8, int, []int16) error) error {
	width := selection.DimensionLen()
	var attributes [8]int16
	var choose func(int, int, int, uint8) error
	choose = func(start, left, count int, mask uint8) error {
		if left == 0 {
			for dependent := range width {
				if mask&(1<<dependent) != 0 {
					continue
				}
				attributes[count] = int16(selection.Dimension(dependent))
				if err := visit(mask, dependent, attributes[:count+1]); err != nil {
					return err
				}
			}
			return nil
		}
		for i := start; i <= width-left; i++ {
			attributes[count] = int16(selection.Dimension(i))
			if err := choose(i+1, left-1, count+1, mask|1<<i); err != nil {
				return err
			}
		}
		return nil
	}
	for size := 1; size < width; size++ {
		if err := choose(0, size, 0, 0); err != nil {
			return err
		}
	}
	return nil
}

func newDependenciesCollector(input ExtendedInput, grouping [8]sqltype.Grouping) (*DependenciesCollector, error) {
	if input.DependenciesType == nil || input.DependenciesType.ID() != sqltype.PgDependencies {
		return nil, fmt.Errorf("invalid dependencies layout")
	}
	c := &DependenciesCollector{
		input: input, grouping: grouping,
		subsets: make([]dependencySubset, (1<<len(input.Columns))-1),
	}
	// Index directly by determinant mask; exclude the full mask, which has no
	// dependent dimension. Index zero deliberately has no allocated buckets.
	for mask := 1; mask < len(c.subsets); mask++ {
		c.subsets[mask] = dependencySubset{mask: uint8(mask), buckets: make(map[uint64]int)}
	}
	return c, nil
}

func (c *DependenciesCollector) addPrepared(ctx context.Context, keys *[8]sqltype.Value, hashes *[8]uint64) error {
	for mask := 1; mask < len(c.subsets); mask++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		subset := &c.subsets[mask]
		hash := uint64(14695981039346656037)
		for column := range c.input.Columns {
			if subset.mask&(1<<column) != 0 {
				hash = (hash ^ hashes[column]) * 1099511628211
			}
		}
		found := 0
		for link := subset.buckets[hash]; link != 0; link = subset.groups[link-1].next {
			group := &subset.groups[link-1]
			equal := true
			for column := range c.input.Columns {
				if subset.mask&(1<<column) == 0 {
					continue
				}
				same, err := c.grouping[column].Equal(ctx, group.values[column], keys[column])
				if err != nil {
					return err
				}
				if !same {
					equal = false
					break
				}
			}
			if equal {
				found = link
				break
			}
		}
		if found == 0 {
			subset.groups = append(subset.groups, dependencyGroup{values: *keys, rows: 1, next: subset.buckets[hash]})
			subset.buckets[hash] = len(subset.groups)
			continue
		}
		group := &subset.groups[found-1]
		group.rows++
		for column := range c.input.Columns {
			bit := uint8(1 << column)
			if (subset.mask|group.conflicts)&bit != 0 {
				continue
			}
			same, err := c.grouping[column].Equal(ctx, group.values[column], keys[column])
			if err != nil {
				return err
			}
			if !same {
				group.conflicts |= bit
			}
		}
	}
	c.rows++
	return ctx.Err()
}

func (c *DependenciesCollector) finish(ctx context.Context) (sqltype.Value, []sqltype.StatisticsItem, error) {
	var items []sqltype.StatisticsItem
	err := visitDependencyDirections(c.input.Definition.Selection, func(mask uint8, dependent int, attributes []int16) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var supporting uint64
		for i := range c.subsets[mask].groups {
			if err := ctx.Err(); err != nil {
				return err
			}
			group := &c.subsets[mask].groups[i]
			if group.conflicts&(1<<dependent) == 0 {
				supporting += group.rows
			}
		}
		if supporting != 0 {
			items = append(items, sqltype.StatisticsItem{
				Attributes: append([]int16(nil), attributes...),
				Value:      float64(supporting) / float64(c.rows),
			})
		}
		return nil
	})
	if err != nil {
		return sqltype.Value{}, nil, err
	}
	if len(items) == 0 {
		return sqltype.ResultNull(c.input.DependenciesType.Usage()), nil, ctx.Err()
	}
	value, err := sqltype.StatisticsValue(c.input.DependenciesType, items)
	return value, items, err
}
