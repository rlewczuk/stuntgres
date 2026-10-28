package storage

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

// ReownRetainedExtendedStatistics is called only for genuine destination TEMP
// retention. Typed replay, not Retag or imported OID matches, remints changed
// usages. All fallible work precedes the caller's atomic replacement boundary.
func ReownRetainedExtendedStatistics(ctx context.Context, fact *ExtendedStatistics, definition *catalog.Statistics, read catalog.Read, source, destination StatisticsSnapshotCodec) (*ExtendedStatistics, error) {
	relation, ok := read.Relation(definition.Relation.Address)
	if !ok || fact.Selection != definition.Selection || fact.ExpressionLen() != definition.Expressions.Len() {
		return nil, fmt.Errorf("stuntgres: incompatible retained statistics")
	}
	value := *fact
	value.Relation, value.Recipes = definition.Relation, definition.Expressions
	value.providers = CaptureStatisticsProviders(read, definition)
	if len(fact.Items) != 0 {
		typ, ok := read.LookupType(sqltype.PgNDistinct)
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing retained ndistinct payload type")
		}
		if fact.Value.Type() != typ {
			var err error
			value.Value, err = sqltype.StatisticsValue(typ, fact.Items)
			if err != nil {
				return nil, err
			}
		}
	}
	if strings.Contains(definition.Selection.Kinds(), "f") {
		typ, ok := read.LookupType(sqltype.PgDependencies)
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing retained dependencies payload type")
		}
		if fact.Dependencies.Type() != typ {
			value.Dependencies = sqltype.ResultNull(typ.Usage())
			if len(fact.DependencyItems) != 0 {
				var err error
				value.Dependencies, err = sqltype.StatisticsValue(typ, fact.DependencyItems)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	value.inputs = slices.Clone(fact.inputs)
	for i, input := range value.inputs {
		slot := relation.IndexForAttribute(input.Key.Attribute)
		if slot < 0 {
			return nil, fmt.Errorf("stuntgres: missing retained statistics input")
		}
		value.inputs[i].Usage = relation.Columns[slot].Usage
	}
	for i := range definition.Selection.Len() {
		slot := relation.IndexForAttribute(definition.Selection.Key(i))
		if slot < 0 {
			return nil, fmt.Errorf("stuntgres: missing retained statistics key")
		}
		value.Usages[i] = relation.Columns[slot].Usage
	}
	remap := func(oldUsage, newUsage sqltype.Usage, v sqltype.Value) (sqltype.Value, error) {
		if source == nil || destination == nil {
			return sqltype.Value{}, fmt.Errorf("missing retained statistics codec")
		}
		encoded, err := source.Encode(oldUsage, v)
		if err != nil {
			return sqltype.Value{}, err
		}
		return destination.Decode(newUsage, encoded)
	}
	value.expressions = slices.Clone(fact.expressions)
	for i, old := range fact.expressions {
		usage := definition.Expressions.At(i).Usage()
		value.Usages[definition.Selection.Len()+i] = usage
		if old.Usage == usage {
			continue
		}
		column := *old
		column.Usage = usage
		column.MCV = slices.Clone(old.MCV)
		for j := range column.MCV {
			var err error
			column.MCV[j].Value, err = remap(old.Usage, usage, column.MCV[j].Value)
			if err != nil {
				return nil, err
			}
		}
		column.Histogram = slices.Clone(old.Histogram)
		for j := range column.Histogram {
			var err error
			column.Histogram[j], err = remap(old.Usage, usage, column.Histogram[j])
			if err != nil {
				return nil, err
			}
		}
		value.expressions[i] = &column
	}
	if fact.MCVData != nil {
		width := definition.Selection.DimensionLen()
		changed := false
		for d := range width {
			changed = changed || fact.Usages[d] != value.Usages[d]
		}
		if changed {
			data := *fact.MCVData
			// Items are immutable integer construction facts and remain shared.
			for d := range width {
				if fact.Usages[d] == value.Usages[d] {
					continue
				}
				data.Dictionaries[d] = make([]sqltype.Value, len(fact.MCVData.Dictionaries[d]))
				for i, v := range fact.MCVData.Dictionaries[d] {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					mapped, err := remap(fact.Usages[d], value.Usages[d], v)
					if err != nil {
						return nil, err
					}
					data.Dictionaries[d][i] = mapped
				}
			}
			value.MCVData = &data
		}
		typ, ok := read.LookupType(sqltype.PgMCVList)
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing retained MCV payload type")
		}
		if changed || fact.MCV.Type() != typ {
			var err error
			value.MCV, err = BuildExtendedMCV(ctx, typ, value.Usages[:width], value.MCVData)
			if err != nil {
				return nil, err
			}
		}
	}
	return &value, ctx.Err()
}
