package storage

import (
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

// ReownRetainedExtendedStatistics is used only after the engine has established
// genuine destination TEMP retention. It does not adopt imported OID matches.
func ReownRetainedExtendedStatistics(fact *ExtendedStatistics, definition *catalog.Statistics, read catalog.Read) (*ExtendedStatistics, error) {
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
			column.MCV[j].Value, err = column.MCV[j].Value.Retag(usage.Type)
			if err != nil {
				return nil, err
			}
		}
		column.Histogram = slices.Clone(old.Histogram)
		for j := range column.Histogram {
			var err error
			column.Histogram[j], err = column.Histogram[j].Retag(usage.Type)
			if err != nil {
				return nil, err
			}
		}
		value.expressions[i] = &column
	}
	return &value, nil
}
