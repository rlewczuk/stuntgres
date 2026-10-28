package storage

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

// ExtendedInput captures every dimension and the stable input usages needed to
// retain stale summaries safely. Columns address a projected dimension row.
type ExtendedInput struct {
	Key              ExtendedStatisticsKey
	Definition       *catalog.Statistics
	Columns          []StatisticsColumn
	Inputs           []StatisticsColumn
	Providers        []catalog.Descriptor
	Type             *sqltype.Descriptor
	DependenciesType *sqltype.Descriptor
	MCVType          *sqltype.Descriptor
	MCVTarget        int
}

type ExtendedCollector struct {
	input        ExtendedInput
	joint        *NDistinctCollector
	dependencies *DependenciesCollector
	mcv          *MCVCollector
	preparer     *NDistinctCollector
	expressions  []ProjectedStatisticsCollector
	rows         uint64
}

func NewExtendedCollector(input ExtendedInput) (*ExtendedCollector, error) {
	if input.Definition == nil || len(input.Columns) != input.Definition.Selection.DimensionLen() || input.Definition.Expressions.Len() != input.Definition.Selection.ExpressionLen() {
		return nil, fmt.Errorf("invalid extended statistics layout")
	}
	c := &ExtendedCollector{input: input}
	var err error
	if strings.ContainsAny(input.Definition.Selection.Kinds(), "dfm") {
		c.preparer, err = newNDistinctCollector(NDistinctInput{Key: input.Key, Definition: input.Definition, Columns: input.Columns, Type: input.Type}, strings.Contains(input.Definition.Selection.Kinds(), "d"))
		if err != nil {
			return nil, err
		}
		if strings.Contains(input.Definition.Selection.Kinds(), "d") {
			c.joint = c.preparer
		}
	}
	if strings.Contains(input.Definition.Selection.Kinds(), "f") {
		c.dependencies, err = newDependenciesCollector(input, c.preparer.grouping)
		if err != nil {
			return nil, err
		}
	}
	if strings.Contains(input.Definition.Selection.Kinds(), "m") {
		c.mcv, err = newMCVCollector(input, c.preparer.grouping)
		if err != nil {
			return nil, err
		}
	}
	c.expressions = make([]ProjectedStatisticsCollector, input.Definition.Expressions.Len())
	for i := range c.expressions {
		if err := c.expressions[i].initialize(input.Definition.Expressions.At(i).Usage()); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// Add consumes each already evaluated result once, sharing it with joint facts.
func (c *ExtendedCollector) Add(ctx context.Context, dimensions []sqltype.Value) error {
	if len(dimensions) != c.input.Definition.Selection.DimensionLen() {
		return fmt.Errorf("invalid extended statistics dimension row")
	}
	if c.preparer != nil {
		keys, hashes, err := c.preparer.prepare(ctx, dimensions)
		if err != nil {
			return err
		}
		if c.joint != nil {
			if err := c.joint.addPrepared(ctx, &keys, &hashes); err != nil {
				return err
			}
		}
		if c.dependencies != nil {
			if err := c.dependencies.addPrepared(ctx, &keys, &hashes); err != nil {
				return err
			}
		}
		if c.mcv != nil {
			if err := c.mcv.addPrepared(ctx, &keys, &hashes); err != nil {
				return err
			}
		}
	}
	for i := range c.expressions {
		if err := c.expressions[i].Add(ctx, dimensions[c.input.Definition.Selection.Len()+i]); err != nil {
			return err
		}
	}
	c.rows++
	return ctx.Err()
}

func (c *ExtendedCollector) Finish(ctx context.Context) (*ExtendedStatistics, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.rows == 0 {
		return nil, nil
	}
	var fact *ExtendedStatistics
	if c.joint != nil {
		var err error
		fact, err = c.joint.Finish(ctx)
		if err != nil {
			return nil, err
		}
	} else {
		fact = &ExtendedStatistics{Relation: c.input.Definition.Relation, Selection: c.input.Definition.Selection, Recipes: c.input.Definition.Expressions}
	}
	if c.dependencies != nil {
		var err error
		fact.Dependencies, fact.DependencyItems, err = c.dependencies.finish(ctx)
		if err != nil {
			return nil, err
		}
	}
	if c.mcv != nil {
		var err error
		fact.MCVData, fact.MCV, err = c.mcv.finish(ctx)
		if err != nil {
			return nil, err
		}
	}
	fact.inputs = slices.Clone(c.input.Inputs)
	fact.providers = slices.Clone(c.input.Providers)
	for i, column := range c.input.Columns {
		fact.Usages[i] = column.Usage
	}
	fact.expressions = make([]*ColumnStatistics, len(c.expressions))
	for i := range c.expressions {
		var err error
		fact.expressions[i], err = c.expressions[i].Finish(ctx)
		if err != nil {
			return nil, err
		}
	}
	return fact, nil
}

// CaptureStatisticsProviders retains actual external descriptor revisions, not
// names or OIDs alone. Relation-column lifetime and usages are checked separately.
func CaptureStatisticsProviders(read catalog.Read, definition *catalog.Statistics) []catalog.Descriptor {
	var providers []catalog.Descriptor
	for i := range definition.Expressions.Len() {
		recipe := definition.Expressions.At(i)
		for j := range recipe.DependencyLen() {
			address := recipe.Dependency(j).Address
			if address.ClassID == definition.Relation.Address.ClassID && address.ObjectID == definition.Relation.Address.ObjectID {
				continue
			}
			address.SubID = 0
			provider := read.Get(address)
			if provider == nil {
				continue
			}
			seen := false
			for _, old := range providers {
				if old.Address() == address {
					seen = true
					break
				}
			}
			if !seen {
				providers = append(providers, provider)
			}
		}
	}
	return providers
}
