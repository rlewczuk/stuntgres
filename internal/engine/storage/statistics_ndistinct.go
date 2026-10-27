package storage

import (
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

type NDistinctInput struct {
	Key        ExtendedStatisticsKey
	Definition *catalog.Statistics
	Columns    []StatisticsColumn
	Type       *sqltype.Descriptor
}

type ndistinctTuple struct {
	values [8]sqltype.Value
	next   int
}

type ndistinctSubset struct {
	mask       uint8
	attributes []int16
	buckets    map[uint64]int
	groups     []ndistinctTuple
}

// NDistinctCollector binds typed grouping once, and prepares each column once
// per row. Subsets share that scratch; retained representatives own their keys.
type NDistinctCollector struct {
	input    NDistinctInput
	grouping [8]sqltype.Grouping
	subsets  []ndistinctSubset
	rows     uint64
}

func NewNDistinctCollector(input NDistinctInput) (*NDistinctCollector, error) {
	if input.Definition == nil || len(input.Columns) != input.Definition.Selection.DimensionLen() || input.Type == nil {
		return nil, fmt.Errorf("invalid ndistinct layout")
	}
	c := &NDistinctCollector{input: input}
	for i, column := range input.Columns {
		if column.Key.Attribute != input.Definition.Selection.Dimension(i) || column.Slot < 0 {
			return nil, fmt.Errorf("invalid ndistinct column selection")
		}
		grouping, err := sqltype.BindGrouping(column.Usage)
		if err != nil {
			return nil, err
		}
		c.grouping[i] = grouping
	}
	width := len(input.Columns)
	c.subsets = make([]ndistinctSubset, 0, (1<<width)-width-1)
	var attributes [8]int16
	var choose func(int, int, int, uint8)
	choose = func(start, left, count int, mask uint8) {
		if left == 0 {
			owned := slices.Clone(attributes[:count])
			c.subsets = append(c.subsets, ndistinctSubset{
				mask: mask, attributes: owned, buckets: make(map[uint64]int),
			})
			return
		}
		for i := start; i <= width-left; i++ {
			attributes[count] = int16(input.Columns[i].Key.Attribute)
			choose(i+1, left-1, count+1, mask|1<<i)
		}
	}
	for size := 2; size <= width; size++ {
		choose(0, size, 0, 0)
	}
	return c, nil
}

func (c *NDistinctCollector) Add(ctx context.Context, row []sqltype.Value) error {
	var keys [8]sqltype.Value
	var hashes [8]uint64
	for i, column := range c.input.Columns {
		if err := ctx.Err(); err != nil {
			return err
		}
		if column.Slot >= len(row) || row[column.Slot].Type() != column.Usage.Type {
			return fmt.Errorf("invalid ndistinct row")
		}
		key, err := c.grouping[i].PrepareKey(ctx, row[column.Slot])
		if err != nil {
			return err
		}
		keys[i] = key
		hashes[i], err = c.grouping[i].Hash(ctx, key)
		if err != nil {
			return err
		}
	}
	for i := range c.subsets {
		subset := &c.subsets[i]
		hash := uint64(14695981039346656037)
		for column := range c.input.Columns {
			if subset.mask&(1<<column) != 0 {
				hash = (hash ^ hashes[column]) * 1099511628211
			}
		}
		found := false
		for link := subset.buckets[hash]; link != 0; link = subset.groups[link-1].next {
			equal := true
			for column := range c.input.Columns {
				if subset.mask&(1<<column) == 0 {
					continue
				}
				same, err := c.grouping[column].Equal(ctx, subset.groups[link-1].values[column], keys[column])
				if err != nil {
					return err
				}
				if !same {
					equal = false
					break
				}
			}
			if equal {
				found = true
				break
			}
		}
		if !found {
			subset.groups = append(subset.groups, ndistinctTuple{values: keys, next: subset.buckets[hash]})
			subset.buckets[hash] = len(subset.groups)
		}
	}
	c.rows++
	return ctx.Err()
}

func (c *NDistinctCollector) Finish(ctx context.Context) (*ExtendedStatistics, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.rows == 0 {
		return nil, nil
	}
	items := make([]sqltype.StatisticsItem, len(c.subsets))
	for i, subset := range c.subsets {
		items[i] = sqltype.StatisticsItem{Attributes: subset.attributes, Value: float64(len(subset.groups))}
	}
	value, err := sqltype.StatisticsValue(c.input.Type, items)
	if err != nil {
		return nil, err
	}
	fact := &ExtendedStatistics{
		Relation: c.input.Definition.Relation, Selection: c.input.Definition.Selection, Recipes: c.input.Definition.Expressions, Value: value, Items: items,
	}
	for i, column := range c.input.Columns {
		fact.Usages[i] = column.Usage
	}
	return fact, nil
}
