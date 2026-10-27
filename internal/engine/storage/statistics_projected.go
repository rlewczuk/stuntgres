package storage

import (
	"context"
	"fmt"
	"stuntgres/internal/sqltype"
)

// ProjectedStatisticsCollector is the ordinary scalar collector over an already
// evaluated typed dimension. It owns summaries, never the caller's row buffer.
type ProjectedStatisticsCollector struct{ scalar statisticsCollector }

func NewProjectedStatisticsCollector(usage sqltype.Usage) (*ProjectedStatisticsCollector, error) {
	c := &ProjectedStatisticsCollector{}
	if err := c.initialize(usage); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *ProjectedStatisticsCollector) initialize(usage sqltype.Usage) error {
	if usage.Type == nil || !usage.Type.Concrete() {
		return fmt.Errorf("invalid statistics result usage")
	}
	c.scalar.column.Usage = usage
	c.scalar.stats = &ColumnStatistics{Usage: usage}
	if width := usage.Type.AttributeFacts().Length; width > 0 {
		c.scalar.stats.AverageWidth = int32(width)
	}
	grouping, err := sqltype.BindGrouping(usage)
	if err != nil {
		if missingStatisticsOperator(err) {
			return nil
		}
		return err
	}
	c.scalar.grouping, c.scalar.stats.DistinctKnown = grouping, true
	c.scalar.buckets = make(map[uint64]int)
	ordering, err := sqltype.BindOrdering(usage)
	if err != nil && !missingStatisticsOperator(err) {
		return err
	}
	c.scalar.ordering, c.scalar.ordered = ordering, err == nil
	return nil
}

func (c *ProjectedStatisticsCollector) Add(ctx context.Context, value sqltype.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.scalar.add(ctx, value)
}

func (c *ProjectedStatisticsCollector) Finish(ctx context.Context) (*ColumnStatistics, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.scalar.stats.Rows == 0 {
		return nil, nil
	}
	if err := c.scalar.finish(ctx); err != nil {
		return nil, err
	}
	return c.scalar.stats, nil
}
