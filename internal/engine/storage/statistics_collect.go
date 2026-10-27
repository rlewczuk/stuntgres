package storage

import (
	"context"
	"fmt"
	"sort"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

const StatisticsTarget = 100

type StatisticsColumn struct {
	Key   StatisticsKey
	Usage sqltype.Usage
	Slot  int
}

type statisticsGroup struct {
	value, key sqltype.Value
	count      uint64
	first      int
	next       int // Hash collision chain, index plus one; zero terminates.
}

type statisticsCollector struct {
	column   StatisticsColumn
	stats    *ColumnStatistics
	grouping sqltype.Grouping
	ordering sqltype.Comparison
	ordered  bool
	buckets  map[uint64]int
	groups   []statisticsGroup
}

func missingStatisticsOperator(err error) bool {
	diagnostic, ok := err.(*sqlstate.Error)
	return ok && diagnostic.Code == "42883"
}

// CollectStatistics consumes borrowed, already visible and layout-mapped rows.
// It neither retains row slices nor opens storage. Callers omit virtual columns.
// Empty populations deliberately publish no replacements, preserving old facts.
func CollectStatistics(ctx context.Context, columns []StatisticsColumn, rows func(func([]sqltype.Value) error) error) (*RelationStatistics, uint64, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	collectors := make([]statisticsCollector, len(columns))
	seen := make(map[StatisticsKey]bool, len(columns))
	for i, column := range columns {
		if column.Key.Attribute <= 0 || column.Slot < 0 || column.Usage.Type == nil || !column.Usage.Type.Concrete() || seen[column.Key] {
			return nil, 0, fmt.Errorf("invalid statistics column layout")
		}
		seen[column.Key] = true
		c := &collectors[i]
		c.column, c.stats = column, &ColumnStatistics{Usage: column.Usage}
		grouping, err := sqltype.BindGrouping(column.Usage)
		if err != nil {
			if !missingStatisticsOperator(err) {
				return nil, 0, err
			}
			continue
		}
		c.grouping, c.stats.DistinctKnown = grouping, true
		c.buckets = make(map[uint64]int)
		ordering, err := sqltype.BindOrdering(column.Usage)
		if err != nil && !missingStatisticsOperator(err) {
			return nil, 0, err
		}
		c.ordering, c.ordered = ordering, err == nil
	}
	var population uint64
	err := rows(func(row []sqltype.Value) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		for i := range collectors {
			c := &collectors[i]
			if c.column.Slot >= len(row) {
				return fmt.Errorf("invalid statistics row layout")
			}
			if err := c.add(ctx, row[c.column.Slot]); err != nil {
				return err
			}
		}
		population++
		return ctx.Err()
	})
	if err != nil {
		return nil, 0, err
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if population == 0 {
		return nil, 0, nil
	}
	result := &RelationStatistics{Columns: make(map[StatisticsKey]*ColumnStatistics, len(columns))}
	for i := range collectors {
		c := &collectors[i]
		if err := c.finish(ctx); err != nil {
			return nil, 0, err
		}
		result.Columns[c.column.Key] = c.stats
	}
	return result, population, ctx.Err()
}

func (c *statisticsCollector) add(ctx context.Context, value sqltype.Value) error {
	if value.Type() != c.column.Usage.Type {
		return fmt.Errorf("invalid statistics row type")
	}
	c.stats.Rows++
	if value.IsNull() {
		c.stats.NullCount++
		return ctx.Err()
	}
	if !c.stats.DistinctKnown {
		return ctx.Err()
	}
	key, err := c.grouping.PrepareKey(ctx, value)
	if err != nil {
		return err
	}
	hash, err := c.grouping.Hash(ctx, key)
	if err != nil {
		return err
	}
	for link := c.buckets[hash]; link != 0; link = c.groups[link-1].next {
		index := link - 1
		equal, err := c.grouping.Equal(ctx, c.groups[index].key, key)
		if err != nil {
			return err
		}
		if equal {
			c.groups[index].count++
			return ctx.Err()
		}
	}
	index := len(c.groups)
	c.groups = append(c.groups, statisticsGroup{value: value, key: key, count: 1, first: index, next: c.buckets[hash]})
	c.buckets[hash] = index + 1
	return ctx.Err()
}

func (c *statisticsCollector) finish(ctx context.Context) error {
	if !c.stats.DistinctKnown {
		return ctx.Err()
	}
	c.stats.DistinctCount = uint64(len(c.groups))
	// This order makes tied membership deterministic by first visible occurrence.
	sort.Slice(c.groups, func(i, j int) bool {
		if c.groups[i].count != c.groups[j].count {
			return c.groups[i].count > c.groups[j].count
		}
		return c.groups[i].first < c.groups[j].first
	})
	retained := 0
	for retained < len(c.groups) && retained < StatisticsTarget && c.groups[retained].count > 1 {
		g := c.groups[retained]
		c.stats.MCV = append(c.stats.MCV, StatisticMCV{Value: g.value, Count: g.count})
		retained++
	}
	if !c.ordered {
		return ctx.Err()
	}
	residual := c.groups[retained:]
	var comparisonError error
	sort.Slice(residual, func(i, j int) bool {
		if comparisonError != nil {
			return false
		}
		var order int
		order, comparisonError = c.ordering.Compare(ctx, residual[i].value, residual[j].value, sqltype.InputEnvironment{})
		return order < 0
	})
	if comparisonError != nil {
		return comparisonError
	}
	if len(residual) == 0 {
		return ctx.Err()
	}
	var weight uint64
	for _, group := range residual {
		weight += group.count
	}
	boundaries := min(uint64(StatisticsTarget+1), weight)
	c.stats.Histogram = make([]sqltype.Value, int(boundaries))
	var index int
	cumulative := residual[0].count
	for i := uint64(0); i < boundaries; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		rank := uint64(0)
		if boundaries > 1 {
			// Quotient/remainder form avoids overflowing (weight-1)*i.
			rank = ((weight-1)/(boundaries-1))*i + ((weight-1)%(boundaries-1))*i/(boundaries-1)
		}
		for rank >= cumulative {
			index++
			cumulative += residual[index].count
		}
		c.stats.Histogram[i] = residual[index].value
	}
	return ctx.Err()
}
