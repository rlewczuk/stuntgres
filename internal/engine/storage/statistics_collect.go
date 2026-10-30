package storage

import (
	"context"
	"fmt"
	"math"
	"sort"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// StatisticsTarget is the fixed expression-summary target. Base columns use
// their captured default or per-column Target instead.
const StatisticsTarget = 100

type StatisticsColumn struct {
	Key    StatisticsKey
	Usage  sqltype.Usage
	Slot   int
	Target int // Captured default or column-specific target, inclusive 1..10000.
	// SamplePopulation is optional; nil retains exact full-input semantics.
	SamplePopulation *float64
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
	width    uint64
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
	collectors := make([]ProjectedStatisticsCollector, len(columns))
	seen := make(map[StatisticsKey]bool, len(columns))
	for i, column := range columns {
		if column.Key.Attribute <= 0 || column.Slot < 0 || column.Target < 1 || column.Target > 10000 || column.Usage.Type == nil || !column.Usage.Type.Concrete() || seen[column.Key] {
			return nil, 0, fmt.Errorf("invalid statistics column layout")
		}
		if population := column.SamplePopulation; population != nil && (*population < 0 || math.IsNaN(*population) || math.IsInf(*population, 0)) {
			return nil, 0, fmt.Errorf("invalid statistics sample population")
		}
		seen[column.Key] = true
		if err := collectors[i].initialize(column.Usage); err != nil {
			return nil, 0, err
		}
		collectors[i].scalar.column = column
	}
	var population uint64
	err := rows(func(row []sqltype.Value) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		for i := range collectors {
			c := &collectors[i]
			if columns[i].Slot >= len(row) {
				return fmt.Errorf("invalid statistics row layout")
			}
			if err := c.Add(ctx, row[columns[i].Slot]); err != nil {
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
		fact, err := c.Finish(ctx)
		if err != nil {
			return nil, 0, err
		}
		result.Columns[columns[i].Key] = fact
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
	base, err := value.BaseValue()
	if err != nil {
		return err
	}
	size := base.Type().AttributeFacts().Length
	if size > 0 {
		c.width += uint64(size)
	} else {
		switch base.Type().ID() {
		case sqltype.Text, sqltype.VarChar, sqltype.BPChar:
			text, err := base.Text()
			if err != nil {
				return err
			}
			c.width += uint64(len(text) + 4)
		case sqltype.Bytea:
			raw, err := base.Binary()
			if err != nil {
				return err
			}
			c.width += uint64(len(raw) + 4)
		}
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
	if nonnull := c.stats.Rows - c.stats.NullCount; nonnull != 0 {
		c.stats.AverageWidth = int32(c.width / nonnull)
	}
	if !c.stats.DistinctKnown {
		return ctx.Err()
	}
	c.stats.DistinctCount = uint64(len(c.groups))
	if population := c.column.SamplePopulation; population != nil && *population > 0 && c.stats.Rows != 0 {
		n := float64(c.stats.Rows - c.stats.NullCount)
		d := float64(c.stats.DistinctCount)
		N := max(n, *population*(1-c.stats.NullFraction()))
		D := d
		if float64(c.stats.Rows) != *population && n > 0 {
			var singletons float64
			for _, group := range c.groups {
				if group.count == 1 {
					singletons++
				}
			}
			if singletons > 0 {
				D = math.Round(n * d / ((n - singletons) + singletons*n/N))
			}
			D = max(d, min(N, D))
		}
		if D > *population/10 {
			c.stats.DistinctOverride = -D / *population
		} else {
			c.stats.DistinctOverride = D
		}
	}
	// This order makes tied membership deterministic by first visible occurrence.
	sort.Slice(c.groups, func(i, j int) bool {
		if c.groups[i].count != c.groups[j].count {
			return c.groups[i].count > c.groups[j].count
		}
		return c.groups[i].first < c.groups[j].first
	})
	target := c.column.Target
	if target == 0 {
		target = StatisticsTarget
	}
	retained := 0
	for retained < len(c.groups) && retained < target && c.groups[retained].count > 1 {
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
	boundaries := min(uint64(target+1), weight)
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
